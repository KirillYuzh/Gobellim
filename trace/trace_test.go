package trace

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type captured struct {
	path string
	body []byte
}

type fakeES struct {
	mu        sync.Mutex
	requests  []captured
	calls     int
	failTimes int
	block     chan struct{}
}

func (f *fakeES) Do(ctx context.Context, method, path string, body io.Reader) (int, []byte, error) {
	b, _ := io.ReadAll(body)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.requests = append(f.requests, captured{path: path, body: b})
	if f.failTimes > 0 {
		f.failTimes--
		return 500, []byte(`{"errors":true}`), nil
	}
	return 200, []byte(`{"errors":false}`), nil
}

func (f *fakeES) snapshot() []captured {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]captured, len(f.requests))
	copy(out, f.requests)
	return out
}

func TestWriterFlushesOnClose(t *testing.T) {
	f := &fakeES{}
	w, err := NewWriter(Options{ES: f, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w.Enqueue(Trace{ID: "t1", Name: "chat"})
	w.EnqueueObservation(Observation{ID: "o1", TraceID: "t1", Kind: KindGeneration})
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	reqs := f.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("want 2 bulk requests (one per stream), got %d", len(reqs))
	}
	for _, r := range reqs {
		if r.path != "/_bulk" {
			t.Fatalf("path = %s", r.path)
		}
		if !strings.Contains(string(r.body), `"create"`) {
			t.Fatalf("data streams require the create action: %s", r.body)
		}
	}
	joined := string(reqs[0].body) + string(reqs[1].body)
	if !strings.Contains(joined, "kit-traces") || !strings.Contains(joined, "kit-observations") {
		t.Fatalf("streams missing: %s", joined)
	}
}

func TestWriterBatchTriggersFlush(t *testing.T) {
	f := &fakeES{}
	w, err := NewWriter(Options{ES: f, BatchSize: 2, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(context.Background())

	w.Enqueue(Trace{ID: "1"})
	w.Enqueue(Trace{ID: "2"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(f.snapshot()) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("batch did not flush")
}

func TestWriterDropsWhenQueueFull(t *testing.T) {
	release := make(chan struct{})
	f := &fakeES{block: release}
	w, err := NewWriter(Options{ES: f, QueueSize: 2, BatchSize: 1, FlushInterval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		w.Enqueue(Trace{ID: strings.Repeat("x", i)})
	}
	if w.Dropped() == 0 {
		t.Fatal("expected dropped documents when the queue is full")
	}
	close(release)
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWriterRetriesThenSucceeds(t *testing.T) {
	f := &fakeES{failTimes: 2}
	w, err := NewWriter(Options{ES: f, MaxRetries: 3, RetryBackoff: time.Millisecond, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w.Enqueue(Trace{ID: "t1"})
	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("Close returned %v, want nil after retries", err)
	}
	f.mu.Lock()
	calls := f.calls
	f.mu.Unlock()
	if calls != 3 {
		t.Fatalf("ES calls = %d, want 3 (2 failures + 1 success)", calls)
	}
}

func TestWriterCloseReturnsErrorAfterExhaustingRetries(t *testing.T) {
	f := &fakeES{failTimes: 100}
	w, err := NewWriter(Options{ES: f, MaxRetries: 1, RetryBackoff: time.Millisecond, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w.Enqueue(Trace{ID: "t1"})
	if err := w.Close(context.Background()); err == nil {
		t.Fatal("Close must surface the flush failure")
	}
}

func TestTraceDocumentFields(t *testing.T) {
	tr := Trace{
		ID: "t1", Name: "chat", UserID: "u", Tenant: "t1",
		StartTime: time.Unix(0, 0).UTC(),
		EndTime:   time.Unix(1, 0).UTC(),
		Usage:     Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
	}
	b, err := json.Marshal(tr.document())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"trace_id":"t1"`, `"total_tokens":7`, `"@timestamp"`, `"tenant":"t1"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("document missing %s: %s", want, b)
		}
	}
}
