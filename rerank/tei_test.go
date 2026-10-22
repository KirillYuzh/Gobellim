package rerank

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

func TestReorderSortsByScoreAndTruncates(t *testing.T) {
	ranks := []Rank{{Index: 2, Score: 0.9}, {Index: 0, Score: 0.5}, {Index: 1, Score: 0.7}}
	got := Reorder(3, ranks, 2)
	want := []int{2, 1}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestReorderIsStableAndDefensive(t *testing.T) {
	ranks := []Rank{{Index: 1, Score: 1}, {Index: 1, Score: 9}, {Index: 7, Score: 5}, {Index: -1, Score: 5}}
	got := Reorder(3, ranks, 0)
	want := []int{1, 0, 2}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestReorderKeepsUnscoredAtTheEnd(t *testing.T) {
	got := Reorder(4, []Rank{{Index: 3, Score: 0.1}}, 0)
	want := []int{3, 0, 1, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestTEIRerankParsesArrayEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body struct {
			Query string   `json:"query"`
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		if body.Query != "q" || len(body.Texts) != 2 {
			t.Errorf("body = %+v", body)
		}
		_, _ = w.Write([]byte(`[{"index":1,"score":0.9},{"index":0,"score":0.1}]`))
	}))
	defer srv.Close()

	c, err := NewTEI(TEIConfig{BaseURL: srv.URL, Model: "bge-reranker"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != "bge-reranker" {
		t.Fatalf("name = %s", c.Name())
	}
	ranks, err := c.Rerank(context.Background(), "q", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ranks) != 2 || ranks[0].Index != 1 || ranks[0].Score != 0.9 {
		t.Fatalf("ranks = %+v", ranks)
	}
}

func TestTEIRerankParsesCohereEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.42}]}`))
	}))
	defer srv.Close()

	c, _ := NewTEI(TEIConfig{BaseURL: srv.URL})
	ranks, err := c.Rerank(context.Background(), "q", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ranks) != 1 || ranks[0].Score != 0.42 {
		t.Fatalf("ranks = %+v", ranks)
	}
	if c.Name() != "tei" {
		t.Fatalf("name = %s", c.Name())
	}
}

func TestTEIRerankEmptyDocuments(t *testing.T) {
	c, _ := NewTEI(TEIConfig{BaseURL: "http://localhost:1"})
	ranks, err := c.Rerank(context.Background(), "q", nil)
	if err != nil || ranks != nil {
		t.Fatalf("ranks = %+v, err = %v", ranks, err)
	}
}

func TestNewTEIRequiresBaseURL(t *testing.T) {
	if _, err := NewTEI(TEIConfig{}); err == nil {
		t.Fatal("want an error")
	} else if !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("err = %v", err)
	}
}

func TestTEIRerankServerErrorIsProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, _ := NewTEI(TEIConfig{BaseURL: srv.URL})
	_, err := c.Rerank(context.Background(), "q", []string{"a"})
	if err == nil || !errors.Is(err, core.ErrProvider) {
		t.Fatalf("err = %v, want core.ErrProvider", err)
	}
}

func TestTEIRerankPicksScoreByKeyPresence(t *testing.T) {
	cases := []struct {
		name string
		body string
		want float64
	}{
		{name: "score wins when present", body: `[{"index":0,"score":0,"relevance_score":0.42}]`, want: 0},
		{name: "relevance score used when score absent", body: `[{"index":0,"relevance_score":0.42}]`, want: 0.42},
		{name: "score used when relevance absent", body: `[{"index":0,"score":0.7}]`, want: 0.7},
		{name: "missing scores default to zero", body: `[{"index":0}]`, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c, _ := NewTEI(TEIConfig{BaseURL: srv.URL})
			ranks, err := c.Rerank(context.Background(), "q", []string{"a"})
			if err != nil {
				t.Fatal(err)
			}
			if len(ranks) != 1 {
				t.Fatalf("ranks = %+v", ranks)
			}
			if ranks[0].Score != tc.want {
				t.Fatalf("score = %v, want %v", ranks[0].Score, tc.want)
			}
		})
	}
}

func TestTEIRerankRejectsOutOfRangeIndex(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "index beyond document count", body: `[{"index":2,"score":0.9}]`},
		{name: "negative index", body: `[{"index":-1,"score":0.9}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c, _ := NewTEI(TEIConfig{BaseURL: srv.URL})
			ranks, err := c.Rerank(context.Background(), "q", []string{"a", "b"})
			if err == nil || !errors.Is(err, core.ErrProvider) {
				t.Fatalf("err = %v, want core.ErrProvider", err)
			}
			if ranks != nil {
				t.Fatalf("ranks = %+v, want none", ranks)
			}
		})
	}
}

// teiStub serves a fixed response body and reports the "truncate" flag the
// client sent.
func teiStub(t *testing.T, body string) (*httptest.Server, <-chan *bool) {
	t.Helper()
	sent := make(chan *bool, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Truncate *bool `json:"truncate"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		select {
		case sent <- req.Truncate:
		default: // never block the handler
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, sent
}

func TestTEIRerankTruncatePolicy(t *testing.T) {
	noTruncate := false
	cases := []struct {
		name string
		cfg  TEIConfig
		want bool
	}{
		{name: "unset defaults to true", cfg: TEIConfig{}, want: true},
		{name: "explicit false disables truncation", cfg: TEIConfig{Truncate: &noTruncate}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, sent := teiStub(t, `[{"index":0,"score":0.1}]`)

			cfg := tc.cfg
			cfg.BaseURL = srv.URL
			c, err := NewTEI(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Rerank(context.Background(), "q", []string{"a"}); err != nil {
				t.Fatal(err)
			}
			got := <-sent
			if got == nil {
				t.Fatal("truncate is missing from the request body")
			}
			if *got != tc.want {
				t.Fatalf("truncate = %v, want %v", *got, tc.want)
			}
		})
	}
}

func TestTEITruncatePolicyIsFixedAtConstruction(t *testing.T) {
	truncate := false
	srv, sent := teiStub(t, `[{"index":0,"score":0.1}]`)

	c, err := NewTEI(TEIConfig{BaseURL: srv.URL, Truncate: &truncate})
	if err != nil {
		t.Fatal(err)
	}
	truncate = true // the caller mutates its own variable after construction

	if _, err := c.Rerank(context.Background(), "q", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	got := <-sent
	if got == nil || *got {
		t.Fatalf("truncate = %v, want false", got)
	}
}
