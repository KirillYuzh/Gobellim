package gigachat

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

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/model"
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

func newGigaServer(t *testing.T, chat http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"access_token":"tok","expires_at":%d}`, time.Now().Add(time.Hour).UnixMilli())
	})
	mux.HandleFunc("/v1/chat/completions", chat)
	return httptest.NewServer(mux)
}

func TestOAuthAndFunctionCall(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic creds" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		if got := r.Header.Get("RqUID"); len(got) != 36 {
			t.Errorf("RqUID = %q, want a uuid4", got)
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("scope") != DefaultScope {
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

	c, err := New(Config{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Chat(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		Tools:    []model.ToolSpec{{Name: "get_ticket", Schema: json.RawMessage(`{"type":"object"}`)}},
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

func TestWrapsSpotlightedToolResult(t *testing.T) {
	var got map[string]any
	srv := newGigaServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	})
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
	if err != nil {
		t.Fatal(err)
	}
	const result = "<untrusted-data>\n{\"city\":\"Пенза\"}\n</untrusted-data>"
	if _, err := c.Chat(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "погода?"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "1", Name: "get_weather", Args: `{"city":"Пенза"}`}}},
		{Role: model.RoleTool, ToolCallID: "1", Name: "get_weather", Content: result},
	}}); err != nil {
		t.Fatal(err)
	}

	msgs, _ := got["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %v", msgs)
	}
	fn, _ := msgs[2].(map[string]any)
	content, _ := fn["content"].(string)
	if !json.Valid([]byte(content)) {
		t.Fatalf("function result is not valid JSON: %q", content)
	}
	var wrapped map[string]string
	if err := json.Unmarshal([]byte(content), &wrapped); err != nil {
		t.Fatal(err)
	}
	if wrapped["result"] != result {
		t.Fatalf("result = %q, want the spotlighted text", wrapped["result"])
	}
}

func TestStructuredEmulation(t *testing.T) {
	srv := newGigaServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		if body["function_call"] == nil {
			t.Errorf("function_call not forced: %v", body)
		}
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","function_call":{"name":"structured_output","arguments":{"answer":"yes"}}}}]}`)
	})
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Chat(context.Background(), model.Request{
		Messages:   []model.Message{{Role: model.RoleUser, Content: "hi"}},
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

func TestFinishReasonFromField(t *testing.T) {
	srv := newGigaServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
	})
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Chat(context.Background(), model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != model.FinishStop {
		t.Fatalf("finish reason = %q, want %q", resp.FinishReason, model.FinishStop)
	}
}

func TestFinishReasonDerived(t *testing.T) {
	cases := []struct {
		name string
		body string
		want model.FinishReason
	}{
		{
			"function call",
			`{"choices":[{"message":{"role":"assistant","function_call":{"name":"get_ticket","arguments":{"id":"1"}}}}]}`,
			model.FinishToolCalls,
		},
		{"plain content", `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`, model.FinishStop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newGigaServer(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, tc.body)
			})
			defer srv.Close()

			c, err := New(Config{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := c.Chat(context.Background(), model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
			if err != nil {
				t.Fatal(err)
			}
			if resp.FinishReason != tc.want {
				t.Fatalf("finish reason = %q, want %q", resp.FinishReason, tc.want)
			}
		})
	}
}

func TestProviderErrorIsClassified(t *testing.T) {
	srv := newGigaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, `{"error":"boom"}`)
	})
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Chat(context.Background(), model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}); !errors.Is(err, core.ErrProvider) {
		t.Fatalf("want ErrProvider, got %v", err)
	}
}

func TestNamedToolChoiceForcesTheFunction(t *testing.T) {
	var got map[string]any
	srv := newGigaServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	})
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Chat(context.Background(), model.Request{
		Messages:   []model.Message{{Role: model.RoleUser, Content: "hi"}},
		Tools:      []model.ToolSpec{{Name: "get_ticket", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: "get_ticket",
	}); err != nil {
		t.Fatal(err)
	}

	fc, _ := got["function_call"].(map[string]any)
	if fc["name"] != "get_ticket" {
		t.Fatalf("function_call = %v, want a forced get_ticket", got["function_call"])
	}
}

func TestDefaultToolChoiceIsAuto(t *testing.T) {
	var got map[string]any
	srv := newGigaServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	})
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Chat(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		Tools:    []model.ToolSpec{{Name: "get_ticket", Schema: json.RawMessage(`{"type":"object"}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	if got["function_call"] != "auto" {
		t.Fatalf("function_call = %v, want auto", got["function_call"])
	}
}

func TestUnsupportedToolChoiceIsAnError(t *testing.T) {
	cases := []struct {
		name   string
		choice string
		want   error
	}{
		{"required", "required", core.ErrProvider},
		{"unoffered name", "absent", core.ErrInvalidArgs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newGigaServer(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("the request must not reach the provider")
			})
			defer srv.Close()

			c, err := New(Config{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Chat(context.Background(), model.Request{
				Messages:   []model.Message{{Role: model.RoleUser, Content: "hi"}},
				Tools:      []model.ToolSpec{{Name: "get_ticket", Schema: json.RawMessage(`{"type":"object"}`)}},
				ToolChoice: tc.choice,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParallelToolCallsAreRejected(t *testing.T) {
	srv := newGigaServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request must not reach the provider")
	})
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Chat(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "1", Name: "a", Args: `{}`}, {ID: "2", Name: "b", Args: `{}`}}},
		{Role: model.RoleTool, ToolCallID: "1", Name: "a", Content: `{}`},
		{Role: model.RoleTool, ToolCallID: "2", Name: "b", Content: `{}`},
	}})
	if !errors.Is(err, core.ErrProvider) {
		t.Fatalf("err = %v, want ErrProvider for parallel tool calls", err)
	}
}

func TestCapabilities(t *testing.T) {
	c, err := New(Config{Credentials: "id:secret", ContextTokens: 131072})
	if err != nil {
		t.Fatal(err)
	}
	var _ model.ContextWindow = c
	var _ model.Client = c
	if got := c.ContextTokens(); got != 131072 {
		t.Fatalf("ContextTokens() = %d, want 131072", got)
	}
	if c.SupportsNativeSchema() {
		t.Fatal("GigaChat has no native schema field")
	}
}

func TestNewValidatesConfig(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("empty credentials: err = %v, want ErrInvalidConfig", err)
	}
	if _, err := New(Config{Credentials: "id:secret", ContextTokens: -1}); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("negative ContextTokens: err = %v, want ErrInvalidConfig", err)
	}
}
