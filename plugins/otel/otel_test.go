package otel

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/KirillYuzh/Gobellim/trace"
)

// newProvider returns a provider backed by an in-memory span recorder.
func newProvider(t *testing.T) (trace.Provider, *tracetest.SpanRecorder) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return NewProvider(tp.Tracer("test")), rec
}

// findSpan returns the recorded span with the given name.
func findSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	t.Fatalf("no span named %q in %v", name, spanNames(spans))
	return nil
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Name())
	}
	return out
}

// A span started without a span in the context is a root; a span started with
// one is its child. The operation and its calls must end up in one trace.
func TestProviderStartsRootAndChild(t *testing.T) {
	provider, rec := newProvider(t)

	ctx, root := provider.Start(context.Background(), "chat")
	_, child := provider.Start(ctx, "call")
	child.End()
	root.End()

	spans := rec.Ended()
	if len(spans) != 2 {
		t.Fatalf("recorded %d spans, want 2", len(spans))
	}
	rootSpan := findSpan(t, spans, "chat")
	childSpan := findSpan(t, spans, "call")

	if rootSpan.Parent().IsValid() {
		t.Errorf("the root span has a parent: %v", rootSpan.Parent())
	}
	if !childSpan.Parent().IsValid() {
		t.Fatal("the child span has no parent")
	}
	if got, want := childSpan.Parent().SpanID(), rootSpan.SpanContext().SpanID(); got != want {
		t.Errorf("the child span parent = %v, want the root span %v", got, want)
	}
	if got, want := childSpan.SpanContext().TraceID(), rootSpan.SpanContext().TraceID(); got != want {
		t.Errorf("the child span trace = %v, want the root span trace %v", got, want)
	}
}

func TestAttributesAreMapped(t *testing.T) {
	provider, rec := newProvider(t)

	_, span := provider.Start(context.Background(), "chat",
		trace.String("s", "x"),
		trace.Int("i", 1),
		trace.Int64("i64", 2),
		trace.Float64("f", 1.5),
		trace.Bool("b", true),
		trace.FinishReason("stop"),
	)
	span.SetAttributes(trace.String("later", "y"))
	span.End()

	attrs := findSpan(t, rec.Ended(), "chat").Attributes()
	got := map[string]attribute.Value{}
	for _, a := range attrs {
		got[string(a.Key)] = a.Value
	}

	if v, ok := got["s"]; !ok || v.AsString() != "x" {
		t.Errorf("s = %v, want x", v)
	}
	if v, ok := got["i"]; !ok || v.AsInt64() != 1 {
		t.Errorf("i = %v, want 1", v)
	}
	if v, ok := got["i64"]; !ok || v.AsInt64() != 2 {
		t.Errorf("i64 = %v, want 2", v)
	}
	if v, ok := got["f"]; !ok || v.AsFloat64() != 1.5 {
		t.Errorf("f = %v, want 1.5", v)
	}
	if v, ok := got["b"]; !ok || !v.AsBool() {
		t.Errorf("b = %v, want true", v)
	}
	if v, ok := got["later"]; !ok || v.AsString() != "y" {
		t.Errorf("later = %v, want y", v)
	}
	// gen_ai.response.finish_reasons is a list of strings in the semantic
	// conventions, not a plain string.
	if v, ok := got[trace.AttrFinishReason]; !ok {
		t.Errorf("%s is missing", trace.AttrFinishReason)
	} else if list := v.AsStringSlice(); len(list) != 1 || list[0] != "stop" {
		t.Errorf("%s = %v, want [stop]", trace.AttrFinishReason, list)
	}
}

// A value without an OpenTelemetry counterpart is dropped: an empty string
// would be indistinguishable from a real empty value.
func TestUnsupportedAttributeIsDropped(t *testing.T) {
	provider, rec := newProvider(t)

	_, span := provider.Start(context.Background(), "chat",
		trace.Attr("bad", struct{ A int }{A: 1}),
		trace.String("good", "x"),
	)
	span.End()

	for _, a := range findSpan(t, rec.Ended(), "chat").Attributes() {
		if string(a.Key) == "bad" {
			t.Errorf("the unsupported attribute was recorded as %v", a.Value)
		}
	}
}

func TestErrorAndEvent(t *testing.T) {
	provider, rec := newProvider(t)

	_, span := provider.Start(context.Background(), "chat")
	span.Event("guard.allowed", trace.String("rail", "input"))
	span.RecordError(errors.New("boom"))
	span.SetError()
	span.End()

	recorded := findSpan(t, rec.Ended(), "chat")
	if recorded.Status().Code.String() != "Error" {
		t.Errorf("status = %v, want Error", recorded.Status())
	}

	var names []string
	for _, e := range recorded.Events() {
		names = append(names, e.Name)
	}
	if len(names) != 2 {
		t.Fatalf("recorded %d events (%v), want 2", len(names), names)
	}
	if names[0] != "guard.allowed" {
		t.Errorf("first event = %q, want guard.allowed", names[0])
	}
	if names[1] != "exception" {
		t.Errorf("second event = %q, want exception", names[1])
	}
}

func TestSetTokens(t *testing.T) {
	provider, rec := newProvider(t)

	_, span := provider.Start(context.Background(), "chat")
	span.SetTokens(3, 4)
	span.End()

	got := map[string]int64{}
	for _, a := range findSpan(t, rec.Ended(), "chat").Attributes() {
		got[string(a.Key)] = a.Value.AsInt64()
	}
	if got[trace.AttrInputTokens] != 3 {
		t.Errorf("%s = %d, want 3", trace.AttrInputTokens, got[trace.AttrInputTokens])
	}
	if got[trace.AttrOutputTokens] != 4 {
		t.Errorf("%s = %d, want 4", trace.AttrOutputTokens, got[trace.AttrOutputTokens])
	}
	if _, ok := got["gen_ai.usage.total_tokens"]; ok {
		t.Error("the span carries gen_ai.usage.total_tokens, which the semantic conventions do not define")
	}
}

func TestNilTracerIsNoop(t *testing.T) {
	provider := NewProvider(nil)
	ctx, span := provider.Start(context.Background(), "chat")
	if ctx == nil || span == nil {
		t.Fatal("a nil tracer must still return a context and a span")
	}
	span.End()
}
