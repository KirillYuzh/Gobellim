package kit

import (
	"github.com/KirillYuzh/Gobellim/internal/call"
	"github.com/KirillYuzh/Gobellim/internal/core"
)

var (
	ErrInvalidConfig    = core.ErrInvalidConfig
	ErrUnauthorized     = core.ErrUnauthorized
	ErrGuardReject      = core.ErrGuardReject
	ErrGuardUnavailable = core.ErrGuardUnavailable
	ErrProvider         = core.ErrProvider
	ErrToolNotFound     = core.ErrToolNotFound
	ErrInvalidArgs      = core.ErrInvalidArgs
	ErrMaxSteps         = core.ErrMaxSteps
	ErrNotFound         = core.ErrNotFound
	ErrTool             = core.ErrTool
	ErrBudget           = core.ErrBudget
	ErrRateLimited      = core.ErrRateLimited
	ErrApprovalRequired = core.ErrApprovalRequired
)

type GuardRejectError = call.GuardRejectError
