package kit

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gobellim/kit/authz"
	"github.com/gobellim/kit/guard"
	"github.com/gobellim/kit/internal/call"
	"github.com/gobellim/kit/model"
	"github.com/gobellim/kit/trace"
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
      - tool:open
  admin:
    permissions:
      - ingest
      - search
`

type discardES struct{}

func (discardES) Do(context.Context, string, string, io.Reader) (int, []byte, error) {
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
	t.Helper()
	policy, err := authz.Parse(strings.NewReader(testPolicy))
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := guard.New(guard.Options{Layers: []guard.Detector{guard.AllowAll()}})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := trace.NewWriter(trace.Options{ES: discardES{}})
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		cfg:    Config{LLM: llm, MaxSteps: 2, MaxCallsPerTool: 1, ToolTimeout: time.Second, ArgsMaxBytes: 1024},
		policy: policy,
		guard:  pipeline,
		writer: writer,
		tools:  make(map[string]*ToolDef),
	}
	app.calls = call.Deps{Authz: policy, Guard: pipeline, Tracer: tracerAdapter{writer}}
	return app
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
	if out.Content != "ok" {
		t.Fatalf("content = %q", out.Content)
	}
	if len(llm.got.Messages) != 2 {
		t.Fatalf("model saw %+v", llm.got.Messages)
	}
	if llm.got.Messages[0].Role != model.RoleSystem || llm.got.Messages[0].Content != "server instructions" {
		t.Fatalf("system = %+v", llm.got.Messages[0])
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

	specs := app.allowedToolSpecs(Principal{Roles: []string{"user"}, Tenant: "t"})
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
