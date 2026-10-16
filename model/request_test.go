package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaToolChoiceNoneOmitsTools(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		writeJSON(w, `{"model":"q","message":{"role":"assistant","content":"x"},"done":true}`)
	}))
	defer srv.Close()

	c, _ := Ollama(srv.URL, "q")
	if _, err := c.Chat(context.Background(), Request{
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		Tools:      []ToolSpec{{Name: "get_ticket", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: "none",
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["tools"]; ok {
		t.Fatalf("tools must be omitted when ToolChoice is none: %v", got["tools"])
	}
}

func TestOpenAIToolChoiceKeyword(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	c, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	if _, err := c.Chat(context.Background(), Request{
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		Tools:      []ToolSpec{{Name: "get_ticket", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: "required",
	}); err != nil {
		t.Fatal(err)
	}
	if got["tool_choice"] != "required" {
		t.Fatalf("tool_choice = %v, want required", got["tool_choice"])
	}
}

func TestOpenAIToolChoiceNamedTool(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	c, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	if _, err := c.Chat(context.Background(), Request{
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		Tools:      []ToolSpec{{Name: "get_ticket", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: "get_ticket",
	}); err != nil {
		t.Fatal(err)
	}
	choice, ok := got["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %v, want an object", got["tool_choice"])
	}
	if choice["type"] != "function" {
		t.Fatalf("tool_choice.type = %v, want function", choice["type"])
	}
	fn, _ := choice["function"].(map[string]any)
	if fn["name"] != "get_ticket" {
		t.Fatalf("tool_choice.function.name = %v, want get_ticket", fn["name"])
	}
}

func TestProviderOptionsCannotOverrideContractKeys(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	c, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	if _, err := c.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
		ProviderOptions: map[string]any{
			"model":    "hacked",
			"messages": []any{},
			"stream":   true,
			"top_p":    0.5,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if got["model"] != "m" {
		t.Fatalf("model overridden by ProviderOptions: %v", got["model"])
	}
	if stream, _ := got["stream"].(bool); stream {
		t.Fatalf("stream overridden by ProviderOptions: %v", got["stream"])
	}
	msgs, ok := got["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages overridden by ProviderOptions: %v", got["messages"])
	}
	if got["top_p"] != 0.5 {
		t.Fatalf("allowed provider option missing: %v", got["top_p"])
	}
}
