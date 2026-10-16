package model

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newGigaServer(t *testing.T, chat http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"access_token":"tok","expires_at":%d}`, time.Now().Add(time.Hour).UnixMilli())
	})
	mux.HandleFunc("/v1/chat/completions", chat)
	return httptest.NewServer(mux)
}

func TestParseFinishReason(t *testing.T) {
	cases := map[string]FinishReason{
		"stop":           FinishStop,
		"tool_calls":     FinishToolCalls,
		"length":         FinishLength,
		"content_filter": FinishContentFilter,
		"":               FinishOther,
		"weird":          FinishOther,
		"function_call":  FinishOther,
	}
	for in, want := range cases {
		if got := ParseFinishReason(in); got != want {
			t.Errorf("ParseFinishReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenAIFinishReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"length"}]}`)
	}))
	defer srv.Close()

	c, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != FinishLength {
		t.Fatalf("finish reason = %q, want %q", resp.FinishReason, FinishLength)
	}
}

func TestOpenAIFinishReasonUnknownBecomesOther(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"weird"}]}`)
	}))
	defer srv.Close()

	c, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m"})
	resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != FinishOther {
		t.Fatalf("finish reason = %q, want %q", resp.FinishReason, FinishOther)
	}
}

func TestOllamaFinishReasonFromDoneReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"model":"q","message":{"role":"assistant","content":"x"},"done_reason":"length","done":true}`)
	}))
	defer srv.Close()

	c, _ := Ollama(srv.URL, "q")
	resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != FinishLength {
		t.Fatalf("finish reason = %q, want %q", resp.FinishReason, FinishLength)
	}
}

func TestOllamaFinishReasonDerived(t *testing.T) {
	cases := []struct {
		name string
		body string
		want FinishReason
	}{
		{"tool calls", `{"message":{"role":"assistant","tool_calls":[{"function":{"name":"f","arguments":{}}}]},"done":true}`, FinishToolCalls},
		{"done", `{"message":{"role":"assistant","content":"x"},"done":true}`, FinishStop},
		{"incomplete", `{"message":{"role":"assistant","content":"x"},"done":false}`, FinishOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, tc.body)
			}))
			defer srv.Close()

			c, _ := Ollama(srv.URL, "q")
			resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
			if err != nil {
				t.Fatal(err)
			}
			if resp.FinishReason != tc.want {
				t.Fatalf("finish reason = %q, want %q", resp.FinishReason, tc.want)
			}
		})
	}
}

func TestGigaChatFinishReasonFromField(t *testing.T) {
	srv := newGigaServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
	})
	defer srv.Close()

	c, err := GigaChat(GigaChatConfig{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != FinishStop {
		t.Fatalf("finish reason = %q, want %q", resp.FinishReason, FinishStop)
	}
}

func TestGigaChatFinishReasonDerived(t *testing.T) {
	cases := []struct {
		name string
		body string
		want FinishReason
	}{
		{
			"function call",
			`{"choices":[{"message":{"role":"assistant","function_call":{"name":"get_ticket","arguments":{"id":"1"}}}}]}`,
			FinishToolCalls,
		},
		{"plain content", `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`, FinishStop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newGigaServer(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, tc.body)
			})
			defer srv.Close()

			c, _ := GigaChat(GigaChatConfig{BaseURL: srv.URL, AuthURL: srv.URL + "/oauth", Credentials: "creds"})
			resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
			if err != nil {
				t.Fatal(err)
			}
			if resp.FinishReason != tc.want {
				t.Fatalf("finish reason = %q, want %q", resp.FinishReason, tc.want)
			}
		})
	}
}
