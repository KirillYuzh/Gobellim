package kit

import (
	"context"
	"testing"

	"github.com/gobellim/kit/guard"
	"github.com/gobellim/kit/model"
)

func TestMetricsAndHealthExposeGuardCounters(t *testing.T) {
	llm := &scripted{t: t, queue: []model.Response{textResponse("ok")}}
	app := newTestApp(t, llm)
	defer func() { _ = app.Close(context.Background()) }()

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{
		System:   "s",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatal(err)
	}

	m := app.Metrics()
	if m.Guard.Scans == 0 {
		t.Fatal("Metrics.Guard.Scans = 0, want the guard scans of the call path counted")
	}
	if len(m.Guard.Latency) != len(guard.LatencyBucketMillis)+1 {
		t.Fatalf("latency histogram = %v, want %d buckets", m.Guard.Latency, len(guard.LatencyBucketMillis)+1)
	}
	if h := app.Health(); h.GuardStats.Scans != m.Guard.Scans {
		t.Fatalf("Health.GuardStats.Scans = %d, Metrics.Guard.Scans = %d", h.GuardStats.Scans, m.Guard.Scans)
	}
}
