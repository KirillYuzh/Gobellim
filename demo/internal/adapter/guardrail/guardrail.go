//go:build !ort

package guardrail

import (
	"log/slog"

	"github.com/gobellim/kit/guard"
)

func New(_, _ string, log *slog.Logger) guard.Detector {
	log.Info("guard L2 not enabled", "hint", "rebuild with -tags ort")
	return nil
}
