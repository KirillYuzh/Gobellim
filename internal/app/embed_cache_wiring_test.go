package app

import (
	"context"
	"sync"
	"testing"

	"github.com/KirillYuzh/Gobellim/embed"
	"github.com/KirillYuzh/Gobellim/model"
)

type countingEmbedder struct {
	mu    sync.Mutex
	calls int
	dim   int
}

func (c *countingEmbedder) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingEmbedder) embed(texts []string) [][]float32 {
	c.mu.Lock()
	c.calls += len(texts)
	c.mu.Unlock()
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, c.dim)
		for j := range v {
			v[j] = float32(len(t)) + float32(j)
		}
		out[i] = v
	}
	return out
}

func (c *countingEmbedder) Query(_ context.Context, texts []string) ([][]float32, error) {
	return c.embed(texts), nil
}

func (c *countingEmbedder) Passages(_ context.Context, texts []string) ([][]float32, error) {
	return c.embed(texts), nil
}

func (c *countingEmbedder) Dim() int      { return c.dim }
func (c *countingEmbedder) Model() string { return "counting-embed" }

func TestEmbedCacheWiringSurvivesIngest(t *testing.T) {
	emb := &countingEmbedder{dim: 8}
	app := newRealApp(t, discardES{}, &scripted{t: t, queue: []model.Response{textResponse("ok")}},
		emb, testPolicyMemory, func(c *Config) {
			c.EmbedCache = embed.CacheConfig{Size: 128}
		})

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	doc := Doc{ID: "d1", Text: "документ для повторной загрузки", ACL: []string{"admin"}}

	if err := app.Ingest(ctx, doc); err != nil {
		t.Fatal(err)
	}
	first := emb.count()
	if first == 0 {
		t.Fatal("the first ingest must embed")
	}

	if err := app.Ingest(ctx, doc); err != nil {
		t.Fatal(err)
	}
	if got := emb.count(); got != first {
		t.Fatalf("re-ingesting an unchanged document re-embedded it: calls %d -> %d", first, got)
	}
}

func TestEmbedCacheZeroSizeStaysOff(t *testing.T) {
	emb := &countingEmbedder{dim: 8}
	app := newRealApp(t, discardES{}, &scripted{t: t, queue: []model.Response{textResponse("ok")}},
		emb, testPolicyMemory, nil)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	doc := Doc{ID: "d2", Text: "документ без кэша", ACL: []string{"admin"}}

	if err := app.Ingest(ctx, doc); err != nil {
		t.Fatal(err)
	}
	first := emb.count()
	if err := app.Ingest(ctx, doc); err != nil {
		t.Fatal(err)
	}
	if got := emb.count(); got <= first {
		t.Fatalf("with the cache disabled the second ingest must embed again: %d -> %d", first, got)
	}
}
