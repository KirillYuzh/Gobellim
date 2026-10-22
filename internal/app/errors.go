package app

import (
	"github.com/KirillYuzh/Gobellim/internal/call"
	"github.com/KirillYuzh/Gobellim/internal/core"
)

var (
	// ErrInvalidConfig signals a missing or unusable configuration, such as an
	// absent Config.LLM, Config.Policy, or required dependency.
	ErrInvalidConfig = core.ErrInvalidConfig
	// ErrUnauthorized signals a missing principal, a policy denial, or a blocked
	// egress attempt.
	ErrUnauthorized = core.ErrUnauthorized
	// ErrGuardReject signals that the guard rejected text on the input rail.
	ErrGuardReject = core.ErrGuardReject
	// ErrGuardUnavailable signals that a required guard layer was unavailable
	// while fail-closed behaviour was in effect.
	ErrGuardUnavailable = core.ErrGuardUnavailable
	// ErrProvider wraps failures from the LLM, embedder, or Elasticsearch, and an
	// answer that Generate could not decode.
	ErrProvider = core.ErrProvider
	// ErrToolNotFound is returned by CallTool for an unregistered tool name.
	ErrToolNotFound = core.ErrToolNotFound
	// ErrInvalidArgs signals arguments that are not JSON or fail the schema, an
	// empty search query, or a chat request with no user/assistant messages.
	ErrInvalidArgs = core.ErrInvalidArgs
	// ErrMaxSteps signals that the Chat or ChatStream loop exhausted
	// Config.MaxSteps.
	ErrMaxSteps = core.ErrMaxSteps
	// ErrNotFound marks a missing object; the facade operations themselves do not
	// return it.
	ErrNotFound = core.ErrNotFound
	// ErrTool marks a non-fatal failure inside a tool, which the facade returns to
	// the model as an error result instead of aborting the turn.
	ErrTool = core.ErrTool
	// ErrBudget signals that a turn exceeded Config.MaxTotalTokens.
	ErrBudget = core.ErrBudget
	// ErrRateLimited signals that the tenant's Config.RateLimit bucket is
	// exhausted.
	ErrRateLimited = core.ErrRateLimited
	// ErrApprovalRequired signals missing, denied, or already-consumed approval
	// for a RequireApproval tool.
	ErrApprovalRequired = core.ErrApprovalRequired
)

// GuardRejectError is the typed guard rejection available through errors.As; it
// carries the rail, tripwire, and guard version.
type GuardRejectError = call.GuardRejectError
