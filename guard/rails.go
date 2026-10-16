package guard

import (
	"fmt"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

type Rail string

const (
	RailInput      Rail = "input"
	RailOutput     Rail = "output"
	RailToolResult Rail = "tool_result"
)

func (r Rail) String() string {
	if r == "" {
		return "unspecified"
	}
	return string(r)
}

func Rails() []Rail {
	return []Rail{RailInput, RailOutput, RailToolResult}
}

func ValidRail(r Rail) bool {
	switch r {
	case RailInput, RailOutput, RailToolResult:
		return true
	default:
		return false
	}
}

func ParseRail(name string) (Rail, error) {
	r := Rail(name)
	if !ValidRail(r) {
		return "", fmt.Errorf("guard: unknown rail %q: %w", name, core.ErrInvalidConfig)
	}
	return r, nil
}
