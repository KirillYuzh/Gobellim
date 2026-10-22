package rerank

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/httpjson"
)

// TEIConfig configures NewTEI. BaseURL is required; APIKey, when set, is sent as
// a Bearer token, Headers are sent with every request, and Model, when set, is
// used as the client name.
//
// Truncate is the server-side truncation policy for long documents: a nil
// pointer keeps the default (the server truncates), an explicit false lets the
// server reject over-long inputs instead of cutting them.
type TEIConfig struct {
	BaseURL    string
	Model      string
	APIKey     string
	Headers    map[string]string
	HTTPClient *http.Client
	// Truncate is the "truncate" flag sent to the server; nil means true.
	Truncate *bool
}

// TEIClient is a Reranker backed by a Text Embeddings Inference /rerank endpoint.
type TEIClient struct {
	cfg TEIConfig
	hc  *http.Client
	// truncate is the resolved truncation policy: TEIConfig.Truncate when it is
	// set, true otherwise. It is resolved once so the client never aliases a
	// caller-owned value.
	truncate bool
}

// NewTEI returns a TEI reranker. BaseURL is required, otherwise it returns
// ErrInvalidConfig. Truncate defaults to true when TEIConfig.Truncate is nil.
func NewTEI(cfg TEIConfig) (*TEIClient, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("rerank: BaseURL is required: %w", core.ErrInvalidConfig)
	}
	truncate := true
	if cfg.Truncate != nil {
		truncate = *cfg.Truncate
	}
	return &TEIClient{cfg: cfg, hc: httpjson.Client(cfg.HTTPClient), truncate: truncate}, nil
}

// Name returns Model when configured, otherwise "tei".
func (c *TEIClient) Name() string {
	if c.cfg.Model != "" {
		return c.cfg.Model
	}
	return "tei"
}

// Rerank posts the query and documents to /rerank and parses the returned
// indices and scores; upstream and decode failures are wrapped with ErrProvider.
func (c *TEIClient) Rerank(ctx context.Context, query string, documents []string) ([]Rank, error) {
	if len(documents) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(c.cfg.Headers)+1)
	for k, v := range c.cfg.Headers {
		headers[k] = v
	}
	if c.cfg.APIKey != "" {
		headers["Authorization"] = "Bearer " + c.cfg.APIKey
	}
	body := map[string]any{"query": query, "texts": documents, "truncate": c.truncate}

	var raw json.RawMessage
	if err := httpjson.Do(ctx, c.hc, http.MethodPost, httpjson.JoinURL(c.cfg.BaseURL, "/rerank"), headers, body, &raw); err != nil {
		return nil, fmt.Errorf("rerank: %s: %w: %w", c.Name(), err, core.ErrProvider)
	}
	ranks, err := parseRanks(raw, len(documents))
	if err != nil {
		return nil, fmt.Errorf("rerank: %s: %w: %w", c.Name(), err, core.ErrProvider)
	}
	return ranks, nil
}

type item struct {
	Index          int      `json:"index"`
	Score          *float64 `json:"score"`
	RelevanceScore *float64 `json:"relevance_score"`
}

// parseRanks decodes a TEI (array) or Cohere ({"results": [...]}) response and
// converts it to ranks. docCount is the number of documents sent; every index
// in the response must address one of them.
func parseRanks(raw []byte, docCount int) ([]Rank, error) {
	var list []item
	if err := json.Unmarshal(raw, &list); err == nil {
		return toRanks(list, docCount)
	}
	var envelope struct {
		Results []item `json:"results"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return toRanks(envelope.Results, docCount)
}

// toRanks converts decoded items to ranks. The score is the "score" field when
// the server sent it, otherwise the "relevance_score" field, otherwise zero;
// the presence of the key decides, not its value, so a legitimate zero score is
// not overwritten by a non-zero relevance score.
func toRanks(items []item, docCount int) ([]Rank, error) {
	out := make([]Rank, 0, len(items))
	for _, it := range items {
		if it.Index < 0 || it.Index >= docCount {
			return nil, fmt.Errorf("index %d out of range [0,%d)", it.Index, docCount)
		}
		score := 0.0
		switch {
		case it.Score != nil:
			score = *it.Score
		case it.RelevanceScore != nil:
			score = *it.RelevanceScore
		}
		out = append(out, Rank{Index: it.Index, Score: score})
	}
	return out, nil
}
