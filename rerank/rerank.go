package rerank

import (
	"context"
	"sort"
)

// Rank pairs a document index with its relevance score; higher scores rank first.
type Rank struct {
	Index int
	Score float64
}

// Reranker scores documents against a query.
type Reranker interface {
	// Rerank returns relevance ranks for documents, typically in descending score order.
	Rerank(ctx context.Context, query string, documents []string) ([]Rank, error)
	// Name identifies the reranker in errors.
	Name() string
}

// Reorder returns indices 0..n-1 ordered by descending score, then by ascending
// index for ties, followed by indices absent from ranks in original order.
// Out-of-range and duplicate ranks are ignored, and a positive topK truncates the
// result.
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
