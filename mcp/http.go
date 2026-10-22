package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/httpjson"
)

const sessionHeader = "Mcp-Session-Id"

type httpTransport struct {
	cfg Config
	hc  *http.Client

	mu        sync.Mutex
	nextID    int64
	sessionID string
}

func newHTTPTransport(cfg Config) (Transport, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp: %s: URL is required: %w", cfg.Name, core.ErrInvalidConfig)
	}
	return &httpTransport{cfg: cfg, hc: httpjson.Client(cfg.Client)}, nil
}

// Call posts a JSON-RPC request and returns the result matching its id.
func (h *httpTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	h.mu.Unlock()

	msgs, err := h.post(ctx, rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}, id)
	if err != nil {
		return nil, err
	}
	resp, err := pickResponse(msgs, id)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error.wrap()
	}
	return resp.Result, nil
}

// Notify posts a JSON-RPC notification and discards the response.
func (h *httpTransport) Notify(ctx context.Context, method string, params any) error {
	_, err := h.post(ctx, rpcRequest{JSONRPC: "2.0", Method: method, Params: params}, 0)
	return err
}

// Close is a no-op; the HTTP transport holds no connection.
func (h *httpTransport) Close() error { return nil }

func (h *httpTransport) post(ctx context.Context, v any, wantID int64) ([]json.RawMessage, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: encode request: %w", h.cfg.Name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.URL, bytes.NewReader(payload)) //nolint:gosec // the MCP endpoint is operator-configured
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: build request: %w", h.cfg.Name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", h.cfg.ProtocolVersion)
	for k, val := range h.cfg.Headers {
		req.Header.Set(k, val)
	}
	h.mu.Lock()
	session := h.sessionID
	h.mu.Unlock()
	if session != "" {
		req.Header.Set(sessionHeader, session)
	}

	resp, err := h.hc.Do(req) //nolint:gosec // the request URL is operator-configured
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: post: %w: %w", h.cfg.Name, err, core.ErrProvider)
	}
	defer func() { _ = resp.Body.Close() }()

	if id := resp.Header.Get(sessionHeader); id != "" {
		h.mu.Lock()
		h.sessionID = id
		h.mu.Unlock()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp: %s: status %d: %s: %w", h.cfg.Name, resp.StatusCode, strings.TrimSpace(string(body)), core.ErrProvider)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return readSSE(resp.Body, h.cfg.Name, wantID)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMessageBytes))
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: read response: %w: %w", h.cfg.Name, err, core.ErrProvider)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, fmt.Errorf("mcp: %s: decode response array: %w: %w", h.cfg.Name, err, core.ErrProvider)
		}
		return arr, nil
	}
	return []json.RawMessage{json.RawMessage(trimmed)}, nil
}

// readSSE reads a server-sent event stream and returns the messages it
// carries. A streamable HTTP server may hold the stream open after it has
// answered, so reading stops as soon as the reply with the wanted id arrives:
// a slow stream must not look like a broken one. wantID of 0 reads to the end.
func readSSE(r io.Reader, name string, wantID int64) ([]json.RawMessage, error) {
	var out []json.RawMessage
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)

	var frame bytes.Buffer
	flush := func() bool {
		chunk := bytes.TrimSpace(frame.Bytes())
		frame.Reset()
		if len(chunk) == 0 {
			return false
		}
		out = append(out, json.RawMessage(append([]byte(nil), chunk...)))
		return wantID > 0 && messageHasID(chunk, wantID)
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if flush() {
				return out, nil
			}
		case strings.HasPrefix(line, "data:"):
			if frame.Len() > 0 {
				frame.WriteByte('\n')
			}
			frame.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("mcp: %s: read sse: %w", name, err)
	}
	return out, nil
}

// messageHasID reports whether the raw JSON-RPC message carries the id.
func messageHasID(raw []byte, wantID int64) bool {
	var resp rpcResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return false
	}
	return resp.ID == wantID
}

func pickResponse(msgs []json.RawMessage, id int64) (rpcResponse, error) {
	for _, m := range msgs {
		var resp rpcResponse
		if err := json.Unmarshal(m, &resp); err != nil {
			continue
		}
		if resp.ID == id {
			return resp, nil
		}
	}
	return rpcResponse{}, fmt.Errorf("mcp: no response for request %d: %w", id, core.ErrProvider)
}
