package kit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gobellim/kit/internal/core"
	"github.com/gobellim/kit/internal/id"
	"github.com/gobellim/kit/model"
)

// CallTool runs one registered tool by name through the same path Chat uses:
// the caller's permission, argument validation and size limit, approval, egress
// policy, the named hooks, the tool_result guard and the output view. The
// principal comes from ctx. It exists so a caller that is not the model — an MCP
// server exposing kit tools — cannot skip the call path. The result is what the
// model would see: a tool failure comes back as an error result, as in Chat.
func (a *App) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	pr, err := principal(ctx)
	if err != nil {
		return "", err
	}
	if err := a.rateLimit(pr); err != nil {
		return "", err
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if len(args) > a.cfg.ArgsMaxBytes {
		return "", fmt.Errorf("kit: tool %s: arguments exceed %d bytes: %w", name, a.cfg.ArgsMaxBytes, core.ErrInvalidArgs)
	}

	a.mu.RLock()
	def := a.tools[name]
	a.mu.RUnlock()
	if def == nil {
		return "", fmt.Errorf("kit: tool %q: %w", name, core.ErrToolNotFound)
	}

	t := &turn{
		traceID:      id.New(),
		principal:    pr,
		callsPerTool: map[string]int{},
		invalidTries: map[string]int{},
		approved:     map[string]bool{},
	}
	return a.execTool(ctx, model.ToolCall{Name: name, Args: string(args)}, t)
}
