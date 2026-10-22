package elasticsearch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/store"
)

func TestDoSendsPathAuthAndReturnsStatus(t *testing.T) {
	var (
		gotURI  string
		gotAuth string
		gotCT   string
		gotBody []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.URL.RequestURI()
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"acknowledged":true}`)
	}))
	defer srv.Close()

	c, err := New(Config{URL: srv.URL + "/es", APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	status, body, err := c.Do(context.Background(), store.Request{
		Method:  http.MethodPut,
		Path:    "/kit-chunks",
		Body:    []byte(`{"settings":{}}`),
		Refresh: "wait_for",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "/es/kit-chunks?refresh=wait_for"; gotURI != want {
		t.Fatalf("request URI = %q, want %q", gotURI, want)
	}
	if gotAuth != "ApiKey secret" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Fatalf("content type = %q", gotCT)
	}
	if string(gotBody) != `{"settings":{}}` {
		t.Fatalf("body = %q", gotBody)
	}
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201: a non-2xx is returned, not raised", status)
	}
	if string(body) != `{"acknowledged":true}` {
		t.Fatalf("response body = %q", body)
	}
}

func TestDoKeepsNonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"missing"}`)
	}))
	defer srv.Close()

	c, err := New(Config{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	status, body, err := c.Do(context.Background(), store.Request{Method: http.MethodGet, Path: "/nope"})
	if err != nil {
		t.Fatalf("a 404 must not be a transport error: %v", err)
	}
	if status != http.StatusNotFound || string(body) != `{"error":"missing"}` {
		t.Fatalf("status = %d, body = %q", status, body)
	}
}

func TestDoRejectsDotSegments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server reached with %q", r.URL.RequestURI())
	}))
	defer srv.Close()

	c, err := New(Config{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/../_cluster/health",
		"/idx/../../_cluster/health",
		"/idx/./_doc/1",
		"/idx/%2e%2e/_search",
	} {
		if _, _, err := c.Do(context.Background(), store.Request{Method: http.MethodGet, Path: path}); err == nil {
			t.Fatalf("path %q was accepted", path)
		}
	}
}

func TestDoPreservesEscapedPath(t *testing.T) {
	var gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
	}))
	defer srv.Close()

	c, err := New(Config{URL: srv.URL + "/es"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Do(context.Background(), store.Request{Method: http.MethodGet, Path: "/idx/_doc/a%2Fb"}); err != nil {
		t.Fatal(err)
	}
	if want := "/es/idx/_doc/a%2Fb"; gotURI != want {
		t.Fatalf("request URI = %q, want %q", gotURI, want)
	}
}

func TestDoRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, maxResponseBytes+1))
	}))
	defer srv.Close()

	c, err := New(Config{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.Do(context.Background(), store.Request{Method: http.MethodGet, Path: "/big"})
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("err = %v, want an explicit size error", err)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	cases := map[string]Config{
		"empty url":     {},
		"relative url":  {URL: "/es"},
		"both auth":     {URL: "http://es:9200", APIKey: "k", Username: "u", Password: "p"},
		"url no scheme": {URL: "es:9200"},
	}
	for name, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Fatalf("%s: New accepted an invalid config", name)
		}
	}
}
