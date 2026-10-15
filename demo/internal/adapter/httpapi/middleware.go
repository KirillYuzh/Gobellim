package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gobellim/kit/demo/internal/domain"
	"github.com/gobellim/kit/demo/internal/usecase"
)

const bearerPrefix = "Bearer "

func requireAuth(auth *usecase.Auth, log *slog.Logger, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			fail(w, log, domain.ErrUnauthorized)
			return
		}
		principal, err := auth.Authenticate(r.Context(), token)
		if err != nil {
			fail(w, log, err)
			return
		}
		next(w, r.WithContext(domain.WithPrincipal(r.Context(), principal)))
	}
}

func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, bearerPrefix))
	return token, token != ""
}
