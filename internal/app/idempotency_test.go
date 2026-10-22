package app

import (
	"context"
	"testing"
)

// TestToolCallIdempotencyKeyIsStable: an effectful tool can read a stable key
// from its context (variant A of the review's idempotency point). The key must be
// the same for repeated identical calls and different for different arguments.
func TestToolCallIdempotencyKeyIsStable(t *testing.T) {
	type sendIn struct {
		To string `json:"to"`
	}

	var keys []string
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, nil,
		Tool("send_mail", func(ctx context.Context, _ sendIn) (string, error) {
			keys = append(keys, CallKey(ctx))
			return "ok", nil
		}, AnyHost()),
	)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	args := []byte(`{"to":"a@example.com"}`)

	if _, err := app.CallTool(ctx, "send_mail", args); err != nil {
		t.Fatal(err)
	}
	// A repeated call with the same arguments (a retried turn) yields the same key.
	if _, err := app.CallTool(ctx, "send_mail", args); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("idempotency keys = %q, want the same non-empty key twice", keys)
	}

	// Different arguments yield a different key.
	if _, err := app.CallTool(ctx, "send_mail", []byte(`{"to":"b@example.com"}`)); err != nil {
		t.Fatal(err)
	}
	if keys[2] == keys[0] {
		t.Fatal("different arguments must yield a different key")
	}
}
