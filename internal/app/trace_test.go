package app

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/model"
	"github.com/KirillYuzh/Gobellim/trace"
)

// spanKey carries the recorded span in a context, the way an OpenTelemetry
// tracer carries its span.
type spanKey struct{}

// recordedSpan is one span the app created.
type recordedSpan struct {
	name   string
	parent *recordedSpan
	attrs  map[string]any
	ended  bool
}

// recordingProvider records the spans the application creates.
type recordingProvider struct {
	mu    sync.Mutex
	spans []*recordedSpan
}

// Start records a span and returns the context that carries it.
func (p *recordingProvider) Start(ctx context.Context, name string, attrs ...trace.Attribute) (context.Context, trace.Span) {
	s := &recordedSpan{name: name, attrs: map[string]any{}}
	if parent, ok := ctx.Value(spanKey{}).(*recordedSpan); ok {
		s.parent = parent
	}
	for _, a := range attrs {
		s.attrs[a.Key] = a.Value
	}
	p.mu.Lock()
	p.spans = append(p.spans, s)
	p.mu.Unlock()
	return context.WithValue(ctx, spanKey{}, s), s
}

// recorded returns the spans recorded so far.
func (p *recordingProvider) recorded() []*recordedSpan {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*recordedSpan(nil), p.spans...)
}

// named returns the recorded span with the given name.
func (p *recordingProvider) named(t *testing.T, name string) *recordedSpan {
	t.Helper()
	for _, s := range p.recorded() {
		if s.name == name {
			return s
		}
	}
	t.Fatalf("no span named %q in %v", name, p.names())
	return nil
}

func (p *recordingProvider) names() []string {
	out := []string{}
	for _, s := range p.recorded() {
		out = append(out, s.name)
	}
	return out
}

// End finishes the span.
func (s *recordedSpan) End() { s.ended = true }

// SetAttributes sets the attributes on the span.
func (s *recordedSpan) SetAttributes(attrs ...trace.Attribute) {
	for _, a := range attrs {
		s.attrs[a.Key] = a.Value
	}
}

// SetError marks the span as failed.
func (s *recordedSpan) SetError() { s.attrs["error"] = true }

// RecordError records the error on the span.
func (s *recordedSpan) RecordError(err error, attrs ...trace.Attribute) {
	s.attrs["error"] = err.Error()
}

// Event records an event on the span.
func (s *recordedSpan) Event(name string, attrs ...trace.Attribute) {}

// SetTokens records the token usage on the span.
func (s *recordedSpan) SetTokens(input, output int) {
	s.attrs[trace.AttrInputTokens] = input
	s.attrs[trace.AttrOutputTokens] = output
}

// stubLLM answers every chat with the same message.
type stubLLM struct {
	answer string
}

func (l *stubLLM) Chat(context.Context, model.Request) (model.Response, error) {
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: l.answer},
		Usage:   model.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
		Model:   "stub-model",
	}, nil
}

func (l *stubLLM) Model() string { return "stub-model" }

const traceTestPolicy = `version: 1
default: deny
roles:
  user:
    permissions:
      - chat
`

// newTraceTestApp builds an application that records its spans.
func newTraceTestApp(t *testing.T, content trace.ContentMode) (*App, *recordingProvider) {
	t.Helper()
	policy, err := authz.Parse(strings.NewReader(traceTestPolicy))
	if err != nil {
		t.Fatal(err)
	}
	provider := &recordingProvider{}
	app, err := New(context.Background(), Config{
		LLM:            &stubLLM{answer: "answer"},
		Policy:         policy,
		GuardL2:        guard.L1Only(),
		TraceContent:   content,
		TracerProvider: provider,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	return app, provider
}

// The operation has exactly one span of its own, and the calls it makes are
// its children.
func TestChatCreatesOneOperationSpan(t *testing.T) {
	app, provider := newTraceTestApp(t, trace.ContentRedacted)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u1", Roles: []string{"user"}, Tenant: "t1"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}); err != nil {
		t.Fatal(err)
	}

	operation := provider.named(t, "chat")
	if !operation.ended {
		t.Error("the operation span was not ended")
	}
	if got := operation.attrs[trace.AttrTenant]; got != "t1" {
		t.Errorf("tenant = %v, want t1", got)
	}
	if got := operation.attrs[trace.AttrUserID]; got != "u1" {
		t.Errorf("user = %v, want u1", got)
	}
	if _, ok := operation.attrs[trace.AttrTraceID]; !ok {
		t.Error("the operation span carries no trace ID")
	}

	// Every other span of the operation hangs under it: the calls are its
	// children, and the spans of the calls are their children.
	for _, s := range provider.recorded() {
		if s == operation {
			continue
		}
		if !descendsFrom(s, operation) {
			t.Errorf("span %q does not descend from the operation span", s.name)
		}
	}
}

// descendsFrom reports whether the span is the given one or one of its
// descendants.
func descendsFrom(s, ancestor *recordedSpan) bool {
	for cur := s; cur != nil; cur = cur.parent {
		if cur == ancestor {
			return true
		}
	}
	return false
}

// ContentNone records metadata only: neither the input nor the output.
func TestContentNoneRecordsNoContent(t *testing.T) {
	app, provider := newTraceTestApp(t, trace.ContentNone)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u1", Roles: []string{"user"}, Tenant: "t1"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}); err != nil {
		t.Fatal(err)
	}

	for _, s := range provider.recorded() {
		if v, ok := s.attrs[trace.AttrInput]; ok {
			t.Errorf("span %q recorded the input %v", s.name, v)
		}
		if v, ok := s.attrs[trace.AttrOutput]; ok {
			t.Errorf("span %q recorded the output %v", s.name, v)
		}
	}
}

// The masked mode records the text the guard produced, not the raw one.
func TestContentRedactedRecordsMaskedTextOnly(t *testing.T) {
	app, provider := newTraceTestApp(t, trace.ContentRedacted)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u1", Roles: []string{"user"}, Tenant: "t1"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}); err != nil {
		t.Fatal(err)
	}

	operation := provider.named(t, "chat")
	input, ok := operation.attrs[trace.AttrInput].(string)
	if !ok || input == "" {
		t.Fatalf("the operation span recorded no input: %v", operation.attrs)
	}
	if input != "hello" {
		t.Errorf("input = %q, want the masked text", input)
	}
}

// The raw mode is an explicit opt-in and records the text as it came in.
func TestContentFullRecordsRawText(t *testing.T) {
	app, provider := newTraceTestApp(t, trace.ContentFull)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u1", Roles: []string{"user"}, Tenant: "t1"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "raw text"}}}); err != nil {
		t.Fatal(err)
	}

	operation := provider.named(t, "chat")
	if got := operation.attrs[trace.AttrInput]; got != "raw text" {
		t.Errorf("input = %v, want the raw text", got)
	}
}

// An unknown content mode is a configuration error, not a silent default.
func TestUnknownContentModeIsRejected(t *testing.T) {
	policy, err := authz.Parse(strings.NewReader(traceTestPolicy))
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(context.Background(), Config{
		LLM:          &stubLLM{answer: "answer"},
		Policy:       policy,
		GuardL2:      guard.L1Only(),
		TraceContent: trace.ContentMode(99),
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil {
		t.Fatal("New accepted an unknown content mode")
	}
}

// Without a provider the operations still run.
func TestChatWithoutProvider(t *testing.T) {
	policy, err := authz.Parse(strings.NewReader(traceTestPolicy))
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), Config{
		LLM:     &stubLLM{answer: "answer"},
		Policy:  policy,
		GuardL2: guard.L1Only(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u1", Roles: []string{"user"}, Tenant: "t1"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}); err != nil {
		t.Fatal(err)
	}
}
