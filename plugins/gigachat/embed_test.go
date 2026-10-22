package gigachat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func itoa(i int) string { return strconv.Itoa(i) }

// The embedder must not send a whole document in one request.
func TestEmbedderBatchesRequests(t *testing.T) {
	var sizes []int
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_at":9999999999999}`)
	})
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		in, _ := body["input"].([]any)
		sizes = append(sizes, len(in))
		var data []string
		for i := range in {
			data = append(data, `{"index":`+itoa(i)+`,"embedding":[1,0]}`)
		}
		_, _ = io.WriteString(w, `{"data":[`+strings.Join(data, ",")+`]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, err := NewEmbedder(EmbedConfig{
		BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds",
		Model: "multilingual-e5-large", BatchSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := e.Passages(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 3 {
		t.Fatalf("vectors = %d, want 3", len(vecs))
	}
	if len(sizes) != 2 || sizes[0] != 2 || sizes[1] != 1 {
		t.Fatalf("request sizes = %v, want [2 1]", sizes)
	}
}

// A dimension that changes under a running index corrupts it silently, so the
// embedder must refuse it.
func TestEmbedderRejectsADimensionChange(t *testing.T) {
	var call int
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_at":9999999999999}`)
	})
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 1 {
			_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,0]}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,0,0]}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, err := NewEmbedder(EmbedConfig{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds", Model: "multilingual-e5-large"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Passages(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Passages(context.Background(), []string{"b"}); err == nil {
		t.Fatal("a dimension change must be refused")
	}
}

func TestEmbedderUsesOAuthAndPassagePrefix(t *testing.T) {
	var gotInput string
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_at":9999999999999}`)
	})
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("bearer = %q", r.Header.Get("Authorization"))
		}
		body := decodeBody(t, r)
		in, _ := body["input"].([]any)
		if len(in) == 1 {
			gotInput, _ = in[0].(string)
		}
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[3,4]}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e, err := NewEmbedder(EmbedConfig{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds", Model: "multilingual-e5-large"})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := e.Passages(context.Background(), []string{"doc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 2 {
		t.Fatalf("vectors = %v", vecs)
	}
	if gotInput != "passage: doc" {
		t.Fatalf("input = %q, want the passage prefix applied", gotInput)
	}
	if e.Dim() != 2 {
		t.Fatalf("Dim() = %d, want the observed dimension", e.Dim())
	}
	want := float32(0.6)
	if diff := vecs[0][0] - want; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("vector = %v, want it normalized to length 1", vecs[0])
	}
}
