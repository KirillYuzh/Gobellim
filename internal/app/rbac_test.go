package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/internal/core"
)

const rbacTestPolicy = `
version: 1
default: deny
roles:
  user:
    permissions:
      - chat
  operator:
    permissions:
      - chat
      - tool:secret
`

// newRBACTestApp builds an app with one registered tool and the given policy.
func newRBACTestApp(t *testing.T, policy *authz.Policy) *App {
	t.Helper()
	registry := NewRegistry()
	registry.Register(Tool("secret", func(_ context.Context, in struct {
		Token string `json:"token"`
	},
	) (string, error) {
		return "leaked:" + in.Token, nil
	}))
	tools, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), Config{
		LLM:     &stubLLM{answer: "answer"},
		Policy:  policy,
		GuardL2: guard.L1Only(),
		Tools:   tools,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	return app
}

// The options must reach the declaration through the app facade: a dropped
// option is a lost policy.
func TestToolOptionsAreNotDropped(t *testing.T) {
	def := Tool("dangerous", func(context.Context, struct{}) (string, error) { return "", nil },
		Desc("does something"),
		Write(),
		Perm("danger:write"),
	)
	if def.Err() != nil {
		t.Fatal(def.Err())
	}
	if def.Description() != "does something" {
		t.Fatalf("description = %q", def.Description())
	}
	if def.Policy().Effect() != EffectWrite {
		t.Fatalf("effect = %v, want write", def.Policy().Effect())
	}
	if def.Permission() != "danger:write" {
		t.Fatalf("permission = %q", def.Permission())
	}
}

// A tool without an explicit Perm is gated by tool:<name>. A principal that
// holds only "chat" must not be able to run it.
func TestPrincipalWithoutToolPermissionCannotCallTool(t *testing.T) {
	policy, err := authz.Parse(strings.NewReader(rbacTestPolicy))
	if err != nil {
		t.Fatal(err)
	}
	app := newRBACTestApp(t, policy)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u1", Roles: []string{"user"}, Tenant: "t1"})
	out, err := app.CallTool(ctx, "secret", json.RawMessage(`{"token":"x"}`))
	if err == nil {
		t.Fatalf("the tool ran without the tool:secret permission: %q", out)
	}
	if !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(out, "leaked") {
		t.Fatalf("the tool leaked its result: %q", out)
	}
}

// The same tool runs for a principal that holds the tool permission.
func TestPrincipalWithToolPermissionCanCallTool(t *testing.T) {
	policy, err := authz.Parse(strings.NewReader(rbacTestPolicy))
	if err != nil {
		t.Fatal(err)
	}
	app := newRBACTestApp(t, policy)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u2", Roles: []string{"operator"}, Tenant: "t1"})
	out, err := app.CallTool(ctx, "secret", json.RawMessage(`{"token":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "leaked:x") {
		t.Fatalf("out = %q, want the tool result", out)
	}
}

// A tool the principal may not call is not offered to the model either.
func TestToolWithoutPermissionIsNotOfferedToTheModel(t *testing.T) {
	policy, err := authz.Parse(strings.NewReader(rbacTestPolicy))
	if err != nil {
		t.Fatal(err)
	}
	app := newRBACTestApp(t, policy)

	pr := core.Principal{UserID: "u1", Roles: []string{"user"}, Tenant: "t1"}
	specs := app.allowedToolSpecs(pr, "")
	if len(specs) != 0 {
		t.Fatalf("the model was offered %d tools the principal may not call", len(specs))
	}

	pr.Roles = []string{"operator"}
	specs = app.allowedToolSpecs(pr, "")
	if len(specs) != 1 || specs[0].Name != "secret" {
		t.Fatalf("specs = %v, want the secret tool", specs)
	}
}
