package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/internal/core"
)

// ToolPrefix returns the kit tool name for an MCP tool: mcp:<server>:<tool>.
func ToolPrefix(server, tool string) string {
	return "mcp:" + server + ":" + tool
}

// Register lists the client's tools and returns one declaration per tool, named
// mcp:<server>:<tool>, using the server's input schema.
//
// The declarations are egress tools: the arguments leave the process for the
// MCP server. The egress allowlist is derived from the client's endpoint, so
// only that host is reachable. A stdio client has no endpoint host, so the
// caller must pass an egress policy of its own — there is no default that
// opens egress to every host.
//
// Extra opts are applied after the defaults. They may narrow the egress
// allowlist, for example with gobellim.Egress("api.example.com"), and they may
// add an approval requirement. They cannot downgrade the effect to read: a
// tool that calls a remote server always egresses.
//
// Register the declarations in a gobellim.Registry. The tool runs through the
// app's normal tool path, so RBAC on tool:<server>:<tool>, argument validation,
// the guard, approval, egress and the audit trail all apply.
func Register(ctx context.Context, c *Client, opts ...gobellim.Option) ([]gobellim.ToolDef, error) {
	if c == nil {
		return nil, fmt.Errorf("mcp: client is required: %w", core.ErrInvalidConfig)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	policy, err := egressPolicy(c)
	if err != nil {
		return nil, err
	}

	defs := make([]gobellim.ToolDef, 0, len(tools))
	for _, t := range tools {
		if t.Name == "" {
			return nil, fmt.Errorf("mcp: %s: server returned a tool with an empty name: %w", c.Name(), core.ErrInvalidConfig)
		}
		base := []gobellim.Option{gobellim.Desc(t.Description), policy}
		def := gobellim.RawTool(ToolPrefix(c.Name(), t.Name), t.InputSchema, callHandler(c, t.Name), append(base, opts...)...)
		if def.Err() != nil {
			return nil, fmt.Errorf("mcp: %s: compile tool %s: %w", c.Name(), t.Name, def.Err())
		}
		defs = append(defs, def)
	}
	return defs, nil
}

// egressPolicy returns the egress policy for the client's tools: the host of
// the MCP endpoint. A stdio client talks to a local process and has no
// endpoint host, so the caller has to name the hosts it allows.
func egressPolicy(c *Client) (gobellim.Option, error) {
	if c.cfg.URL == "" {
		return nil, fmt.Errorf("mcp: %s: a stdio client has no endpoint host: pass an egress policy, e.g. gobellim.Egress(\"example.com\"): %w",
			c.cfg.Name, core.ErrInvalidConfig)
	}
	u, err := url.Parse(c.cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: endpoint %q: %w: %w", c.cfg.Name, c.cfg.URL, err, core.ErrInvalidConfig)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("mcp: %s: endpoint %q has no host: %w", c.cfg.Name, c.cfg.URL, core.ErrInvalidConfig)
	}
	return gobellim.Egress(host), nil
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
