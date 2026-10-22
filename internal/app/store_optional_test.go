package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/model"
)

// TestNewWithoutStore covers the README promise that the core runs on your
// infrastructure: Config.ES is optional. Without a store, Chat still works and
// every store-backed operation fails with ErrInvalidConfig instead of panicking
// on a nil client.
func TestNewWithoutStore(t *testing.T) {
	policy, err := authz.Parse(strings.NewReader(testPolicyMemory))
	if err != nil {
		t.Fatal(err)
	}
	llm := &scripted{t: t, queue: []model.Response{textResponse("привет")}}

	app, err := New(context.Background(), Config{
		LLM:     llm,
		Policy:  policy,
		GuardL2: guard.L1Only(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New without Config.ES: %v", err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	if err := app.Setup(context.Background()); err != nil {
		t.Fatalf("Setup without a store must be a no-op: %v", err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})

	msg, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "привет"}}})
	if err != nil {
		t.Fatalf("Chat without a store: %v", err)
	}
	if msg.Message.Content != "привет" {
		t.Fatalf("content = %q", msg.Message.Content)
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"Search", func() error { _, err := app.Search(ctx, SearchRequest{Query: "x"}); return err }},
		{"Delete", func() error { return app.Delete(ctx, "d1") }},
		{"Prune", func() error { _, err := app.Prune(ctx, 0); return err }},
		{"MemoryLoad", func() error { _, err := app.MemoryLoad(ctx, "th", 0); return err }},
		{"MemoryWorking", func() error { _, err := app.MemoryWorking(ctx, "th"); return err }},
	} {
		if err := tc.call(); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s without a store: err = %v, want ErrInvalidConfig", tc.name, err)
		}
	}
}
