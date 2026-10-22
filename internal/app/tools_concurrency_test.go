package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/KirillYuzh/Gobellim/model"
)

func twoCalls(a, b string) model.Response {
	return model.Response{Message: model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{ID: "c1", Name: a, Args: `{"n":1}`},
			{ID: "c2", Name: b, Args: `{"n":2}`},
		},
	}}
}

type callIn struct {
	N int `json:"n"`
}

// TestReadOnlyToolCallsRunConcurrently: a batch of read-only calls may run out of
// the model's order, so both must be in flight at once (BR-108).
func TestReadOnlyToolCallsRunConcurrently(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})

	llm := &scripted{t: t, queue: []model.Response{twoCalls("slow_a", "slow_b"), textResponse("done")}}

	handler := func(name string) func(context.Context, callIn) (string, error) {
		return func(ctx context.Context, _ callIn) (string, error) {
			started <- name
			select {
			case <-release:
				return name + " ok", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	app := newRealApp(t, discardES{}, llm, nil, testPolicyMemory, func(c *Config) {
		c.MaxSteps = 4
		c.ToolTimeout = 3 * time.Second
	},
		Tool("slow_a", handler("a")),
		Tool("slow_b", handler("b")),
	)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	errs := make(chan error, 1)
	go func() {
		_, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "go"}}})
		errs <- err
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d of 2 read-only calls were in flight at once", i)
		}
	}
	close(release)
	if err := <-errs; err != nil {
		t.Fatalf("chat: %v", err)
	}
}

// TestEffectfulToolCallsStaySequential: one effectful call makes the whole batch
// sequential, so writes keep the model's order and never interleave (BR-108).
func TestEffectfulToolCallsStaySequential(t *testing.T) {
	var (
		mu          sync.Mutex
		inFlight    int
		maxInFlight int
	)
	handler := func(_ context.Context, _ callIn) (string, error) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
		return "ok", nil
	}

	llm := &scripted{t: t, queue: []model.Response{twoCalls("read_a", "write_b"), textResponse("done")}}
	app := newRealApp(t, discardES{}, llm, nil, testPolicyMemory, func(c *Config) {
		c.MaxSteps = 4
		c.ToolTimeout = 3 * time.Second
	},
		Tool("read_a", handler),
		Tool("write_b", handler, Write()),
	)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "go"}}}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if maxInFlight > 1 {
		t.Fatalf("an effectful batch ran %d calls at once; it must stay sequential", maxInFlight)
	}
}
