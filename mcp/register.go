package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gobellim/kit"
	"github.com/gobellim/kit/internal/core"
)

// ToolPrefix is the namespace kit tools from an MCP server live under. A tool
// called "read_file" on a server called "fs" becomes "mcp:fs:read_file", and its
// permission is therefore "tool:mcp:fs:read_file".
func ToolPrefix(server, tool string) string {
	return "mcp:" + server + ":" + tool
}

// Register lists the server's tools and registers each one with the app. Every
// tool gets the Egress effect (an MCP server is an outbound dependency, so the
// Rule of Two and host policy still apply) and the caller-supplied options are
// applied on top, so the caller can replace the description, require approval
// or narrow the effect.
//
// The input schema is taken from the server verbatim; a schema the JSON Schema
// compiler rejects fails registration with ErrInvalidConfig rather than being
// silently dropped.
func Register(ctx context.Context, app *kit.App, c *Client, opts ...kit.ToolOption) ([]*kit.ToolDef, error) {
	if app == nil {
		return nil, fmt.Errorf("mcp: app is required: %w", core.ErrInvalidConfig)
	}
	if c == nil {
		return nil, fmt.Errorf("mcp: client is required: %w", core.ErrInvalidConfig)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}

	defs := make([]*kit.ToolDef, 0, len(tools))
	for _, t := range tools {
		if t.Name == "" {
			return nil, fmt.Errorf("mcp: %s: server returned a tool with an empty name: %w", c.Name(), core.ErrInvalidConfig)
		}
		base := []kit.ToolOption{
			kit.Desc(t.Description),
			kit.Effects(kit.EffectEgress),
		}
		def, err := kit.RawTool(app, ToolPrefix(c.Name(), t.Name), t.InputSchema, callHandler(c, t.Name), append(base, opts...)...)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	return defs, nil
}

// callHandler adapts one MCP tool to a kit tool handler. A server-reported
// error becomes a Go error, which kit hands back to the model as data (I-11)
// instead of failing the turn.
func callHandler(c *Client, tool string) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		res, err := c.CallTool(ctx, tool, args)
		if err != nil {
			return "", err
		}
		if res.IsError {
			return "", fmt.Errorf("mcp: %s: tool %s: %s: %w", c.Name(), tool, res.Text(), core.ErrTool)
		}
		if len(res.StructuredContent) > 0 {
			return string(res.StructuredContent), nil
		}
		return res.Text(), nil
	}
}
