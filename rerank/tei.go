package rerank

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/httpjson"
)

type TEIConfig struct {
	BaseURL    string
	Model      string
	APIKey     string
	Headers    map[string]string
	HTTPClient *http.Client
}

type TEIClient struct {
	cfg TEIConfig
	hc  *http.Client
}

func NewTEI(cfg TEIConfig) (*TEIClient, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("rerank: BaseURL is required: %w", core.ErrInvalidConfig)
	}
	return &TEIClient{cfg: cfg, hc: httpjson.Client(cfg.HTTPClient)}, nil
}

func (c *TEIClient) Name() string {
	if c.cfg.Model != "" {
		return c.cfg.Model
	}
	return "tei"
}

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
	body := map[string]any{"query": query, "texts": documents, "truncate": true}

	var raw json.RawMessage
	if err := httpjson.Do(ctx, c.hc, http.MethodPost, httpjson.JoinURL(c.cfg.BaseURL, "/rerank"), headers, body, &raw); err != nil {
		return nil, fmt.Errorf("rerank: %s: %w: %w", c.Name(), err, core.ErrProvider)
	}
	ranks, err := parseRanks(raw)
	if err != nil {
		return nil, fmt.Errorf("rerank: %s: %w: %w", c.Name(), err, core.ErrProvider)
	}
	return ranks, nil
}

type item struct {
	Index          int     `json:"index"`
	Score          float64 `json:"score"`
	RelevanceScore float64 `json:"relevance_score"`
}

func parseRanks(raw []byte) ([]Rank, error) {
	var list []item
	if err := json.Unmarshal(raw, &list); err == nil {
		return toRanks(list), nil
	}
	var envelope struct {
		Results []item `json:"results"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return toRanks(envelope.Results), nil
}

func toRanks(items []item) []Rank {
	out := make([]Rank, 0, len(items))
	for _, it := range items {
		score := it.Score
		if score == 0 && it.RelevanceScore != 0 {
			score = it.RelevanceScore
		}
		out = append(out, Rank{Index: it.Index, Score: score})
	}
	return out
}
