package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gobellim/kit"
	"github.com/gobellim/kit/authz"
	"github.com/gobellim/kit/model"
	"github.com/gobellim/kit/store"
)

type nopES struct{}

func (nopES) Do(context.Context, store.Request) (int, []byte, error) {
	return 200, []byte(`{}`), nil
}

type nopLLM struct{}

func (nopLLM) Model() string { return "nop" }

func (nopLLM) Chat(context.Context, model.Request) (model.Response, error) {
	return model.Response{}, nil
}

func testApp(t *testing.T) *kit.App {
	t.Helper()
	policy, err := authz.Parse(strings.NewReader("version: 1\ndefault: deny\nroles:\n  user:\n    permissions:\n      - \"*\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	app, err := kit.New(context.Background(), kit.Config{ES: nopES{}, LLM: nopLLM{}, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	return app
}

func TestRegisterAddsPrefixedToolsWithEgress(t *testing.T) {
	srv := jsonRPCServer(t, false)
	defer srv.Close()

	c, err := New(Config{Name: "fs", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	defs, err := Register(context.Background(), testApp(t), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("defs = %d, want 1", len(defs))
	}
	def := defs[0]
	if def.Name() != "mcp:fs:echo" {
		t.Fatalf("name = %q", def.Name())
	}
	if def.Permission() != "tool:mcp:fs:echo" {
		t.Fatalf("permission = %q", def.Permission())
	}
	if def.Effect() != kit.EffectEgress {
		t.Fatalf("effect = %v, want egress", def.Effect())
	}
	if !json.Valid(def.Schema()) {
		t.Fatalf("schema is not JSON: %s", def.Schema())
	}
}

func TestRegisterCallerOptionsOverrideDefaults(t *testing.T) {
	srv := jsonRPCServer(t, false)
	defer srv.Close()

	c, err := New(Config{Name: "fs", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	defs, err := Register(context.Background(), testApp(t), c, kit.Effects(kit.EffectRead), kit.RequireApproval(true))
	if err != nil {
		t.Fatal(err)
	}
	if defs[0].Effect() != kit.EffectRead {
		t.Fatalf("effect = %v, want the caller's read", defs[0].Effect())
	}
	if !defs[0].RequireApproval() {
		t.Fatal("require approval must be honoured")
	}
}

func TestRegisterRejectsUncompilableSchema(t *testing.T) {
	c := &Client{cfg: Config{Name: "bad"}}
	c.transport = &stubTransport{
		call: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
			switch method {
			case "initialize":
				return json.RawMessage(`{}`), nil
			case "tools/list":
				return json.RawMessage(`{"tools":[{"name":"broken","inputSchema":{"type":"nonsense"}}]}`), nil
			}
			return json.RawMessage(`{}`), nil
		},
	}
	if _, err := Register(context.Background(), testApp(t), c); err == nil {
		t.Fatal("want a schema compilation error")
	}
}

type stubTransport struct {
	call func(ctx context.Context, method string, params any) (json.RawMessage, error)
}

func (s *stubTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return s.call(ctx, method, params)
}

func (s *stubTransport) Notify(context.Context, string, any) error { return nil }

func (s *stubTransport) Close() error { return nil }
