package embed

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func benchDoc(paragraphs int) []string {
	const sentence = "The retrieval pipeline guards untrusted input, masks personal data and bounds every call the model makes. "
	chunks := make([]string, 0, paragraphs)
	for i := range paragraphs {
		chunks = append(chunks, fmt.Sprintf("paragraph %d: %s", i, strings.Repeat(sentence, 4)))
	}
	return chunks
}

const benchDim = 1024

type benchEmbedder struct {
	countingEmbedder
	latency time.Duration
}

func (b *benchEmbedder) Query(_ context.Context, texts []string) ([][]float32, error) {
	return b.record(texts, roleQuery)
}

func (b *benchEmbedder) Passages(_ context.Context, texts []string) ([][]float32, error) {
	return b.record(texts, rolePassage)
}

func (b *benchEmbedder) record(texts []string, role cacheRole) ([][]float32, error) {
	b.mu.Lock()
	b.calls = append(b.calls, append([]string(nil), texts...))
	b.mu.Unlock()
	if b.latency > 0 {
		time.Sleep(b.latency)
	}
	vecs := make([][]float32, len(texts))
	for i, t := range texts {
		vecs[i] = synthVector(role, t, benchDim)
	}
	return vecs, nil
}

func benchCached(b *testing.B, inner Embedder, size int) *CachedEmbedder {
	b.Helper()
	c, err := NewCached(inner, CacheConfig{Size: size})
	if err != nil {
		b.Fatal(err)
	}
	return c
}

func BenchmarkIngestRepeat(b *testing.B) {
	chunks := benchDoc(12)
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		latency time.Duration
	}{
		{"local-5ms", 5 * time.Millisecond},
		{"remote-20ms", 20 * time.Millisecond},
	} {
		b.Run(tc.name+"/cold/no-cache", func(b *testing.B) {
			inner := &benchEmbedder{latency: tc.latency}
			inner.model, inner.dim = "bench-model", benchDim
			c := benchCached(b, inner, 0)
			b.ResetTimer()
			for b.Loop() {
				if _, err := c.Passages(ctx, chunks); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(tc.name+"/cold/cache", func(b *testing.B) {
			inner := &benchEmbedder{latency: tc.latency}
			inner.model, inner.dim = "bench-model", benchDim
			b.ResetTimer()
			for b.Loop() {
				c := benchCached(b, inner, 128)
				if _, err := c.Passages(ctx, chunks); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(tc.name+"/repeat/cache", func(b *testing.B) {
			inner := &benchEmbedder{latency: tc.latency}
			inner.model, inner.dim = "bench-model", benchDim
			c := benchCached(b, inner, 128)
			if _, err := c.Passages(ctx, chunks); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for b.Loop() {
				vecs, err := c.Passages(ctx, chunks)
				if err != nil {
					b.Fatal(err)
				}
				if len(vecs) != len(chunks) {
					b.Fatalf("%d vectors, want %d", len(vecs), len(chunks))
				}
			}
			b.StopTimer()
			if got := inner.embedded(); got != len(chunks) {
				b.Fatalf("the warm pass embedded %d texts, want %d", got, len(chunks))
			}
		})
	}
}

func BenchmarkEmbedCacheOverhead(b *testing.B) {
	chunks := benchDoc(12)
	ctx := context.Background()

	b.Run("direct", func(b *testing.B) {
		inner := &benchEmbedder{}
		inner.model, inner.dim = "bench-model", benchDim
		b.ResetTimer()
		for b.Loop() {
			if _, err := inner.Passages(ctx, chunks); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("cached-hit", func(b *testing.B) {
		inner := &benchEmbedder{}
		inner.model, inner.dim = "bench-model", benchDim
		c := benchCached(b, inner, 128)
		if _, err := c.Passages(ctx, chunks); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for b.Loop() {
			if _, err := c.Passages(ctx, chunks); err != nil {
				b.Fatal(err)
			}
		}
	})
}
