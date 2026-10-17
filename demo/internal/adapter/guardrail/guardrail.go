//go:build !ort

package guardrail

import (
	"log/slog"

	"github.com/KirillYuzh/Gobellim/guard"
)

func New(_, _ string, log *slog.Logger) guard.Detector {
	log.Info("guard L2 not enabled: running L1 only", "hint", "rebuild with -tags ort")
	return guard.L1Only()
}
