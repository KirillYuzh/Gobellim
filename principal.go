package kit

import (
	"context"

	"github.com/gobellim/kit/internal/core"
)

type Principal = core.Principal

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return core.WithPrincipal(ctx, p)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	return core.PrincipalFrom(ctx)
}
