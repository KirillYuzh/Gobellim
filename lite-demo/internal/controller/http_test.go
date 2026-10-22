package controller_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"some-project/internal/controller"
	"some-project/internal/entity"
	"some-project/internal/policy"
	"some-project/internal/repository"
	"some-project/internal/routes"
	"some-project/internal/usecase"
)

type staticLLM struct{ content string }

func (l staticLLM) Chat(_ context.Context, _ entity.CompletionRequest) (entity.Completion, error) {
	return entity.Completion{Content: l.content}, nil
}

type stubMock struct{}

func (stubMock) KB(context.Context, string) ([]entity.Chunk, error) {
	return []entity.Chunk{{ID: "1", Text: "chunk", Mock: true}}, nil
}

func (stubMock) Lookup(context.Context, string) (repository.Lookup, error) {
	return repository.Lookup{Code: "a", Body: "body"}, nil
}

func (stubMock) Followup(context.Context, string, string) (repository.Followup, error) {
	return repository.Followup{Code: "b", Body: "body"}, nil
}

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	p := &policy.Policy{Default: "deny", Roles: map[string][]string{
		"user": {"chat", "tool:kb_search"},
	}}
	uc, err := usecase.New(usecase.Deps{
		LLM:    staticLLM{content: "ok"},
		Mock:   stubMock{},
		Policy: p,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("usecase.New: %v", err)
	}
	return routes.New(controller.New(uc, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second))
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestChatOK(t *testing.T) {
	rec := do(t, newHandler(t), http.MethodPost, "/v1/chat",
		`{"user":{"id":"u","tenant":"t","roles":["user"]},"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestChatRejectsBadRequests(t *testing.T) {
	h := newHandler(t)
	cases := map[string]struct {
		body   string
		status int
	}{
		"broken json":   {`{`, http.StatusBadRequest},
		"unknown field": {`{"user":{"id":"u","tenant":"t","roles":["user"]},"messages":[],"extra":1}`, http.StatusBadRequest},
		"missing user":  {`{"messages":[{"role":"user","content":"hi"}]}`, http.StatusBadRequest},
		"no permission": {`{"user":{"id":"u","tenant":"t","roles":["guest"]},"messages":[{"role":"user","content":"hi"}]}`, http.StatusForbidden},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/v1/chat", tc.body)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.status, rec.Body)
			}
		})
	}
}

func TestHealth(t *testing.T) {
	rec := do(t, newHandler(t), http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "kb_search") {
		t.Fatalf("body = %s", rec.Body)
	}
}
