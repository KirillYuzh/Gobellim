package embed

import (
	"context"
	"fmt"
	"net/http"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/httpjson"
)

type remoteRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type remoteResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// PostEmbeddings sends the OpenAI-compatible /embeddings request and returns
// one vector per input, in input order. Transport and shape errors are joined
// with core.ErrProvider. Remote embedder implementations use it to keep the
// request shape and error contract consistent.
func PostEmbeddings(ctx context.Context, hc *http.Client, url string, headers map[string]string, model string, input []string) ([][]float32, error) {
	var out remoteResponse
	if err := httpjson.Do(ctx, hc, http.MethodPost, url, headers, remoteRequest{Model: model, Input: input}, &out); err != nil {
		return nil, fmt.Errorf("embed: %w: %w", err, core.ErrProvider)
	}
	if len(out.Data) != len(input) {
		return nil, fmt.Errorf("embed: got %d vectors for %d inputs: %w", len(out.Data), len(input), core.ErrProvider)
	}
	vecs := make([][]float32, len(input))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			return nil, fmt.Errorf("embed: response index %d out of range: %w", d.Index, core.ErrProvider)
		}
		vecs[d.Index] = d.Embedding
	}
	return vecs, nil
}
