package embed

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/httpjson"
	"github.com/KirillYuzh/Gobellim/internal/oauth"
)

const (
	// GigaChatDefaultBaseURL is used when GigaChatConfig.BaseURL is empty.
	GigaChatDefaultBaseURL = "https://api.giga.chat"
	// GigaChatDefaultAuthURL is used when GigaChatConfig.AuthURL is empty.
	GigaChatDefaultAuthURL = "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	// GigaChatDefaultScope is used when GigaChatConfig.Scope is empty.
	GigaChatDefaultScope = "GIGACHAT_API_PERS"
	// GigaChatDefaultModel is used when GigaChatConfig.Model is empty.
	GigaChatDefaultModel = "Embeddings"
)

// GigaChatConfig configures NewGigaChat. Credentials is required; empty BaseURL,
// AuthURL, Scope, and Model use the GigaChatDefault* values, and Preset overrides
// the model-derived preset.
type GigaChatConfig struct {
	BaseURL     string
	AuthURL     string
	Credentials string
	Scope       string
	Model       string
	Dim         int
	Preset      *Preset
	HTTPClient  *http.Client
}

// GigaChatEmbedder is an Embedder backed by the GigaChat /v1/embeddings API,
// authenticating with an OAuth token obtained from Credentials.
type GigaChatEmbedder struct {
	cfg    GigaChatConfig
	hc     *http.Client
	ts     *oauth.TokenSource
	preset Preset
	mu     sync.RWMutex
	dim    int
}

// NewGigaChat returns a GigaChat embedder. Credentials is required; empty
// BaseURL, AuthURL, Scope, and Model fall back to the GigaChatDefault* constants.
func NewGigaChat(cfg GigaChatConfig) (*GigaChatEmbedder, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = GigaChatDefaultBaseURL
	}
	if cfg.AuthURL == "" {
		cfg.AuthURL = GigaChatDefaultAuthURL
	}
	if cfg.Scope == "" {
		cfg.Scope = GigaChatDefaultScope
	}
	if cfg.Model == "" {
		cfg.Model = GigaChatDefaultModel
	}
	if cfg.Credentials == "" {
		return nil, fmt.Errorf("embed: gigachat: Credentials is required: %w", core.ErrInvalidConfig)
	}
	hc := httpjson.Client(cfg.HTTPClient)
	return &GigaChatEmbedder{
		cfg:    cfg,
		hc:     hc,
		ts:     &oauth.TokenSource{AuthURL: cfg.AuthURL, Credentials: cfg.Credentials, Scope: cfg.Scope, HTTPClient: hc},
		preset: resolvePreset(cfg.Preset, cfg.Model),
		dim:    cfg.Dim,
	}, nil
}

// Model returns the configured model name.
func (e *GigaChatEmbedder) Model() string { return e.cfg.Model }

// Dim returns the configured dimension, or the first dimension observed from a
// response when none was configured.
func (e *GigaChatEmbedder) Dim() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dim
}

func (e *GigaChatEmbedder) setDim(n int) {
	e.mu.Lock()
	if e.dim == 0 {
		e.dim = n
	}
	e.mu.Unlock()
}

// Query embeds search query texts with the preset's query prefix, normalizing
// each vector when the preset requests it.
func (e *GigaChatEmbedder) Query(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embed(ctx, texts, e.preset.QueryPrefix)
}

// Passages embeds document texts with the preset's passage prefix, normalizing
// each vector when the preset requests it.
func (e *GigaChatEmbedder) Passages(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embed(ctx, texts, e.preset.PassagePrefix)
}

func (e *GigaChatEmbedder) embed(ctx context.Context, texts []string, prefix string) ([][]float32, error) {
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
	vecs, err := postEmbeddings(ctx, e.hc, httpjson.JoinURL(e.cfg.BaseURL, "/v1/embeddings"), headers, e.cfg.Model, in)
	if err != nil {
		return nil, err
	}
	if len(vecs) > 0 {
		e.setDim(len(vecs[0]))
	}
	if e.preset.Normalize {
		for _, v := range vecs {
			normalizeL2(v)
		}
	}
	return vecs, nil
}
