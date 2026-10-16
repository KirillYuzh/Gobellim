package trace

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gobellim/kit/internal/id"
	"github.com/gobellim/kit/store"
)

type captured struct {
	path string
	body []byte
}

type fakeES struct {
	mu        sync.Mutex
	requests  []captured
	calls     int
	handler   func(n int) (int, []byte)
	failTimes int
	block     chan struct{}
}

func (f *fakeES) Do(ctx context.Context, req store.Request) (int, []byte, error) {
	b := req.Body
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		}
	}
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.requests = append(f.requests, captured{path: req.Path, body: b})
	handler := f.handler
	failTimes := f.failTimes
	if failTimes > 0 {
		f.failTimes--
	}
	f.mu.Unlock()

	if handler != nil {
		status, body := handler(n)
		return status, body, nil
	}
	if failTimes > 0 {
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

func (f *fakeES) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func testConfig() exportConfig {
	return exportConfig{
		maxStringLength: DefaultMaxStringLength,
		maxDepth:        DefaultMaxDepth,
		maxArrayLength:  DefaultMaxArrayLength,
		maxObjectKeys:   DefaultMaxObjectKeys,
	}
}

func TestWriterFlushesOnClose(t *testing.T) {
	f := &fakeES{}
	w, err := NewWriter(Options{ES: f, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w.Enqueue(Trace{ID: "t1", Name: "chat"})
	w.EnqueueObservation(Observation{ID: "o1", TraceID: "t1", Name: "gen", Kind: KindGeneration})
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
	if calls := f.callCount(); calls != 3 {
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

func TestWriterTreatsVersionConflictAsSuccess(t *testing.T) {
	f := &fakeES{handler: func(int) (int, []byte) {
		return 200, []byte(`{"errors":true,"items":[{"create":{"status":409,"error":{"type":"version_conflict_engine_exception","reason":"already exists"}}}]}`)
	}}
	w, err := NewWriter(Options{ES: f, RetryBackoff: time.Millisecond, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w.Enqueue(Trace{ID: "t1"})
	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("version conflict must count as success, got %v", err)
	}
	if calls := f.callCount(); calls != 1 {
		t.Fatalf("ES calls = %d, want 1 (conflicts are not retried)", calls)
	}
}

func TestWriterReportsRealBulkFailure(t *testing.T) {
	f := &fakeES{handler: func(int) (int, []byte) {
		return 200, []byte(`{"errors":true,"items":[{"create":{"status":400,"error":{"type":"mapper_parsing_exception","reason":"bad doc"}}}]}`)
	}}
	w, err := NewWriter(Options{ES: f, MaxRetries: 1, RetryBackoff: time.Millisecond, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w.Enqueue(Trace{ID: "t1"})
	if err := w.Close(context.Background()); err == nil {
		t.Fatal("real bulk failures must surface as an error")
	}
	if calls := f.callCount(); calls != 2 {
		t.Fatalf("ES calls = %d, want 2 (initial + one retry)", calls)
	}
}

func TestWriterDetectsRealFailureBehindConflict(t *testing.T) {
	f := &fakeES{handler: func(int) (int, []byte) {
		return 200, []byte(`{"errors":true,"items":[` +
			`{"create":{"status":409,"error":{"type":"version_conflict_engine_exception","reason":"exists"}}},` +
			`{"create":{"status":400,"error":{"type":"mapper_parsing_exception","reason":"bad"}}}]}`)
	}}
	w, err := NewWriter(Options{ES: f, MaxRetries: 1, RetryBackoff: time.Millisecond, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w.Enqueue(Trace{ID: "t1"})
	if err := w.Close(context.Background()); err == nil {
		t.Fatal("a real failure behind a version conflict must not be swallowed")
	}
}

func TestWriterCloseIsIdempotentAndConcurrent(t *testing.T) {
	f := &fakeES{}
	w, err := NewWriter(Options{ES: f, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w.Enqueue(Trace{ID: "t1"})
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("second Close must be a no-op, got %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.Close(context.Background())
		}()
	}
	wg.Wait()
}

func TestWriterEnqueueDuringCloseDoesNotPanic(t *testing.T) {
	f := &fakeES{}
	w, err := NewWriter(Options{ES: f, QueueSize: 4, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				w.Enqueue(Trace{ID: "t"})
				w.EnqueueObservation(Observation{ID: "o", TraceID: "t", Name: "n"})
			}
		}()
	}
	time.Sleep(2 * time.Millisecond)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = w.Close(context.Background())
	}()
	wg.Wait()
}

func TestWriterSamplerDropsTraceAndObservations(t *testing.T) {
	f := &fakeES{}
	w, err := NewWriter(Options{ES: f, FlushInterval: time.Hour, Sampler: SamplerNever})
	if err != nil {
		t.Fatal(err)
	}
	w.Enqueue(Trace{ID: "t1"})
	w.EnqueueObservation(Observation{ID: "o1", TraceID: "t1", Name: "n"})
	w.EnqueueScore(Score{TraceID: "t1", Name: "s"})
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(f.snapshot()); got != 0 {
		t.Fatalf("sampled-out documents were sent: %d requests", got)
	}
}

func TestWriterProcessorCanDropObservation(t *testing.T) {
	f := &fakeES{}
	w, err := NewWriter(Options{ES: f, FlushInterval: time.Hour, Processors: []func(*Observation){
		func(o *Observation) { o.Name = "" },
	}})
	if err != nil {
		t.Fatal(err)
	}
	w.EnqueueObservation(Observation{ID: "o1", TraceID: "t1", Name: "original"})
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(f.snapshot()); got != 0 {
		t.Fatalf("dropped observation was still sent: %d requests", got)
	}
}

func TestWriterForwardsToExtraSinks(t *testing.T) {
	f := &fakeES{}
	rec := NewRecorder()
	w, err := NewWriter(Options{ES: f, FlushInterval: time.Hour, Sinks: []Sink{rec}})
	if err != nil {
		t.Fatal(err)
	}
	w.Enqueue(Trace{ID: "t1"})
	w.EnqueueObservation(Observation{ID: "o1", TraceID: "t1", Name: "n"})
	w.EnqueueScore(Score{TraceID: "t1", Name: "s"})
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap := rec.Snapshot()
	if len(snap.Traces) != 1 || len(snap.Observations) != 1 || len(snap.Scores) != 1 {
		t.Fatalf("extra sink did not receive everything: %+v", snap)
	}
}

func TestRecorderSnapshotAndReset(t *testing.T) {
	r := NewRecorder()
	r.Enqueue(Trace{ID: "t1"})
	r.EnqueueObservation(Observation{ID: "o1"})
	r.EnqueueScore(Score{TraceID: "t1", Name: "s"})
	s := r.Snapshot()
	if len(s.Traces) != 1 || len(s.Observations) != 1 || len(s.Scores) != 1 {
		t.Fatalf("snapshot wrong: %+v", s)
	}
	r.Reset()
	s = r.Snapshot()
	if len(s.Traces) != 0 || len(s.Observations) != 0 || len(s.Scores) != 0 {
		t.Fatalf("reset did not clear: %+v", s)
	}
}

func TestRecorderIsConcurrencySafe(t *testing.T) {
	r := NewRecorder()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Enqueue(Trace{ID: "t"})
				r.EnqueueObservation(Observation{ID: "o"})
				r.EnqueueScore(Score{TraceID: "t"})
				_ = r.Snapshot()
			}
		}()
	}
	wg.Wait()
	s := r.Snapshot()
	if len(s.Traces) != 800 || len(s.Observations) != 800 || len(s.Scores) != 800 {
		t.Fatalf("lost records: %d/%d/%d", len(s.Traces), len(s.Observations), len(s.Scores))
	}
}

func TestTextPolicy(t *testing.T) {
	if TextMasked.String() != "masked" || TextFull.String() != "full" {
		t.Fatalf("policy names = %q/%q", TextMasked.String(), TextFull.String())
	}
	if TextMasked.IsRaw() || !TextFull.IsRaw() {
		t.Fatal("IsRaw must be true only for TextFull")
	}
}

func TestSamplerRatioIsDeterministic(t *testing.T) {
	s := SamplerRatio(0.5)
	other := SamplerRatio(0.5)
	for _, traceID := range []string{"", "abc", "0123456789abcdef0123456789abcdef", "not-hex-at-all"} {
		first := s(traceID)
		if first != s(traceID) {
			t.Fatalf("non-deterministic for %q", traceID)
		}
		if first != other(traceID) {
			t.Fatalf("not stable across samplers for %q", traceID)
		}
	}
	if !s("00000000000000000000000000000000") {
		t.Fatal("id 0 must sample at p=0.5")
	}
	if s("ffffffffffffffffffffffffffffffff") {
		t.Fatal("max id must not sample at p=0.5")
	}
	if SamplerRatio(0)("anything") {
		t.Fatal("p=0 must never sample")
	}
	if !SamplerRatio(1)("anything") {
		t.Fatal("p=1 must always sample")
	}
	if SamplerRatio(-1)("anything") {
		t.Fatal("p<0 must never sample")
	}
	if !SamplerRatio(2)("anything") {
		t.Fatal("p>1 must always sample")
	}
}

func TestSamplerFromString(t *testing.T) {
	for _, s := range []string{"always", "never", "ratio:0.1"} {
		if _, err := SamplerFromString(s); err != nil {
			t.Fatalf("SamplerFromString(%q) = %v", s, err)
		}
	}
	for _, s := range []string{"", "sometimes", "ratio:", "ratio:abc", "0.5"} {
		if _, err := SamplerFromString(s); err == nil {
			t.Fatalf("SamplerFromString(%q) must fail", s)
		}
	}
	if smp, _ := SamplerFromString("never"); smp("x") {
		t.Fatal("never must not sample")
	}
}

func TestTraceDocumentFields(t *testing.T) {
	tr := Trace{
		ID: "t1", Name: "chat", UserID: "u", Tenant: "t1",
		Tags:      []string{"prod"},
		StartTime: time.Unix(0, 0).UTC(),
		EndTime:   time.Unix(1, 0).UTC(),
		Usage:     Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},

		GuardVersion: "v1",
		GuardModel:   "gliner",
		PolicyHash:   "abc",
		TextPolicy:   TextFull,
	}
	b, err := json.Marshal(tr.document(testConfig()))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"trace_id":"t1"`, `"total_tokens":7`, `"@timestamp"`, `"tenant":"t1"`,
		`"tags":["prod"]`, `"text_policy":"full"`, `"guard_version":"v1"`,
		`"guard_model":"gliner"`, `"policy_hash":"abc"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("document missing %s: %s", want, b)
		}
	}
}

func TestTraceDocumentOmitsEmptyAuditFields(t *testing.T) {
	b, err := json.Marshal((Trace{ID: "t1"}).document(testConfig()))
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"guard_version", "guard_model", "policy_hash"} {
		if strings.Contains(string(b), unwanted) {
			t.Fatalf("document must omit empty %s: %s", unwanted, b)
		}
	}
	if !strings.Contains(string(b), `"text_policy":"masked"`) {
		t.Fatalf("text_policy must always be recorded: %s", b)
	}
}

func TestObservationDocumentTags(t *testing.T) {
	b, err := json.Marshal((Observation{ID: "o1", Name: "n", Tags: []string{"a", "b"}}).document(testConfig()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"tags":["a","b"]`) {
		t.Fatalf("observation tags missing: %s", b)
	}
}

func TestDocumentTruncatesInputAndMetadata(t *testing.T) {
	cfg := exportConfig{maxStringLength: 10, maxDepth: 4, maxArrayLength: 2, maxObjectKeys: 10}
	tr := Trace{
		ID:    "t1",
		Input: strings.Repeat("a", 100),
		Metadata: map[string]any{
			"long":   strings.Repeat("b", 100),
			"arr":    []any{1, 2, 3, 4, 5},
			"nested": map[string]any{"x": strings.Repeat("c", 100)},
		},
	}
	doc := tr.document(cfg)
	if doc["input"] != strings.Repeat("a", 10) {
		t.Fatalf("input not clipped: %q", doc["input"])
	}
	md := doc["metadata"].(map[string]any)
	if md["long"] != strings.Repeat("b", 10) {
		t.Fatalf("metadata string not clipped: %q", md["long"])
	}
	if got := len(md["arr"].([]any)); got != 2 {
		t.Fatalf("metadata array not capped: %d", got)
	}
	if md["nested"].(map[string]any)["x"] != strings.Repeat("c", 10) {
		t.Fatalf("nested metadata string not clipped: %q", md["nested"])
	}
}

func TestTruncateRespectsMaxDepth(t *testing.T) {
	deep := map[string]any{"l1": map[string]any{"l2": strings.Repeat("z", 50)}}

	shallow := exportConfig{maxStringLength: 3, maxDepth: 1, maxArrayLength: 10, maxObjectKeys: 10}
	out := shallow.truncate(deep).(map[string]any)
	if out["l1"].(map[string]any)["l2"] != strings.Repeat("z", 50) {
		t.Fatal("must not descend past MaxDepth")
	}

	deeper := shallow
	deeper.maxDepth = 2
	out = deeper.truncate(deep).(map[string]any)
	if out["l1"].(map[string]any)["l2"] != strings.Repeat("z", 3) {
		t.Fatalf("must clip within MaxDepth: %q", out["l1"].(map[string]any)["l2"])
	}
}

func TestTruncateRespectsMaxObjectKeys(t *testing.T) {
	cfg := exportConfig{maxStringLength: 100, maxDepth: 4, maxArrayLength: 10, maxObjectKeys: 2}
	out := cfg.truncate(map[string]any{"a": 1, "b": 2, "c": 3, "d": 4}).(map[string]any)
	if len(out) != 2 {
		t.Fatalf("object keys not capped: %d", len(out))
	}
}

func TestDocumentRedaction(t *testing.T) {
	cfg := exportConfig{maxStringLength: 100, maxDepth: 4, maxArrayLength: 10, maxObjectKeys: 10, hideInput: true, hideOutput: true}
	tr := Trace{ID: "t", Input: "secret", Output: "secret"}
	doc := tr.document(cfg)
	if doc["input"] != "" || doc["output"] != "" {
		t.Fatalf("trace input/output not hidden: %v/%v", doc["input"], doc["output"])
	}
	obs := Observation{ID: "o", Name: "n", Input: "secret", Output: "secret", StatusMessage: strings.Repeat("s", 100)}
	odoc := obs.document(cfg)
	if odoc["input"] != "" || odoc["output"] != "" {
		t.Fatalf("observation input/output not hidden: %v/%v", odoc["input"], odoc["output"])
	}
	if odoc["status_message"] != strings.Repeat("s", 100) {
		t.Fatalf("status_message must not be hidden: %v", odoc["status_message"])
	}
}

func TestIDsDiffer(t *testing.T) {
	a, b := id.New(), id.New()
	if a == "" || b == "" {
		t.Fatal("id.New returned an empty id")
	}
	if a == b {
		t.Fatalf("id.New returned the same value twice: %q", a)
	}
	u1, u2 := id.UUIDv4(), id.UUIDv4()
	if len(u1) != 36 {
		t.Fatalf("UUIDv4 length = %d, want 36: %q", len(u1), u1)
	}
	if u1 == u2 {
		t.Fatalf("UUIDv4 returned the same value twice: %q", u1)
	}
}
