package guard

import (
	"fmt"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// Rail identifies where guard runs; it affects the decision table, never detection.
type Rail string

const (
	// RailInput is the rail for input before the model call.
	RailInput Rail = "input"
	// RailOutput is the rail for model output before it is returned.
	RailOutput Rail = "output"
	// RailToolResult is the rail for tool results before they re-enter the model.
	RailToolResult Rail = "tool_result"
)

// String returns the rail name, or "unspecified" for the empty value.
func (r Rail) String() string {
	if r == "" {
		return "unspecified"
	}
	return string(r)
}

// Rails returns the three valid rails in declaration order.
func Rails() []Rail {
	return []Rail{RailInput, RailOutput, RailToolResult}
}

// ValidRail reports whether r is one of the three declared rails.
func ValidRail(r Rail) bool {
	switch r {
	case RailInput, RailOutput, RailToolResult:
		return true
	default:
		return false
	}
}

// ParseRail converts name to a Rail, returning ErrInvalidConfig for unknown names.
func ParseRail(name string) (Rail, error) {
	r := Rail(name)
	if !ValidRail(r) {
		return "", fmt.Errorf("guard: unknown rail %q: %w", name, core.ErrInvalidConfig)
	}
	return r, nil
}
