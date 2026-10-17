package embed

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/httpjson"
)

// OpenAIConfig configures NewOpenAI. BaseURL and Model are required; the client
// posts to BaseURL joined with /embeddings, and Preset overrides the model-derived
// preset.
type OpenAIConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	Dim        int
	Preset     *Preset
	HTTPClient *http.Client
}

// OpenAIEmbedder is an Embedder backed by an OpenAI-compatible /embeddings endpoint.
type OpenAIEmbedder struct {
	cfg    OpenAIConfig
	hc     *http.Client
	preset Preset
	mu     sync.RWMutex
	dim    int
}

// NewOpenAI returns an embedder for an OpenAI-compatible API. BaseURL and Model
// are required, otherwise it returns ErrInvalidConfig.
func NewOpenAI(cfg OpenAIConfig) (*OpenAIEmbedder, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("embed: openai: BaseURL is required: %w", core.ErrInvalidConfig)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("embed: openai: Model is required: %w", core.ErrInvalidConfig)
	}
	return &OpenAIEmbedder{
		cfg:    cfg,
		hc:     httpjson.Client(cfg.HTTPClient),
		preset: resolvePreset(cfg.Preset, cfg.Model),
		dim:    cfg.Dim,
	}, nil
}

func resolvePreset(explicit *Preset, model string) Preset {
	if explicit != nil {
		return *explicit
	}
	if p, ok := PresetFor(model); ok {
		return p
	}
	return Preset{}
}

// Model returns the configured model name.
func (e *OpenAIEmbedder) Model() string { return e.cfg.Model }

// Dim returns the configured dimension, or the first dimension observed from a
// response when none was configured.
func (e *OpenAIEmbedder) Dim() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dim
}

func (e *OpenAIEmbedder) setDim(n int) {
	e.mu.Lock()
	if e.dim == 0 {
		e.dim = n
	}
	e.mu.Unlock()
}

// Query embeds search query texts with the preset's query prefix, normalizing
// each vector when the preset requests it.
func (e *OpenAIEmbedder) Query(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embed(ctx, texts, e.preset.QueryPrefix)
}

// Passages embeds document texts with the preset's passage prefix, normalizing
// each vector when the preset requests it.
func (e *OpenAIEmbedder) Passages(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embed(ctx, texts, e.preset.PassagePrefix)
}

func (e *OpenAIEmbedder) embed(ctx context.Context, texts []string, prefix string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	in := make([]string, len(texts))
	for i, t := range texts {
		in[i] = prefix + t
	}
	headers := map[string]string{}
	if e.cfg.APIKey != "" {
		headers["Authorization"] = "Bearer " + e.cfg.APIKey
	}
	vecs, err := postEmbeddings(ctx, e.hc, httpjson.JoinURL(e.cfg.BaseURL, "/embeddings"), headers, e.cfg.Model, in)
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
