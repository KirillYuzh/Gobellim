package kit

import (
	"github.com/gobellim/kit/internal/call"
	"github.com/gobellim/kit/internal/core"
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
