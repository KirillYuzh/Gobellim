package langfuse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
)

func sampleEvents(t *testing.T) []gobellim.Event {
	t.Helper()
	now := time.Now()
	return []gobellim.Event{
		{Kind: gobellim.EventTaskStarted, Time: now, RunID: "run-1", Task: "triage", PrincipalID: "u-1", Tenant: "acme"},
		{
			Kind: gobellim.EventModelCompleted, Time: now, RunID: "run-1", Task: "triage",
			Model: "GigaChat", PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
			Latency: time.Second, ModelCalls: 1,
		},
		{
			Kind: gobellim.EventGateAllowed, Time: now, RunID: "run-1", Task: "triage",
			Tool: "orders.read", Effect: gobellim.EffectRead, Decision: gobellim.Allow,
			RuleIDs: []string{"read"}, ArgsDigest: "abc", Integrity: gobellim.Derived, Sensitivity: gobellim.Public,
		},
		{Kind: gobellim.EventTaskCompleted, Time: now, RunID: "run-1", Task: "triage", ModelCalls: 1, ToolCalls: 1},
	}
}

func TestDryRun(t *testing.T) {
	var buf bytes.Buffer
	sink, err := New(Config{DryRun: true, Writer: &buf, BatchSize: 10, FlushInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range sampleEvents(t) {
		sink.Emit(context.Background(), e)
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	var types []string
	scanner := bufio.NewScanner(&buf)
	for scanner.Scan() {
		var payload struct {
			Batch []struct {
				Type string `json:"type"`
			} `json:"batch"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		for _, e := range payload.Batch {
			types = append(types, e.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"trace-create": true, "generation-create": true, "span-create": true}
	for _, typ := range types {
		delete(want, typ)
	}
	if len(want) != 0 {
		t.Fatalf("missing event types %v in %v", want, types)
	}
}

func TestHTTPIngestion(t *testing.T) {
	var gotPath, gotUser, gotPass string
	var gotBatch int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUser, gotPass, _ = r.BasicAuth()
		var payload struct {
			Batch []json.RawMessage `json:"batch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		gotBatch = len(payload.Batch)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink, err := New(Config{
		Host: server.URL, PublicKey: "pub", SecretKey: "sec",
		BatchSize: 2, FlushInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range sampleEvents(t) {
		sink.Emit(context.Background(), e)
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	if gotPath != "/api/public/ingestion" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotUser != "pub" || gotPass != "sec" {
		t.Fatalf("basic auth = %q/%q", gotUser, gotPass)
	}
	if gotBatch == 0 {
		t.Fatal("empty batch")
	}
}

func TestCloseIsIdempotentAndEmitAfterCloseIsSafe(t *testing.T) {
	sink, err := New(Config{DryRun: true, BatchSize: 1, FlushInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	sink.Emit(context.Background(), gobellim.Event{Kind: gobellim.EventTaskStarted})
	if sink.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1: an event lost after Close must still be visible", sink.Dropped())
	}
}

func TestOnDrop(t *testing.T) {
	var dropped []gobellim.Event
	s := &Sink{
		cfg:    Config{OnDrop: func(e gobellim.Event) { dropped = append(dropped, e) }},
		events: make(chan gobellim.Event, 1),
	}
	ctx := context.Background()
	s.Emit(ctx, gobellim.Event{RunID: "run-1"})
	s.Emit(ctx, gobellim.Event{RunID: "run-2"})
	if s.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", s.Dropped())
	}
	if len(dropped) != 1 || dropped[0].RunID != "run-2" {
		t.Fatalf("OnDrop got %+v", dropped)
	}
}

func TestRetriesTransientError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink, err := New(Config{
		Host: srv.URL, PublicKey: "p", SecretKey: "s",
		BatchSize: 1, FlushInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	sink.Emit(context.Background(), sampleEvents(t)[0])
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sink.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("requests = %d, want 2", hits.Load())
	}
	if sink.Dropped() != 0 {
		t.Fatalf("dropped = %d", sink.Dropped())
	}
}
