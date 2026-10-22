// Package controller — HTTP-граница: разбор запроса, вызов usecase, ответ.
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"some-project/internal/entity"
	"some-project/internal/errmap"
	"some-project/internal/usecase"
	"some-project/pkg/rest"
)

// Controller — зависимости HTTP-слоя.
type Controller struct {
	uc      *usecase.UseCase
	log     *slog.Logger
	timeout time.Duration
}

// New собирает контроллер.
func New(uc *usecase.UseCase, log *slog.Logger, timeout time.Duration) *Controller {
	if log == nil {
		log = slog.Default()
	}
	return &Controller{uc: uc, log: log, timeout: timeout}
}

func (c *Controller) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := rest.Bind(w, r, v); err != nil {
		errmap.Write(w, c.log, fmt.Errorf("%w: %w", entity.ErrInvalid, err))
		return false
	}
	return true
}

// ctx даёт одному запросу один дедлайн: бюджет шагов не заменяет бюджет времени.
func (c *Controller) ctx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), c.timeout)
}
