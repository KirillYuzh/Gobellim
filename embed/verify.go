package embed

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

var localModelFiles = []string{"model.onnx", "tokenizer.json", "config.json"}

func resolveModelsDir(dir string) string {
	if dir == "" {
		if v := os.Getenv("KIT_MODELS_DIR"); v != "" {
			return v
		}
		return "./models"
	}
	return dir
}

func verifyDir(dir, model string) error {
	dir = resolveModelsDir(dir)
	if model == "" {
		return fmt.Errorf("embed: model is required: %w", core.ErrInvalidConfig)
	}
	d := ModelDir(dir, model)
	for _, name := range localModelFiles {
		path := filepath.Join(d, name)
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("embed: model file %s: %w: %w", path, err, core.ErrInvalidConfig)
		}
	}
	preset, err := verifyPreset(d, model)
	if err != nil {
		return err
	}
	if err := preset.Validate(); err != nil {
		return err
	}
	return nil
}

func verifyPreset(dir, model string) (Preset, error) {
	fileCfg := filepath.Join(dir, "kit_embedder.json")
	if _, err := os.Stat(fileCfg); err == nil {
		p, lerr := LoadFileConfig(fileCfg)
		if lerr != nil {
			return Preset{}, lerr
		}
		return p, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Preset{}, fmt.Errorf("embed: stat %s: %w: %w", fileCfg, err, core.ErrInvalidConfig)
	}
	if p, ok := PresetFor(model); ok {
		return p, nil
	}
	return Preset{}, fmt.Errorf("embed: no preset for model %q and no kit_embedder.json in %s: %w", model, dir, core.ErrInvalidConfig)
}
