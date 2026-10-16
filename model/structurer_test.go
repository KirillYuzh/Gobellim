package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

type stubClient struct {
	got  Request
	resp Response
	err  error
}

func (c *stubClient) Model() string { return "stub" }

func (c *stubClient) Chat(_ context.Context, req Request) (Response, error) {
	c.got = req
	return c.resp, c.err
}

func TestStructureSendsSchemaAndProse(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"{\"ok\":true}"}}]}`)
	}))
	defer srv.Close()

	c, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	s := Structurer{
		Client:     c,
		Schema:     json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`),
		SchemaMode: SchemaInline,
	}
	text, err := s.Structure(context.Background(), "the patient has a fever")
	if err != nil {
		t.Fatal(err)
	}
	if text != `{"ok":true}` {
		t.Fatalf("answer = %q", text)
	}

	msgs, _ := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1 (single user message)", len(msgs))
	}
	m0, _ := msgs[0].(map[string]any)
	if m0["role"] != "user" {
		t.Fatalf("role = %v, want user", m0["role"])
	}
	content, _ := m0["content"].(string)
	if !strings.Contains(content, structureInstruction) {
		t.Fatalf("instruction missing: %q", content)
	}
	if !strings.Contains(content, `{"type":"object","properties":{"ok":{"type":"boolean"}}}`) {
		t.Fatalf("schema missing: %q", content)
	}
	if !strings.Contains(content, "the patient has a fever") {
		t.Fatalf("prose missing: %q", content)
	}
	if _, ok := got["response_format"]; ok {
		t.Fatalf("inline mode must not send response_format: %v", got["response_format"])
	}
}

func TestStructureDowngradesNativeForUnknownClient(t *testing.T) {
	c := &stubClient{resp: Response{Message: Message{Content: `{"ok":true}`}}}
	s := Structurer{
		Client:     c,
		Schema:     json.RawMessage(`{"type":"object"}`),
		SchemaMode: SchemaNative,
	}
	if _, err := s.Structure(context.Background(), "prose"); err != nil {
		t.Fatal(err)
	}
	if c.got.SchemaMode != SchemaInline {
		t.Fatalf("SchemaMode = %v, want SchemaInline (downgraded)", c.got.SchemaMode)
	}
	if len(c.got.JSONSchema) != 0 {
		t.Fatalf("unknown client must not receive the schema natively: %q", c.got.JSONSchema)
	}
	content := c.got.Messages[len(c.got.Messages)-1].Content
	if !strings.Contains(content, `{"type":"object"}`) {
		t.Fatalf("schema was not inlined after downgrade: %q", content)
	}
}

func TestStructureNativeClientSendsSchemaNatively(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"{}"}}]}`)
	}))
	defer srv.Close()

	c, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	s := Structurer{
		Client:     c,
		Schema:     json.RawMessage(`{"type":"object"}`),
		SchemaMode: SchemaNative,
	}
	if _, err := s.Structure(context.Background(), "prose"); err != nil {
		t.Fatal(err)
	}
	rf, ok := got["response_format"].(map[string]any)
	if !ok || rf["type"] != "json_schema" {
		t.Fatalf("response_format = %v, want a native json_schema", got["response_format"])
	}
	msgs, _ := got["messages"].([]any)
	content, _ := msgs[0].(map[string]any)["content"].(string)
	if strings.Contains(content, `{"type":"object"}`) {
		t.Fatalf("native mode must not duplicate the schema inline: %q", content)
	}
	if !strings.Contains(content, "prose") {
		t.Fatalf("prose missing: %q", content)
	}
}

func TestStructureCustomInstructionsReplacePrefix(t *testing.T) {
	c := &stubClient{resp: Response{Message: Message{Content: "{}"}}}
	s := Structurer{
		Client:       c,
		Schema:       json.RawMessage(`{"type":"object"}`),
		SchemaMode:   SchemaInline,
		Instructions: "Map the note to the ticket schema.",
	}
	if _, err := s.Structure(context.Background(), "prose"); err != nil {
		t.Fatal(err)
	}
	content := c.got.Messages[len(c.got.Messages)-1].Content
	if !strings.Contains(content, "Map the note to the ticket schema.") {
		t.Fatalf("custom instructions missing: %q", content)
	}
	if strings.Contains(content, structureInstruction) {
		t.Fatalf("built-in instruction must be replaced: %q", content)
	}
}

func TestStructureMissingConfig(t *testing.T) {
	if _, err := (Structurer{Schema: json.RawMessage(`{}`)}).Structure(context.Background(), "x"); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("missing Client: want ErrInvalidConfig, got %v", err)
	}
	c := &stubClient{}
	if _, err := (Structurer{Client: c}).Structure(context.Background(), "x"); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("missing Schema: want ErrInvalidConfig, got %v", err)
	}
}

func TestStructureReturnsAnswerVerbatim(t *testing.T) {
	const answer = "  {\"a\":\n1}  "
	c := &stubClient{resp: Response{Message: Message{Content: answer}}}
	s := Structurer{Client: c, Schema: json.RawMessage(`{"type":"object"}`)}
	got, err := s.Structure(context.Background(), "prose")
	if err != nil {
		t.Fatal(err)
	}
	if got != answer {
		t.Fatalf("answer = %q, want verbatim %q", got, answer)
	}
}

func TestStructureDoesNotMaskProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, `{"error":"boom"}`)
	}))
	defer srv.Close()

	c, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	s := Structurer{Client: c, Schema: json.RawMessage(`{"type":"object"}`)}
	_, err := s.Structure(context.Background(), "prose")
	if !errors.Is(err, core.ErrProvider) {
		t.Fatalf("want ErrProvider, got %v", err)
	}
}

func TestFenceProseEscapesBackticks(t *testing.T) {
	got := fenceProse("a ``` b")
	if !strings.HasPrefix(got, "````\n") || !strings.HasSuffix(got, "\n````") {
		t.Fatalf("fence = %q, want a 4-backtick fence", got)
	}
}
