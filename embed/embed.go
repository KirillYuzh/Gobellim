package embed

import (
	"context"
	"math"
)

// Embedder computes vectors for search queries and for document passages.
// Query and Passages are separate because presets may prepend different prefixes.
type Embedder interface {
	// Query embeds search query texts with the preset's query prefix.
	Query(ctx context.Context, texts []string) ([][]float32, error)
	// Passages embeds document texts with the preset's passage prefix.
	Passages(ctx context.Context, texts []string) ([][]float32, error)
	// Dim reports the vector dimension, or 0 when it is not yet known.
	Dim() int
	// Model reports the model name used for requests and cache keys.
	Model() string
}

// Closer is implemented by embedders that hold resources such as ONNX sessions.
type Closer interface {
	// Close releases the embedder's resources.
	Close() error
}

// NormalizeL2 scales v to unit length in place; a zero vector is left as is.
// Remote embedder implementations apply it when the preset requests it.
func NormalizeL2(v []float32) {
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
