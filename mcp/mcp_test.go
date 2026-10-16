package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestMCPStdioHelperProcess is not a real test: when KIT_MCP_HELPER is set it
// acts as a tiny MCP server over stdio, so the stdio transport can be tested
// against a real subprocess.
func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("KIT_MCP_HELPER") != "1" {
		return
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1<<20)
	out := bufio.NewWriter(os.Stdout)

	for in.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			continue
		}
		if len(req.ID) == 0 {
			continue // notification
		}
		id := string(req.ID)
		switch req.Method {
		case "initialize":
			_, _ = fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"fake","version":"1"}}}`, id)
		case "tools/list":
			_, _ = fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo","description":"echo text","inputSchema":{"type":"object","properties":{"text":{"type":"string"}}}}]}}`, id)
		case "tools/call":
			_, _ = fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"hello"}]}}`, id)
		default:
			_, _ = fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found"}}`, id)
		}
		_ = out.WriteByte('\n')
		_ = out.Flush()
	}
	os.Exit(0)
}

func TestStdioClientListsAndCallsTools(t *testing.T) {
	c, err := New(Config{
		Name:    "fake",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPStdioHelperProcess"},
		Env:     []string{"KIT_MCP_HELPER=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" || tools[0].Description != "echo text" {
		t.Fatalf("tools = %+v", tools)
	}
	if info := c.ServerInfo(); info.Name != "fake" || info.Version != "1" {
		t.Fatalf("serverInfo = %+v", info)
	}

	res, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "hello" || res.IsError {
		t.Fatalf("result = %+v", res)
	}
}

func TestStdioCallUnknownMethodIsProviderError(t *testing.T) {
	c, err := New(Config{
		Name:    "fake",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPStdioHelperProcess"},
		Env:     []string{"KIT_MCP_HELPER=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = c.transport.Call(context.Background(), "does/not/exist", nil)
	if err == nil {
		t.Fatal("want an rpc error")
	}
	var rpc *rpcError
	if !errors.As(err, &rpc) || rpc.Code != -32601 {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRequiresExactlyOneTransport(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("want an error when neither Command nor URL is set")
	}
	if _, err := New(Config{Command: "x", URL: "http://y"}); err == nil {
		t.Fatal("want an error when both Command and URL are set")
	}
	if _, err := New(Config{Name: "n", URL: "http://y"}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func jsonRPCServer(t *testing.T, sse bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		id := string(req.ID)
		var result string
		switch req.Method {
		case "initialize":
			result = `{"serverInfo":{"name":"fake-http","version":"2"}}`
		case "tools/list":
			result = `{"tools":[{"name":"echo","description":"d","inputSchema":{"type":"object"}}]}`
		case "tools/call":
			result = `{"content":[{"type":"text","text":"ok"}]}`
		default:
			result = `null`
		}
		payload := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, id, result)
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, payload)
	}))
}

func TestHTTPClientListsAndCallsTools(t *testing.T) {
	srv := jsonRPCServer(t, false)
	defer srv.Close()

	c, err := New(Config{Name: "http", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
	res, err := c.CallTool(ctx, "echo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "ok" {
		t.Fatalf("result = %+v", res)
	}
}

func TestHTTPClientReadsSSE(t *testing.T) {
	srv := jsonRPCServer(t, true)
	defer srv.Close()

	c, err := New(Config{Name: "sse", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
}

func TestHTTPClientEchoesSessionHeader(t *testing.T) {
	var gotSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if req.Method == "initialize" {
			w.Header().Set(sessionHeader, "sess-123")
		}
		gotSession = r.Header.Get(sessionHeader)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{}}`, req.ID)
		default:
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[]}}`, req.ID)
		}
	}))
	defer srv.Close()

	c, err := New(Config{Name: "sess", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotSession != "sess-123" {
		t.Fatalf("session header = %q, want sess-123", gotSession)
	}
}

func TestCallResultTextJoinsTextParts(t *testing.T) {
	res := CallResult{Content: []Content{{Type: "text", Text: "a"}, {Type: "image"}, {Type: "text", Text: "b"}}}
	if got := res.Text(); got != "a\nb" {
		t.Fatalf("Text() = %q", got)
	}
}

func TestToolPrefix(t *testing.T) {
	if got := ToolPrefix("fs", "read_file"); got != "mcp:fs:read_file" {
		t.Fatalf("ToolPrefix = %q", got)
	}
	if !strings.HasPrefix(ToolPrefix("fs", "read_file"), "mcp:") {
		t.Fatal("prefix must be namespaced")
	}
}
