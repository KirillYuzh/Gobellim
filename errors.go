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
)
