package model

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type capClient struct {
	model  string
	native bool

	mu            sync.Mutex
	probes        int
	requests      []Request
	planned       []Message
	nativePlanned bool
	fail          bool
}

func (c *capClient) Model() string { return c.model }

func (c *capClient) SupportsNativeSchema() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.probes++
	return c.native
}

func (c *capClient) Chat(_ context.Context, req Request) (Response, error) {
	msgs, native := schemaPlan(req)
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.planned = msgs
	c.nativePlanned = native
	fail := c.fail
	c.mu.Unlock()
	if fail {
		return Response{}, retryableError()
	}
	return Response{Message: Message{Role: RoleAssistant, Content: c.model}}, nil
}

func (c *capClient) probeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.probes
}

func (c *capClient) lastRequest() Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		return Request{}
	}
	return c.requests[len(c.requests)-1]
}

func (c *capClient) lastPrompt() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.planned) - 1; i >= 0; i-- {
		if c.planned[i].Role == RoleUser {
			return c.planned[i].Content
		}
	}
	return ""
}

func (c *capClient) usedNativePlan() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nativePlanned
}

func newChain(t *testing.T, links ...Client) Client {
	t.Helper()
	c, err := Chain(ChainConfig{Backoff: time.Millisecond}, links...)
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	return c
}

func nativeLink(model string) *capClient { return &capClient{model: model, native: true} }

func plainLink(model string) *capClient { return &capClient{model: model} }

func TestChainSupportsNativeSchemaOnlyWhenEveryLinkDoes(t *testing.T) {
	tests := []struct {
		name  string
		links []Client
		want  bool
	}{
		{"all-native", []Client{nativeLink("a"), nativeLink("b")}, true},
		{"mixed", []Client{nativeLink("a"), plainLink("b")}, false},
		{"mixed-reverse", []Client{plainLink("a"), nativeLink("b")}, false},
		{"single-native", []Client{nativeLink("a")}, true},
		{"single-plain", []Client{plainLink("a")}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SupportsNativeSchema(newChain(t, tc.links...)); got != tc.want {
				t.Fatalf("SupportsNativeSchema(chain) = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestChainProbesCapabilitiesOnceAtConstruction(t *testing.T) {
	a, b := nativeLink("a"), nativeLink("b")
	c := newChain(t, a, b)

	probes := a.probeCount() + b.probeCount()
	if probes != 2 {
		t.Fatalf("construction probed %d links, want 2", probes)
	}
	for range 100 {
		if !SupportsNativeSchema(c) {
			t.Fatal("SupportsNativeSchema(chain) = false, want true")
		}
	}
	if got := a.probeCount() + b.probeCount(); got != probes {
		t.Fatalf("probing the chain re-read %d links; capabilities must be folded once at construction, not per probe", got-probes)
	}
}

func TestChainCapabilitiesFoldThroughRealClients(t *testing.T) {
	openai, err := NewOpenAI(OpenAIConfig{BaseURL: "http://127.0.0.1:1/v1", Model: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	ollama, err := NewOllama(OllamaConfig{BaseURL: "http://127.0.0.1:1", Model: "llama-test"})
	if err != nil {
		t.Fatal(err)
	}
	giga, err := GigaChat(GigaChatConfig{BaseURL: "http://127.0.0.1:1", Model: "GigaChat-2", Credentials: "creds"})
	if err != nil {
		t.Fatal(err)
	}

	if !SupportsNativeSchema(newChain(t, openai, ollama)) {
		t.Fatal("a chain of two native clients must support native schema")
	}
	if SupportsNativeSchema(newChain(t, openai, giga)) {
		t.Fatal("a chain containing a link without native schema must not claim it")
	}
}

func TestChainNestedCapabilitiesFold(t *testing.T) {
	inner := newChain(t, nativeLink("a"), plainLink("b"))
	outer := newChain(t, inner, nativeLink("c"))
	if SupportsNativeSchema(outer) {
		t.Fatal("a chain of chains must fold to the weakest link")
	}
	if !SupportsNativeSchema(newChain(t, newChain(t, nativeLink("a")), nativeLink("c"))) {
		t.Fatal("nested all-native chains must stay native")
	}
}

func TestChainNativeSchemaOfEmptyChainIsFalse(t *testing.T) {
	if chainNativeSchema(nil) {
		t.Fatal("chainNativeSchema(nil) = true, want false")
	}
	if chainNativeSchema([]Client{}) {
		t.Fatal("chainNativeSchema([]) = true, want false")
	}
}

func TestChainNilClientIsNotNative(t *testing.T) {
	if chainNativeSchema([]Client{nil}) {
		t.Fatal("a nil link must not make the chain native")
	}
}

func TestChainDowngradesNativeSchemaForAnIncapableLink(t *testing.T) {
	native, plain := nativeLink("a"), plainLink("b")
	native.fail = true
	c := newChain(t, native, plain)

	schema := json.RawMessage(`{"type":"object"}`)
	if _, err := c.Chat(context.Background(), Request{
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		JSONSchema: schema,
		SchemaMode: SchemaNative,
	}); err != nil {
		t.Fatal(err)
	}

	if got := native.lastRequest().SchemaMode; got != SchemaNative {
		t.Fatalf("the capable link saw SchemaMode %v, want %v: a link that can do it must not be downgraded", got, SchemaNative)
	}
	if !native.usedNativePlan() {
		t.Fatal("the capable link did not plan the request as native")
	}

	got := plain.lastRequest()
	if got.SchemaMode != SchemaInline {
		t.Fatalf("the incapable link saw SchemaMode %v, want %v", got.SchemaMode, SchemaInline)
	}
	if string(got.JSONSchema) != string(schema) {
		t.Fatalf("the schema was lost on downgrade: %q", got.JSONSchema)
	}
	if plain.usedNativePlan() {
		t.Fatal("the incapable link planned the request as native")
	}
	if prompt := plain.lastPrompt(); !strings.Contains(prompt, string(schema)) {
		t.Fatalf("the downgraded prompt does not state the schema: %q", prompt)
	}
}

func TestChainLeavesInlineSchemaAlone(t *testing.T) {
	native, plain := nativeLink("a"), plainLink("b")
	native.fail = true
	c := newChain(t, native, plain)
	if _, err := c.Chat(context.Background(), Request{
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		JSONSchema: json.RawMessage(`{"type":"object"}`),
		SchemaMode: SchemaInline,
	}); err != nil {
		t.Fatal(err)
	}
	if got := plain.lastRequest().SchemaMode; got != SchemaInline {
		t.Fatalf("SchemaMode = %v, want %v", got, SchemaInline)
	}
}

func TestStructurerDowngradesForMixedChain(t *testing.T) {
	native, plain := nativeLink("a"), plainLink("b")
	native.fail = true
	schema := json.RawMessage(`{"type":"object"}`)

	out, err := Structurer{Client: newChain(t, native, plain), Schema: schema, SchemaMode: SchemaNative}.
		Structure(context.Background(), "prose")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("want the link's content back")
	}
	if got := native.lastRequest().SchemaMode; got != SchemaInline {
		t.Fatalf("a mixed chain asked for %v, want the downgraded inline mode", got)
	}
	if got := plain.lastRequest().SchemaMode; got != SchemaInline {
		t.Fatalf("mixed chain asked a link without native schema for %v", got)
	}
}

func TestStructurerKeepsNativeForAllNativeChain(t *testing.T) {
	native := nativeLink("a")
	schema := json.RawMessage(`{"type":"object"}`)

	if _, err := (Structurer{Client: newChain(t, native), Schema: schema, SchemaMode: SchemaNative}).
		Structure(context.Background(), "prose"); err != nil {
		t.Fatal(err)
	}
	if got := native.lastRequest().SchemaMode; got != SchemaNative {
		t.Fatalf("all-native chain ran with SchemaMode %v, want %v", got, SchemaNative)
	}
	if !native.usedNativePlan() {
		t.Fatal("an all-native chain must keep the native plan")
	}
}

func TestChainDowngradeDoesNotMutateCallerRequest(t *testing.T) {
	plain := plainLink("a")
	req := Request{
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		JSONSchema: json.RawMessage(`{"type":"object"}`),
		SchemaMode: SchemaNative,
	}
	if _, err := newChain(t, plain).Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if req.SchemaMode != SchemaNative {
		t.Fatalf("the caller's request was rewritten to %v", req.SchemaMode)
	}
	if len(req.Messages) != 1 || req.Messages[0].Content != "hi" {
		t.Fatalf("the caller's messages were rewritten: %+v", req.Messages)
	}
}

func TestSupportsNativeSchemaNilClient(t *testing.T) {
	if SupportsNativeSchema(nil) {
		t.Fatal("SupportsNativeSchema(nil) = true, want false")
	}
}
