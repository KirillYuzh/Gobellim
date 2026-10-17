package guardtest

import (
	"context"

	"github.com/KirillYuzh/Gobellim/guard"
)

type allowAll struct{}

func (allowAll) Name() string { return "allow-all" }

func (allowAll) Detect(context.Context, string) ([]guard.Finding, error) { return nil, nil }

// AllowAll returns a Detector that never reports findings; it exists for tests
// and is deliberately kept out of the public guard package.
func AllowAll() guard.Detector { return allowAll{} }
