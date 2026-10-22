// Command lite-demo is the HTTP demo service.
package main

import (
	"log/slog"
	"os"

	"some-project/internal/app"
	"some-project/internal/config"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	if err := app.Run(cfg, log); err != nil {
		log.Error("app", "err", err)
		os.Exit(1)
	}
}
