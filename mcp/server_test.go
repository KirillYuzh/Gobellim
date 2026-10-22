package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/guard"
)

func echoServer(t *testing.T, handler func(ctx context.Context, args json.RawMessage) (string, error)) *Server {
	t.Helper()
	srv, err := NewServer(ServerConfig{
		Name:    "kit",
		Version: "1.0.0",
		Tools: []ServerTool{{
			Name:        "echo",
			Description: "echo back the message",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`),
			Handler:     handler,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestServerHTTPRoundTrip(t *testing.T) {
	srv := echoServer(t, func(_ context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Msg string `json:"msg"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		return "echo: " + in.Msg, nil
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c, err := New(Config{Name: "kit", URL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if got := c.ServerInfo().Name; got != "kit" {
		t.Fatalf("serverInfo name = %q", got)
	}

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" || tools[0].Description != "echo back the message" {
		t.Fatalf("tools = %+v", tools)
	}
	if !json.Valid(tools[0].InputSchema) {
		t.Fatalf("inputSchema is not JSON: %s", tools[0].InputSchema)
	}

	res, err := c.CallTool(ctx, "echo", json.RawMessage(`{"msg":"hi"}`))
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if res.IsError || !strings.Contains(res.Text(), "echo: hi") {
		t.Fatalf("result = %+v (text %q)", res, res.Text())
	}
}

func TestServerToolErrorIsContent(t *testing.T) {
	srv := echoServer(t, func(context.Context, json.RawMessage) (string, error) {
		return "", errors.New("boom")
	})
	res := srv.handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{}}}`))
	if res == nil || res.Error != nil {
		t.Fatalf("want a result, got %+v", res)
	}
	body, _ := json.Marshal(res.Result)
	if !strings.Contains(string(body), `"isError":true`) || !strings.Contains(string(body), "boom") {
		t.Fatalf("result = %s", body)
	}
}

func TestServerUnknownToolIsAnError(t *testing.T) {
	srv := echoServer(t, func(context.Context, json.RawMessage) (string, error) { return "", nil })
	res := srv.handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"nope"}}`))
	if res == nil || res.Error == nil || res.Error.Code != codeInvalidParams {
		t.Fatalf("want invalid params, got %+v", res)
	}
	if string(res.ID) != "7" {
		t.Fatalf("id = %s, want 7", res.ID)
	}
}

func TestServerStdioFraming(t *testing.T) {
	srv := echoServer(t, func(context.Context, json.RawMessage) (string, error) { return "ok", nil })
	in := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":"abc","method":"tools/list"}`,
		``,
	}, "\n"))

	var out strings.Builder
	if err := srv.ServeStdio(context.Background(), in, &out); err != nil {
		t.Fatalf("stdout: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 replies (the notification is silent), got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"serverInfo"`) {
		t.Fatalf("first reply = %s", lines[0])
	}
	if !strings.Contains(lines[1], `"id":"abc"`) || !strings.Contains(lines[1], `"tools"`) {
		t.Fatalf("second reply = %s", lines[1])
	}
}

func TestServerRejectsBrowserOrigin(t *testing.T) {
	srv := echoServer(t, func(context.Context, json.RawMessage) (string, error) { return "", nil })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://evil.example")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	get, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	getResp, err := ts.Client().Do(get)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = io.Copy(io.Discard, getResp.Body); _ = getResp.Body.Close() }()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", getResp.StatusCode)
	}
}

type echoIn struct {
	Msg string `json:"msg"`
}

func testAppWithTools(t *testing.T, ts *gobellim.ToolSet) *gobellim.App {
	t.Helper()
	policy, err := authz.Parse(strings.NewReader("version: 1\ndefault: deny\nroles:\n  user:\n    permissions:\n      - \"*\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	app, err := gobellim.New(context.Background(), gobellim.Config{ES: nopES{}, LLM: nopLLM{}, Policy: policy, GuardL2: guard.L1Only(), Tools: ts})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	return app
}

func TestFromToolRunsThroughTheCallPath(t *testing.T) {
	def := gobellim.Tool("echo", func(_ context.Context, in echoIn) (string, error) {
		return in.Msg, nil
	}, gobellim.Desc("echo"))
	reg := gobellim.NewRegistry()
	reg.Register(def)
	ts, err := reg.Build()
	if err != nil {
		t.Fatal(err)
	}
	app := testAppWithTools(t, ts)
	srv, err := NewServer(ServerConfig{Name: "kit", Tools: []ServerTool{FromTool(app, def)}})
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"hi"}}}`)

	authorized := gobellim.WithPrincipal(context.Background(), gobellim.Principal{UserID: "u", Roles: []string{"user"}, Tenant: "t"})
	res := srv.handle(authorized, payload)
	if res == nil || res.Error != nil {
		t.Fatalf("authorized call failed: %+v", res)
	}
	body, _ := json.Marshal(res.Result)
	if !strings.Contains(string(body), "hi") {
		t.Fatalf("result = %s", body)
	}

	anonymous := srv.handle(context.Background(), payload)
	body, _ = json.Marshal(anonymous.Result)
	if !strings.Contains(string(body), `"isError":true`) {
		t.Fatalf("call without a principal must fail: %s", body)
	}
}
