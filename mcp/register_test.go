package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/model"
	"github.com/KirillYuzh/Gobellim/store"
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

func testApp(t *testing.T, tools *gobellim.ToolSet, policy *authz.Policy) *gobellim.App {
	t.Helper()
	app, err := gobellim.New(context.Background(), gobellim.Config{
		ES: nopES{}, LLM: nopLLM{}, Policy: policy, GuardL2: guard.L1Only(), Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	return app
}

// An MCP tool registered in an app runs through the app's tool path: it is
// gated by tool:<server>:<tool>, and a principal without that permission
// cannot run it.
func TestRegisteredToolIsGatedByTheApp(t *testing.T) {
	srv := jsonRPCServer(t, false)
	defer srv.Close()

	c, err := New(Config{Name: "fs", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	defs, err := Register(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	registry := gobellim.NewRegistry()
	registry.Register(defs...)
	tools, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}

	policy, err := authz.Parse(strings.NewReader(
		"version: 1\ndefault: deny\nroles:\n  user:\n    permissions:\n      - chat\n"))
	if err != nil {
		t.Fatal(err)
	}
	app := testApp(t, tools, policy)

	ctx := gobellim.WithPrincipal(context.Background(),
		gobellim.Principal{UserID: "u1", Roles: []string{"user"}, Tenant: "t1"})
	if _, err := app.CallTool(ctx, "mcp:fs:echo", json.RawMessage(`{}`)); err == nil {
		t.Fatal("a principal without tool:mcp:fs:echo must not run the tool")
	}
}

func TestRegisterAddsPrefixedToolsWithEgress(t *testing.T) {
	srv := jsonRPCServer(t, false)
	defer srv.Close()

	c, err := New(Config{Name: "fs", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	defs, err := Register(context.Background(), c)
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
	if def.Policy().Effect() != gobellim.EffectEgress {
		t.Fatalf("effect = %v, want egress", def.Policy().Effect())
	}
	if !json.Valid(def.Schema()) {
		t.Fatalf("schema is not JSON: %s", def.Schema())
	}
}

// The caller's options are honoured, but a tool that calls a remote server
// always egresses: the effect cannot be downgraded to read.
func TestRegisterCallerOptionsOverrideDefaults(t *testing.T) {
	srv := jsonRPCServer(t, false)
	defer srv.Close()

	c, err := New(Config{Name: "fs", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	defs, err := Register(context.Background(), c, gobellim.Read().Approve())
	if err != nil {
		t.Fatal(err)
	}
	if defs[0].Policy().Effect() != gobellim.EffectEgress {
		t.Fatalf("effect = %v, want egress", defs[0].Policy().Effect())
	}
	if !defs[0].Policy().RequiresApproval() {
		t.Fatal("require approval must be honoured")
	}
}

// The egress allowlist is the endpoint host, not every host.
func TestRegisterDerivesEgressAllowlistFromTheEndpoint(t *testing.T) {
	srv := jsonRPCServer(t, false)
	defer srv.Close()

	c, err := New(Config{Name: "fs", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	defs, err := Register(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	hosts := defs[0].Policy().EgressHosts()
	if len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("hosts = %v, want [127.0.0.1]", hosts)
	}
	if defs[0].Policy().EgressAny() {
		t.Fatal("the allowlist must not open egress to every host")
	}
}

// A stdio client has no endpoint host, so the caller must name the hosts it
// allows. There is no default that opens egress to every host.
func TestRegisterRequiresAnEgressPolicyForStdio(t *testing.T) {
	c := &Client{cfg: Config{Name: "fs", Command: "true"}}
	c.transport = &stubTransport{
		call: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
			switch method {
			case "initialize":
				return json.RawMessage(`{}`), nil
			case "tools/list":
				return json.RawMessage(`{"tools":[{"name":"echo","inputSchema":{"type":"object"}}]}`), nil
			}
			return json.RawMessage(`{}`), nil
		},
	}
	if _, err := Register(context.Background(), c); err == nil {
		t.Fatal("a stdio client must not get a default egress policy")
	}
}

func TestRegisterRejectsUncompilableSchema(t *testing.T) {
	c := &Client{cfg: Config{Name: "bad", URL: "http://mcp.example.com"}}
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
	if _, err := Register(context.Background(), c); err == nil {
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
