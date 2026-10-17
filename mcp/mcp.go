package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// ProtocolVersion is the MCP protocol revision used when Config.ProtocolVersion
// is empty.
const ProtocolVersion = "2025-06-18"

const (
	clientName    = "gobellim-kit"
	clientVersion = "1.0.0"
)

// Config describes one MCP client connection: a stdio server via Command, Args,
// Env and Dir, or a streamable HTTP endpoint via URL, Headers and Client.
type Config struct {
	Name    string
	Command string
	Args    []string
	Env     []string
	Dir     string
	URL     string
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

// ServerInfo is the name and version an MCP server reports during initialize.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Tool is a tool advertised by an MCP server, including its input schema as sent
// by the server.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Content is one item in a tools/call result; CallResult.Text uses only items
// whose Type is "text".
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// CallResult is the decoded result of tools/call: content items, optional
// structured content, and the server's isError flag.
type CallResult struct {
	Content           []Content       `json:"content,omitempty"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// Text joins the text content items with newlines, ignoring other content types.
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

// Transport is the JSON-RPC channel a Client uses: Call sends a request and
// waits for its response, Notify sends a notification, and Close releases it.
type Transport interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	Notify(ctx context.Context, method string, params any) error
	Close() error
}

// Client is an MCP client over stdio or streamable HTTP; it initializes lazily
// and caches the server info from the handshake.
type Client struct {
	cfg       Config
	transport Transport

	mu         sync.RWMutex
	serverInfo ServerInfo
	ready      bool
}

// New creates a client from cfg, selecting the stdio transport when Command is
// set and the HTTP transport when URL is set. It fails if neither or both are set.
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

// Name returns the configured client name, which prefixes registered tools.
func (c *Client) Name() string { return c.cfg.Name }

// ServerInfo returns the info reported by the server; it is zero until
// Initialize succeeds.
func (c *Client) ServerInfo() ServerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverInfo
}

// Initialize performs the initialize handshake and sends
// notifications/initialized, recording the server info. It is idempotent and is
// called lazily by ListTools and CallTool.
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

// ListTools initializes the client and reads tools/list, following nextCursor
// until the server stops paginating.
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

// CallTool initializes the client, sends tools/call, and decodes the result;
// empty args are sent as {}.
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

// Close closes the underlying transport, if any.
func (c *Client) Close() error {
	if c.transport == nil {
		return nil
	}
	return c.transport.Close()
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error renders the JSON-RPC code and message.
func (e *rpcError) Error() string {
	return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message)
}

func (e *rpcError) unwrap() error {
	return fmt.Errorf("%w: %w", e, core.ErrProvider)
}
