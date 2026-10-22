package gigachat

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/KirillYuzh/Gobellim/embed"
	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/httpjson"
	"github.com/KirillYuzh/Gobellim/internal/oauth"
)

// EmbedConfig configures the GigaChat embedder. Credentials is required; empty
// BaseURL, AuthURL, Scope and Model use the Default* values, and Preset
// overrides the model-derived preset. BatchSize caps the number of texts sent
// in one request; zero uses embed.DefaultRemoteBatchSize.
type EmbedConfig struct {
	BaseURL     string
	AuthURL     string
	Credentials string
	Scope       string
	Model       string
	Dim         int
	Preset      *embed.Preset
	BatchSize   int
	HTTPClient  *http.Client
}

// Embedder is an embed.Embedder backed by the GigaChat /v1/embeddings API,
// authenticating with an OAuth token obtained from Credentials.
type Embedder struct {
	cfg    EmbedConfig
	hc     *http.Client
	ts     *oauth.TokenSource
	preset embed.Preset
	batch  int
	mu     sync.RWMutex
	dim    int
}

// NewEmbedder returns a GigaChat embedder. Credentials is required; empty
// BaseURL, AuthURL, Scope and Model fall back to the Default* constants.
func NewEmbedder(cfg EmbedConfig) (*Embedder, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.AuthURL == "" {
		cfg.AuthURL = DefaultAuthURL
	}
	if cfg.Scope == "" {
		cfg.Scope = DefaultScope
	}
	if cfg.Model == "" {
		cfg.Model = DefaultEmbedModel
	}
	if cfg.Credentials == "" {
		return nil, fmt.Errorf("gigachat: Credentials is required: %w", core.ErrInvalidConfig)
	}
	hc := httpjson.Client(cfg.HTTPClient)
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = embed.DefaultRemoteBatchSize
	}
	return &Embedder{
		cfg:    cfg,
		hc:     hc,
		ts:     &oauth.TokenSource{AuthURL: cfg.AuthURL, Credentials: cfg.Credentials, Scope: cfg.Scope, HTTPClient: hc},
		preset: embed.ResolvePreset(cfg.Preset, cfg.Model),
		batch:  batch,
		dim:    cfg.Dim,
	}, nil
}

// Model returns the configured embedding model name.
func (e *Embedder) Model() string { return e.cfg.Model }

// Dim returns the configured dimension, or the first dimension observed from a
// response when none was configured.
func (e *Embedder) Dim() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dim
}

// setDim remembers the dimension of the first response and rejects a later
// response of a different size: a dimension that changes under a running
// index corrupts it silently.
func (e *Embedder) setDim(n int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dim == 0 {
		e.dim = n
		return nil
	}
	if e.dim != n {
		return fmt.Errorf("gigachat: embed: dimension %d does not match the expected %d: %w",
			n, e.dim, core.ErrProvider)
	}
	return nil
}

// checkDim validates the vectors of one batch against the dimension the
// embedder already knows.
func (e *Embedder) checkDim(vecs [][]float32) error {
	if len(vecs) == 0 {
		return nil
	}
	dim := len(vecs[0])
	for _, v := range vecs {
		if len(v) != dim {
			return fmt.Errorf("gigachat: embed: batch mixes vector dimensions %d and %d: %w",
				dim, len(v), core.ErrProvider)
		}
	}
	return e.setDim(dim)
}

// Query embeds search query texts with the preset's query prefix, normalizing
// each vector when the preset requests it.
func (e *Embedder) Query(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embed(ctx, texts, e.preset.QueryPrefix)
}

// Passages embeds document texts with the preset's passage prefix, normalizing
// each vector when the preset requests it.
func (e *Embedder) Passages(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embed(ctx, texts, e.preset.PassagePrefix)
}

func (e *Embedder) embed(ctx context.Context, texts []string, prefix string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	token, err := e.ts.Token(ctx)
	if err != nil {
		return nil, err
	}
	in := make([]string, len(texts))
	for i, t := range texts {
		in[i] = prefix + t
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	url := httpjson.JoinURL(e.cfg.BaseURL, "/v1/embeddings")
	size := e.batch
	if size <= 0 {
		size = embed.DefaultRemoteBatchSize
	}
	out := make([][]float32, 0, len(in))
	for start := 0; start < len(in); start += size {
		end := min(start+size, len(in))
		vecs, err := embed.PostEmbeddings(ctx, e.hc, url, headers, e.cfg.Model, in[start:end])
		if err != nil {
			return nil, err
		}
		if err := e.checkDim(vecs); err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	if e.preset.Normalize {
		for _, v := range out {
			embed.NormalizeL2(v)
		}
	}
	return out, nil
}
