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
	if _, err := resolvePresetFromDir(d, model); err != nil {
		return err
	}
	return nil
}

// resolvePresetFromDir resolves the preset of a model directory: the
// kit_embedder.json file next to the weights when it exists, otherwise the
// built-in preset of the family the model name identifies. It is the single
// rule shared by VerifyDir and NewLocal, so a directory that verifies also
// loads and a directory that loads also verifies.
func resolvePresetFromDir(dir, model string) (Preset, error) {
	fileCfg := filepath.Join(dir, "kit_embedder.json")
	if _, err := os.Stat(fileCfg); err == nil {
		p, lerr := LoadFileConfig(fileCfg)
		if lerr != nil {
			return Preset{}, lerr
		}
		if verr := p.Validate(); verr != nil {
			return Preset{}, verr
		}
		return p, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Preset{}, fmt.Errorf("embed: stat %s: %w: %w", fileCfg, err, core.ErrInvalidConfig)
	}
	p, err := presetForModel(model)
	if err != nil {
		return Preset{}, fmt.Errorf("embed: preset: %w: %w (no kit_embedder.json in %s)", err, core.ErrInvalidConfig, dir)
	}
	if verr := p.Validate(); verr != nil {
		return Preset{}, verr
	}
	return p, nil
}
