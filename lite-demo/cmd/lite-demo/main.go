// Command lite-demo — HTTP-демо-сервис.
package main

import (
	"log/slog"
	"os"

	"some-project/internal/app"
	"some-project/internal/config"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
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
