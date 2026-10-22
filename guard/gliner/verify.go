package gliner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

var guardModelFiles = []string{"model.onnx", "tokenizer.json", "config.json"}

func resolveModelsDir(dir string) string {
	if dir == "" {
		if v := os.Getenv("KIT_MODELS_DIR"); v != "" {
			return v
		}
		return "./models"
	}
	return dir
}

func invalidConfig(err error) error {
	return fmt.Errorf("gliner: %w: %w", err, core.ErrInvalidConfig)
}

func verifyFiles(dir, model string) error {
	if model == "" {
		return invalidConfig(errors.New("model name is required"))
	}
	base := filepath.Join(dir, model)
	for _, name := range guardModelFiles {
		path := filepath.Join(base, name)
		if _, err := os.Stat(path); err != nil {
			return invalidConfig(fmt.Errorf("model file %s: %w", path, err))
		}
	}
	// A config that is not valid JSON would fail later, in the middle of a
	// run; reject it here, where the operator can still act on it.
	return parseConfigJSON(filepath.Join(base, "config.json"))
}

func parseConfigJSON(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return invalidConfig(fmt.Errorf("config %s: %w", path, err))
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return invalidConfig(fmt.Errorf("config %s: %w", path, err))
	}
	return nil
}
