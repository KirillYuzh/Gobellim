package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gobellim/kit/demo/internal/domain"
	"github.com/gobellim/kit/demo/internal/usecase"
)

const maxBodyBytes = 1 << 20

type loginRequest struct {
	Login string `json:"login"`
}

type sessionResponse struct {
	Token     string `json:"token"`
	UserID    string `json:"user_id"`
	Login     string `json:"login"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expires_at"`
}

func handleLogin(auth *usecase.Auth, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := decode(w, r, &req); err != nil {
			fail(w, log, err)
			return
		}
		session, err := auth.Login(r.Context(), req.Login)
		if err != nil {
			fail(w, log, err)
			return
		}
		writeJSON(w, http.StatusOK, sessionResponse{
			Token:     session.Token,
			UserID:    session.Principal.UserID,
			Login:     session.Principal.Login,
			Role:      string(session.Principal.Role),
			ExpiresAt: session.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
}

type meResponse struct {
	UserID string `json:"user_id"`
	Login  string `json:"login"`
	Role   string `json:"role"`
	Tenant string `json:"tenant"`
}

func handleMe(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := domain.PrincipalFrom(r.Context())
		if !ok {
			fail(w, log, domain.ErrUnauthorized)
			return
		}
		writeJSON(w, http.StatusOK, meResponse{
			UserID: principal.UserID,
			Login:  principal.Login,
			Role:   string(principal.Role),
			Tenant: principal.Tenant,
		})
	}
}

type askRequest struct {
	Prompt string `json:"prompt"`
}

type askResponse struct {
	Answer string `json:"answer"`
}

func handleAsk(bot *usecase.Bot, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := domain.PrincipalFrom(r.Context())
		if !ok {
			fail(w, log, domain.ErrUnauthorized)
			return
		}
		var req askRequest
		if err := decode(w, r, &req); err != nil {
			fail(w, log, err)
			return
		}
		answer, err := bot.Ask(r.Context(), principal, req.Prompt)
		if err != nil {
			fail(w, log, err)
			return
		}
		writeJSON(w, http.StatusOK, askResponse{Answer: answer.Content})
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w: %w", err, domain.ErrInvalidInput)
	}
	return nil
}
