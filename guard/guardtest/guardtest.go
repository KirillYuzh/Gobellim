package guardtest

import (
	"context"

	"github.com/KirillYuzh/Gobellim/guard"
)

type allowAll struct{}

func (allowAll) Name() string { return "allow-all" }

func (allowAll) Detect(context.Context, string) ([]guard.Finding, error) { return nil, nil }

func AllowAll() guard.Detector { return allowAll{} }
