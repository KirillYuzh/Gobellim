package gigachat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
)

type testEnv struct {
	mux       *http.ServeMux
	oauthHits atomic.Int32
	lastChat  atomic.Value // chatRequest
}

func newTestEnv(t *testing.T) (*testEnv, *Model) {
	t.Helper()
	env := &testEnv{mux: http.NewServeMux()}
	env.mux.HandleFunc("/oauth", func(w http.ResponseWriter, r *http.Request) {
		env.oauthHits.Add(1)
		if got := r.Header.Get("Authorization"); got != "Basic key" {
			t.Errorf("oauth auth = %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if got := r.Form.Get("scope"); got != "GIGACHAT_API_PERS" {
			t.Errorf("scope = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"expires_at":   time.Now().Add(time.Hour).UnixMilli(),
		})
	})
	env.mux.HandleFunc("/api/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("chat auth = %q", got)
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode chat: %v", err)
			return
		}
		env.lastChat.Store(req)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"role": "assistant", "content": `{"Reply":"ok"}`},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
			"model": "GigaChat",
		})
	})
	server := httptest.NewServer(env.mux)
	t.Cleanup(server.Close)

	m, err := New(Config{
		AuthKey:  "key",
		BaseURL:  server.URL + "/api/v1",
		OAuthURL: server.URL + "/oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	return env, m
}

func TestChatMapsRequestAndResponse(t *testing.T) {
	env, m := newTestEnv(t)
	req := gobellim.Request{
		Messages: []gobellim.Message{
			{Role: gobellim.RoleSystem, Content: "system"},
			{Role: gobellim.RoleUser, Content: "hi"},
		},
		Tools: []gobellim.ToolSpec{{
			Name:        "orders.read",
			Description: "read an order",
			Schema:      json.RawMessage(`{"type":"object"}`),
		}},
		JSONSchema: json.RawMessage(`{"type":"object"}`),
		MaxTokens:  100,
	}
	resp, err := m.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != `{"Reply":"ok"}` {
		t.Fatalf("content = %q", resp.Message.Content)
	}
	if resp.Usage.TotalTokens != 15 || resp.Model != "GigaChat" {
		t.Fatalf("usage/model = %+v/%q", resp.Usage, resp.Model)
	}
	if resp.FinishReason != gobellim.FinishStop {
		t.Fatalf("finish = %v", resp.FinishReason)
	}

	sent, _ := env.lastChat.Load().(chatRequest)
	if len(sent.Functions) != 1 || sent.Functions[0].Name != "orders.read" {
		t.Fatalf("functions = %+v", sent.Functions)
	}
	if !strings.Contains(sent.Messages[0].Content, "Return only a JSON object") {
		t.Fatalf("schema was not appended: %q", sent.Messages[0].Content)
	}
	if sent.FunctionCall != "auto" {
		t.Fatalf("function_call = %v", sent.FunctionCall)
	}

	if _, err := m.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := env.oauthHits.Load(); got != 1 {
		t.Fatalf("oauth hits = %d, want 1 (token must be cached)", got)
	}
}

func TestToolCall(t *testing.T) {
	env := &testEnv{mux: http.NewServeMux()}
	env.mux.HandleFunc("/oauth", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"expires_at":   time.Now().Add(time.Hour).UnixMilli(),
		})
	})
	env.mux.HandleFunc("/api/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":          "assistant",
					"content":       "",
					"function_call": map[string]any{"name": "orders.read", "arguments": `{"id":"A-1"}`},
				},
				"finish_reason": "function_call",
			}},
			"model": "GigaChat",
		})
	})
	server := httptest.NewServer(env.mux)
	defer server.Close()

	m, err := New(Config{AuthKey: "key", BaseURL: server.URL + "/api/v1", OAuthURL: server.URL + "/oauth"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := m.Chat(context.Background(), gobellim.Request{
		Messages: []gobellim.Message{{Role: gobellim.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", resp.Message.ToolCalls)
	}
	if resp.Message.ToolCalls[0].Name != "orders.read" || resp.Message.ToolCalls[0].Args != `{"id":"A-1"}` {
		t.Fatalf("tool call = %+v", resp.Message.ToolCalls[0])
	}
	if resp.FinishReason != gobellim.FinishToolCalls {
		t.Fatalf("finish = %v", resp.FinishReason)
	}
}

func TestUnauthorizedRetry(t *testing.T) {
	var chatHits atomic.Int32
	env := &testEnv{mux: http.NewServeMux()}
	env.mux.HandleFunc("/oauth", func(w http.ResponseWriter, r *http.Request) {
		env.oauthHits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"expires_at":   time.Now().Add(time.Hour).UnixMilli(),
		})
	})
	env.mux.HandleFunc("/api/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if chatHits.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
			"model": "GigaChat",
		})
	})
	server := httptest.NewServer(env.mux)
	defer server.Close()

	m, err := New(Config{AuthKey: "key", BaseURL: server.URL + "/api/v1", OAuthURL: server.URL + "/oauth"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := m.Chat(context.Background(), gobellim.Request{
		Messages: []gobellim.Message{{Role: gobellim.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("content = %q", resp.Message.Content)
	}
	if env.oauthHits.Load() != 2 || chatHits.Load() != 2 {
		t.Fatalf("oauth=%d chat=%d, want 2/2", env.oauthHits.Load(), chatHits.Load())
	}
}
