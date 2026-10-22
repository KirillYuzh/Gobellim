package embed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/httpjson"
)

// DefaultRemoteBatchSize is the maximum number of texts the remote embedder
// sends in one request. A document with thousands of chunks must not become
// one multi-megabyte POST.
const DefaultRemoteBatchSize = 64

// OpenAIConfig configures NewOpenAI. BaseURL and Model are required; the client
// posts to BaseURL joined with /embeddings, and Preset overrides the model-derived
// preset. BatchSize is the maximum number of texts per request and defaults to
// DefaultRemoteBatchSize.
type OpenAIConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	Dim        int
	Preset     *Preset
	HTTPClient *http.Client
	BatchSize  int
}

// OpenAIEmbedder is an Embedder backed by an OpenAI-compatible /embeddings endpoint.
type OpenAIEmbedder struct {
	cfg    OpenAIConfig
	hc     *http.Client
	preset Preset
	batch  int
	mu     sync.RWMutex
	dim    int
}

// NewOpenAI returns an embedder for an OpenAI-compatible API. BaseURL and Model
// are required, otherwise it returns ErrInvalidConfig. A model name that names
// no known family gets the empty preset; a name that only mentions a family is
// an error, because the embedder must not guess how the provider preprocesses
// its input.
func NewOpenAI(cfg OpenAIConfig) (*OpenAIEmbedder, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("embed: openai: BaseURL is required: %w", core.ErrInvalidConfig)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("embed: openai: Model is required: %w", core.ErrInvalidConfig)
	}
	preset, err := resolveRemotePreset(cfg.Preset, cfg.Model)
	if err != nil {
		return nil, err
	}
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = DefaultRemoteBatchSize
	}
	return &OpenAIEmbedder{
		cfg:    cfg,
		hc:     httpjson.Client(cfg.HTTPClient),
		preset: preset,
		batch:  batch,
		dim:    cfg.Dim,
	}, nil
}

// ResolvePreset returns the explicit preset when one is given, otherwise the
// built-in preset for the model name, otherwise an empty preset (no prefixes,
// no L2 normalization). Remote embedder implementations use it to agree on
// preprocessing.
//
// It never fails: a model whose name identifies no known family gets the
// empty preset. NewOpenAI is stricter and rejects such a model, because a
// wrong preset silently changes every vector it produces.
func ResolvePreset(explicit *Preset, model string) Preset {
	p, _ := resolveRemotePreset(explicit, model)
	return p
}

// resolveRemotePreset returns the preset a remote model needs: the explicit
// preset when one is given, the built-in preset when the model name
// identifies a known family, and an empty preset for a model that names no
// family. A name that identifies several families, or that only mentions
// one, is an error: the embedder must not guess how the provider preprocesses
// its input.
func resolveRemotePreset(explicit *Preset, model string) (Preset, error) {
	if explicit != nil {
		return *explicit, nil
	}
	p, err := presetForModel(model)
	if err != nil {
		if errors.Is(err, errNoFamily) {
			return Preset{}, nil
		}
		return Preset{}, fmt.Errorf("embed: openai: %w", err)
	}
	return p, nil
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

// setDim remembers the dimension of the first response and rejects a later
// response of a different size: a dimension that changes under a running
// index corrupts it silently.
func (e *OpenAIEmbedder) setDim(n int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dim == 0 {
		e.dim = n
		return nil
	}
	if e.dim != n {
		return fmt.Errorf("embed: openai: dimension %d does not match the expected %d: %w",
			n, e.dim, core.ErrProvider)
	}
	return nil
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
	url := httpjson.JoinURL(e.cfg.BaseURL, "/embeddings")
	size := e.batch
	if size <= 0 {
		// A zero-value embedder must not spin forever.
		size = DefaultRemoteBatchSize
	}
	out := make([][]float32, 0, len(in))
	for start := 0; start < len(in); start += size {
		end := min(start+size, len(in))
		vecs, err := PostEmbeddings(ctx, e.hc, url, headers, e.cfg.Model, in[start:end])
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
			NormalizeL2(v)
		}
	}
	return out, nil
}

// checkDim validates the vectors of one batch against the dimension the
// embedder already knows.
func (e *OpenAIEmbedder) checkDim(vecs [][]float32) error {
	if len(vecs) == 0 {
		return nil
	}
	dim := len(vecs[0])
	for _, v := range vecs {
		if len(v) != dim {
			return fmt.Errorf("embed: openai: batch mixes vector dimensions %d and %d: %w",
				dim, len(v), core.ErrProvider)
		}
	}
	return e.setDim(dim)
}
