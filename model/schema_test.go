package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func openAISchemaServer(t *testing.T, got *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = decodeBody(t, r)
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"{}"}}]}`)
	}))
}

func TestSchemaInlineInjectsIntoLastUserMessage(t *testing.T) {
	var got map[string]any
	srv := openAISchemaServer(t, &got)
	defer srv.Close()

	c, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	if _, err := c.Chat(context.Background(), Request{
		Messages: []Message{
			{Role: RoleSystem, Content: "sys"},
			{Role: RoleUser, Content: "first"},
			{Role: RoleUser, Content: "second"},
		},
		JSONSchema: json.RawMessage(`{"type":"object"}`),
		SchemaMode: SchemaInline,
	}); err != nil {
		t.Fatal(err)
	}

	if _, ok := got["response_format"]; ok {
		t.Fatalf("SchemaInline must not send response_format: %v", got["response_format"])
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	last, _ := msgs[2].(map[string]any)
	content, _ := last["content"].(string)
	if !strings.HasPrefix(content, "second") {
		t.Fatalf("last user content = %q", content)
	}
	if !strings.Contains(content, "Return only JSON that validates against this JSON Schema:") || !strings.Contains(content, `{"type":"object"}`) {
		t.Fatalf("schema instruction missing: %q", content)
	}
	if sys, _ := msgs[0].(map[string]any); sys["content"] != "sys" {
		t.Fatalf("system message changed: %v", sys["content"])
	}
}

func TestSchemaSystemCreatesSystemMessage(t *testing.T) {
	var got map[string]any
	srv := openAISchemaServer(t, &got)
	defer srv.Close()

	c, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	if _, err := c.Chat(context.Background(), Request{
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		JSONSchema: json.RawMessage(`{"type":"object"}`),
		SchemaMode: SchemaSystem,
	}); err != nil {
		t.Fatal(err)
	}

	if _, ok := got["response_format"]; ok {
		t.Fatalf("SchemaSystem must not send response_format: %v", got["response_format"])
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	sys, _ := msgs[0].(map[string]any)
	if sys["role"] != "system" {
		t.Fatalf("first message role = %v, want system", sys["role"])
	}
	content, _ := sys["content"].(string)
	if !strings.Contains(content, "Return only JSON that validates against this JSON Schema:") || !strings.Contains(content, `{"type":"object"}`) {
		t.Fatalf("schema instruction missing: %q", content)
	}
}

func TestSchemaSystemAppendsToExistingSystemMessage(t *testing.T) {
	var got map[string]any
	srv := openAISchemaServer(t, &got)
	defer srv.Close()

	c, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	if _, err := c.Chat(context.Background(), Request{
		Messages: []Message{
			{Role: RoleSystem, Content: "sys"},
			{Role: RoleUser, Content: "hi"},
		},
		JSONSchema: json.RawMessage(`{"type":"object"}`),
		SchemaMode: SchemaSystem,
	}); err != nil {
		t.Fatal(err)
	}

	msgs, _ := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (no new system message)", len(msgs))
	}
	sys, _ := msgs[0].(map[string]any)
	content, _ := sys["content"].(string)
	if !strings.HasPrefix(content, "sys") || !strings.Contains(content, `{"type":"object"}`) {
		t.Fatalf("system content = %q", content)
	}
}
