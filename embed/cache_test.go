package embed

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

type countingEmbedder struct {
	model string
	dim   int
	err   error

	mu    sync.Mutex
	calls [][]string
}

func (f *countingEmbedder) Model() string { return f.model }

func (f *countingEmbedder) Dim() int { return f.dim }

func (f *countingEmbedder) Query(_ context.Context, texts []string) ([][]float32, error) {
	return f.record(texts, roleQuery)
}

func (f *countingEmbedder) Passages(_ context.Context, texts []string) ([][]float32, error) {
	return f.record(texts, rolePassage)
}

func (f *countingEmbedder) record(texts []string, role cacheRole) ([][]float32, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), texts...))
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	vecs := make([][]float32, len(texts))
	for i, t := range texts {
		vecs[i] = synthVector(role, t, f.dim)
	}
	return vecs, nil
}

func (f *countingEmbedder) embedded() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += len(c)
	}
	return n
}

func (f *countingEmbedder) batches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func synthVector(role cacheRole, text string, dim int) []float32 {
	sum := sha256.Sum256([]byte(string(role) + "\x00" + text))
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(sum[i%len(sum)]) / 255
	}
	return v
}

func mustCached(t *testing.T, inner Embedder, size int) *CachedEmbedder {
	t.Helper()
	c, err := NewCached(inner, CacheConfig{Size: size})
	if err != nil {
		t.Fatalf("NewCached: %v", err)
	}
	return c
}

func docChunks() []string {
	return []string{
		"Retrieval augmented generation grounds answers in indexed documents.",
		"Chunkers split documents on token budgets, not on newlines.",
		"Embeddings map text into a vector space where distance means similarity.",
		"The guard pipeline masks personal data before anything reaches the model.",
		"Rate limits protect a tenant from exhausting the shared provider budget.",
	}
}

func TestCachedRepeatIngestDoesNotEmbedTwice(t *testing.T) {
	inner := &countingEmbedder{model: "test-model", dim: 8}
	c := mustCached(t, inner, 32)
	ctx := context.Background()

	chunks := docChunks()
	first, err := c.Passages(ctx, chunks)
	if err != nil {
		t.Fatal(err)
	}
	paid := inner.embedded()
	if paid != len(chunks) {
		t.Fatalf("first ingest embedded %d texts, want %d", paid, len(chunks))
	}

	second, err := c.Passages(ctx, docChunks())
	if err != nil {
		t.Fatal(err)
	}
	if got := inner.embedded(); got != paid {
		t.Fatalf("second ingest embedded %d texts, want %d: re-ingesting the same document must not be paid twice", got, paid)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("cached vectors differ from the first embed:\n first=%v\nsecond=%v", first, second)
	}
}

func TestCachedDisabledByZeroSize(t *testing.T) {
	for _, size := range []int{0, -1} {
		inner := &countingEmbedder{model: "test-model", dim: 4}
		c := mustCached(t, inner, size)
		ctx := context.Background()
		chunks := docChunks()
		for range 3 {
			if _, err := c.Passages(ctx, chunks); err != nil {
				t.Fatal(err)
			}
		}
		if got, want := inner.embedded(), 3*len(chunks); got != want {
			t.Fatalf("Size=%d embedded %d texts, want %d: a zero size must disable the cache", size, got, want)
		}
		if c.Len() != 0 {
			t.Fatalf("Size=%d held %d entries, want 0", size, c.Len())
		}
	}
}

func TestCachedHitEqualsFreshEmbed(t *testing.T) {
	dim := 16
	inner := &countingEmbedder{model: "test-model", dim: dim}
	c := mustCached(t, inner, 32)
	ctx := context.Background()
	chunks := docChunks()

	cached, err := c.Passages(ctx, chunks)
	if err != nil {
		t.Fatal(err)
	}
	hit, err := c.Passages(ctx, docChunks())
	if err != nil {
		t.Fatal(err)
	}
	fresh := &countingEmbedder{model: "test-model", dim: dim}
	want, err := fresh.Passages(ctx, docChunks())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hit, want) {
		t.Fatalf("cache hit = %v, fresh embed = %v", hit, want)
	}
	if !reflect.DeepEqual(cached, want) {
		t.Fatalf("first embed = %v, fresh embed = %v", cached, want)
	}

	hit[0][0] = 42
	again, err := c.Passages(ctx, docChunks())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("mutating a returned vector changed the cache: %v, want %v", again, want)
	}
}

func TestCachedKeySeparatesModelAndMode(t *testing.T) {
	ctx := context.Background()
	text := "the same text"

	first := &countingEmbedder{model: "model-a", dim: 4}
	second := &countingEmbedder{model: "model-b", dim: 4}
	c := mustCached(t, first, 8)
	if _, err := c.Passages(ctx, []string{text}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Passages(ctx, []string{text}); err != nil {
		t.Fatal(err)
	}
	if got := first.embedded(); got != 1 {
		t.Fatalf("model-a embedded %d texts, want 1", got)
	}

	other := mustCached(t, second, 8)
	if _, err := other.Passages(ctx, []string{text}); err != nil {
		t.Fatal(err)
	}
	if got := second.embedded(); got != 1 {
		t.Fatalf("model-b embedded %d texts, want 1: a different model must miss", got)
	}

	c2 := mustCached(t, &countingEmbedder{model: "model-a", dim: 4}, 8)
	if _, err := c2.Query(ctx, []string{text}); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Passages(ctx, []string{text}); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Query(ctx, []string{text}); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Passages(ctx, []string{text}); err != nil {
		t.Fatal(err)
	}
	probe := &countingEmbedder{model: "model-a", dim: 4}
	wantQuery, err := probe.Query(ctx, []string{text})
	if err != nil {
		t.Fatal(err)
	}
	wantPassage, err := probe.Passages(ctx, []string{text})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(wantQuery, wantPassage) {
		t.Fatal("test setup: the fake must not answer queries and passages identically")
	}
	gotQuery, err := c2.Query(ctx, []string{text})
	if err != nil {
		t.Fatal(err)
	}
	gotPassage, err := c2.Passages(ctx, []string{text})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotQuery, wantQuery) || !reflect.DeepEqual(gotPassage, wantPassage) {
		t.Fatalf("mode separation broken: query=%v/%v passage=%v/%v", gotQuery, wantQuery, gotPassage, wantPassage)
	}
}

func TestCachedKeyFramingCannotBeConfused(t *testing.T) {
	a := cacheKey(rolePassage, "ab", "c")
	b := cacheKey(rolePassage, "a", "bc")
	if a == b {
		t.Fatalf("model/text split is ambiguous: both key to %s", a)
	}
	if cacheKey(roleQuery, "m", "t") == cacheKey(rolePassage, "m", "t") {
		t.Fatal("query and passage must not share a key")
	}
	if len(a) != 64 {
		t.Fatalf("key = %q, want 64 hex characters (a sha256 digest)", a)
	}
}

func TestCachedIsBoundedBySize(t *testing.T) {
	inner := &countingEmbedder{model: "test-model", dim: 4}
	c := mustCached(t, inner, 4)
	ctx := context.Background()

	for i := range 20 {
		text := fmt.Sprintf("chunk-%d", i)
		if _, err := c.Passages(ctx, []string{text}); err != nil {
			t.Fatal(err)
		}
		if got := c.Len(); got > 4 {
			t.Fatalf("after %d inserts the cache holds %d entries, want at most 4", i+1, got)
		}
	}
	if got := c.Len(); got != 4 {
		t.Fatalf("cache holds %d entries, want exactly the bound 4", got)
	}
	if got := inner.embedded(); got != 20 {
		t.Fatalf("embedded %d texts, want 20", got)
	}

	if _, err := c.Passages(ctx, []string{"chunk-19"}); err != nil {
		t.Fatal(err)
	}
	if got := inner.embedded(); got != 20 {
		t.Fatalf("embedded %d texts: the newest chunk was evicted", got)
	}
	if _, err := c.Passages(ctx, []string{"chunk-0"}); err != nil {
		t.Fatal(err)
	}
	if got := inner.embedded(); got != 21 {
		t.Fatalf("embedded %d texts: the evicted chunk was still cached", got)
	}
}

func TestCachedDeduplicatesRepeatedTextInOneBatch(t *testing.T) {
	inner := &countingEmbedder{model: "test-model", dim: 4}
	c := mustCached(t, inner, 8)
	ctx := context.Background()

	vecs, err := c.Passages(ctx, []string{"same", "same", "other", "same"})
	if err != nil {
		t.Fatal(err)
	}
	if got := inner.embedded(); got != 2 {
		t.Fatalf("embedded %d texts, want 2 (the repeated chunk is paid for once)", got)
	}
	if len(inner.calls[0]) != 2 {
		t.Fatalf("inner batch = %v, want the two distinct texts", inner.calls[0])
	}
	if !reflect.DeepEqual(vecs[0], vecs[1]) || !reflect.DeepEqual(vecs[1], vecs[3]) {
		t.Fatalf("repeated text got different vectors: %v", vecs)
	}
	vecs[0][0] = 99
	if vecs[1][0] == 99 {
		t.Fatal("repeated text must still receive its own vector slice")
	}
}

func TestCachedConcurrentIngest(t *testing.T) {
	const size = 16
	inner := &countingEmbedder{model: "test-model", dim: 4}
	c := mustCached(t, inner, size)
	ctx := context.Background()

	chunks := docChunks()
	want := make([][]float32, len(chunks))
	for i, t := range chunks {
		want[i] = synthVector(rolePassage, t, 4)
	}

	if _, err := c.Passages(ctx, chunks); err != nil {
		t.Fatal(err)
	}
	paid := inner.embedded()

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vecs, err := c.Passages(ctx, docChunks())
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			if len(vecs) != len(chunks) {
				t.Errorf("goroutine %d: %d vectors, want %d", i, len(vecs), len(chunks))
				return
			}
			if !reflect.DeepEqual(vecs, want) {
				t.Errorf("goroutine %d: vectors = %v, want %v", i, vecs, want)
			}
		}(i)
	}
	wg.Wait()

	if got := inner.embedded(); got != paid {
		t.Fatalf("concurrent reads embedded %d texts, want %d", got, paid)
	}
	if got := c.Len(); got != len(chunks) {
		t.Fatalf("cache holds %d entries, want %d", got, len(chunks))
	}

	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			text := fmt.Sprintf("fresh-%d", i)
			vecs, err := c.Passages(ctx, []string{text})
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			if !reflect.DeepEqual(vecs[0], synthVector(rolePassage, text, 4)) {
				t.Errorf("goroutine %d: vector = %v, want %v", i, vecs[0], synthVector(rolePassage, text, 4))
			}
		}(i)
	}
	wg.Wait()
	if got := c.Len(); got > size {
		t.Fatalf("cache grew to %d entries under concurrency, want at most %d", got, size)
	}
}

func TestCachedConcurrentColdStartConverges(t *testing.T) {
	inner := &countingEmbedder{model: "test-model", dim: 4}
	c := mustCached(t, inner, 2)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := c.Passages(ctx, []string{fmt.Sprintf("chunk-%d", i%4)}); err != nil {
				t.Errorf("goroutine %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if got := c.Len(); got > 2 {
		t.Fatalf("cache holds %d entries, want at most the bound 2", got)
	}
}

func TestCachedDoesNotCacheFailures(t *testing.T) {
	boom := errors.New("provider is down")
	inner := &countingEmbedder{model: "test-model", dim: 4, err: boom}
	c := mustCached(t, inner, 8)
	ctx := context.Background()

	if _, err := c.Passages(ctx, docChunks()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if c.Len() != 0 {
		t.Fatalf("a failed embed cached %d entries", c.Len())
	}
}

func TestCachedDoesNotCacheShortResult(t *testing.T) {
	inner := &shortEmbedder{}
	c := mustCached(t, inner, 8)
	ctx := context.Background()

	_, err := c.Passages(ctx, []string{"a", "b"})
	if !errors.Is(err, core.ErrProvider) {
		t.Fatalf("err = %v, want %v", err, core.ErrProvider)
	}
	if c.Len() != 0 {
		t.Fatalf("a short result cached %d entries", c.Len())
	}
}

func TestCachedDelegatesIdentity(t *testing.T) {
	inner := &countingEmbedder{model: "test-model", dim: 12}
	c := mustCached(t, inner, 8)
	if c.Model() != "test-model" {
		t.Fatalf("Model() = %q, want test-model", c.Model())
	}
	if c.Dim() != 12 {
		t.Fatalf("Dim() = %d, want 12", c.Dim())
	}
}

func TestCachedRequiresEmbedder(t *testing.T) {
	if _, err := NewCached(nil, CacheConfig{Size: 8}); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("err = %v, want %v", err, core.ErrInvalidConfig)
	}
}

func TestCachedEmptyInput(t *testing.T) {
	inner := &countingEmbedder{model: "test-model", dim: 4}
	c := mustCached(t, inner, 8)
	vecs, err := c.Passages(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 0 {
		t.Fatalf("vectors = %v, want none", vecs)
	}
	if inner.batches() != 0 {
		t.Fatalf("empty input reached the embedder %d times", inner.batches())
	}
}

func TestCachedKeyDoesNotRetainText(t *testing.T) {
	secret := "patient 42 has diagnosis zoster and lives at 221b"
	key := cacheKey(rolePassage, "test-model", secret)
	if strings.Contains(key, secret) || strings.Contains(key, "zoster") || strings.Contains(key, "221b") {
		t.Fatalf("key %q carries the text it hashes", key)
	}
	for _, r := range key {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("key %q is not a hex digest", key)
		}
	}
}

type shortEmbedder struct{ countingEmbedder }

func (f *shortEmbedder) Passages(_ context.Context, texts []string) ([][]float32, error) {
	vecs, _ := f.record(texts, rolePassage)
	if len(texts) == 0 {
		return vecs, nil
	}
	return vecs[:len(texts)-1], nil
}
