package trace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type otlpCollector struct {
	mu       sync.Mutex
	payloads []otlpPayload
	headers  []http.Header
}

func (c *otlpCollector) handler(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p otlpPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		c.mu.Lock()
		c.payloads = append(c.payloads, p)
		c.headers = append(c.headers, r.Header.Clone())
		c.mu.Unlock()
		w.WriteHeader(status)
	}
}

func (c *otlpCollector) first(t *testing.T) (otlpPayload, http.Header, int) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.payloads[0], c.headers[0], len(c.payloads)
}

func attrValue(attrs []otlpKeyValue, key string) (otlpValue, bool) {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value, true
		}
	}
	return otlpValue{}, false
}

func stringAttrVal(attrs []otlpKeyValue, key string) string {
	v, ok := attrValue(attrs, key)
	if !ok || v.StringValue == nil {
		return ""
	}
	return *v.StringValue
}

func intAttrVal(attrs []otlpKeyValue, key string) int64 {
	v, ok := attrValue(attrs, key)
	if !ok || v.IntValue == nil {
		return 0
	}
	n, _ := strconv.ParseInt(*v.IntValue, 10, 64)
	return n
}

func boolAttrVal(attrs []otlpKeyValue, key string) (bool, bool) {
	v, ok := attrValue(attrs, key)
	if !ok || v.BoolValue == nil {
		return false, false
	}
	return *v.BoolValue, true
}

func spanByName(t *testing.T, spans []otlpSpan, name string) otlpSpan {
	t.Helper()
	for _, sp := range spans {
		if sp.Name == name {
			return sp
		}
	}
	t.Fatalf("span %q not found in %d spans", name, len(spans))
	return otlpSpan{}
}

func TestOTLPExportsTraceAndObservations(t *testing.T) {
	c := &otlpCollector{}
	srv := httptest.NewServer(c.handler(http.StatusOK))
	defer srv.Close()

	exp, err := NewOTLP(OTLPOptions{Endpoint: srv.URL + "/v1/traces", FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = exp.Close(context.Background()) }()

	exp.Enqueue(Trace{ID: "t1", Name: "chat"})
	exp.EnqueueObservation(Observation{
		ID: "o1", TraceID: "t1", Name: "gen", Kind: KindGeneration,
		Model: "gpt-4o", Usage: Usage{PromptTokens: 11, CompletionTokens: 3},
	})
	exp.EnqueueObservation(Observation{
		ID: "o2", TraceID: "t1", Name: "retrieve", Kind: KindSpan, GuardDegraded: true,
	})

	if err := exp.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	p, hdr, n := c.first(t)
	if n != 1 {
		t.Fatalf("want exactly one export, got %d", n)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if len(p.ResourceSpans) != 1 {
		t.Fatalf("resourceSpans = %d", len(p.ResourceSpans))
	}

	rs := p.ResourceSpans[0]
	if got := stringAttrVal(rs.Resource.Attributes, "service.name"); got != "kit" {
		t.Fatalf("service.name = %q", got)
	}
	if len(rs.ScopeSpans) != 1 || rs.ScopeSpans[0].Scope.Name != "kit" {
		t.Fatalf("scope = %+v", rs.ScopeSpans)
	}
	spans := rs.ScopeSpans[0].Spans
	if len(spans) != 3 {
		t.Fatalf("want 3 spans, got %d", len(spans))
	}

	root := spanByName(t, spans, "chat")
	if root.TraceID != otlpTraceID("t1") {
		t.Fatalf("root traceId = %s, want %s", root.TraceID, otlpTraceID("t1"))
	}
	if root.SpanID != otlpSpanID("t1") {
		t.Fatalf("root spanId = %s, want %s", root.SpanID, otlpSpanID("t1"))
	}
	if root.ParentSpanID != "" {
		t.Fatalf("root parentSpanId = %q, want empty", root.ParentSpanID)
	}
	if root.Kind != otlpKindInternal {
		t.Fatalf("root kind = %d, want %d", root.Kind, otlpKindInternal)
	}

	gen := spanByName(t, spans, "gen")
	if gen.TraceID != root.TraceID {
		t.Fatalf("child traceId = %s, want %s", gen.TraceID, root.TraceID)
	}
	if gen.ParentSpanID != root.SpanID {
		t.Fatalf("gen parentSpanId = %s, want %s", gen.ParentSpanID, root.SpanID)
	}
	if got := stringAttrVal(gen.Attributes, "gen_ai.request.model"); got != "gpt-4o" {
		t.Fatalf("gen_ai.request.model = %q", got)
	}
	if got := intAttrVal(gen.Attributes, "gen_ai.usage.input_tokens"); got != 11 {
		t.Fatalf("gen_ai.usage.input_tokens = %d, want 11", got)
	}
	if got := intAttrVal(gen.Attributes, "gen_ai.usage.output_tokens"); got != 3 {
		t.Fatalf("gen_ai.usage.output_tokens = %d, want 3", got)
	}
	if got := stringAttrVal(gen.Attributes, "kit.kind"); got != string(KindGeneration) {
		t.Fatalf("kit.kind = %q", got)
	}

	ret := spanByName(t, spans, "retrieve")
	if ret.ParentSpanID != root.SpanID {
		t.Fatalf("retrieve parentSpanId = %s, want %s", ret.ParentSpanID, root.SpanID)
	}
	if v, ok := boolAttrVal(ret.Attributes, "kit.guard_degraded"); !ok || !v {
		t.Fatalf("kit.guard_degraded = %v (present=%v), want true", v, ok)
	}
}

func TestOTLPScoreAttachesAsRootEvent(t *testing.T) {
	c := &otlpCollector{}
	srv := httptest.NewServer(c.handler(http.StatusOK))
	defer srv.Close()

	exp, err := NewOTLP(OTLPOptions{Endpoint: srv.URL, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = exp.Close(context.Background()) }()

	exp.EnqueueScore(Score{TraceID: "t1", Name: "accuracy", Value: 0.75, Comment: "ok"})
	exp.Enqueue(Trace{ID: "t1", Name: "chat"})
	if err := exp.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	p, _, _ := c.first(t)
	spans := p.ResourceSpans[0].ScopeSpans[0].Spans
	if len(spans) != 1 {
		t.Fatalf("want only the root span, got %d", len(spans))
	}
	root := spanByName(t, spans, "chat")
	if len(root.Events) != 1 || root.Events[0].Name != "score" {
		t.Fatalf("root events = %+v", root.Events)
	}
	if got := stringAttrVal(root.Events[0].Attributes, "score.name"); got != "accuracy" {
		t.Fatalf("score.name = %q", got)
	}
}

func TestOTLPStandaloneScoreSpan(t *testing.T) {
	c := &otlpCollector{}
	srv := httptest.NewServer(c.handler(http.StatusOK))
	defer srv.Close()

	exp, err := NewOTLP(OTLPOptions{Endpoint: srv.URL, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = exp.Close(context.Background()) }()

	exp.EnqueueScore(Score{TraceID: "missing", Name: "quality", Value: 1})
	if err := exp.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	p, _, _ := c.first(t)
	spans := p.ResourceSpans[0].ScopeSpans[0].Spans
	if len(spans) != 1 {
		t.Fatalf("want one standalone score span, got %d", len(spans))
	}
	if got := stringAttrVal(spans[0].Attributes, "kit.score_trace_id"); got != "missing" {
		t.Fatalf("kit.score_trace_id = %q", got)
	}
}

func TestOTLPRequiresEndpoint(t *testing.T) {
	if _, err := NewOTLP(OTLPOptions{}); err == nil {
		t.Fatal("an empty endpoint must be rejected")
	}
}

func TestOTLPFlushFailsWithoutHanging(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	exp, err := NewOTLP(OTLPOptions{Endpoint: srv.URL, FlushInterval: time.Hour, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = exp.Close(context.Background()) }()

	exp.Enqueue(Trace{ID: "t1", Name: "chat"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exp.Flush(ctx); err == nil {
		t.Fatal("Flush must return an error when the server responds 500")
	}
	if got := calls.Load(); got != int64(otlpMaxAttempts) {
		t.Fatalf("attempts = %d, want %d (initial + one retry)", got, otlpMaxAttempts)
	}
}
