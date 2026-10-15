// Package embed defines the embeddings API and its providers.
package embed

import (
	"context"
	"math"
)

// Embedder turns texts into vectors. Query and Passages are separate because
// models such as e5 need distinct prefixes.
type Embedder interface {
	Query(ctx context.Context, texts []string) ([][]float32, error)
	Passages(ctx context.Context, texts []string) ([][]float32, error)
	Dim() int
	Model() string
}

func normalizeL2(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}
