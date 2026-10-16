// Package mcp is a small Model Context Protocol client: it speaks JSON-RPC 2.0
// over stdio (a spawned server process) or streamable HTTP, lists the server's
// tools and calls them. Register turns those tools into kit tools, each named
// mcp:<server>:<tool> with the Egress effect, so the ordinary authorization and
// guard path applies to them unchanged.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gobellim/kit/internal/core"
)

// ProtocolVersion is the MCP revision this client speaks.
const ProtocolVersion = "2025-06-18"

// clientName/clientVersion identify this client during the initialize handshake.
const (
	clientName    = "gobellim-kit"
	clientVersion = "1.0.0"
)

// Config selects a transport and names the server. Exactly one of Command
// (stdio) and URL (streamable HTTP) must be set.
type Config struct {
	Name    string   // server name; becomes the middle segment of mcp:<name>:<tool>
	Command string   // stdio: executable to spawn
	Args    []string // stdio: arguments
	Env     []string // stdio: extra environment entries ("K=V")
	Dir     string   // stdio: working directory
	URL     string   // http: streamable HTTP endpoint
	Headers map[string]string
	Client  *http.Client
	Logger  *slog.Logger

	ProtocolVersion string
}

func (c Config) withDefaults() Config {
	if c.Name == "" {
		c.Name = "mcp"
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.ProtocolVersion == "" {
		c.ProtocolVersion = ProtocolVersion
	}
	return c
}

// ServerInfo is what the server reports about itself during initialize.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Tool is one tool the server exposes.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Content is one item of a tool result. Only text is interpreted; other kinds
// are preserved in the raw result for the caller.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// CallResult is the outcome of tools/call.
type CallResult struct {
	Content           []Content       `json:"content,omitempty"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// Text joins the text parts of the result.
func (r CallResult) Text() string {
	out := ""
	for i, c := range r.Content {
		if c.Type != "text" {
			continue
		}
		if i > 0 && out != "" {
			out += "\n"
		}
		out += c.Text
	}
	return out
}

// Transport carries JSON-RPC messages to one server. Call performs a
// request/response exchange and returns the raw result; Notify sends a
// notification and ignores any reply. The built-in implementations are the
// stdio transport (a spawned process) and the streamable-HTTP transport.
type Transport interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	Notify(ctx context.Context, method string, params any) error
	Close() error
}

// Client is a JSON-RPC client bound to one MCP server.
type Client struct {
	cfg       Config
	transport Transport

	mu         sync.RWMutex
	serverInfo ServerInfo
	ready      bool
}

// New builds a client for the configured transport. It does not talk to the
// server yet; call Initialize for that. Close releases the transport.
func New(cfg Config) (*Client, error) {
	cfg = cfg.withDefaults()
	var (
		tr  Transport
		err error
	)
	switch {
	case cfg.Command != "" && cfg.URL != "":
		return nil, fmt.Errorf("mcp: set either Command or URL, not both: %w", core.ErrInvalidConfig)
	case cfg.Command != "":
		tr, err = newStdioTransport(cfg)
	case cfg.URL != "":
		tr, err = newHTTPTransport(cfg)
	default:
		return nil, fmt.Errorf("mcp: Command or URL is required: %w", core.ErrInvalidConfig)
	}
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, transport: tr}, nil
}

// Name is the server name used in tool names.
func (c *Client) Name() string { return c.cfg.Name }

// ServerInfo returns what the server reported during Initialize.
func (c *Client) ServerInfo() ServerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverInfo
}

// Initialize performs the MCP handshake. It is idempotent.
func (c *Client) Initialize(ctx context.Context) error {
	c.mu.RLock()
	ready := c.ready
	c.mu.RUnlock()
	if ready {
		return nil
	}

	params := map[string]any{
		"protocolVersion": c.cfg.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": clientName, "version": clientVersion},
	}
	raw, err := c.transport.Call(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("mcp: %s: initialize: %w", c.cfg.Name, err)
	}
	var res struct {
		ProtocolVersion string     `json:"protocolVersion"`
		ServerInfo      ServerInfo `json:"serverInfo"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("mcp: %s: decode initialize: %w: %w", c.cfg.Name, err, core.ErrProvider)
	}
	if res.ProtocolVersion != "" && res.ProtocolVersion != c.cfg.ProtocolVersion {
		c.cfg.Logger.Warn("mcp: server protocol version differs",
			"server", c.cfg.Name, "want", c.cfg.ProtocolVersion, "got", res.ProtocolVersion)
	}
	if err := c.transport.Notify(ctx, "notifications/initialized", nil); err != nil {
		return fmt.Errorf("mcp: %s: initialized notification: %w", c.cfg.Name, err)
	}

	c.mu.Lock()
	c.serverInfo = res.ServerInfo
	c.ready = true
	c.mu.Unlock()
	return nil
}

// ListTools returns every tool the server exposes, following pagination.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	if err := c.Initialize(ctx); err != nil {
		return nil, err
	}
	var (
		out    []Tool
		cursor string
	)
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.transport.Call(ctx, "tools/list", params)
		if err != nil {
			return nil, fmt.Errorf("mcp: %s: tools/list: %w", c.cfg.Name, err)
		}
		var page struct {
			Tools []Tool `json:"tools"`
			Next  string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("mcp: %s: decode tools/list: %w: %w", c.cfg.Name, err, core.ErrProvider)
		}
		out = append(out, page.Tools...)
		if page.Next == "" {
			return out, nil
		}
		cursor = page.Next
	}
}

// CallTool invokes a tool by its server-side name.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	if err := c.Initialize(ctx); err != nil {
		return CallResult{}, err
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var arguments any
	if err := json.Unmarshal(args, &arguments); err != nil {
		return CallResult{}, fmt.Errorf("mcp: %s: tool %s: invalid arguments: %w: %w", c.cfg.Name, name, err, core.ErrInvalidArgs)
	}
	raw, err := c.transport.Call(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return CallResult{}, err
	}
	var res CallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return CallResult{}, fmt.Errorf("mcp: %s: decode tools/call: %w: %w", c.cfg.Name, err, core.ErrProvider)
	}
	return res, nil
}

// Close releases the transport (for stdio, it terminates the server process).
func (c *Client) Close() error {
	if c.transport == nil {
		return nil
	}
	return c.transport.Close()
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message)
}

func (e *rpcError) unwrap() error {
	return fmt.Errorf("%w: %w", e, core.ErrProvider)
}
