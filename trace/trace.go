// Package trace creates the spans that describe a Gobellim operation.
//
// The package owns the signal, not the transport: it defines the Span and
// Provider interfaces and the typed attribute constructors, while the
// application supplies the provider that exports the spans. The core package
// has no external dependencies — the OpenTelemetry adapter lives in
// plugins/otel.
//
// A span is a child of the span in the context it was started with, and a root
// span when the context carries none. That keeps an operation inside the
// trace the caller already opened.
//
// Without a provider the operations still run: the spans simply go nowhere.
// Use Noop for that case.
package trace

import "context"

// Span is one operation inside a trace. A span is created by a Provider and
// must be ended by the caller.
type Span interface {
	// End finishes the span and records its end time.
	End()

	// SetAttributes sets one or more attributes on the span.
	// Attribute keys should follow OpenTelemetry semantic conventions where applicable.
	SetAttributes(attrs ...Attribute)

	// SetError marks the span as having an error status.
	SetError()

	// RecordError records an error on the span with optional attributes.
	// The error text should not contain secrets, PII, or full prompts.
	RecordError(err error, attrs ...Attribute)

	// Event adds a timestamped event to the span with optional attributes.
	// Events are point-in-time facts within the span's lifetime.
	Event(name string, attrs ...Attribute)

	// SetTokens records token usage for LLM operations.
	// Maps to the OpenTelemetry GenAI semantic conventions.
	SetTokens(inputTokens, outputTokens int)
}

// Attribute represents a key-value pair for span attributes.
// Keys should follow OpenTelemetry semantic conventions where applicable.
//
// The value is one of string, int, int64, int32, float64, float32, bool or
// []string. A value of any other type is dropped by the provider instead of
// being recorded as an empty string.
type Attribute struct {
	Key   string
	Value any
}

// Attr creates an attribute. The value is used as it is: use Int, Int64,
// Float64, String or Bool to pick the type explicitly.
func Attr(key string, value any) Attribute {
	return Attribute{Key: key, Value: value}
}

// Int creates an integer attribute.
func Int(key string, value int) Attribute {
	return Attr(key, value)
}

// Int64 creates an int64 attribute.
func Int64(key string, value int64) Attribute {
	return Attr(key, value)
}

// Float64 creates a float64 attribute.
func Float64(key string, value float64) Attribute {
	return Attr(key, value)
}

// String creates a string attribute.
func String(key string, value string) Attribute {
	return Attr(key, value)
}

// Bool creates a boolean attribute.
func Bool(key string, value bool) Attribute {
	return Attr(key, value)
}

// Provider creates spans. The application owns the provider: it decides where
// the spans go, how they are batched and how many of them are kept.
type Provider interface {
	// Start creates a span named name. The returned context carries the span
	// and must be passed to the operations it covers, so that they become its
	// children. The span must be ended by the caller.
	Start(ctx context.Context, name string, attrs ...Attribute) (context.Context, Span)
}

// Noop returns a Provider that records nothing. It is the provider an
// application uses when no tracing backend is configured.
func Noop() Provider { return noopProvider{} }

// noopSpan is a Span implementation that does nothing.
type noopSpan struct{}

func (n noopSpan) End()                                      {}
func (n noopSpan) SetAttributes(attrs ...Attribute)          {}
func (n noopSpan) SetError()                                 {}
func (n noopSpan) RecordError(err error, attrs ...Attribute) {}
func (n noopSpan) Event(name string, attrs ...Attribute)     {}
func (n noopSpan) SetTokens(input, output int)               {}

// noopProvider is a Provider that returns noop spans.
type noopProvider struct{}

// Start returns the context unchanged and a span that records nothing.
func (n noopProvider) Start(ctx context.Context, name string, attrs ...Attribute) (context.Context, Span) {
	return ctx, noopSpan{}
}
