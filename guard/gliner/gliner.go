//go:build !ort

package gliner

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/internal/core"
)

// Config selects the model directory and model name for the L2 detector.
type Config struct {
	Dir   string
	Model string
}

// Detector is the L2 GLiNER detector; without the ort build tag it is a stub
// whose Detect fails with ErrGuardUnavailable.
type Detector struct {
	cfg Config
}

// New returns a stub detector after checking that a models directory is set;
// detection requires a build with the ort tag.
func New(cfg Config) (*Detector, error) {
	if cfg.Dir == "" {
		return nil, fmt.Errorf("gliner: models dir is required: %w", core.ErrInvalidConfig)
	}
	return &Detector{cfg: cfg}, nil
}

// Name returns "L2".
func (*Detector) Name() string { return "L2" }

// Detect always returns ErrGuardUnavailable: the default build has no ONNX runtime.
func (*Detector) Detect(context.Context, string) ([]guard.Finding, error) {
	return nil, fmt.Errorf("gliner: built without ORT support; rebuild with -tags ort: %w", core.ErrGuardUnavailable)
}

// Health reports that the detector was built without ORT support.
func (*Detector) Health() core.HealthStatus {
	return core.HealthStatus{Detail: "gliner: built without ORT support; rebuild with -tags ort"}
}

// VerifyDir checks that the model directory contains the required files and a
// parseable config.json.
func VerifyDir(dir, model string) error {
	dir = resolveModelsDir(dir)
	if err := verifyFiles(dir, model); err != nil {
		return err
	}
	return parseConfigJSON(filepath.Join(dir, model, "config.json"))
}

// CheckParity always returns ErrGuardUnavailable: the parity gate needs the ort
// build tag and the native ONNX runtime.
func CheckParity(_, _ string, _ float64) (Report, error) {
	return Report{}, fmt.Errorf("gliner: parity gate requires the ort build tag and the native ONNX runtime; rebuild with -tags ort: %w", core.ErrGuardUnavailable)
}
