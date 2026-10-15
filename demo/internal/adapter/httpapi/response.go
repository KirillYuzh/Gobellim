package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gobellim/kit/demo/internal/domain"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, log *slog.Logger, err error) {
	status := statusOf(err)
	if status >= http.StatusInternalServerError {
		log.Error("request failed", "err", err, "status", status)
	}
	writeJSON(w, status, map[string]string{"error": messageOf(err)})
}

func statusOf(err error) int {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrUnavailable):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func messageOf(err error) string {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return "invalid request"
	case errors.Is(err, domain.ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, domain.ErrNotFound):
		return "not found"
	case errors.Is(err, domain.ErrUnavailable):
		return "upstream unavailable"
	default:
		return "internal error"
	}
}
