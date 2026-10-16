package embed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gobellim/kit/internal/core"
)

type Pooling string

const (
	PoolingMean Pooling = "mean"
	PoolingCLS  Pooling = "cls"
)

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

func PresetFor(model string) (Preset, bool) {
	m := strings.ToLower(model)
	for _, f := range familyPresets {
		if strings.Contains(m, f.match) {
			return f.preset, true
		}
	}
	return Preset{}, false
}

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

type LocalConfig struct {
	Dir      string
	Model    string
	Sessions int
}

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

func LoadFileConfig(path string) (Preset, error) {
	f, err := os.Open(path)
	if err != nil {
		return Preset{}, fmt.Errorf("embed: open %s: %w: %w", path, err, core.ErrInvalidConfig)
	}
	defer f.Close()

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
