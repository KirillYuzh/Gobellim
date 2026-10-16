package kit

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// syncBuffer is a race-safe io.Writer: the trace writer goroutine shares the
// logger with the test.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestPolicyDenialsAreSecurityEvents: a refusal by RBAC or the egress policy is
// reported as an operator-facing security event, not only as an aborted turn.
func TestPolicyDenialsAreSecurityEvents(t *testing.T) {
	type fetchIn struct {
		URL string `json:"url"`
	}

	logs := &syncBuffer{}
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.Logger = slog.New(slog.NewTextHandler(logs, nil))
	})
	if _, err := Tool(app, "fetch_page",
		func(context.Context, fetchIn) (string, error) { return "body", nil },
		Effects(EffectEgress), EgressHosts("api.example.com")); err != nil {
		t.Fatal(err)
	}

	// RBAC: a role without the tool permission.
	guest := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"guest"}, Tenant: "t"})
	if _, err := app.CallTool(guest, "fetch_page", []byte(`{"url":"https://api.example.com/x"}`)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if !strings.Contains(logs.String(), "tool_denied_rbac") {
		t.Fatalf("no rbac security event logged: %q", logs.String())
	}

	// Egress: a host outside the allowlist.
	admin := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.CallTool(admin, "fetch_page", []byte(`{"url":"https://evil.example.net/x"}`)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if !strings.Contains(logs.String(), "egress_host_not_allowed") {
		t.Fatalf("no egress security event logged: %q", logs.String())
	}
}
