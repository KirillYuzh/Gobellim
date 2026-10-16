//go:build !ort

package embed

import (
	"fmt"

	"github.com/gobellim/kit/internal/core"
)

func NewLocal(cfg LocalConfig) (Embedder, error) {
	return nil, fmt.Errorf("embed: local ONNX embedder requires the ort build tag (dir=%s model=%s): %w", cfg.Dir, cfg.Model, core.ErrInvalidConfig)
}

func VerifyDir(dir, model string) error {
	return verifyDir(dir, model)
}
