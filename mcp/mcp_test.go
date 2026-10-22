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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

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
			continue
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

// TestMCPStdioOversizedHelperProcess writes one message larger than the
// reader's limit, so the reader fails instead of delivering it.
func TestMCPStdioOversizedHelperProcess(t *testing.T) {
	if os.Getenv("KIT_MCP_BIG") != "1" {
		return
	}
	big := strings.Repeat("x", maxMessageBytes+1024)
	_, _ = fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":\"%s\"}\n", big)
	_ = os.Stdout.Sync()
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

// A reader failure must reach the caller: the transport is done, so a later
// call fails at once instead of waiting for its deadline.
func TestStdioReadErrorFailsLaterCalls(t *testing.T) {
	c, err := New(Config{
		Name:    "big",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPStdioOversizedHelperProcess"},
		Env:     []string{"KIT_MCP_BIG=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	_, err = c.transport.Call(ctx, "initialize", nil)
	if err == nil {
		t.Fatal("want an error when the reader fails")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the call waited %s for a reply that could never come", elapsed)
	}
	if !errors.Is(err, core.ErrProvider) {
		t.Fatalf("err = %v, want a provider error", err)
	}
}

// The transport owns goroutines for the life of the connection: they must be
// gone once the client is closed, or every connection leaks three of them.
func TestStdioTransportGoroutinesEndWithTheClient(t *testing.T) {
	before := runtime.NumGoroutine()
	c, err := New(Config{
		Name:    "fake",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPStdioHelperProcess"},
		Env:     []string{"KIT_MCP_HELPER=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// The goroutines wind down asynchronously, so give them a moment.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutines leaked: %d before, %d after", before, runtime.NumGoroutine())
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

// A streamable HTTP server may hold the stream open after it has answered.
// The call must return as soon as the reply arrives, so a slow stream does not
// look like a broken one.
func TestHTTPClientReturnsWhileTheStreamStaysOpen(t *testing.T) {
	release := make(chan struct{})
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
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		payload := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"tools":[]}}`, req.ID)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	c, err := New(Config{Name: "sse-open", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	done := make(chan error, 1)
	go func() {
		_, err := c.ListTools(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the call waited for the stream to end instead of the reply")
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
