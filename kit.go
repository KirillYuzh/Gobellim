// Package kit wires the Elasticsearch client, the LLM, the embedder and the
// RBAC policy into one App and exposes Chat, Ingest, Search, Delete, Score and
// Generate.
package kit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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

type Config struct {
	ES  store.ES
	LLM model.Client
	// Embedder is required by Ingest and Search only; a chat-only App may leave it nil.
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
	TraceStoreRaw   bool
	Logger          *slog.Logger
}

type App struct {
	cfg     Config
	policy  *authz.Policy
	guard   *guard.Pipeline
	writer  *trace.Writer
	calls   call.Deps
	chunker *chunk.Chunker
	mu      sync.RWMutex
	tools   map[string]*ToolDef
	order   []string
}

// New validates the configuration and builds the App. It performs no network
// I/O; call Setup to create the Elasticsearch schema.
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

	layers := []guard.Detector{rules.New()}
	if cfg.GuardL2 != nil {
		layers = append(layers, cfg.GuardL2)
	}
	pipeline, err := guard.New(guard.Options{Layers: layers, MaxChars: cfg.GuardMaxChars, CacheSize: cfg.GuardCacheSize})
	if err != nil {
		return nil, err
	}
	chunker, err := chunk.New(cfg.Tokenizer, cfg.ChunkSize, cfg.ChunkOverlap)
	if err != nil {
		return nil, err
	}
	writer, err := trace.NewWriter(trace.Options{ES: cfg.ES, Logger: cfg.Logger})
	if err != nil {
		return nil, err
	}

	app := &App{
		cfg:     cfg,
		policy:  cfg.Policy,
		guard:   pipeline,
		writer:  writer,
		chunker: chunker,
		tools:   make(map[string]*ToolDef),
	}
	app.calls = call.Deps{Authz: cfg.Policy, Guard: pipeline, Tracer: tracerAdapter{writer}}
	return app, nil
}

// Setup creates the ILM policy and the data stream templates, plus the chunks
// index when an embedder with a known dimension is configured.
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

type tracerAdapter struct{ w *trace.Writer }

func (t tracerAdapter) Observe(_ context.Context, o trace.Observation) { t.w.EnqueueObservation(o) }

func principal(ctx context.Context) (core.Principal, error) {
	pr, ok := core.PrincipalFrom(ctx)
	if !ok || pr.IsZero() {
		return core.Principal{}, fmt.Errorf("kit: no principal in context: %w", core.ErrUnauthorized)
	}
	return pr, nil
}

type ChatRequest struct {
	// System is the server-side system prompt. A client message with the system
	// role is still discarded, so the application owns the instructions.
	System      string
	Messages    []model.Message
	SessionID   string
	Temperature *float64
	MaxTokens   int
}

// Chat runs the guard, model and tools loop and returns the final message.
// There is no streaming.
func (a *App) Chat(ctx context.Context, req ChatRequest) (out model.Message, err error) {
	pr, err := principal(ctx)
	if err != nil {
		return model.Message{}, err
	}
	traceID := id.New()
	started := time.Now()
	var total model.Usage

	tr := trace.Trace{
		ID: traceID, Name: "chat", UserID: pr.UserID, Tenant: pr.Tenant,
		SessionID: req.SessionID, StartTime: started,
	}
	defer func() {
		tr.EndTime = time.Now()
		tr.Output = out.Content
		tr.Usage = toTraceUsage(total)
		a.writer.Enqueue(tr)
	}()

	client := sanitizeMessages(req.Messages)
	if len(client) == 0 {
		return model.Message{}, fmt.Errorf("kit: chat: no user or assistant messages: %w", core.ErrInvalidArgs)
	}
	for i := range client {
		v, gerr := call.Guard(ctx, a.calls, call.Req{
			Kind: call.KindGuard, Name: "guard:input", Rail: guard.RailInput,
			Text: client[i].Content, TraceID: traceID,
		})
		if gerr != nil {
			return model.Message{}, gerr
		}
		client[i].Content = v.Text
	}

	msgs := make([]model.Message, 0, len(client)+1)
	if req.System != "" {
		msgs = append(msgs, model.Message{Role: model.RoleSystem, Content: req.System})
	}
	msgs = append(msgs, client...)

	specs := a.allowedToolSpecs(pr)
	callsPerTool := make(map[string]int)
	invalidTries := make(map[string]int)

	for step := 0; step < a.cfg.MaxSteps; step++ {
		resp, cerr := call.Do(ctx, a.calls, call.Req{
			Kind: call.KindLLM, Name: a.cfg.LLM.Model(), Model: a.cfg.LLM.Model(), TraceID: traceID,
		}, func(ctx context.Context, span *call.Span) (model.Response, error) {
			span.Kind = trace.KindGeneration
			r, rerr := a.cfg.LLM.Chat(ctx, model.Request{
				Messages:    msgs,
				Tools:       specs,
				Temperature: req.Temperature,
				MaxTokens:   req.MaxTokens,
			})
			span.Output = r.Message.Content
			span.Usage = trace.Usage{
				PromptTokens:     r.Usage.PromptTokens,
				CompletionTokens: r.Usage.CompletionTokens,
				TotalTokens:      r.Usage.TotalTokens,
			}
			return r, rerr
		})
		if cerr != nil {
			return model.Message{}, cerr
		}
		total.PromptTokens += resp.Usage.PromptTokens
		total.CompletionTokens += resp.Usage.CompletionTokens
		total.TotalTokens += resp.Usage.TotalTokens

		if len(resp.Message.ToolCalls) == 0 {
			v, gerr := call.Guard(ctx, a.calls, call.Req{
				Kind: call.KindGuard, Name: "guard:output", Rail: guard.RailOutput,
				Text: resp.Message.Content, TraceID: traceID,
			})
			if gerr != nil {
				return model.Message{}, gerr
			}
			final := resp.Message
			final.Content = v.Text
			return final, nil
		}

		msgs = append(msgs, resp.Message)
		for _, tc := range resp.Message.ToolCalls {
			result, terr := a.execTool(ctx, tc, pr, traceID, callsPerTool, invalidTries)
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

func (a *App) execTool(ctx context.Context, tc model.ToolCall, pr core.Principal, traceID string, callsPerTool, invalidTries map[string]int) (string, error) {
	a.mu.RLock()
	def := a.tools[tc.Name]
	a.mu.RUnlock()
	if def == nil {
		return "", fmt.Errorf("kit: tool %q: %w", tc.Name, core.ErrToolNotFound)
	}

	if err := a.policy.Authorize(pr, def.permission); err != nil {
		return "", err
	}
	callsPerTool[tc.Name]++
	if callsPerTool[tc.Name] > a.cfg.MaxCallsPerTool {
		return "", fmt.Errorf("kit: tool %q exceeded max_calls=%d: %w", tc.Name, a.cfg.MaxCallsPerTool, core.ErrMaxSteps)
	}
	if len(tc.Args) > a.cfg.ArgsMaxBytes {
		return "", fmt.Errorf("kit: tool %q arguments exceed %d bytes: %w", tc.Name, a.cfg.ArgsMaxBytes, core.ErrInvalidArgs)
	}
	if err := def.validate(tc.Args); err != nil {
		invalidTries[tc.Name]++
		if invalidTries[tc.Name] > 1 {
			return "", err
		}

		return fmt.Sprintf("invalid arguments: %v", err), nil
	}

	out, err := call.Do(ctx, a.calls, call.Req{
		Kind:       call.KindTool,
		Name:       tc.Name,
		Permission: def.permission,
		TraceID:    traceID,
	}, func(ctx context.Context, span *call.Span) (string, error) {
		tctx, cancel := context.WithTimeout(ctx, a.cfg.ToolTimeout)
		defer cancel()
		res, ierr := def.invoke(tctx, tc.Args)
		span.Output = res
		return res, ierr
	})
	if err != nil {
		return "", err
	}

	v, err := call.Guard(ctx, a.calls, call.Req{
		Kind: call.KindGuard, Name: "guard:tool_result", Rail: guard.RailToolResult,
		Text: out, TraceID: traceID,
	})
	if err == nil {
		return v.Text, nil
	}
	if errors.Is(err, core.ErrGuardReject) {
		return "[blocked by guard]", nil
	}
	return "", err
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
	pr, err := principal(ctx)
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
	tr := trace.Trace{ID: traceID, Name: "ingest", UserID: pr.UserID, Tenant: pr.Tenant, StartTime: started, Input: doc.Text}
	var indexed, quarantined int
	defer func() {
		tr.EndTime = time.Now()
		tr.Metadata = map[string]any{"indexed": indexed, "quarantined": quarantined}
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

	if _, err := a.esDo(ctx, call.Req{
		Kind: call.KindIngest, Name: store.IndexChunks, Permission: authz.PermIngest, TraceID: traceID,
	}, http.MethodPost, "/_bulk", func() ([]byte, error) { return store.ChunksBulkBody(chunks) }); err != nil {
		return err
	}
	return nil
}

type SearchRequest struct {
	Query string
	TopK  int
}

type SearchHit struct {
	ID       string  `json:"-"`
	DocID    string  `json:"doc_id"`
	Text     string  `json:"text"`
	Position int     `json:"position"`
	Score    float64 `json:"-"`
}

func (a *App) Search(ctx context.Context, req SearchRequest) ([]SearchHit, error) {
	pr, err := principal(ctx)
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
	tr := trace.Trace{ID: traceID, Name: "search", UserID: pr.UserID, Tenant: pr.Tenant, StartTime: started, Input: req.Query}
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

	body, err := a.esDo(ctx, call.Req{
		Kind: call.KindSearch, Name: store.IndexChunks, Permission: authz.PermSearch, TraceID: traceID,
	}, http.MethodPost, "/"+store.IndexChunks+"/_msearch", func() ([]byte, error) {
		return store.MSearchNDJSON(store.SearchQuery{Text: req.Query, Vector: vectors[0], Principal: pr, TopK: topK})
	})
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
	pr, err := principal(ctx)
	if err != nil {
		return err
	}
	if err := a.policy.Authorize(pr, authz.PermDelete); err != nil {
		return err
	}
	query := map[string]any{"query": map[string]any{"bool": map[string]any{"filter": []any{
		map[string]any{"term": map[string]any{"doc_id": docID}},
		map[string]any{"term": map[string]any{store.TenantField: pr.Tenant}},
	}}}}
	_, err = a.esDo(ctx, call.Req{
		Kind: call.KindIngest, Name: store.IndexChunks, Permission: authz.PermDelete,
	}, http.MethodPost, "/"+store.IndexChunks+"/_delete_by_query", func() ([]byte, error) {
		return json.Marshal(query)
	})
	return err
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

type GenerateRequest struct {
	Prompt      string
	System      string
	MaxTokens   int
	Temperature *float64
	SessionID   string
}

// Generate asks the model for a value of type T using a JSON Schema derived
// from T, then decodes the answer into T.
func Generate[T any](ctx context.Context, app *App, req GenerateRequest) (T, error) {
	var zero T

	schema, err := schemaFor[T]()
	if err != nil {
		return zero, err
	}

	msgs := make([]model.Message, 0, 2)
	if req.System != "" {
		msgs = append(msgs, model.Message{Role: model.RoleUser, Content: req.System})
	}
	msgs = append(msgs, model.Message{Role: model.RoleUser, Content: req.Prompt})

	traceID := id.New()
	started := time.Now()
	tr := trace.Trace{ID: traceID, Name: "generate", StartTime: started, Input: req.Prompt, SessionID: req.SessionID}

	resp, err := call.Do(ctx, app.calls, call.Req{
		Kind: call.KindLLM, Name: app.cfg.LLM.Model(), Model: app.cfg.LLM.Model(), TraceID: traceID,
	}, func(ctx context.Context, span *call.Span) (model.Response, error) {
		span.Kind = trace.KindGeneration
		r, rerr := app.cfg.LLM.Chat(ctx, model.Request{
			Messages:    msgs,
			JSONSchema:  schema,
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
		})
		span.Output = r.Message.Content
		span.Usage = trace.Usage{PromptTokens: r.Usage.PromptTokens, CompletionTokens: r.Usage.CompletionTokens, TotalTokens: r.Usage.TotalTokens}
		return r, rerr
	})
	if err != nil {
		tr.EndTime = time.Now()
		app.writer.Enqueue(tr)
		return zero, err
	}

	v, err := call.Guard(ctx, app.calls, call.Req{
		Kind: call.KindGuard, Name: "guard:output", Rail: guard.RailOutput,
		Text: resp.Message.Content, TraceID: traceID,
	})
	tr.EndTime = time.Now()
	if err != nil {
		app.writer.Enqueue(tr)
		return zero, err
	}
	tr.Output = v.Text
	tr.Usage = toTraceUsage(resp.Usage)
	app.writer.Enqueue(tr)

	var out T
	if err := json.Unmarshal([]byte(v.Text), &out); err != nil {
		return zero, fmt.Errorf("kit: generate: decode %T: %w: %w", out, err, core.ErrProvider)
	}
	return out, nil
}

func (a *App) esDo(ctx context.Context, req call.Req, method, path string, build func() ([]byte, error)) ([]byte, error) {
	return call.Do(ctx, a.calls, req, func(ctx context.Context, span *call.Span) ([]byte, error) {
		body, err := build()
		if err != nil {
			return nil, err
		}
		status, resp, err := a.cfg.ES.Do(ctx, method, path, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("kit: es %s %s: %w: %w", method, path, err, core.ErrProvider)
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("kit: es %s %s: status %d: %s: %w", method, path, status, store.Snippet(resp), core.ErrProvider)
		}
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
