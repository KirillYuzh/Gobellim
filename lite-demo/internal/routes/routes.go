// Package routes — таблица маршрутов.
package routes

import (
	"log/slog"
	"net/http"

	"some-project/internal/controller"
	"some-project/pkg/rest"
)

// New собирает mux. Паника на границе отдаёт 500 вместо оборванного соединения.
func New(c *controller.Controller) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", c.Health)
	mux.HandleFunc("POST /v1/chat", c.Chat)
	mux.HandleFunc("POST /v1/followup", c.Followup)
	return recoverer(mux)
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "value", v, "path", r.URL.Path)
				rest.JSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
