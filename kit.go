package kit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gobellim/kit/authz"
	"github.com/gobellim/kit/chunk"
	"github.com/gobellim/kit/embed"
	"github.com/gobellim/kit/guard"
	"github.com/gobellim/kit/guard/rules"
	"github.com/gobellim/kit/internal/call"
	"github.com/gobellim/kit/internal/core"
	"github.com/gobellim/kit/internal/id"
	"github.com/gobellim/kit/model"
	"github.com/gobellim/kit/store"
	"github.com/gobellim/kit/trace"
)

const (
	DefaultMaxSteps        = 8
	DefaultMaxCallsPerTool = 3
	DefaultToolTimeout     = 30 * time.Second
	DefaultArgsMaxBytes    = 32 * 1024
	DefaultTopK            = 10
)

type EgressPolicy struct {
	RuleOfTwo bool
}

type ApprovalRequest struct {
	UserID      string
	Tenant      string
	Tool        string
	Args        string
	Fingerprint string
	Effect      Effect
}

type Approver interface {
	Approve(ctx context.Context, req ApprovalRequest) (bool, error)
}

type ApproverFunc func(ctx context.Context, req ApprovalRequest) (bool, error)

func (f ApproverFunc) Approve(ctx context.Context, req ApprovalRequest) (bool, error) {
	return f(ctx, req)
}

type Config struct {
	ES              store.ES
	LLM             model.Client
	Embedder        embed.Embedder
	Policy          *authz.Policy
	GuardL2         guard.Detector
	GuardMaxChars   int
	GuardCacheSize  int
	Tokenizer       chunk.Tokenizer
	ChunkSize       int
	ChunkOverlap    int
	MaxSteps        int
	MaxCallsPerTool int
	ToolTimeout     time.Duration
	ArgsMaxBytes    int
	TopK            int
	Logger          *slog.Logger

	TraceText      trace.TextPolicy
	Sampler        trace.Sampler
	HideInput      bool
	HideOutput     bool
	Sinks          []trace.Sink
	MaxTotalTokens int
	Egress         EgressPolicy
	Approver       Approver
	BeforeToolCall BeforeToolHook
	AfterToolCall  AfterToolHook
}

type App struct {
	cfg        Config
	policy     *authz.Policy
	guard      *guard.Pipeline
	writer     *trace.Writer
	calls      call.Deps
	chunker    *chunk.Chunker
	policyHash string
	mu         sync.RWMutex
	tools      map[string]*ToolDef
	order      []string
}

func New(_ context.Context, cfg Config) (*App, error) {
	if cfg.ES == nil {
		return nil, fmt.Errorf("kit: Config.ES is required: %w", core.ErrInvalidConfig)
	}
	if cfg.LLM == nil {
		return nil, fmt.Errorf("kit: Config.LLM is required: %w", core.ErrInvalidConfig)
	}
	if cfg.Policy == nil {
		return nil, fmt.Errorf("kit: Config.Policy is required: %w", core.ErrInvalidConfig)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = DefaultMaxSteps
	}
	if cfg.MaxCallsPerTool <= 0 {
		cfg.MaxCallsPerTool = DefaultMaxCallsPerTool
	}
	if cfg.ToolTimeout <= 0 {
		cfg.ToolTimeout = DefaultToolTimeout
	}
	if cfg.ArgsMaxBytes <= 0 {
		cfg.ArgsMaxBytes = DefaultArgsMaxBytes
	}
	if cfg.TopK <= 0 {
		cfg.TopK = DefaultTopK
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = chunk.DefaultSize
	}
	if cfg.ChunkOverlap <= 0 {
		cfg.ChunkOverlap = chunk.DefaultOverlap
	}
	if cfg.Tokenizer == nil {
		cfg.Tokenizer = chunk.WhitespaceTokenizer{}
	}
	if cfg.TraceText.IsRaw() {
		cfg.Logger.Warn("kit: trace text policy is FULL: unmasked input and output are written to Elasticsearch")
	}
	if cw, ok := cfg.LLM.(model.ContextWindow); ok {
		effective := cfg.GuardMaxChars
		if effective <= 0 {
			effective = guard.DefaultMaxChars
		}
		if window := cw.ContextTokens(); window > 0 && effective > window {
			return nil, fmt.Errorf("kit: guard window %d chars exceeds the model context window %d tokens: %w",
				effective, window, core.ErrInvalidConfig)
		}
	}

	hash := policyHash(cfg.Policy)

	layers := []guard.Detector{rules.New()}
	guardModel := ""
	if cfg.GuardL2 != nil {
		layers = append(layers, cfg.GuardL2)
		guardModel = cfg.GuardL2.Name()
	}
	guardVersion := hash + "/" + layerID(layers)
	pipeline, err := guard.New(guard.Options{
		Layers:    layers,
		MaxChars:  cfg.GuardMaxChars,
		CacheSize: cfg.GuardCacheSize,
		Version:   guardVersion,
	})
	if err != nil {
		return nil, err
	}
	chunker, err := chunk.New(cfg.Tokenizer, cfg.ChunkSize, cfg.ChunkOverlap)
	if err != nil {
		return nil, err
	}
	writer, err := trace.NewWriter(trace.Options{
		ES:         cfg.ES,
		Sinks:      cfg.Sinks,
		Text:       cfg.TraceText,
		Sampler:    cfg.Sampler,
		HideInput:  cfg.HideInput,
		HideOutput: cfg.HideOutput,
		Logger:     cfg.Logger,
	})
	if err != nil {
		return nil, err
	}

	app := &App{
		cfg:        cfg,
		policy:     cfg.Policy,
		guard:      pipeline,
		writer:     writer,
		chunker:    chunker,
		policyHash: hash,
		tools:      make(map[string]*ToolDef),
	}
	app.calls = call.Deps{
		Authz:        cfg.Policy,
		Guard:        pipeline,
		Tracer:       tracerAdapter{writer},
		GuardVersion: pipeline.Version(),
		GuardModel:   guardModel,
		PolicyHash:   hash,
		TextPolicy:   cfg.TraceText,
	}
	return app, nil
}

func (a *App) Setup(ctx context.Context) error {
	return store.EnsureSchema(ctx, a.cfg.ES, a.embeddingDims())
}

func (a *App) embeddingDims() int {
	if a.cfg.Embedder == nil {
		return 0
	}
	return a.cfg.Embedder.Dim()
}

func (a *App) Close(ctx context.Context) error {
	return a.writer.Close(ctx)
}

func (a *App) Guard() *guard.Pipeline { return a.guard }

func (a *App) GuardVersion() string { return a.guard.Version() }

func (a *App) PolicyHash() string { return a.policyHash }

func (a *App) logger() *slog.Logger {
	if a.cfg.Logger != nil {
		return a.cfg.Logger
	}
	return slog.Default()
}

type tracerAdapter struct{ w *trace.Writer }

func (t tracerAdapter) Observe(_ context.Context, o trace.Observation) { t.w.EnqueueObservation(o) }

func principal(ctx context.Context) (core.Principal, error) {
	pr, ok := core.PrincipalFrom(ctx)
	if !ok || pr.IsZero() {
		return core.Principal{}, fmt.Errorf("kit: no principal in context: %w", core.ErrUnauthorized)
	}
	return pr, nil
}

func principalScoped(ctx context.Context) (core.Principal, error) {
	pr, err := principal(ctx)
	if err != nil {
		return core.Principal{}, err
	}
	if err := pr.Validate(); err != nil {
		return core.Principal{}, fmt.Errorf("kit: %w: %w", err, core.ErrUnauthorized)
	}
	return pr, nil
}

func layerID(layers []guard.Detector) string {
	names := make([]string, 0, len(layers))
	for _, l := range layers {
		names = append(names, l.Name())
	}
	return strings.Join(names, "+")
}

func policyHash(p *authz.Policy) string {
	roles := make([]string, 0, len(p.Roles))
	for r := range p.Roles {
		roles = append(roles, r)
	}
	sort.Strings(roles)

	h := sha256.New()
	h.Write([]byte("v1|" + p.Default + "\n"))
	for _, r := range roles {
		perms := append([]string(nil), p.Roles[r].Permissions...)
		sort.Strings(perms)
		h.Write([]byte(r + "=" + strings.Join(perms, ",") + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (a *App) guardTrace(pr core.Principal, traceID, name string, tags []string, started time.Time) trace.Trace {
	return trace.Trace{
		ID:           traceID,
		Name:         name,
		UserID:       pr.UserID,
		Tenant:       pr.Tenant,
		StartTime:    started,
		Tags:         tags,
		TextPolicy:   a.cfg.TraceText,
		GuardVersion: a.calls.GuardVersion,
		GuardModel:   a.calls.GuardModel,
		PolicyHash:   a.policyHash,
	}
}

func (a *App) guardText(ctx context.Context, rail guard.Rail, name, text, traceID string) (string, bool, error) {
	v, err := call.Guard(ctx, a.calls, call.Req{
		Kind: call.KindGuard, Name: name, Rail: rail, Text: text, TraceID: traceID,
	})
	if err != nil {
		return "", false, err
	}
	return v.Text, v.Action != guard.Allow, nil
}

func errorResult(msg string) string {
	b, err := json.Marshal(map[string]string{"error": msg})
	if err != nil {
		return `{"error":"tool error"}`
	}
	return string(b)
}

type ChatRequest struct {
	System      string
	Messages    []model.Message
	SessionID   string
	Temperature *float64
	MaxTokens   int
	ToolChoice  string
	Tags        []string
}

type turn struct {
	traceID      string
	principal    core.Principal
	callsPerTool map[string]int
	invalidTries map[string]int
	approved     map[string]bool
	untrusted    bool
}

func (a *App) Chat(ctx context.Context, req ChatRequest) (out model.Message, err error) {
	pr, err := principal(ctx)
	if err != nil {
		return model.Message{}, err
	}
	traceID := id.New()
	started := time.Now()
	var total model.Usage

	tr := a.guardTrace(pr, traceID, "chat", req.Tags, started)
	tr.SessionID = req.SessionID
	defer func() {
		tr.EndTime = time.Now()
		tr.Output = out.Content
		tr.Usage = trace.Usage{
			PromptTokens:     total.PromptTokens,
			CompletionTokens: total.CompletionTokens,
			TotalTokens:      total.TotalTokens,
		}
		a.writer.Enqueue(tr)
	}()

	client := sanitizeMessages(req.Messages)
	if len(client) == 0 {
		return model.Message{}, fmt.Errorf("kit: chat: no user or assistant messages: %w", core.ErrInvalidArgs)
	}
	t := &turn{
		traceID:      traceID,
		principal:    pr,
		callsPerTool: make(map[string]int),
		invalidTries: make(map[string]int),
		approved:     make(map[string]bool),
	}
	for i := range client {
		text, untrusted, gerr := a.guardText(ctx, guard.RailInput, "guard:input", client[i].Content, traceID)
		if gerr != nil {
			return model.Message{}, gerr
		}
		if untrusted {
			t.untrusted = true
		}
		client[i].Content = text
	}

	msgs := make([]model.Message, 0, len(client)+1)
	if req.System != "" {
		msgs = append(msgs, model.Message{Role: model.RoleSystem, Content: req.System})
	}
	msgs = append(msgs, client...)

	specs := a.allowedToolSpecs(pr)
	tr.Input = client[len(client)-1].Content

	for step := 0; step < a.cfg.MaxSteps; step++ {
		toolChoice := req.ToolChoice
		if step == a.cfg.MaxSteps-1 && len(specs) > 0 {
			toolChoice = "none"
		}

		resp, cerr := call.Do(ctx, a.calls, call.Req{
			Kind: call.KindLLM, Name: a.cfg.LLM.Model(), Model: a.cfg.LLM.Model(), TraceID: traceID,
		}, func(ctx context.Context, span *call.Span) (model.Response, error) {
			span.Kind = trace.KindGeneration
			r, rerr := a.cfg.LLM.Chat(ctx, model.Request{
				Messages:    msgs,
				Tools:       specs,
				Temperature: req.Temperature,
				MaxTokens:   req.MaxTokens,
				ToolChoice:  toolChoice,
			})
			span.Output = r.Message.Content
			span.Usage = trace.Usage{
				PromptTokens:     r.Usage.PromptTokens,
				CompletionTokens: r.Usage.CompletionTokens,
				TotalTokens:      r.Usage.TotalTokens,
			}
			span.Metadata = map[string]any{"finish_reason": string(r.FinishReason)}
			return r, rerr
		})
		if cerr != nil {
			return model.Message{}, cerr
		}
		total.PromptTokens += resp.Usage.PromptTokens
		total.CompletionTokens += resp.Usage.CompletionTokens
		total.TotalTokens += resp.Usage.TotalTokens
		if a.cfg.MaxTotalTokens > 0 && total.TotalTokens > a.cfg.MaxTotalTokens {
			return model.Message{}, fmt.Errorf("kit: chat: token budget %d exceeded (%d): %w",
				a.cfg.MaxTotalTokens, total.TotalTokens, core.ErrBudget)
		}

		if len(resp.Message.ToolCalls) == 0 {
			text, _, gerr := a.guardText(ctx, guard.RailOutput, "guard:output", resp.Message.Content, traceID)
			if gerr != nil {
				return model.Message{}, gerr
			}
			final := resp.Message
			final.Content = text
			return final, nil
		}

		msgs = append(msgs, resp.Message)
		for _, tc := range resp.Message.ToolCalls {
			result, terr := a.execTool(ctx, tc, t)
			if terr != nil {
				return model.Message{}, terr
			}
			msgs = append(msgs, model.Message{
				Role:       model.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    result,
			})
		}
	}
	return model.Message{}, fmt.Errorf("kit: chat: reached max_steps=%d: %w", a.cfg.MaxSteps, core.ErrMaxSteps)
}

var urlHostRe = regexp.MustCompile(`(?i)\b(?:https?|ftp)://[^\s/?#"'\\]*`)

func (a *App) execTool(ctx context.Context, tc model.ToolCall, t *turn) (string, error) {
	a.mu.RLock()
	def := a.tools[tc.Name]
	a.mu.RUnlock()
	if def == nil {
		return errorResult(fmt.Sprintf("tool %q is not available", tc.Name)), nil
	}

	if err := a.policy.Authorize(t.principal, def.permission); err != nil {
		return "", err
	}
	t.callsPerTool[tc.Name]++
	if t.callsPerTool[tc.Name] > a.cfg.MaxCallsPerTool {
		return errorResult(fmt.Sprintf("tool %s exceeded its call limit", tc.Name)), nil
	}
	if len(tc.Args) > a.cfg.ArgsMaxBytes {
		return errorResult("arguments too large"), nil
	}
	if err := def.validate(tc.Args); err != nil {
		t.invalidTries[tc.Name]++
		if t.invalidTries[tc.Name] > 1 {
			return "", err
		}
		return errorResult(err.Error()), nil
	}
	if def.requireApproval {
		if err := a.approve(ctx, def, tc.Args, t); err != nil {
			return "", err
		}
	}
	if err := a.checkEgress(def, tc.Args, t); err != nil {
		return "", err
	}

	info := ToolCallInfo{
		UserID:      t.principal.UserID,
		Tenant:      t.principal.Tenant,
		Tool:        tc.Name,
		Args:        tc.Args,
		Fingerprint: def.Fingerprint(tc.Args),
		Effect:      def.effect,
		CallIndex:   t.callsPerTool[tc.Name],
	}

	execute := func() (string, error) {
		return call.Do(ctx, a.calls, call.Req{
			Kind:       call.KindTool,
			Name:       tc.Name,
			Permission: def.permission,
			TraceID:    t.traceID,
			Tags:       []string{"effect:" + def.effect.String()},
		}, func(ctx context.Context, span *call.Span) (string, error) {
			tctx, cancel := context.WithTimeout(ctx, a.cfg.ToolTimeout)
			defer cancel()
			res, ierr := def.invoke(tctx, tc.Args)
			span.Output = res
			return res, ierr
		})
	}

	var (
		result  string
		execErr error
	)
	if h := a.beforeHook(def); h != nil {
		proceed, output, herr := h(ctx, info)
		switch {
		case herr != nil:
			execErr = herr
		case !proceed:
			result = output
		default:
			result, execErr = execute()
		}
	} else {
		result, execErr = execute()
	}

	if h := a.afterHook(def); h != nil {
		result, execErr = h(ctx, info, result, execErr)
	}

	if execErr != nil {
		if isFatalExecErr(execErr) {
			return "", execErr
		}
		return errorResult(execErr.Error()), nil
	}

	v, gerr := call.Guard(ctx, a.calls, call.Req{
		Kind: call.KindGuard, Name: "guard:tool_result", Rail: guard.RailToolResult,
		Text: result, TraceID: t.traceID,
	})
	if gerr != nil {
		if errors.Is(gerr, core.ErrGuardReject) {
			t.untrusted = true
			return errorResult("result blocked by guard"), nil
		}
		return "", gerr
	}
	if v.Action != guard.Allow {
		t.untrusted = true
	}

	final := v.Text
	if def.validateOutput {
		if verr := def.validateResult(final); verr != nil {
			a.logger().Warn("kit: tool output does not match its schema", "tool", def.name, "err", verr)
		}
	}
	return def.ModelView(final), nil
}

func (a *App) beforeHook(def *ToolDef) BeforeToolHook {
	if def.beforeTool != nil {
		return def.beforeTool
	}
	return a.cfg.BeforeToolCall
}

func (a *App) afterHook(def *ToolDef) AfterToolHook {
	if def.afterTool != nil {
		return def.afterTool
	}
	return a.cfg.AfterToolCall
}

func isFatalExecErr(err error) bool {
	return errors.Is(err, core.ErrProvider) ||
		errors.Is(err, core.ErrUnauthorized) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

func (a *App) approve(ctx context.Context, def *ToolDef, args string, t *turn) error {
	if a.cfg.Approver == nil {
		return fmt.Errorf("kit: tool %s requires approval but no Approver is configured: %w",
			def.name, core.ErrApprovalRequired)
	}
	fingerprint := def.Fingerprint(args)
	if t.approved == nil {
		t.approved = make(map[string]bool)
	}
	if t.approved[fingerprint] {
		return fmt.Errorf("kit: tool %s: approval %s was already consumed in this turn: %w",
			def.name, fingerprint, core.ErrApprovalRequired)
	}
	ok, err := a.cfg.Approver.Approve(ctx, ApprovalRequest{
		UserID:      t.principal.UserID,
		Tenant:      t.principal.Tenant,
		Tool:        def.name,
		Args:        args,
		Fingerprint: fingerprint,
		Effect:      def.effect,
	})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("kit: tool %s: approval denied: %w", def.name, core.ErrApprovalRequired)
	}
	t.approved[fingerprint] = true
	return nil
}

func (a *App) checkEgress(def *ToolDef, args string, t *turn) error {
	if !def.effect.IsEgress() {
		return nil
	}
	if a.cfg.Egress.RuleOfTwo && t.untrusted {
		return fmt.Errorf("kit: tool %s: egress blocked after untrusted input in this turn: %w",
			def.name, core.ErrUnauthorized)
	}
	if len(def.egressHosts) == 0 {
		return nil
	}
	for _, host := range urlHosts(args) {
		if !hostAllowed(host, def.egressHosts) {
			return fmt.Errorf("kit: tool %s: host %q is not allowed: %w", def.name, host, core.ErrUnauthorized)
		}
	}
	return nil
}

func urlHosts(args string) []string {
	matches := urlHostRe.FindAllString(args, -1)
	out := make([]string, 0, len(matches))
	for _, raw := range matches {
		host := raw
		if _, rest, ok := strings.Cut(raw, "://"); ok {
			host = rest
		}
		if i := strings.IndexAny(host, "/?#"); i >= 0 {
			host = host[:i]
		}
		if i := strings.LastIndex(host, "@"); i >= 0 {
			host = host[i+1:]
		}
		if i := strings.LastIndex(host, ":"); i >= 0 && strings.Count(host, ":") == 1 {
			if _, err := strconv.Atoi(host[i+1:]); err == nil {
				host = host[:i]
			}
		}
		if host != "" {
			out = append(out, strings.ToLower(host))
		}
	}
	return out
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(host)
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

func (a *App) allowedToolSpecs(pr core.Principal) []model.ToolSpec {
	a.mu.RLock()
	defer a.mu.RUnlock()

	specs := make([]model.ToolSpec, 0, len(a.order))
	for _, name := range a.order {
		def := a.tools[name]
		if def == nil || !a.policy.Allows(pr, def.permission) {
			continue
		}
		specs = append(specs, model.ToolSpec{Name: def.name, Description: def.description, Schema: def.schema})
	}
	return specs
}

func sanitizeMessages(in []model.Message) []model.Message {
	out := make([]model.Message, 0, len(in))
	for _, m := range in {
		if m.Role != model.RoleUser && m.Role != model.RoleAssistant {
			continue
		}
		out = append(out, model.Message{Role: m.Role, Content: m.Content})
	}
	return out
}

type Doc struct {
	ID     string
	Text   string
	ACL    []string
	Source string
}

func (a *App) Ingest(ctx context.Context, doc Doc) (err error) {
	pr, err := principalScoped(ctx)
	if err != nil {
		return err
	}
	if doc.ID == "" {
		return fmt.Errorf("kit: ingest: doc ID is required: %w", core.ErrInvalidConfig)
	}
	if err := a.policy.Authorize(pr, authz.PermIngest); err != nil {
		return err
	}
	if a.cfg.Embedder == nil {
		return fmt.Errorf("kit: ingest requires an Embedder: %w", core.ErrInvalidConfig)
	}

	traceID := id.New()
	started := time.Now()
	tr := a.guardTrace(pr, traceID, "ingest", nil, started)
	tr.Input = doc.Text
	var indexed, quarantined, deleted int
	defer func() {
		tr.EndTime = time.Now()
		tr.Metadata = map[string]any{"indexed": indexed, "quarantined": quarantined, "deleted": deleted}
		a.writer.Enqueue(tr)
	}()

	parts := a.chunker.Split(doc.Text)
	if len(parts) == 0 {
		return nil
	}

	vectors, err := call.Do(ctx, a.calls, call.Req{
		Kind: call.KindEmbed, Name: a.cfg.Embedder.Model(), Permission: authz.PermIngest, TraceID: traceID,
	}, func(ctx context.Context, span *call.Span) ([][]float32, error) {
		return a.cfg.Embedder.Passages(ctx, parts)
	})
	if err != nil {
		return err
	}
	if len(vectors) != len(parts) {
		return fmt.Errorf("kit: ingest: embedder returned %d vectors for %d chunks: %w", len(vectors), len(parts), core.ErrProvider)
	}

	chunks := make([]store.Chunk, 0, len(parts))
	for i, text := range parts {
		v, gerr := call.Guard(ctx, a.calls, call.Req{
			Kind: call.KindGuard, Name: "guard:chunk", Rail: guard.RailInput,
			Text: text, TraceID: traceID,
		})
		switch {
		case errors.Is(gerr, core.ErrGuardReject):
			chunks = append(chunks, store.Chunk{
				ID: fmt.Sprintf("%s#%d", doc.ID, i), DocID: doc.ID, Tenant: pr.Tenant,
				ACL: doc.ACL, Position: i, Source: doc.Source, Quarantine: true,
			})
			quarantined++
			continue
		case gerr != nil:
			return gerr
		}
		chunks = append(chunks, store.Chunk{
			ID: fmt.Sprintf("%s#%d", doc.ID, i), DocID: doc.ID, Tenant: pr.Tenant,
			ACL: doc.ACL, Text: v.Text, Vector: vectors[i], Position: i, Source: doc.Source,
		})
		indexed++
	}

	body, err := store.DeleteByDocBody(doc.ID, pr.Tenant)
	if err != nil {
		return err
	}
	delReq := store.RawRequest(http.MethodPost, "/"+store.IndexChunks+"/_delete_by_query", body, "application/json")
	delReq.Refresh = "true"
	delReq.Idempotent = true
	resp, err := a.esDo(ctx, call.Req{
		Kind: call.KindIngest, Name: store.IndexChunks, Permission: authz.PermIngest, TraceID: traceID,
	}, delReq)
	if err != nil {
		return err
	}
	if deleted, err = store.ParseDeleteByQuery(resp); err != nil {
		return fmt.Errorf("kit: ingest: %w", err)
	}

	bulk, err := store.ChunksBulkBody(chunks, a.cfg.Embedder.Dim())
	if err != nil {
		return err
	}
	bulkReq := store.NDJSONRequest(http.MethodPost, "/_bulk", bulk)
	bulkReq.Refresh = "wait_for"
	resp, err = a.esDo(ctx, call.Req{
		Kind: call.KindIngest, Name: store.IndexChunks, Permission: authz.PermIngest, TraceID: traceID,
	}, bulkReq)
	if err != nil {
		return err
	}
	if err := store.BulkErrors(resp); err != nil {
		return fmt.Errorf("kit: ingest: %w: %w", err, core.ErrProvider)
	}
	return nil
}

type SearchRequest struct {
	Query  string
	TopK   int
	Filter store.Filter
}

type SearchHit struct {
	ID       string  `json:"-"`
	DocID    string  `json:"doc_id"`
	Text     string  `json:"text"`
	Position int     `json:"position"`
	Score    float64 `json:"-"`
}

func (a *App) Search(ctx context.Context, req SearchRequest) ([]SearchHit, error) {
	pr, err := principalScoped(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.policy.Authorize(pr, authz.PermSearch); err != nil {
		return nil, err
	}
	if a.cfg.Embedder == nil {
		return nil, fmt.Errorf("kit: search requires an Embedder: %w", core.ErrInvalidConfig)
	}
	if req.Query == "" {
		return nil, fmt.Errorf("kit: search: empty query: %w", core.ErrInvalidArgs)
	}
	topK := req.TopK
	if topK <= 0 {
		topK = a.cfg.TopK
	}

	traceID := id.New()
	started := time.Now()
	var hits []SearchHit
	tr := a.guardTrace(pr, traceID, "search", nil, started)
	tr.Input = req.Query
	defer func() {
		tr.EndTime = time.Now()
		tr.Metadata = map[string]any{"hits": len(hits)}
		a.writer.Enqueue(tr)
	}()

	vectors, err := call.Do(ctx, a.calls, call.Req{
		Kind: call.KindEmbed, Name: a.cfg.Embedder.Model(), Permission: authz.PermSearch, TraceID: traceID,
	}, func(ctx context.Context, span *call.Span) ([][]float32, error) {
		return a.cfg.Embedder.Query(ctx, []string{req.Query})
	})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, nil
	}

	nd, err := store.MSearchNDJSON(store.SearchQuery{
		Text:      req.Query,
		Vector:    vectors[0],
		Principal: pr,
		TopK:      topK,
		Filter:    req.Filter,
	})
	if err != nil {
		return nil, err
	}
	msearchReq := store.NDJSONRequest(http.MethodPost, "/"+store.IndexChunks+"/_msearch", nd)
	body, err := a.esDo(ctx, call.Req{
		Kind: call.KindSearch, Name: store.IndexChunks, Permission: authz.PermSearch, TraceID: traceID,
	}, msearchReq)
	if err != nil {
		return nil, err
	}

	lists, err := store.ParseMSearch(body)
	if err != nil {
		return nil, err
	}
	fused := store.RRF(lists, 0, topK)
	hits = make([]SearchHit, 0, len(fused))
	for _, h := range fused {
		var src SearchHit
		if err := json.Unmarshal(h.Source, &src); err != nil {
			return nil, fmt.Errorf("kit: search: decode hit: %w", err)
		}
		src.ID = h.ID
		src.Score = h.Score
		hits = append(hits, src)
	}
	return hits, nil
}

func (a *App) Delete(ctx context.Context, docID string) error {
	pr, err := principalScoped(ctx)
	if err != nil {
		return err
	}
	if err := a.policy.Authorize(pr, authz.PermDelete); err != nil {
		return err
	}
	body, err := store.DeleteByDocBody(docID, pr.Tenant)
	if err != nil {
		return err
	}
	req := store.RawRequest(http.MethodPost, "/"+store.IndexChunks+"/_delete_by_query", body, "application/json")
	req.Refresh = "true"
	req.Idempotent = true

	traceID := id.New()
	resp, err := a.esDo(ctx, call.Req{
		Kind: call.KindDelete, Name: store.IndexChunks, Permission: authz.PermDelete, TraceID: traceID,
	}, req)
	if err != nil {
		return err
	}
	if _, err := store.ParseDeleteByQuery(resp); err != nil {
		return fmt.Errorf("kit: delete: %w", err)
	}
	return nil
}

func (a *App) Prune(ctx context.Context, olderThan time.Duration) (int, error) {
	pr, err := principal(ctx)
	if err != nil {
		return 0, err
	}
	if err := a.policy.Authorize(pr, authz.PermPrune); err != nil {
		return 0, err
	}
	traceID := id.New()
	deleted := 0
	for _, stream := range []string{store.DataStreamTraces, store.DataStreamObservations} {
		req := store.RawRequest(http.MethodPost, "/"+stream+"/_delete_by_query", store.PruneBody(olderThan), "application/json")
		req.Refresh = "false"
		req.Idempotent = true
		resp, err := a.esDo(ctx, call.Req{
			Kind: call.KindDelete, Name: stream, Permission: authz.PermPrune, TraceID: traceID,
		}, req)
		if err != nil {
			return deleted, err
		}
		n, err := store.ParseDeleteByQuery(resp)
		if err != nil {
			return deleted, fmt.Errorf("kit: prune %s: %w", stream, err)
		}
		deleted += n
	}
	return deleted, nil
}

func (a *App) Score(ctx context.Context, s trace.Score) error {
	pr, err := principal(ctx)
	if err != nil {
		return err
	}
	if err := a.policy.Authorize(pr, authz.PermTrace); err != nil {
		return err
	}
	a.writer.EnqueueScore(s)
	return nil
}

type ErrorStrategy int

const (
	Strict ErrorStrategy = iota
	Warn
	Fallback
)

type GenerateRequest[T any] struct {
	Prompt           string
	System           string
	MaxTokens        int
	Temperature      *float64
	SessionID        string
	SchemaMode       model.SchemaMode
	ErrorStrategy    ErrorStrategy
	Fallback         *T
	Tags             []string
	StructuringModel model.Client
}

type Generated[T any] struct {
	Value        T
	Raw          string
	UsedFallback bool
	FinishReason model.FinishReason
}

func Generate[T any](ctx context.Context, app *App, req GenerateRequest[T]) (Generated[T], error) {
	var zero Generated[T]

	pr, err := principal(ctx)
	if err != nil {
		return zero, err
	}
	if err := app.policy.Authorize(pr, authz.PermGenerate); err != nil {
		return zero, err
	}
	if req.ErrorStrategy == Fallback && req.Fallback == nil {
		return zero, fmt.Errorf("kit: generate: Fallback strategy requires Fallback: %w", core.ErrInvalidConfig)
	}

	schema, err := schemaFor[T]()
	if err != nil {
		return zero, err
	}

	traceID := id.New()
	started := time.Now()
	tr := app.guardTrace(pr, traceID, "generate", req.Tags, started)
	defer func() {
		tr.EndTime = time.Now()
		app.writer.Enqueue(tr)
	}()

	prompt, _, err := app.guardText(ctx, guard.RailInput, "guard:input", req.Prompt, traceID)
	if err != nil {
		return zero, err
	}
	tr.Input = prompt

	msgs := make([]model.Message, 0, 2)
	if req.System != "" {
		msgs = append(msgs, model.Message{Role: model.RoleSystem, Content: req.System})
	}
	msgs = append(msgs, model.Message{Role: model.RoleUser, Content: prompt})

	mode := req.SchemaMode
	if mode == model.SchemaNative && !model.SupportsNativeSchema(app.cfg.LLM) {
		mode = model.SchemaInline
	}

	resp, err := call.Do(ctx, app.calls, call.Req{
		Kind: call.KindLLM, Name: app.cfg.LLM.Model(), Model: app.cfg.LLM.Model(), TraceID: traceID,
	}, func(ctx context.Context, span *call.Span) (model.Response, error) {
		span.Kind = trace.KindGeneration
		r, rerr := app.cfg.LLM.Chat(ctx, model.Request{
			Messages:    msgs,
			JSONSchema:  schema,
			SchemaMode:  mode,
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
		})
		span.Output = r.Message.Content
		span.Usage = trace.Usage{
			PromptTokens:     r.Usage.PromptTokens,
			CompletionTokens: r.Usage.CompletionTokens,
			TotalTokens:      r.Usage.TotalTokens,
		}
		span.Metadata = map[string]any{"finish_reason": string(r.FinishReason)}
		return r, rerr
	})
	if err != nil {
		return zero, err
	}
	tr.Usage = toTraceUsage(resp.Usage)

	text, _, err := app.guardText(ctx, guard.RailOutput, "guard:output", resp.Message.Content, traceID)
	if err != nil {
		return zero, err
	}
	tr.Output = text

	out := Generated[T]{Raw: text, FinishReason: resp.FinishReason}
	decodeErr := json.Unmarshal([]byte(strings.TrimSpace(text)), &out.Value)

	if decodeErr != nil && req.StructuringModel != nil {
		structured, serr := app.structure(ctx, req.StructuringModel, schema, mode,
			req.MaxTokens, req.Temperature, text, traceID)
		if serr == nil {
			if derr := json.Unmarshal([]byte(strings.TrimSpace(structured)), &out.Value); derr == nil {
				decodeErr = nil
			}
		}
	}

	if decodeErr != nil {
		switch req.ErrorStrategy {
		case Warn:
			app.logger().Warn("kit: generate: answer did not decode", "type", fmt.Sprintf("%T", out.Value), "err", decodeErr)
			return out, nil
		case Fallback:
			out.Value = *req.Fallback
			out.UsedFallback = true
			return out, nil
		default:
			return zero, fmt.Errorf("kit: generate: decode answer: %w: %w", decodeErr, core.ErrProvider)
		}
	}
	return out, nil
}

func (a *App) structure(ctx context.Context, sm model.Client, schema json.RawMessage, mode model.SchemaMode,
	maxTokens int, temperature *float64, prose, traceID string,
) (string, error) {
	return call.Do(ctx, a.calls, call.Req{
		Kind: call.KindLLM, Name: sm.Model(), Model: sm.Model(), TraceID: traceID,
	}, func(ctx context.Context, span *call.Span) (string, error) {
		span.Kind = trace.KindGeneration
		if span.Metadata == nil {
			span.Metadata = map[string]any{}
		}
		span.Metadata["structuring"] = true
		structured, serr := model.Structurer{
			Client:      sm,
			Schema:      schema,
			SchemaMode:  mode,
			MaxTokens:   maxTokens,
			Temperature: temperature,
		}.Structure(ctx, prose)
		span.Output = structured
		return structured, serr
	})
}

func (a *App) esDo(ctx context.Context, req call.Req, esReq store.Request) ([]byte, error) {
	return call.Do(ctx, a.calls, req, func(ctx context.Context, span *call.Span) ([]byte, error) {
		status, resp, err := a.cfg.ES.Do(ctx, esReq)
		if err != nil {
			return nil, fmt.Errorf("kit: es %s %s: %w: %w", esReq.Method, esReq.EffectivePath(), err, core.ErrProvider)
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("kit: es %s %s: status %d: %s: %w",
				esReq.Method, esReq.EffectivePath(), status, store.Snippet(resp), core.ErrProvider)
		}
		if span.Metadata == nil {
			span.Metadata = map[string]any{}
		}
		span.Metadata["status"] = status
		return resp, nil
	})
}

func toTraceUsage(u model.Usage) trace.Usage {
	return trace.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
}
