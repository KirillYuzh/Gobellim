package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// numericServer answers /embeddings with one vector per input. Every vector
// holds the numeric value of its input text, so a test can check that the
// vectors come back in the order the texts went in.
func numericServer(t *testing.T, sizes *[]int, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body remoteRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		if mu != nil {
			mu.Lock()
			*sizes = append(*sizes, len(body.Input))
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[`)
		for i, in := range body.Input {
			if i > 0 {
				_, _ = io.WriteString(w, ",")
			}
			v, err := strconv.ParseFloat(in, 64)
			if err != nil {
				t.Errorf("input %q is not a number: %v", in, err)
				return
			}
			_, _ = fmt.Fprintf(w, `{"index":%d,"embedding":[%v]}`, i, v)
		}
		_, _ = io.WriteString(w, `]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func numberedTexts(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = strconv.Itoa(i + 1)
	}
	return out
}

func TestOpenAIEmbedderBatchesRequests(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	srv := numericServer(t, &sizes, &mu)

	e, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "test-model", BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := e.Passages(context.Background(), numberedTexts(5))
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 5 {
		t.Fatalf("vectors = %d, want 5", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 1 || v[0] != float32(i+1) {
			t.Fatalf("vector %d = %v, want [%d]: the batches must keep the input order", i, v, i+1)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sizes) != 3 || sizes[0] != 2 || sizes[1] != 2 || sizes[2] != 1 {
		t.Fatalf("request sizes = %v, want [2 2 1]", sizes)
	}
}

func TestOpenAIEmbedderUsesTheDefaultBatchSize(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	srv := numericServer(t, &sizes, &mu)

	e, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Passages(context.Background(), numberedTexts(130)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sizes) != 3 || sizes[0] != 64 || sizes[1] != 64 || sizes[2] != 2 {
		t.Fatalf("request sizes = %v, want [64 64 2]", sizes)
	}
}

func TestOpenAIEmbedderStopsAtTheFirstFailedBatch(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body remoteRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[`)
		for i := range body.Input {
			if i > 0 {
				_, _ = io.WriteString(w, ",")
			}
			_, _ = fmt.Fprintf(w, `{"index":%d,"embedding":[1]}`, i)
		}
		_, _ = io.WriteString(w, `]}`)
	}))
	defer srv.Close()

	e, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "test-model", BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Passages(context.Background(), []string{"a", "b", "c", "d", "e"}); !errors.Is(err, core.ErrProvider) {
		t.Fatalf("err = %v, want %v", err, core.ErrProvider)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("the embedder sent %d requests, want 2: it must stop at the first failure", calls)
	}
}

func TestOpenAIEmbedderRejectsADimensionChange(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,2]}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,2,3]}]}`)
	}))
	defer srv.Close()

	e, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Query(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Query(context.Background(), []string{"b"}); !errors.Is(err, core.ErrProvider) {
		t.Fatalf("err = %v, want %v: a changed dimension corrupts the index", err, core.ErrProvider)
	}
}

func TestOpenAIEmbedderRejectsAMismatchWithTheConfiguredDim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,2]}]}`)
	}))
	defer srv.Close()

	e, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "test-model", Dim: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Query(context.Background(), []string{"a"}); !errors.Is(err, core.ErrProvider) {
		t.Fatalf("err = %v, want %v: the response must match the configured dimension", err, core.ErrProvider)
	}
}

func TestNewOpenAIRejectsAnAmbiguousModelName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	if _, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "my-bge-m3-tuned"}); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("err = %v, want %v", err, core.ErrInvalidConfig)
	}
}
