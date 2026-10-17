package embed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// Pooling selects how token embeddings are reduced to one vector.
type Pooling string

const (
	// PoolingMean averages token embeddings over the attention mask.
	PoolingMean Pooling = "mean"
	// PoolingCLS uses the first token's embedding.
	PoolingCLS Pooling = "cls"
)

// Preset describes how an embedding model preprocesses text: prefixes, pooling,
// L2 normalization, and a maximum length. MaxLen is applied only by the local
// ONNX encoder; remote providers truncate server-side.
type Preset struct {
	Name          string
	Pooling       Pooling
	QueryPrefix   string
	PassagePrefix string
	Normalize     bool
	MaxLen        int
}

var (
	presetE5  = Preset{Name: "e5", Pooling: PoolingMean, QueryPrefix: "query: ", PassagePrefix: "passage: ", Normalize: true, MaxLen: 512}
	presetBGE = Preset{Name: "bge-m3", Pooling: PoolingCLS, Normalize: true, MaxLen: 8192}
)

var familyPresets = []struct {
	match  string
	preset Preset
}{
	{"multilingual-e5", presetE5},
	{"bge-m3", presetBGE},
}

// PresetFor returns a built-in preset for models whose name contains a known
// family such as multilingual-e5 or bge-m3. The match is case-insensitive.
func PresetFor(model string) (Preset, bool) {
	m := strings.ToLower(model)
	for _, f := range familyPresets {
		if strings.Contains(m, f.match) {
			return f.preset, true
		}
	}
	return Preset{}, false
}

// Validate reports an error if Pooling is not mean or cls, or MaxLen is not positive.
func (p Preset) Validate() error {
	switch p.Pooling {
	case PoolingMean, PoolingCLS:
	default:
		return fmt.Errorf("embed: preset: unknown pooling %q: %w", p.Pooling, core.ErrInvalidConfig)
	}
	if p.MaxLen <= 0 {
		return fmt.Errorf("embed: preset: max_len must be positive: %w", core.ErrInvalidConfig)
	}
	return nil
}

// LocalConfig configures NewLocal. Dir is the models root, defaulting to
// KIT_MODELS_DIR and then ./models, and Sessions is the ONNX session pool size,
// defaulting to DefaultSessions.
type LocalConfig struct {
	Dir      string
	Model    string
	Sessions int
}

// ModelDir returns the directory for model under modelsDir, replacing "/" in the
// model name with "__".
func ModelDir(modelsDir, model string) string {
	return filepath.Join(modelsDir, strings.ReplaceAll(model, "/", "__"))
}

type fileConfig struct {
	Pooling       string `json:"pooling"`
	QueryPrefix   string `json:"query_prefix"`
	PassagePrefix string `json:"passage_prefix"`
	Normalize     *bool  `json:"normalize"`
	MaxLen        int    `json:"max_len"`
}

// LoadFileConfig reads a kit_embedder.json preset. Unknown fields and invalid
// pooling values yield ErrInvalidConfig; an empty pooling defaults to mean and a
// non-positive max_len defaults to 512.
func LoadFileConfig(path string) (Preset, error) {
	f, err := os.Open(path)
	if err != nil {
		return Preset{}, fmt.Errorf("embed: open %s: %w: %w", path, err, core.ErrInvalidConfig)
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var fc fileConfig
	if err := dec.Decode(&fc); err != nil {
		return Preset{}, fmt.Errorf("embed: parse %s: %w: %w", path, err, core.ErrInvalidConfig)
	}

	p := Preset{
		Pooling:       Pooling(fc.Pooling),
		QueryPrefix:   fc.QueryPrefix,
		PassagePrefix: fc.PassagePrefix,
		MaxLen:        fc.MaxLen,
	}
	if fc.Normalize != nil {
		p.Normalize = *fc.Normalize
	}
	if p.Pooling == "" {
		p.Pooling = PoolingMean
	}
	if p.Pooling != PoolingMean && p.Pooling != PoolingCLS {
		return Preset{}, fmt.Errorf("embed: unknown pooling %q in %s: %w", fc.Pooling, path, core.ErrInvalidConfig)
	}
	if p.MaxLen <= 0 {
		p.MaxLen = 512
	}
	return p, nil
}
