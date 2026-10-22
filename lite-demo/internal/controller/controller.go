// Package controller is the HTTP boundary: bind the request, call the usecase,
// write the response.
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

// Controller holds the HTTP dependencies.
type Controller struct {
	uc      *usecase.UseCase
	log     *slog.Logger
	timeout time.Duration
}

// New builds the controller.
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

// ctx gives one request one deadline: the step budget is not a time budget.
func (c *Controller) ctx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), c.timeout)
}
