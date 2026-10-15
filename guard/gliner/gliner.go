//go:build !ort

// Package gliner is the ML guard layer; without the ort build tag it fails
// closed.
package gliner

import (
	"context"
	"fmt"

	"github.com/gobellim/kit/guard"
	"github.com/gobellim/kit/internal/core"
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

func (*Detector) Detect(context.Context, guard.Rail, string) ([]guard.Finding, error) {
	return nil, fmt.Errorf("gliner: built without ORT support; rebuild with -tags ort: %w", core.ErrGuardUnavailable)
}
