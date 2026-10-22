package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/id"
	"github.com/KirillYuzh/Gobellim/model"
)

// CallTool invokes one registered tool through the same path as Chat: RBAC,
// argument size and schema validation, approval, egress policy, the middleware
// chain, the tool_result guard, the output schema, and View. It requires a
// principal in the context, returns ErrToolNotFound for an unknown name and
// ErrInvalidArgs for oversized or invalid arguments, and returns non-fatal tool
// failures as error results.
func (a *App) CallTool(ctx context.Context, name string, args json.RawMessage) (result string, err error) {
	ao := a.beginAudit(ctx, "tool")
	defer func() { ao.end(err) }()
	ao.field("tool", name)
	pr, err := principal(ctx)
	if err != nil {
		return "", err
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if len(args) > a.cfg.ArgsMaxBytes {
		return "", fmt.Errorf("gobellim: tool %s: arguments exceed %d bytes: %w", name, a.cfg.ArgsMaxBytes, core.ErrInvalidArgs)
	}

	def := a.tools.Get(name)
	if def == nil {
		return "", fmt.Errorf("gobellim: tool %q: %w", name, core.ErrToolNotFound)
	}

	t := &turn{
		traceID:      id.New(),
		principal:    pr,
		callsPerTool: map[string]int{},
		approved:     map[string]bool{},
	}
	ao.trace(t.traceID)
	// Spend the tenant's rate-limit budget only on a call policy allows; a
	// denial keeps its security-event logging in execTool (BR-107).
	if a.policy.Allows(pr, def.Permission()) {
		if err := a.rateLimit(pr); err != nil {
			return "", err
		}
	}
	return a.execTool(ctx, model.ToolCall{Name: name, Args: string(args)}, t)
}
