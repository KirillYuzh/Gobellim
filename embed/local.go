//go:build !ort

package embed

import (
	"fmt"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// NewLocal returns an ONNX-backed embedder. In builds without the ort tag it
// always returns ErrInvalidConfig; the implementation lives in local_ort.go.
func NewLocal(cfg LocalConfig) (Embedder, error) {
	return nil, fmt.Errorf("embed: local ONNX embedder requires the ort build tag (dir=%s model=%s): %w", cfg.Dir, cfg.Model, core.ErrInvalidConfig)
}

// VerifyDir checks the model files and preset. This build only checks files and
// presets; the ort build also loads and closes the encoder.
func VerifyDir(dir, model string) error {
	return verifyDir(dir, model)
}
