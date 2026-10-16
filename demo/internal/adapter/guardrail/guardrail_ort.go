//go:build ort

package guardrail

import (
	"log/slog"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/guard/gliner"
)

const sessions = 2

func New(dir, model string, log *slog.Logger) guard.Detector {
	detector, err := gliner.New(gliner.Config{Dir: dir, Model: model, Sessions: sessions})
	if err != nil {
		log.Warn("guard L2 unavailable", "model", model, "err", err)
		return nil
	}
	log.Info("guard L2 enabled", "model", model, "dir", dir)
	return detector
}
