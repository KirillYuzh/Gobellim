package kit

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

// TestIngestRequiresATokenizer: the whitespace tokenizer is no longer a silent
// default. A missing Config.Tokenizer must fail Ingest loudly (ErrInvalidConfig)
// rather than chunk on words and silently exceed the model's token budget.
func TestIngestRequiresATokenizer(t *testing.T) {
	policy, err := authz.Parse(strings.NewReader(testPolicyMemory))
	if err != nil {
		t.Fatal(err)
	}
	llm := &scripted{t: t, queue: []model.Response{textResponse("ok")}}

	app, err := New(context.Background(), Config{
		LLM:      llm,
		Embedder: fakeEmbedder{dim: 4},
		Policy:   policy,
		GuardL2:  guard.L1Only(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New without a tokenizer must still succeed: %v", err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if err := app.Ingest(ctx, Doc{ID: "d1", Text: "текст", ACL: []string{"admin"}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Ingest without a tokenizer err = %v, want ErrInvalidConfig", err)
	}
}
