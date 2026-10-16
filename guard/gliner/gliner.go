//go:build !ort

package gliner

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/internal/core"
)

type Config struct {
	Dir   string
	Model string
}

type Detector struct {
	cfg Config
}

func New(cfg Config) (*Detector, error) {
	if cfg.Dir == "" {
		return nil, fmt.Errorf("gliner: models dir is required: %w", core.ErrInvalidConfig)
	}
	return &Detector{cfg: cfg}, nil
}

func (*Detector) Name() string { return "L2" }

func (*Detector) Detect(context.Context, string) ([]guard.Finding, error) {
	return nil, fmt.Errorf("gliner: built without ORT support; rebuild with -tags ort: %w", core.ErrGuardUnavailable)
}

func (*Detector) Health() core.HealthStatus {
	return core.HealthStatus{Detail: "gliner: built without ORT support; rebuild with -tags ort"}
}

func VerifyDir(dir, model string) error {
	dir = resolveModelsDir(dir)
	if err := verifyFiles(dir, model); err != nil {
		return err
	}
	return parseConfigJSON(filepath.Join(dir, model, "config.json"))
}

func CheckParity(_, _ string, _ float64) (Report, error) {
	return Report{}, fmt.Errorf("gliner: parity gate requires the ort build tag and the native ONNX runtime; rebuild with -tags ort: %w", core.ErrGuardUnavailable)
}
