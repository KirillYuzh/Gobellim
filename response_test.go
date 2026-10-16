package kit

import (
	"context"
	"testing"

	"github.com/KirillYuzh/Gobellim/model"
	"github.com/KirillYuzh/Gobellim/trace"
)

// TestChatResponseCarriesTheAuditTrail: Chat returns a Response, so the caller
// can correlate the answer with its trace and see which guard and policy version
// produced the turn's decisions (and whether the turn is tainted).
func TestChatResponseCarriesTheAuditTrail(t *testing.T) {
	rec := trace.NewRecorder()
	llm := &scripted{t: t, queue: []model.Response{textResponse("готово")}}
	app := newRealApp(t, discardES{}, llm, nil, testPolicyMemory, func(c *Config) {
		c.Sinks = []trace.Sink{rec}
	})

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	res, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "привет"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Message.Content != "готово" {
		t.Fatalf("content = %q", res.Message.Content)
	}
	if res.TraceID == "" || res.Usage.TotalTokens != 2 {
		t.Fatalf("trace id = %q, usage = %+v", res.TraceID, res.Usage)
	}
	if res.Decision.GuardVersion == "" || res.Decision.PolicyHash == "" {
		t.Fatalf("decision = %+v", res.Decision)
	}
	if res.Decision.Untrusted {
		t.Fatal("a clean turn must not be marked untrusted")
	}

	snap := rec.Snapshot()
	if len(snap.Traces) != 1 || snap.Traces[0].ID != res.TraceID {
		t.Fatalf("response trace id %q does not match the recorded trace", res.TraceID)
	}

	// The audit trail survives a failed turn: even on an error the caller gets
	// the trace ID and the guard/policy versions that were in force.
	failed, err := app.Chat(ctx, ChatRequest{})
	if err == nil {
		t.Fatal("want an error for a request without messages")
	}
	if failed.TraceID == "" || failed.Decision.GuardVersion == "" {
		t.Fatalf("a failed turn must still carry the audit trail: %+v", failed.Decision)
	}
}
