package model

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gobellim/kit/internal/httpjson"
)

type fakeClient struct {
	model string
	mu    sync.Mutex
	calls int
	fn    func(call int) (Response, error)
}

func (f *fakeClient) Model() string { return f.model }

func (f *fakeClient) Chat(_ context.Context, _ Request) (Response, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()
	return f.fn(n)
}

func (f *fakeClient) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func retryableError() error {
	return &httpjson.StatusError{StatusCode: http.StatusInternalServerError, Body: "boom"}
}

func TestChainRetriesRetryableThenFallsBack(t *testing.T) {
	first := &fakeClient{model: "a", fn: func(int) (Response, error) {
		return Response{}, retryableError()
	}}
	second := &fakeClient{model: "b", fn: func(int) (Response, error) {
		return Response{Message: Message{Role: RoleAssistant, Content: "ok"}}, nil
	}}

	c, err := Chain(ChainConfig{MaxRetries: 2, Backoff: time.Millisecond}, first, second)
	if err != nil {
		t.Fatal(err)
	}
	if c.Model() != "a" {
		t.Fatalf("Model() = %q, want a", c.Model())
	}
	resp, err := c.Chat(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("resp = %+v", resp)
	}
	if first.count() != 3 {
		t.Fatalf("first link calls = %d, want 3 (1 attempt + 2 retries)", first.count())
	}
	if second.count() != 1 {
		t.Fatalf("second link calls = %d, want 1", second.count())
	}
}

func TestChainDoesNotRetryPermanentError(t *testing.T) {
	first := &fakeClient{model: "a", fn: func(int) (Response, error) {
		return Response{}, &httpjson.StatusError{StatusCode: http.StatusBadRequest}
	}}
	second := &fakeClient{model: "b", fn: func(int) (Response, error) {
		return Response{Message: Message{Content: "ok"}}, nil
	}}

	c, err := Chain(ChainConfig{MaxRetries: 5, Backoff: time.Millisecond}, first, second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Chat(context.Background(), Request{}); err != nil {
		t.Fatal(err)
	}
	if first.count() != 1 {
		t.Fatalf("first link calls = %d, want 1 (a 400 is not retryable)", first.count())
	}
}

func TestChainReturnsLastError(t *testing.T) {
	first := &fakeClient{fn: func(int) (Response, error) { return Response{}, retryableError() }}
	second := &fakeClient{fn: func(int) (Response, error) {
		return Response{}, &httpjson.StatusError{StatusCode: http.StatusBadRequest, Body: "nope"}
	}}

	c, err := Chain(ChainConfig{MaxRetries: 1, Backoff: time.Millisecond}, first, second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Chat(context.Background(), Request{})
	if err == nil {
		t.Fatal("want error")
	}
	var se *httpjson.StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusBadRequest {
		t.Fatalf("err = %v, want the final link's 400", err)
	}
	if first.count() != 2 {
		t.Fatalf("first link calls = %d, want 2", first.count())
	}
	if second.count() != 1 {
		t.Fatalf("second link calls = %d, want 1", second.count())
	}
}

func TestChainRequiresClients(t *testing.T) {
	if _, err := Chain(ChainConfig{}); err == nil {
		t.Fatal("want error for an empty chain")
	}
	if _, err := Chain(ChainConfig{}, nil); err == nil {
		t.Fatal("want error for a nil client")
	}
}
