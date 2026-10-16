package httpjson

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestJoinURLInsertsSlash(t *testing.T) {
	cases := []struct{ base, path, want string }{
		{"http://x/v1", "/chat", "http://x/v1/chat"},
		{"http://x/v1/", "/chat", "http://x/v1/chat"},
		{"http://x/v1", "chat", "http://x/v1/chat"},
		{"http://x/v1/", "chat", "http://x/v1/chat"},
		{"http://x/v1", "", "http://x/v1"},
	}
	for _, c := range cases {
		if got := JoinURL(c.base, c.path); got != c.want {
			t.Errorf("JoinURL(%q, %q) = %q, want %q", c.base, c.path, got, c.want)
		}
	}
}

func TestStatusErrorRetryable(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{400, false}, {401, false}, {404, false}, {409, false}, {422, false},
		{408, true}, {425, true}, {429, true}, {500, true}, {503, true},
	}
	for _, c := range cases {
		if got := (&StatusError{StatusCode: c.code}).IsRetryable(); got != c.want {
			t.Errorf("IsRetryable(%d) = %v, want %v", c.code, got, c.want)
		}
	}
}

func TestDoEmptyBodyIsNoOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	hc := NewClient(Config{})
	var out struct{ X int }
	if err := Do(context.Background(), hc, http.MethodGet, srv.URL, nil, nil, &out); err != nil {
		t.Fatalf("Do with an empty body returned %v, want nil", err)
	}
	if out.X != 0 {
		t.Fatalf("out should be untouched, got %+v", out)
	}
}

func TestDoDoesNotRetry400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer srv.Close()

	hc := NewClient(Config{MaxRetries: 3, Backoff: time.Millisecond})
	err := Do(context.Background(), hc, http.MethodPost, srv.URL, nil, map[string]any{"a": 1}, nil)
	if err == nil {
		t.Fatal("want error")
	}
	var se *StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusBadRequest {
		t.Fatalf("err = %v, want a 400 StatusError", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("calls = %d, want 1 (a 400 must not be retried)", n)
	}
}

func TestDoRetries429WithRetryAfterThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	hc := NewClient(Config{MaxRetries: 2, Backoff: time.Millisecond})
	var out struct {
		OK bool `json:"ok"`
	}
	if err := Do(context.Background(), hc, http.MethodPost, srv.URL, nil, map[string]any{"a": 1}, &out); err != nil {
		t.Fatalf("Do = %v", err)
	}
	if !out.OK {
		t.Fatalf("out = %+v, want OK", out)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("calls = %d, want 2", n)
	}
}

func TestDoReportsRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	hc := NewClient(Config{MaxRetries: 0, Backoff: time.Millisecond})
	err := Do(context.Background(), hc, http.MethodGet, srv.URL, nil, nil, nil)
	if d := RetryAfter(err); d != 2*time.Second {
		t.Fatalf("RetryAfter = %v, want 2s", d)
	}
	if !IsRetryable(err) {
		t.Fatal("a 429 must be retryable")
	}
}
