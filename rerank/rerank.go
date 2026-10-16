package rerank

import (
	"context"
	"sort"
)

type Rank struct {
	Index int
	Score float64
}

type Reranker interface {
	Rerank(ctx context.Context, query string, documents []string) ([]Rank, error)
	Name() string
}

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
