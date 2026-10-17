//go:build esintegration

// Package kit: the Elasticsearch round-trip test. It is gated behind the
// `esintegration` build tag so the default `go test ./...` stays offline.
//
// Run it against the repository's local stack:
//
//	docker compose -f demo/deploy/docker-compose.yml up -d elasticsearch
//	KIT_ES_URL=http://localhost:9200 go test -tags esintegration -run TestElasticsearchRoundTrip -v .
package kit

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/chunk"
	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/store"
)

type httpES struct {
	base string
	hc   *http.Client
}

func (e *httpES) Do(ctx context.Context, req store.Request) (int, []byte, error) {
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	hr, err := http.NewRequestWithContext(ctx, req.Method, e.base+req.EffectivePath(), body)
	if err != nil {
		return 0, nil, err
	}
	if len(req.Body) > 0 {
		ct := req.ContentType
		if ct == "" {
			ct = "application/json"
		}
		hr.Header.Set("Content-Type", ct)
	}
	resp, err := e.hc.Do(hr)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

const esIntegrationPolicy = `
version: 1
default: deny
roles:
  admin:
    permissions:
      - ingest
      - search
      - delete
  guest:
    permissions:
      - search
`

// TestElasticsearchRoundTrip exercises the parts a fake client cannot: index
// creation, _bulk, _msearch with kNN+BM25 and RRF, the ACL filter, and
// delete-by-query.
func TestElasticsearchRoundTrip(t *testing.T) {
	base := strings.TrimRight(os.Getenv("KIT_ES_URL"), "/")
	if base == "" {
		t.Skip("set KIT_ES_URL (e.g. http://localhost:9200) to run the Elasticsearch integration test")
	}

	ctx := context.Background()
	policy, err := authz.Parse(strings.NewReader(esIntegrationPolicy))
	if err != nil {
		t.Fatal(err)
	}
	es := &httpES{base: base, hc: &http.Client{Timeout: 30 * time.Second}}
	app, err := New(ctx, Config{
		ES:        es,
		LLM:       &fakeLLM{},
		Embedder:  fakeEmbedder{dim: 384},
		Policy:    policy,
		Tokenizer: chunk.WhitespaceTokenizer{},
		GuardL2:   guard.L1Only(),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	// The index keeps the mapping it was created with, so a stale index from an
	// older schema would silently reject the search filter (embed_model as text
	// instead of keyword). This test owns a local/dedicated stack, so it starts
	// from a clean schema.
	for _, idx := range []string{store.IndexChunks, store.IndexMemoryMessages, store.IndexMemoryWorking} {
		_, _, _ = es.Do(ctx, store.RawRequest(http.MethodDelete, "/"+idx, nil, ""))
	}
	if err := app.Setup(ctx); err != nil {
		t.Fatalf("Setup against a real Elasticsearch: %v", err)
	}

	docID := "es-it-" + time.Now().UTC().Format("20060102T150405.000000000")
	tenant := "it-" + docID
	admin := WithPrincipal(ctx, Principal{UserID: "u-admin", Roles: []string{"admin"}, Tenant: tenant})
	guest := WithPrincipal(ctx, Principal{UserID: "u-guest", Roles: []string{"guest"}, Tenant: tenant})

	const text = "возврат оформляется в течение четырнадцати дней"
	if err := app.Ingest(admin, Doc{ID: docID, Text: text, ACL: []string{"admin"}}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	hits, err := app.Search(admin, SearchRequest{Query: "возврат", TopK: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !hasDoc(hits, docID) {
		t.Fatalf("the indexed document is not searchable: %+v", hits)
	}

	leaked, err := app.Search(guest, SearchRequest{Query: "возврат", TopK: 5})
	if err != nil {
		t.Fatalf("guest Search: %v", err)
	}
	if len(leaked) != 0 {
		t.Fatalf("the ACL filter leaked %d hit(s) to a role outside Doc.ACL: %+v", len(leaked), leaked)
	}

	if err := app.Delete(admin, docID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	after, err := app.Search(admin, SearchRequest{Query: "возврат", TopK: 5})
	if err != nil {
		t.Fatalf("Search after Delete: %v", err)
	}
	if hasDoc(after, docID) {
		t.Fatalf("the document is still searchable after Delete: %+v", after)
	}
}

func hasDoc(hits []SearchHit, docID string) bool {
	for _, h := range hits {
		if h.DocID == docID {
			return true
		}
	}
	return false
}
