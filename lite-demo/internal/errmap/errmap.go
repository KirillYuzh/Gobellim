// Package errmap — единственное место, где ошибка превращается в HTTP-ответ.
package errmap

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"some-project/internal/entity"
	"some-project/pkg/rest"
)

// Status выбирает HTTP-код. Отмена клиентом и дедлайн проверяются первыми:
// их мог обернуть любой промежуточный слой.
func Status(err error) int {
	switch {
	case errors.Is(err, context.Canceled):
		return 499 // клиент закрыл запрос
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.Is(err, entity.ErrInvalid):
		return http.StatusBadRequest
	case errors.Is(err, entity.ErrUnauthorized):
		return http.StatusForbidden
	case errors.Is(err, entity.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, entity.ErrBudget):
		return http.StatusTooManyRequests
	case errors.Is(err, entity.ErrUpstream):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// Message — безопасный текст для клиента: без деталей upstream и политики.
func Message(err error) string {
	switch {
	case errors.Is(err, entity.ErrInvalid):
		return "invalid request"
	case errors.Is(err, entity.ErrUnauthorized):
		return "forbidden"
	case errors.Is(err, entity.ErrNotFound):
		return "not found"
	case errors.Is(err, entity.ErrBudget):
		return "budget exceeded"
	case errors.Is(err, entity.ErrUpstream):
		return "upstream unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "request timeout"
	case errors.Is(err, context.Canceled):
		return "request canceled"
	default:
		return "internal error"
	}
}

// Write отправляет ошибку и логирует 5xx вместе с причиной.
func Write(w http.ResponseWriter, log *slog.Logger, err error) {
	status := Status(err)
	if status >= 500 && status != 499 && log != nil {
		log.Error("request failed", "status", status, "err", err)
	}
	rest.JSON(w, status, map[string]string{"error": Message(err)})
}
