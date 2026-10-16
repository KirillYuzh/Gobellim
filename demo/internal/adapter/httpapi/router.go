package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/KirillYuzh/Gobellim/demo/internal/domain"
	"github.com/KirillYuzh/Gobellim/demo/internal/usecase"
)

func New(auth *usecase.Auth, bot *usecase.Bot, kb *usecase.Knowledge, audit usecase.Auditor, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("POST /auth/login", handleLogin(auth, log))
	mux.HandleFunc("GET /auth/me", requireAuth(auth, log, handleMe(log)))
	mux.HandleFunc("POST /bot/ask", requireAuth(auth, log, handleAsk(bot, log)))
	mux.HandleFunc("POST /bot/triage", requireAuth(auth, log, handleTriage(bot, log)))
	mux.HandleFunc("GET /stats", requireAuth(auth, log, handleStats(audit, log)))

	mux.HandleFunc("POST /kb", requireAuth(auth, log, handleKbCreate(kb, log)))
	mux.HandleFunc("GET /kb", requireAuth(auth, log, handleKbList(kb, log)))
	mux.HandleFunc("GET /kb/{id}", requireAuth(auth, log, handleKbGet(kb, log)))
	mux.HandleFunc("PUT /kb/{id}", requireAuth(auth, log, handleKbUpdate(kb, log)))
	mux.HandleFunc("DELETE /kb/{id}", requireAuth(auth, log, handleKbDelete(kb, log)))
	mux.HandleFunc("POST /kb/search", requireAuth(auth, log, handleKbSearch(kb, log)))
	return mux
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func principalOf(w http.ResponseWriter, r *http.Request, log *slog.Logger) (domain.Principal, bool) {
	principal, ok := domain.PrincipalFrom(r.Context())
	if !ok {
		fail(w, log, domain.ErrUnauthorized)
		return domain.Principal{}, false
	}
	return principal, true
}
