package embed

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/gobellim/kit/internal/core"
	"github.com/gobellim/kit/internal/httpjson"
	"github.com/gobellim/kit/internal/oauth"
)

const (
	GigaChatDefaultBaseURL = "https://api.giga.chat"
	GigaChatDefaultAuthURL = "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	GigaChatDefaultScope   = "GIGACHAT_API_PERS"
	GigaChatDefaultModel   = "Embeddings"
)

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

type GigaChatEmbedder struct {
	cfg    GigaChatConfig
	hc     *http.Client
	ts     *oauth.TokenSource
	preset Preset
	mu     sync.RWMutex
	dim    int
}

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

func (e *GigaChatEmbedder) Model() string { return e.cfg.Model }

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

func (e *GigaChatEmbedder) Query(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embed(ctx, texts, e.preset.QueryPrefix)
}

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
