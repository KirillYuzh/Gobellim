// Package otel adapts an OpenTelemetry tracer to the trace.Provider interface.
//
// The core trace package stays free of external dependencies; this module is
// the bridge. It turns Gobellim attributes into OpenTelemetry attributes and
// Gobellim spans into OpenTelemetry spans.
//
// The application owns the exporter, the sampler and the batching — this
// package only creates spans. A span is a child of the span in the context it
// was started with, and a root span when the context carries none.
package otel

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/KirillYuzh/Gobellim/trace"
)

// NewProvider returns a Provider that records the spans with the given
// OpenTelemetry tracer. A nil tracer yields a no-op provider.
func NewProvider(tracer oteltrace.Tracer) trace.Provider {
	if tracer == nil {
		return trace.Noop()
	}
	return &provider{tracer: tracer}
}

// provider records the spans with an OpenTelemetry tracer.
type provider struct {
	tracer oteltrace.Tracer
}

// Start creates an OpenTelemetry span. The returned context carries the span,
// so the operations started with it become its children.
func (p *provider) Start(ctx context.Context, name string, attrs ...trace.Attribute) (context.Context, trace.Span) {
	ctx, span := p.tracer.Start(ctx, name, oteltrace.WithAttributes(toOTelAttributes(attrs)...))
	return ctx, &spanWrapper{span: span}
}

// spanWrapper is an OpenTelemetry span behind the trace.Span interface.
type spanWrapper struct {
	span oteltrace.Span
}

// End finishes the span.
func (s *spanWrapper) End() { s.span.End() }

// SetAttributes sets the attributes on the span.
func (s *spanWrapper) SetAttributes(attrs ...trace.Attribute) {
	if len(attrs) == 0 {
		return
	}
	s.span.SetAttributes(toOTelAttributes(attrs)...)
}

// SetError marks the span as failed.
func (s *spanWrapper) SetError() { s.span.SetStatus(codes.Error, "") }

// RecordError records the error as an exception event.
func (s *spanWrapper) RecordError(err error, attrs ...trace.Attribute) {
	if err == nil {
		return
	}
	s.span.RecordError(err, oteltrace.WithAttributes(toOTelAttributes(attrs)...))
}

// Event adds an event to the span.
func (s *spanWrapper) Event(name string, attrs ...trace.Attribute) {
	s.span.AddEvent(name, oteltrace.WithAttributes(toOTelAttributes(attrs)...))
}

// SetTokens records the token usage of a generation.
func (s *spanWrapper) SetTokens(input, output int) {
	s.SetAttributes(trace.Tokens(input, output)...)
}

// toOTelAttributes converts Gobellim attributes into OpenTelemetry ones. An
// attribute whose value has no OpenTelemetry counterpart is dropped: an empty
// string would be indistinguishable from a real empty value.
func toOTelAttributes(attrs []trace.Attribute) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		kv, ok := toOTelAttribute(a)
		if !ok {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// toOTelAttribute converts one Gobellim attribute. The second result reports
// whether the value has an OpenTelemetry counterpart.
func toOTelAttribute(a trace.Attribute) (attribute.KeyValue, bool) {
	switch v := a.Value.(type) {
	case string:
		return attribute.String(a.Key, v), true
	case int:
		return attribute.Int(a.Key, v), true
	case int64:
		return attribute.Int64(a.Key, v), true
	case int32:
		return attribute.Int64(a.Key, int64(v)), true
	case float64:
		return attribute.Float64(a.Key, v), true
	case float32:
		return attribute.Float64(a.Key, float64(v)), true
	case bool:
		return attribute.Bool(a.Key, v), true
	case []string:
		return attribute.StringSlice(a.Key, v), true
	default:
		return attribute.KeyValue{}, false
	}
}
