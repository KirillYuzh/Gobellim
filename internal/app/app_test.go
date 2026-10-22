package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/guard"
	internaltool "github.com/KirillYuzh/Gobellim/internal/tool"
	"github.com/KirillYuzh/Gobellim/model"
	"github.com/KirillYuzh/Gobellim/store"
	"github.com/KirillYuzh/Gobellim/trace"
)

func TestReflectSchemaStripsMetaAndForbidsExtra(t *testing.T) {
	type in struct {
		ID string `json:"id"`
	}
	raw, err := internaltool.SchemaFor[in]()
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
	type in struct {
		ID string `json:"id"`
	}
	def := Tool("get_ticket", func(context.Context, in) (string, error) { return "ok", nil })
	if err := def.Err(); err != nil {
		t.Fatal(err)
	}
	if err := def.Validate(json.RawMessage(`{"id":"1"}`)); err != nil {
		t.Fatalf("valid args rejected: %v", err)
	}
	if err := def.Validate(json.RawMessage(`{"id":"1","extra":true}`)); err == nil {
		t.Fatal("unknown field must be rejected")
	}
	if err := def.Validate(json.RawMessage(`not json`)); err == nil {
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

// mustPolicy parses a policy or fails the test.
func mustPolicy(t *testing.T, policyYAML string) *authz.Policy {
	t.Helper()
	policy, err := authz.Parse(strings.NewReader(policyYAML))
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

// newApp builds an App through New, registering defs as its tool set, and closes
// it at cleanup.
func newApp(t *testing.T, cfg Config, defs ...ToolDef) *App {
	t.Helper()
	if len(defs) > 0 {
		set, err := NewRegistry().Register(defs...).Build()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Tools = set
	}
	if cfg.GuardL2 == nil {
		cfg.GuardL2 = guard.L1Only()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	app, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	return app
}

func newTestApp(t *testing.T, llm model.Client) *App {
	return newTestAppWithPolicy(t, llm, testPolicy)
}

func newTestAppWithPolicy(t *testing.T, llm model.Client, policyYAML string) *App {
	t.Helper()
	return newApp(t, Config{LLM: llm, Policy: mustPolicy(t, policyYAML)})
}

func unspotlight(s string) string {
	s = strings.TrimPrefix(s, "<untrusted-data>\n")
	return strings.TrimSuffix(s, "\n</untrusted-data>")
}

// AnyHost is the public any-host egress option; the app facade does not
// re-export it, so the tests name the policy directly.
func AnyHost() Policy { return Policy{}.AnyHost() }

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
	type in struct {
		A string `json:"a"`
	}
	noop := func(context.Context, in) (string, error) { return "", nil }
	app := newApp(t, Config{LLM: &fakeLLM{}, Policy: mustPolicy(t, testPolicy)},
		Tool("open", noop, Perm("tool:open")),
		Tool("locked", noop, Perm("tool:locked")),
	)
	defer func() { _ = app.Close(context.Background()) }()

	specs := app.allowedToolSpecs(Principal{Roles: []string{"user"}, Tenant: "t"}, "")
	if len(specs) != 1 || specs[0].Name != "open" {
		t.Fatalf("specs = %+v, want only open", specs)
	}
}

func TestSearchAndIngestNeedEmbedder(t *testing.T) {
	app := newTestApp(t, &fakeLLM{})
	defer func() { _ = app.Close(context.Background()) }()
	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})

	if err := app.Ingest(ctx, Doc{ID: "1", Text: "text", ACL: []string{"admin"}}); !errors.Is(err, ErrInvalidConfig) {
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
	app := newApp(t, Config{LLM: llm, Policy: mustPolicy(t, testPolicyAdmin)},
		Tool("boom", func(context.Context, boomIn) (string, error) {
			return "", errors.New("kaboom")
		}))
	defer func() { _ = app.Close(context.Background()) }()

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
	app := newApp(t, Config{LLM: llm, Policy: mustPolicy(t, testPolicyAdmin), MaxSteps: 1},
		Tool("open", func(context.Context, in) (string, error) { return "ok", nil }))
	defer func() { _ = app.Close(context.Background()) }()

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
	noop := func(context.Context, json.RawMessage) (string, error) { return "", nil }
	def := RawTool("fetch", nil, noop, Egress("api.example.com"))

	if err := app.checkEgress(def, json.RawMessage(`{"url":"https://api.example.com/v1"}`), &turn{}); err != nil {
		t.Fatalf("allowed host rejected: %v", err)
	}
	if err := app.checkEgress(def, json.RawMessage(`{"url":"https://evil.example.net/v1"}`), &turn{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("host outside the allowlist err = %v, want ErrUnauthorized", err)
	}
	if err := app.checkEgress(def, json.RawMessage(`{"url":"https://cdn.api.example.com/x"}`), &turn{}); err != nil {
		t.Fatalf("subdomain of an allowed host rejected: %v", err)
	}
	read := RawTool("get", nil, noop)
	if err := app.checkEgress(read, json.RawMessage(`{"url":"https://evil.example.net"}`), &turn{}); err != nil {
		t.Fatalf("read tool must not be egress-checked: %v", err)
	}
}

func TestRuleOfTwoIsOnByDefaultInTheEgressCheck(t *testing.T) {
	app := &App{cfg: Config{}}
	def := RawTool("send", nil, func(context.Context, json.RawMessage) (string, error) { return "", nil }, AnyHost())

	if err := app.checkEgress(def, json.RawMessage(`{}`), &turn{}); err != nil {
		t.Fatalf("clean turn: %v", err)
	}
	if err := app.checkEgress(def, json.RawMessage(`{}`), &turn{untrusted: true}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("untrusted turn err = %v, want ErrUnauthorized", err)
	}
}

func TestApprovalIsRequiredAndBound(t *testing.T) {
	app := &App{cfg: Config{}}
	def := Tool("pay", func(context.Context, struct{}) (string, error) { return "", nil }, Policy{}.Approve())
	args := json.RawMessage(`{"amount":1}`)
	tn := &turn{principal: Principal{UserID: "u", Tenant: "t"}}

	if err := app.approve(context.Background(), def, args, tn); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("without an approver err = %v, want ErrApprovalRequired", err)
	}

	var seen ApprovalRequest
	app.cfg.Approver = ApproverFunc(func(_ context.Context, req ApprovalRequest) (Decision, error) {
		seen = req
		return Approve, nil
	})
	if err := app.approve(context.Background(), def, args, tn); err != nil {
		t.Fatal(err)
	}
	if seen.Tool != "pay" || seen.Fingerprint != def.Key(args) {
		t.Fatalf("approval request = %+v, want the tool fingerprint", seen)
	}

	app.cfg.Approver = ApproverFunc(func(context.Context, ApprovalRequest) (Decision, error) { return Deny, nil })
	if err := app.approve(context.Background(), def, args, &turn{principal: Principal{UserID: "u", Tenant: "t"}}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("denied err = %v, want ErrApprovalRequired", err)
	}
}

func TestFingerprintIgnoresKeyOrder(t *testing.T) {
	def := Tool("pay", func(context.Context, struct{}) (string, error) { return "", nil })
	a := def.Key(json.RawMessage(`{"amount":1,"to":"bob"}`))
	b := def.Key(json.RawMessage(`{"to":"bob","amount":1}`))
	if a != b {
		t.Fatalf("fingerprints differ: %s != %s", a, b)
	}
	if a == def.Key(json.RawMessage(`{"amount":2,"to":"bob"}`)) {
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

type windowLLM struct{ window int }

func (w windowLLM) Model() string { return "windowed" }

func (w windowLLM) Chat(context.Context, model.Request) (model.Response, error) {
	return model.Response{}, nil
}

func (w windowLLM) ContextTokens() int { return w.window }

func TestNewRejectsGuardWindowWiderThanModelContext(t *testing.T) {
	policy := mustPolicy(t, testPolicy)

	_, err := New(context.Background(), Config{
		ES: discardES{}, LLM: windowLLM{window: 1000}, Policy: policy, GuardMaxChars: 2000, GuardL2: guard.L1Only(),
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig when the guard window exceeds the model context", err)
	}

	app, err := New(context.Background(), Config{
		ES: discardES{}, LLM: windowLLM{window: 100000}, Policy: policy, GuardMaxChars: 2000, GuardL2: guard.L1Only(),
	})
	if err != nil {
		t.Fatalf("a wide enough context window must be accepted: %v", err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	plain, err := New(context.Background(), Config{ES: discardES{}, LLM: &fakeLLM{}, Policy: policy, GuardMaxChars: 2000, GuardL2: guard.L1Only()})
	if err != nil {
		t.Fatalf("a client without ContextWindow must be accepted: %v", err)
	}
	defer func() { _ = plain.Close(context.Background()) }()
}

// A middleware may stop a call by returning without calling next, and the
// substituted result still goes through the result guard and the output schema.
func TestMiddlewareSubstitutesAndSkipsExecution(t *testing.T) {
	type in struct {
		A string `json:"a"`
	}
	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("lookup", `{"a":"1"}`),
		textResponse("done"),
	}}
	executed := false
	var got Call
	mw := func(_ context.Context, c Call, _ Next) (json.RawMessage, error) {
		got = c
		return json.RawMessage(`"cached-result"`), nil
	}
	app := newApp(t, Config{
		LLM: llm, Policy: mustPolicy(t, testPolicyAdmin), Middleware: []Middleware{mw},
	}, Tool("lookup", func(context.Context, in) (string, error) {
		executed = true
		return "real-result", nil
	}))
	defer func() { _ = app.Close(context.Background()) }()

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	if executed {
		t.Fatal("the tool must not run when the middleware does not call next")
	}
	last := llm.got[1].Messages[len(llm.got[1].Messages)-1]
	if last.Role != model.RoleTool || unspotlight(last.Content) != `"cached-result"` {
		t.Fatalf("tool result = %+v, want the substituted output", last)
	}
	if got.Tool != "lookup" || got.Index != 1 || got.Policy.Effect() != EffectRead {
		t.Fatalf("call = %+v, want the resolved call info", got)
	}
	if got.Key == "" || got.UserID != "u" || got.Tenant != "t" {
		t.Fatalf("call = %+v, want user/tenant/key", got)
	}
}

// A middleware sees the tool error and may rewrite the result.
func TestMiddlewareRewritesResultOnFailure(t *testing.T) {
	type in struct {
		A string `json:"a"`
	}
	type recovered struct {
		Recovered bool `json:"recovered"`
	}
	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("boom", `{"a":"1"}`),
		textResponse("done"),
	}}
	ran := false
	mw := func(ctx context.Context, _ Call, next Next) (json.RawMessage, error) {
		_, err := next(ctx)
		ran = true
		if err == nil || !strings.Contains(err.Error(), "kaboom") {
			t.Fatalf("middleware err = %v, want the tool error", err)
		}
		return json.RawMessage(`{"recovered":true}`), nil
	}
	app := newApp(t, Config{
		LLM: llm, Policy: mustPolicy(t, testPolicyAdmin), Middleware: []Middleware{mw},
	}, Tool("boom", func(context.Context, in) (recovered, error) {
		return recovered{}, errors.New("kaboom")
	}))
	defer func() { _ = app.Close(context.Background()) }()

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("the middleware must run on failure")
	}
	last := llm.got[1].Messages[len(llm.got[1].Messages)-1]
	if last.Role != model.RoleTool || unspotlight(last.Content) != `{"recovered":true}` {
		t.Fatalf("tool result = %+v, want the rewritten result", last)
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
	runs := 0
	app := newApp(t, Config{
		LLM: llm, Policy: mustPolicy(t, testPolicyAdmin), MaxSteps: 3, MaxCallsPerTool: 3,
		Approver: ApproverFunc(func(context.Context, ApprovalRequest) (Decision, error) { return Approve, nil }),
	}, Tool("pay", func(context.Context, in) (string, error) {
		runs++
		return "paid", nil
	}, Policy{}.Approve()))
	defer func() { _ = app.Close(context.Background()) }()

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	_, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("err = %v, want ErrApprovalRequired for a replayed approval", err)
	}
	if runs != 1 {
		t.Fatalf("tool ran %d times, want exactly 1", runs)
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
	provider := &recordingProvider{}
	app := newApp(t, Config{LLM: llm, Policy: mustPolicy(t, testPolicyAdmin), TracerProvider: provider})
	defer func() { _ = app.Close(context.Background()) }()
	app.cfg.Pricing = model.Pricing{InputPerMTok: 3, OutputPerMTok: 15}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}

	const want = 0.0105
	near := func(v any) bool {
		f, ok := v.(float64)
		return ok && f > want-1e-12 && f < want+1e-12
	}
	operation := provider.named(t, "chat")
	if !near(operation.attrs[trace.AttrCost]) {
		t.Fatalf("operation cost = %v, want ~%v", operation.attrs[trace.AttrCost], want)
	}
	var sawGenCost bool
	for _, s := range provider.recorded() {
		if s.attrs[trace.AttrKind] == KindGeneration && near(s.attrs[trace.AttrCost]) {
			sawGenCost = true
		}
	}
	if !sawGenCost {
		t.Fatalf("the generation span must carry the cost: %v", provider.names())
	}
}
