package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/gobellim/kit/demo/internal/usecase"
)

func New(auth *usecase.Auth, bot *usecase.Bot, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("POST /auth/login", handleLogin(auth, log))
	mux.HandleFunc("GET /auth/me", requireAuth(auth, log, handleMe(log)))
	mux.HandleFunc("POST /bot/ask", requireAuth(auth, log, handleAsk(bot, log)))
	return mux
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
