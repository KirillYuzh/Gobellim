package gobellim

import "errors"

var (
	// ErrInvalidConfig signals a missing or unusable configuration.
	ErrInvalidConfig = errors.New("gobellim: invalid configuration")
	// ErrUnauthorized signals a missing principal or a policy denial.
	ErrUnauthorized = errors.New("gobellim: unauthorized")
	// ErrInvalidArgs signals arguments that are not valid JSON or fail the schema.
	ErrInvalidArgs = errors.New("gobellim: invalid arguments")
	// ErrMaxSteps signals an exhausted model-call or tool-call budget.
	ErrMaxSteps = errors.New("gobellim: max steps exceeded")
	// ErrBudget signals an exceeded byte, time or context-item budget.
	ErrBudget = errors.New("gobellim: budget exceeded")
	// ErrUngrounded signals an argument that has no trusted origin.
	ErrUngrounded = errors.New("gobellim: ungrounded argument")
	// ErrUnsupportedType signals a schema type outside the supported subset.
	ErrUnsupportedType = errors.New("gobellim: unsupported schema type")
	// ErrApprovalInvalid is the umbrella error for a pending action that cannot
	// be executed. Every specific cause also matches this one.
	ErrApprovalInvalid = errors.New("gobellim: invalid approval")
	// ErrApprovalExpired signals an approval past its expiry.
	ErrApprovalExpired = errors.New("gobellim: approval expired")
	// ErrApprovalReplay signals an approval that was already used.
	ErrApprovalReplay = errors.New("gobellim: approval already used")
	// ErrApprovalDrift signals a pending action that changed since approval, or
	// a tool whose effect or identity changed under the same arguments.
	ErrApprovalDrift = errors.New("gobellim: approved action changed")
)
