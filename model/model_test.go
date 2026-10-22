package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return m
}

func writeJSON(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, s)
}

func TestOpenAIChatParsesToolCalls(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing bearer token")
		}
		got = decodeBody(t, r)
		writeJSON(w, `{"model":"m","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_ticket","arguments":"{\"id\":\"42\"}"}}]}}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`)
	}))
	defer srv.Close()

	c, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "secret", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
		Tools:    []ToolSpec{{Name: "get_ticket", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", resp.Message.ToolCalls)
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "get_ticket" || tc.Args != `{"id":"42"}` {
		t.Fatalf("tool call = %+v", tc)
	}
	if resp.Usage.TotalTokens != 8 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if tools, _ := got["tools"].([]any); len(tools) != 1 {
		t.Fatalf("request tools = %v", got["tools"])
	}
}

func TestOpenAIStructuredOutput(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"{}"}}]}`)
	}))
	defer srv.Close()

	c, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	_, err := c.Chat(context.Background(), Request{
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		JSONSchema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	rf, ok := got["response_format"].(map[string]any)
	if !ok || rf["type"] != "json_schema" {
		t.Fatalf("response_format = %v", got["response_format"])
	}
}

func TestOpenAIProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, `{"error":"boom"}`)
	}))
	defer srv.Close()

	c, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	_, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if !errors.Is(err, core.ErrProvider) {
		t.Fatalf("want ErrProvider, got %v", err)
	}
}

func TestVLLMConstructor(t *testing.T) {
	c, err := VLLM("http://vllm:8000/v1", "qwen3-32b")
	if err != nil {
		t.Fatal(err)
	}
	if c.Model() != "qwen3-32b" {
		t.Fatalf("model = %s", c.Model())
	}
}

func TestOllamaChat(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		got = decodeBody(t, r)
		writeJSON(w, `{"model":"q","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_ticket","arguments":{"id":"42"}}}]},"prompt_eval_count":3,"eval_count":5,"done":true}`)
	}))
	defer srv.Close()

	c, err := Ollama(srv.URL, "q")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Chat(context.Background(), Request{
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		Tools:      []ToolSpec{{Name: "get_ticket", Schema: json.RawMessage(`{"type":"object"}`)}},
		JSONSchema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Args != `{"id":"42"}` {
		t.Fatalf("tool calls = %+v", resp.Message.ToolCalls)
	}
	if resp.Usage.TotalTokens != 8 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if _, ok := got["format"]; !ok {
		t.Fatalf("format not sent: %v", got)
	}
	if stream, _ := got["stream"].(bool); stream {
		t.Fatalf("stream must be false")
	}
}

func TestOllamaEmptyArgsStayValid(t *testing.T) {
	if got := RawObject(""); len(got) != 0 {
		t.Fatalf("RawObject(\"\") = %v", got)
	}
	if got := RawObject("not json"); len(got) != 0 {
		t.Fatalf("invalid json should yield empty object, got %v", got)
	}
}
