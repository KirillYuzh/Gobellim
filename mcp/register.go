package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	kit "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/internal/core"
)

// ToolPrefix returns the kit tool name for an MCP tool: mcp:<server>:<tool>.
func ToolPrefix(server, tool string) string {
	return "mcp:" + server + ":" + tool
}

// Register lists the client's tools and registers each with app as
// mcp:<server>:<tool>, using the server's input schema and the egress effect.
// Extra opts are applied after the defaults and may override them.
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
			kit.EgressAny(),
		}
		def, err := kit.RawTool(app, ToolPrefix(c.Name(), t.Name), t.InputSchema, callHandler(c, t.Name), append(base, opts...)...)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	return defs, nil
}

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
