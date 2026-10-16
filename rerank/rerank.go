// Package rerank reorders retrieval candidates with a cross-encoder reranker.
// Hybrid search (BM25 + kNN fused by RRF) is a recall step; a reranker is the
// precision step that scores the candidates against the query before the caller
// sees the top-k.
package rerank

import (
	"context"
	"sort"
)

// Rank is one reranker verdict: the document's position in the input slice and
// its relevance score (higher is better).
type Rank struct {
	Index int
	Score float64
}

// Reranker scores documents against a query. Implementations talk to a
// cross-encoder server; the ordering and truncation live in Reorder so every
// implementation behaves the same.
type Reranker interface {
	Rerank(ctx context.Context, query string, documents []string) ([]Rank, error)
	Name() string
}

// Reorder turns the reranker's verdicts into a best-first order over n
// documents and truncates it to topK (topK <= 0 keeps everything).
//
// It is defensive on purpose: an out-of-range or repeated index is ignored, and
// documents the reranker did not score keep their original relative order at
// the end. The result is stable, so a no-op outcome is byte-for-byte the input
// order.
func Reorder(n int, ranks []Rank, topK int) []int {
	if n <= 0 {
		return nil
	}
	seen := make([]bool, n)
	scores := make([]float64, n)
	for _, r := range ranks {
		if r.Index < 0 || r.Index >= n || seen[r.Index] {
			continue
		}
		seen[r.Index] = true
		scores[r.Index] = r.Score
	}

	scored := make([]int, 0, n)
	unscored := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if seen[i] {
			scored = append(scored, i)
		} else {
			unscored = append(unscored, i)
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		a, b := scored[i], scored[j]
		if scores[a] != scores[b] {
			return scores[a] > scores[b]
		}
		return a < b
	})

	order := append(scored, unscored...)
	if topK > 0 && len(order) > topK {
		order = order[:topK]
	}
	return order
}
