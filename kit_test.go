package kit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/guard/guardtest"
	"github.com/KirillYuzh/Gobellim/internal/call"
	"github.com/KirillYuzh/Gobellim/model"
	"github.com/KirillYuzh/Gobellim/store"
	"github.com/KirillYuzh/Gobellim/trace"
)

func TestReflectSchemaStripsMetaAndForbidsExtra(t *testing.T) {
	type in struct {
		ID string `json:"id"`
	}
	raw, err := schemaFor[in]()
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "$schema") || strings.Contains(s, "$id") {
		t.Fatalf("$schema/$id must be stripped: %s", s)
	}
	if !strings.Contains(s, `"additionalProperties":false`) {
		t.Fatalf("additionalProperties must be false: %s", s)
	}
}

func TestToolValidatesArgs(t *testing.T) {
	app := &App{tools: map[string]*ToolDef{}}
	type in struct {
		ID string `json:"id"`
	}
	def, err := Tool(app, "get_ticket", func(context.Context, in) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := def.validate(`{"id":"1"}`); err != nil {
		t.Fatalf("valid args rejected: %v", err)
	}
	if err := def.validate(`{"id":"1","extra":true}`); err == nil {
		t.Fatal("unknown field must be rejected")
	}
	if err := def.validate(`not json`); err == nil {
		t.Fatal("invalid JSON must be rejected")
	}
	if !strings.Contains(string(def.Schema()), `"required":["id"]`) {
		t.Fatalf("id must be required: %s", def.Schema())
	}
	if def.Permission() != "tool:get_ticket" {
		t.Fatalf("permission = %s", def.Permission())
	}
}

func TestSanitizeMessagesDropsClientToolRoles(t *testing.T) {
	in := []model.Message{
		{Role: model.RoleSystem, Content: "sys"},
		{Role: model.RoleUser, Content: "hi"},
		{Role: model.RoleAssistant, Content: "yo", ToolCalls: []model.ToolCall{{ID: "1", Name: "x"}}},
		{Role: model.RoleTool, Content: "res", ToolCallID: "1"},
	}
	out := sanitizeMessages(in)
	if len(out) != 2 {
		t.Fatalf("got %+v", out)
	}
	if out[0].Role != model.RoleUser || out[1].Role != model.RoleAssistant {
		t.Fatalf("only user/assistant must survive: %+v", out)
	}
	if len(out[1].ToolCalls) != 0 || out[1].ToolCallID != "" {
		t.Fatalf("tool_calls and tool_call_id must be dropped: %+v", out[1])
	}
}

const testPolicy = `
version: 1
default: deny
roles:
  user:
    permissions:
      - chat
      - tool:open
  admin:
    permissions:
      - chat
      - ingest
      - search
`

type discardES struct{}

func (discardES) Do(context.Context, store.Request) (int, []byte, error) {
	return 200, []byte(`{}`), nil
}

type fakeLLM struct {
	got   model.Request
	calls int
}

func (f *fakeLLM) Model() string { return "fake" }

func (f *fakeLLM) Chat(_ context.Context, req model.Request) (model.Response, error) {
	f.calls++
	f.got = req
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "ok"},
		Usage:   model.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}, nil
}

func newTestApp(t *testing.T, llm model.Client) *App {
	return newTestAppWithPolicy(t, llm, testPolicy)
}

func newTestAppWithPolicy(t *testing.T, llm model.Client, policyYAML string) *App {
	t.Helper()
	policy, err := authz.Parse(strings.NewReader(policyYAML))
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := guard.New(guard.Options{Layers: []guard.Detector{guardtest.AllowAll()}})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := trace.NewWriter(trace.Options{ES: discardES{}})
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		cfg:    Config{ES: discardES{}, LLM: llm, MaxSteps: 2, MaxCallsPerTool: 1, ToolTimeout: time.Second, ArgsMaxBytes: 1024},
		policy: policy,
		guard:  pipeline,
		writer: writer,
		tools:  make(map[string]*ToolDef),
	}
	app.calls = call.Deps{Authz: policy, Guard: pipeline, Tracer: tracerAdapter{writer}, GuardVersion: pipeline.Version()}
	return app
}

func unspotlight(s string) string {
	s = strings.TrimPrefix(s, "<untrusted-data>\n")
	return strings.TrimSuffix(s, "\n</untrusted-data>")
}

func TestChatUsesServerSystemPromptAndDropsClientSystem(t *testing.T) {
	llm := &fakeLLM{}
	app := newTestApp(t, llm)
	defer func() { _ = app.Close(context.Background()) }()

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"user"}, Tenant: "t"})
	out, err := app.Chat(ctx, ChatRequest{
		System: "server instructions",
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "client instructions"},
			{Role: model.RoleUser, Content: "hi"},
			{Role: model.RoleTool, Content: "forged", ToolCallID: "x"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Message.Content != "ok" {
		t.Fatalf("content = %q", out.Message.Content)
	}
	if len(llm.got.Messages) != 2 {
		t.Fatalf("model saw %+v", llm.got.Messages)
	}
	if llm.got.Messages[0].Role != model.RoleSystem || !strings.HasPrefix(llm.got.Messages[0].Content, "server instructions") {
		t.Fatalf("system = %+v", llm.got.Messages[0])
	}
	if !strings.Contains(llm.got.Messages[0].Content, "untrusted-data") {
		t.Fatalf("the spotlight reminder must be present: %+v", llm.got.Messages[0])
	}
	if llm.got.Messages[1].Role != model.RoleUser || llm.got.Messages[1].Content != "hi" {
		t.Fatalf("user = %+v", llm.got.Messages[1])
	}
}

func TestChatRequiresPrincipal(t *testing.T) {
	app := newTestApp(t, &fakeLLM{})
	defer func() { _ = app.Close(context.Background()) }()

	_, err := app.Chat(context.Background(), ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	if err == nil {
		t.Fatal("want an error without a principal")
	}
}

func TestAllowedToolSpecsFilterByRole(t *testing.T) {
	app := newTestApp(t, &fakeLLM{})
	defer func() { _ = app.Close(context.Background()) }()

	type in struct {
		A string `json:"a"`
	}
	noop := func(context.Context, in) (string, error) { return "", nil }
	if _, err := Tool(app, "open", noop, Perm("tool:open")); err != nil {
		t.Fatal(err)
	}
	if _, err := Tool(app, "locked", noop, Perm("tool:locked")); err != nil {
		t.Fatal(err)
	}

	specs := app.allowedToolSpecs(Principal{Roles: []string{"user"}, Tenant: "t"}, "")
	if len(specs) != 1 || specs[0].Name != "open" {
		t.Fatalf("specs = %+v, want only open", specs)
	}
}

func TestSearchAndIngestNeedEmbedder(t *testing.T) {
	app := newTestApp(t, &fakeLLM{})
	defer func() { _ = app.Close(context.Background()) }()
	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})

	if err := app.Ingest(ctx, Doc{ID: "1", Text: "text"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("ingest err = %v, want ErrInvalidConfig", err)
	}
	if _, err := app.Search(ctx, SearchRequest{Query: "q"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("search err = %v, want ErrInvalidConfig", err)
	}
}

const (
	testPolicyAdmin = `
version: 1
default: deny
roles:
  admin:
    permissions:
      - "*"
`
	testPolicyGenerate = `
version: 1
default: deny
roles:
  admin:
    permissions:
      - generate
`
)

type scripted struct {
	t     *testing.T
	queue []model.Response
	got   []model.Request
}

func (s *scripted) Model() string { return "scripted" }

func (s *scripted) Chat(_ context.Context, req model.Request) (model.Response, error) {
	s.got = append(s.got, req)
	if len(s.queue) == 0 {
		s.t.Fatalf("model called more times than scripted")
	}
	r := s.queue[0]
	s.queue = s.queue[1:]
	return r, nil
}

func toolCallResponse(name, args string) model.Response {
	return model.Response{Message: model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{{ID: "call-1", Name: name, Args: args}},
	}}
}

func textResponse(text string) model.Response {
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: text},
		Usage:   model.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}
}

func TestToolErrorBecomesResult(t *testing.T) {
	type boomIn struct {
		Reason string `json:"reason,omitempty"`
	}
	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("boom", `{"reason":"x"}`),
		textResponse("recovered"),
	}}
	app := newTestAppWithPolicy(t, llm, testPolicyAdmin)
	defer func() { _ = app.Close(context.Background()) }()

	if _, err := Tool(app, "boom", func(context.Context, boomIn) (string, error) {
		return "", errors.New("kaboom")
	}); err != nil {
		t.Fatal(err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	out, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("chat = %v, want the tool error to be a result", err)
	}
	if out.Message.Content != "recovered" {
		t.Fatalf("out = %+v", out)
	}
	if len(llm.got) != 2 {
		t.Fatalf("model calls = %d, want 2", len(llm.got))
	}
	msgs := llm.got[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != model.RoleTool || last.ToolCallID != "call-1" || !strings.Contains(last.Content, "kaboom") {
		t.Fatalf("tool result = %+v, want the error text", last)
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(unspotlight(last.Content)), &parsed); err != nil {
		t.Fatalf("tool error result is not JSON: %v (%s)", err, last.Content)
	}
	if parsed["error"] == "" {
		t.Fatalf("tool error result = %s, want an error field", last.Content)
	}
}

func TestChatLastStepForcesFinalAnswer(t *testing.T) {
	type in struct {
		A string `json:"a"`
	}
	llm := &scripted{t: t, queue: []model.Response{toolCallResponse("open", `{"a":"1"}`)}}
	app := newTestAppWithPolicy(t, llm, testPolicyAdmin)
	defer func() { _ = app.Close(context.Background()) }()
	app.cfg.MaxSteps = 1
	if _, err := Tool(app, "open", func(context.Context, in) (string, error) { return "ok", nil }); err != nil {
		t.Fatal(err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	_, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	if got := llm.got[0].ToolChoice; got != "none" {
		t.Fatalf("last-step tool_choice = %q, want \"none\"", got)
	}
}

func TestChatBudgetExceeded(t *testing.T) {
	llm := &scripted{t: t, queue: []model.Response{textResponse("ok")}}
	app := newTestApp(t, llm)
	defer func() { _ = app.Close(context.Background()) }()
	app.cfg.MaxTotalTokens = 1

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"user"}, Tenant: "t"})
	_, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("err = %v, want ErrBudget", err)
	}
}

func TestGenerateNeedsPrincipalAndPermission(t *testing.T) {
	type ticket struct {
		Severity int    `json:"severity"`
		Summary  string `json:"summary"`
	}
	ctx := context.Background()

	llm := &scripted{t: t, queue: []model.Response{textResponse(`{"severity":2,"summary":"ok"}`)}}
	noPerm := newTestAppWithPolicy(t, llm, testPolicy)
	defer func() { _ = noPerm.Close(context.Background()) }()
	if _, err := Generate[ticket](ctx, noPerm, GenerateRequest[ticket]{Prompt: "x"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("without a principal err = %v, want ErrUnauthorized", err)
	}
	withPrincipal := WithPrincipal(ctx, Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := Generate[ticket](withPrincipal, noPerm, GenerateRequest[ticket]{Prompt: "x"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("without the permission err = %v, want ErrUnauthorized", err)
	}

	allowed := newTestAppWithPolicy(t, &scripted{t: t, queue: []model.Response{textResponse(`{"severity":2,"summary":"ok"}`)}}, testPolicyGenerate)
	defer func() { _ = allowed.Close(context.Background()) }()
	got, err := Generate[ticket](withPrincipal, allowed, GenerateRequest[ticket]{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Value.Severity != 2 || got.Value.Summary != "ok" {
		t.Fatalf("value = %+v", got.Value)
	}
	if got.UsedFallback {
		t.Fatal("a decoded answer must not be marked as a fallback")
	}
}

func TestGenerateFallbackStrategy(t *testing.T) {
	testGen := func(t *testing.T, strategy ErrorStrategy) Generated[int] {
		t.Helper()
		llm := &scripted{t: t, queue: []model.Response{textResponse("not json at all")}}
		app := newTestAppWithPolicy(t, llm, testPolicyGenerate)
		defer func() { _ = app.Close(context.Background()) }()
		ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
		fb := 42
		got, err := Generate[int](ctx, app, GenerateRequest[int]{Prompt: "x", ErrorStrategy: strategy, Fallback: &fb})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	t.Run("fallback", func(t *testing.T) {
		got := testGen(t, Fallback)
		if !got.UsedFallback || got.Value != 42 {
			t.Fatalf("got = %+v, want the fallback value flagged", got)
		}
	})
	t.Run("warn", func(t *testing.T) {
		got := testGen(t, Warn)
		if got.UsedFallback || got.Raw == "" {
			t.Fatalf("got = %+v, want the raw text and no fallback flag", got)
		}
	})
}

func TestGenerateFallbackRequiresValue(t *testing.T) {
	llm := &scripted{t: t, queue: []model.Response{textResponse("1")}}
	app := newTestAppWithPolicy(t, llm, testPolicyGenerate)
	defer func() { _ = app.Close(context.Background()) }()
	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := Generate[int](ctx, app, GenerateRequest[int]{Prompt: "x", ErrorStrategy: Fallback}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestEgressHostAllowlist(t *testing.T) {
	app := &App{cfg: Config{}}
	def := &ToolDef{name: "fetch", effect: EffectEgress, egressHosts: []string{"api.example.com"}}

	if err := app.checkEgress(def, `{"url":"https://api.example.com/v1"}`, &turn{}); err != nil {
		t.Fatalf("allowed host rejected: %v", err)
	}
	if err := app.checkEgress(def, `{"url":"https://evil.example.net/v1"}`, &turn{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("host outside the allowlist err = %v, want ErrUnauthorized", err)
	}
	if err := app.checkEgress(def, `{"url":"https://cdn.api.example.com/x"}`, &turn{}); err != nil {
		t.Fatalf("subdomain of an allowed host rejected: %v", err)
	}
	read := &ToolDef{name: "get", effect: EffectRead}
	if err := app.checkEgress(read, `{"url":"https://evil.example.net"}`, &turn{}); err != nil {
		t.Fatalf("read tool must not be egress-checked: %v", err)
	}
}

func TestRuleOfTwoIsOnByDefaultInTheEgressCheck(t *testing.T) {
	app := &App{cfg: Config{}}
	def := &ToolDef{name: "send", effect: EffectEgress, egressAny: true}

	if err := app.checkEgress(def, `{}`, &turn{}); err != nil {
		t.Fatalf("clean turn: %v", err)
	}
	if err := app.checkEgress(def, `{}`, &turn{untrusted: true}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("untrusted turn err = %v, want ErrUnauthorized", err)
	}
}

func TestEgressWithoutHostPolicyIsDenied(t *testing.T) {
	app := &App{cfg: Config{}}
	open := &ToolDef{name: "open", effect: EffectEgress}
	if err := app.checkEgress(open, `{"url":"https://any.example.com"}`, &turn{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("egress without a host policy err = %v, want ErrUnauthorized", err)
	}

	allowed := &ToolDef{name: "allowed", effect: EffectEgress, egressAny: true}
	if err := app.checkEgress(allowed, `{"url":"https://any.example.com"}`, &turn{}); err != nil {
		t.Fatalf("EgressAny rejected: %v", err)
	}
}

func TestApprovalIsRequiredAndBound(t *testing.T) {
	app := &App{cfg: Config{}}
	def := &ToolDef{name: "pay", requireApproval: true}
	tn := &turn{principal: Principal{UserID: "u", Tenant: "t"}}

	if err := app.approve(context.Background(), def, `{"amount":1}`, tn); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("without an approver err = %v, want ErrApprovalRequired", err)
	}

	var seen ApprovalRequest
	app.cfg.Approver = ApproverFunc(func(_ context.Context, req ApprovalRequest) (bool, error) {
		seen = req
		return true, nil
	})
	if err := app.approve(context.Background(), def, `{"amount":1}`, tn); err != nil {
		t.Fatal(err)
	}
	if seen.Tool != "pay" || seen.Fingerprint != def.Fingerprint(`{"amount":1}`) {
		t.Fatalf("approval request = %+v, want the tool fingerprint", seen)
	}

	app.cfg.Approver = ApproverFunc(func(context.Context, ApprovalRequest) (bool, error) { return false, nil })
	if err := app.approve(context.Background(), def, `{"amount":1}`, tn); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("denied err = %v, want ErrApprovalRequired", err)
	}
}

func TestFingerprintIgnoresKeyOrder(t *testing.T) {
	def := &ToolDef{name: "pay"}
	a := def.Fingerprint(`{"amount":1,"to":"bob"}`)
	b := def.Fingerprint(`{"to":"bob","amount":1}`)
	if a != b {
		t.Fatalf("fingerprints differ: %s != %s", a, b)
	}
	if a == def.Fingerprint(`{"amount":2,"to":"bob"}`) {
		t.Fatal("different arguments must produce a different fingerprint")
	}
}

func TestIngestAndSearchRequireTenant(t *testing.T) {
	app := newTestAppWithPolicy(t, &fakeLLM{}, testPolicyAdmin)
	defer func() { _ = app.Close(context.Background()) }()
	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}})

	if err := app.Ingest(ctx, Doc{ID: "1", Text: "t"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ingest without a tenant err = %v, want ErrUnauthorized", err)
	}
	if _, err := app.Search(ctx, SearchRequest{Query: "q"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("search without a tenant err = %v, want ErrUnauthorized", err)
	}
}

func TestPruneNeedsPermission(t *testing.T) {
	app := newTestApp(t, &fakeLLM{})
	defer func() { _ = app.Close(context.Background()) }()
	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Prune(ctx, time.Hour); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestRetrievalToolNeedsEmbedder(t *testing.T) {
	app := newTestApp(t, &fakeLLM{})
	defer func() { _ = app.Close(context.Background()) }()
	if _, err := RetrievalTool(app, ""); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

type windowLLM struct{ window int }

func (w windowLLM) Model() string { return "windowed" }

func (w windowLLM) Chat(context.Context, model.Request) (model.Response, error) {
	return model.Response{}, nil
}

func (w windowLLM) ContextTokens() int { return w.window }

func TestNewRejectsGuardWindowWiderThanModelContext(t *testing.T) {
	policy, err := authz.Parse(strings.NewReader(testPolicy))
	if err != nil {
		t.Fatal(err)
	}

	_, err = New(context.Background(), Config{
		ES: discardES{}, LLM: windowLLM{window: 1000}, Policy: policy, GuardMaxChars: 2000,
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig when the guard window exceeds the model context", err)
	}

	app, err := New(context.Background(), Config{
		ES: discardES{}, LLM: windowLLM{window: 100000}, Policy: policy, GuardMaxChars: 2000,
	})
	if err != nil {
		t.Fatalf("a wide enough context window must be accepted: %v", err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	plain, err := New(context.Background(), Config{ES: discardES{}, LLM: &fakeLLM{}, Policy: policy, GuardMaxChars: 2000})
	if err != nil {
		t.Fatalf("a client without ContextWindow must be accepted: %v", err)
	}
	defer func() { _ = plain.Close(context.Background()) }()
}

func newRecorderApp(t *testing.T, llm model.Client, policyYAML string) (*App, *trace.Recorder) {
	t.Helper()
	policy, err := authz.Parse(strings.NewReader(policyYAML))
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := guard.New(guard.Options{Layers: []guard.Detector{guardtest.AllowAll()}})
	if err != nil {
		t.Fatal(err)
	}
	rec := trace.NewRecorder()
	writer, err := trace.NewWriter(trace.Options{ES: discardES{}, Sinks: []trace.Sink{rec}})
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		cfg:    Config{ES: discardES{}, LLM: llm, MaxSteps: 2, MaxCallsPerTool: 3, ToolTimeout: time.Second, ArgsMaxBytes: 1024},
		policy: policy,
		guard:  pipeline,
		writer: writer,
		tools:  make(map[string]*ToolDef),
	}
	app.calls = call.Deps{Authz: policy, Guard: pipeline, Tracer: tracerAdapter{writer}, GuardVersion: pipeline.Version()}
	return app, rec
}

func TestBeforeToolHookSubstitutesAndSkipsExecution(t *testing.T) {
	type in struct {
		A string `json:"a"`
	}
	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("lookup", `{"a":"1"}`),
		textResponse("done"),
	}}
	app := newTestAppWithPolicy(t, llm, testPolicyAdmin)
	defer func() { _ = app.Close(context.Background()) }()

	executed := false
	var got ToolCallInfo
	if _, err := Tool(app, "lookup", func(context.Context, in) (string, error) {
		executed = true
		return "real-result", nil
	}, BeforeToolCall(func(_ context.Context, info ToolCallInfo) (bool, string, error) {
		got = info
		return false, "cached-result", nil
	})); err != nil {
		t.Fatal(err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	if executed {
		t.Fatal("tool must not execute when the before hook does not proceed")
	}
	last := llm.got[1].Messages[len(llm.got[1].Messages)-1]
	if last.Role != model.RoleTool || unspotlight(last.Content) != "cached-result" {
		t.Fatalf("tool result = %+v, want the substituted output", last)
	}
	if got.Tool != "lookup" || got.CallIndex != 1 || got.Effect != EffectRead {
		t.Fatalf("info = %+v, want the resolved call info", got)
	}
	if got.Fingerprint == "" || got.UserID != "u" || got.Tenant != "t" {
		t.Fatalf("info = %+v, want user/tenant/fingerprint", got)
	}
}

func TestAfterToolHookRewritesAndRunsOnFailure(t *testing.T) {
	type in struct {
		A string `json:"a"`
	}
	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("boom", `{"a":"1"}`),
		textResponse("done"),
	}}
	app := newTestAppWithPolicy(t, llm, testPolicyAdmin)
	defer func() { _ = app.Close(context.Background()) }()

	ran := false
	if _, err := Tool(app, "boom", func(context.Context, in) (string, error) {
		return "", errors.New("kaboom")
	}, AfterToolCall(func(_ context.Context, _ ToolCallInfo, result string, err error) (string, error) {
		ran = true
		if err == nil || !strings.Contains(err.Error(), "kaboom") {
			t.Fatalf("after hook err = %v, want the tool error", err)
		}
		return `{"recovered":true}`, nil
	})); err != nil {
		t.Fatal(err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("the after hook must run on failure")
	}
	last := llm.got[1].Messages[len(llm.got[1].Messages)-1]
	if last.Role != model.RoleTool || unspotlight(last.Content) != `{"recovered":true}` {
		t.Fatalf("tool result = %+v, want the rewritten result", last)
	}
}

func TestToolHookOverridesAppHook(t *testing.T) {
	type in struct {
		A string `json:"a"`
	}
	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("t1", `{"a":"1"}`),
		textResponse("done"),
	}}
	app := newTestAppWithPolicy(t, llm, testPolicyAdmin)
	defer func() { _ = app.Close(context.Background()) }()

	appHookCalled := false
	app.cfg.BeforeToolCall = func(context.Context, ToolCallInfo) (bool, string, error) {
		appHookCalled = true
		return false, "app-level", nil
	}
	if _, err := Tool(app, "t1", func(context.Context, in) (string, error) { return "real", nil },
		BeforeToolCall(func(context.Context, ToolCallInfo) (bool, string, error) {
			return false, "tool-level", nil
		})); err != nil {
		t.Fatal(err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	last := llm.got[1].Messages[len(llm.got[1].Messages)-1]
	if unspotlight(last.Content) != "tool-level" {
		t.Fatalf("result = %q, want the tool-level hook output", last.Content)
	}
	if appHookCalled {
		t.Fatal("the app-level hook must not run when the tool set its own")
	}
}

func TestAppToolHookRunsWhenToolHasNone(t *testing.T) {
	type in struct {
		A string `json:"a"`
	}
	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("plain", `{"a":"1"}`),
		textResponse("done"),
	}}
	app := newTestAppWithPolicy(t, llm, testPolicyAdmin)
	defer func() { _ = app.Close(context.Background()) }()

	app.cfg.BeforeToolCall = func(context.Context, ToolCallInfo) (bool, string, error) {
		return false, "app-level", nil
	}
	if _, err := Tool(app, "plain", func(context.Context, in) (string, error) { return "real", nil }); err != nil {
		t.Fatal(err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	last := llm.got[1].Messages[len(llm.got[1].Messages)-1]
	if unspotlight(last.Content) != "app-level" {
		t.Fatalf("result = %q, want the app-level hook output", last.Content)
	}
}

func TestApprovalIsConsumedOncePerTurn(t *testing.T) {
	type in struct {
		Amount int `json:"amount"`
	}
	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("pay", `{"amount":1}`),
		toolCallResponse("pay", `{"amount":1}`),
	}}
	app := newTestAppWithPolicy(t, llm, testPolicyAdmin)
	defer func() { _ = app.Close(context.Background()) }()
	app.cfg.MaxSteps = 3
	app.cfg.MaxCallsPerTool = 3
	app.cfg.Approver = ApproverFunc(func(context.Context, ApprovalRequest) (bool, error) { return true, nil })

	runs := 0
	if _, err := Tool(app, "pay", func(context.Context, in) (string, error) {
		runs++
		return "paid", nil
	}, RequireApproval(true)); err != nil {
		t.Fatal(err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	_, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("err = %v, want ErrApprovalRequired for a replayed approval", err)
	}
	if runs != 1 {
		t.Fatalf("tool ran %d times, want exactly 1", runs)
	}
}

func TestWithTraceMetadataAppearsOnObservations(t *testing.T) {
	llm := &scripted{t: t, queue: []model.Response{{
		Message:      model.Message{Role: model.RoleAssistant, Content: "ok"},
		Usage:        model.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		FinishReason: model.FinishStop,
	}}}
	app, rec := newRecorderApp(t, llm, testPolicyAdmin)
	defer func() { _ = app.Close(context.Background()) }()

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	ctx = WithTraceMetadata(ctx, map[string]any{"ab_arm": "b", "finish_reason": "ctx"})
	if md := TraceMetadataFrom(ctx); md["ab_arm"] != "b" {
		t.Fatalf("TraceMetadataFrom = %+v", md)
	}

	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}

	snap := rec.Snapshot()
	if len(snap.Observations) == 0 {
		t.Fatal("no observations recorded")
	}
	var sawGeneration bool
	for _, o := range snap.Observations {
		if o.Metadata["ab_arm"] != "b" {
			t.Fatalf("context metadata missing from observation %s: %+v", o.Name, o.Metadata)
		}
		if o.Kind == trace.KindGeneration {
			sawGeneration = true
			if o.Metadata["finish_reason"] != string(model.FinishStop) {
				t.Fatalf("per-call finish_reason must win over the context value: %+v", o.Metadata)
			}
		}
	}
	if !sawGeneration {
		t.Fatal("the model call must be recorded as a generation observation")
	}
}

func TestChatRequiresChatPermission(t *testing.T) {
	const noChat = `
version: 1
default: deny
roles:
  reader:
    permissions:
      - tool:open
`
	llm := &scripted{t: t, queue: []model.Response{textResponse("ok")}}
	app := newTestAppWithPolicy(t, llm, noChat)
	defer func() { _ = app.Close(context.Background()) }()

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"reader"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if len(llm.got) != 0 {
		t.Fatal("the model must not be called without the chat permission")
	}
}

func TestChatRecordsCost(t *testing.T) {
	llm := &scripted{t: t, queue: []model.Response{{
		Message:      model.Message{Role: model.RoleAssistant, Content: "ok"},
		Usage:        model.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500},
		FinishReason: model.FinishStop,
	}}}
	app, rec := newRecorderApp(t, llm, testPolicyAdmin)
	defer func() { _ = app.Close(context.Background()) }()
	app.cfg.Pricing = model.Pricing{InputPerMTok: 3, OutputPerMTok: 15}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}

	snap := rec.Snapshot()
	if len(snap.Traces) != 1 {
		t.Fatalf("traces = %d, want 1", len(snap.Traces))
	}
	const want = 0.0105
	if got := snap.Traces[0].Usage.Cost; got < want-1e-12 || got > want+1e-12 {
		t.Fatalf("trace cost = %v, want ~%v", got, want)
	}
	var sawGenCost bool
	for _, o := range snap.Observations {
		if o.Kind == trace.KindGeneration && o.Usage.Cost > want-1e-12 && o.Usage.Cost < want+1e-12 {
			sawGenCost = true
		}
	}
	if !sawGenCost {
		t.Fatalf("generation observation must carry the cost: %+v", snap.Observations)
	}
}
