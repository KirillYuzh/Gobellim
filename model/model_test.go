package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gobellim/kit/internal/core"
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

func TestGigaChatOAuthAndFunctionCall(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic creds" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		if got := r.Header.Get("RqUID"); len(got) != 36 {
			t.Errorf("RqUID = %q, want a uuid4", got)
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("scope") != "GIGACHAT_API_PERS" {
			t.Errorf("scope form = %v", r.Form)
		}
		_, _ = fmt.Fprintf(w, `{"access_token":"tok","expires_at":%d}`, time.Now().Add(time.Hour).UnixMilli())
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("bearer = %q", r.Header.Get("Authorization"))
		}
		body := decodeBody(t, r)
		if _, ok := body["functions"]; !ok {
			t.Errorf("functions not sent: %v", body)
		}
		writeJSON(w, `{"model":"GigaChat","choices":[{"message":{"role":"assistant","function_call":{"name":"get_ticket","arguments":{"id":"42"}}}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := GigaChat(GigaChatConfig{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
		Tools:    []ToolSpec{{Name: "get_ticket", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "get_ticket" {
		t.Fatalf("tool calls = %+v", resp.Message.ToolCalls)
	}
	if resp.Usage.TotalTokens != 3 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestGigaChatStructuredEmulation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"access_token":"tok","expires_at":%d}`, time.Now().Add(time.Hour).UnixMilli())
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		if body["function_call"] == nil {
			t.Errorf("function_call not forced: %v", body)
		}
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","function_call":{"name":"structured_output","arguments":{"answer":"yes"}}}}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _ := GigaChat(GigaChatConfig{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
	resp, err := c.Chat(context.Background(), Request{
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		JSONSchema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != `{"answer":"yes"}` {
		t.Fatalf("content = %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 0 {
		t.Fatalf("structured output must not surface as a tool call: %+v", resp.Message.ToolCalls)
	}
}

func TestOllamaEmptyArgsStayValid(t *testing.T) {
	if got := rawObject(""); len(got) != 0 {
		t.Fatalf("rawObject(\"\") = %v", got)
	}
	if got := rawObject("not json"); len(got) != 0 {
		t.Fatalf("invalid json should yield empty object, got %v", got)
	}
}
