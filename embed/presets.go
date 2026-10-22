package embed

import (
	"encoding/json"
	"errors"
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

// familyPreset is a known model family and the preset it needs.
type familyPreset struct {
	match  string
	preset Preset
}

var familyPresets = []familyPreset{
	{"multilingual-e5", presetE5},
	{"bge-m3", presetBGE},
}

// errNoFamily marks a model name that identifies no known family. It is a
// sentinel rather than a failure: a provider may serve any model, so the
// caller decides whether an unidentified family is allowed.
var errNoFamily = errors.New("no known family")

// PresetFor returns the built-in preset of the model family a name identifies,
// such as multilingual-e5 or bge-m3.
//
// A family is identified when a path segment of the model name (the name is
// split on "/") equals the family name or starts with it followed by a
// separator ('-', '_' or '.'); the comparison is case-insensitive. So
// "intfloat/multilingual-e5-small" identifies multilingual-e5 and
// "bge-m3-tuned" identifies bge-m3, while "my-bge-m3-tuned" identifies no
// family: a family name in the middle of a segment is not enough to pick its
// pooling and normalization, and guessing silently changes the vectors.
//
// PresetFor reports false for a name that identifies no family and for a name
// that identifies several of them. A caller that must not guess — the local
// encoder, the remote embedder constructor — treats such a name as an error
// instead of a default.
func PresetFor(model string) (Preset, bool) {
	p, err := presetForModel(model)
	if err != nil {
		return Preset{}, false
	}
	return p, true
}

// presetForModel resolves the built-in preset of the family a model name
// identifies. It returns an error wrapping errNoFamily when the name
// identifies no family, and an error wrapping core.ErrInvalidConfig when the
// name identifies several families or only mentions one: an ambiguous name
// must be resolved by the caller, not guessed.
func presetForModel(model string) (Preset, error) {
	named := namedFamilies(model)
	if len(named) == 1 {
		return named[0].preset, nil
	}
	if len(named) > 1 {
		names := make([]string, 0, len(named))
		for _, f := range named {
			names = append(names, f.match)
		}
		return Preset{}, fmt.Errorf("model %q identifies several known families (%s): %w",
			model, strings.Join(names, ", "), core.ErrInvalidConfig)
	}
	if mention := mentionedFamily(model); mention != "" {
		return Preset{}, fmt.Errorf("model %q mentions the known family %q but does not identify it: %w",
			model, mention, core.ErrInvalidConfig)
	}
	return Preset{}, fmt.Errorf("model %q: %w", model, errNoFamily)
}

// namedFamilies returns the known families the model name identifies.
func namedFamilies(model string) []familyPreset {
	var out []familyPreset
	for _, segment := range strings.Split(strings.ToLower(model), "/") {
		for _, f := range familyPresets {
			if identifiesFamily(segment, f.match) && !hasFamily(out, f.match) {
				out = append(out, f)
			}
		}
	}
	return out
}

// identifiesFamily reports whether a path segment names the family: it equals
// the family name or starts with it followed by a separator.
func identifiesFamily(segment, family string) bool {
	if !strings.HasPrefix(segment, family) {
		return false
	}
	rest := segment[len(family):]
	if rest == "" {
		return true
	}
	return rest[0] == '-' || rest[0] == '_' || rest[0] == '.'
}

// mentionedFamily returns the known family the model name mentions without
// identifying it.
func mentionedFamily(model string) string {
	m := strings.ToLower(model)
	for _, f := range familyPresets {
		if strings.Contains(m, f.match) {
			return f.match
		}
	}
	return ""
}

func hasFamily(list []familyPreset, match string) bool {
	for _, f := range list {
		if f.match == match {
			return true
		}
	}
	return false
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
