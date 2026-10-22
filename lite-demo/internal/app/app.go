// Package app собирает зависимости и владеет жизненным циклом сервера.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"some-project/internal/config"
	"some-project/internal/controller"
	"some-project/internal/llm"
	"some-project/internal/policy"
	"some-project/internal/repository"
	"some-project/internal/routes"
	"some-project/internal/usecase"
)

// Run собирает и запускает сервис до SIGINT или SIGTERM.
func Run(cfg config.Config, log *slog.Logger) error {
	pol, err := policy.Load(cfg.PolicyPath)
	if err != nil {
		return err
	}

	uc, err := usecase.New(usecase.Deps{
		LLM: llm.NewGigaChat(
			cfg.GigaChatKey, cfg.GigaChatModel, cfg.GigaChatBaseURL, cfg.GigaChatAuthURL, cfg.GigaChatScope,
		),
		Mock:   repository.NewMockd(cfg.MockURL),
		Policy: pol,
		Logger: log,
	})
	if err != nil {
		return err
	}

	log.Info("service ready", "tools", uc.Health().Tools, "gigachat", cfg.GigaChatModel, "mock", cfg.MockURL)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           routes.New(controller.New(uc, log, cfg.RequestTimeout)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// WriteTimeout согласован с дедлайном запроса, иначе успешный
		// ход обрывается на записи.
		WriteTimeout: cfg.RequestTimeout + 10*time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("http", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http: %w", err)
	case <-ctx.Done():
	}

	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}
