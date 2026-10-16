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

func WithTraceMetadata(ctx context.Context, md map[string]any) context.Context {
	return core.WithSpanMetadata(ctx, md)
}

func TraceMetadataFrom(ctx context.Context) map[string]any {
	return core.SpanMetadataFrom(ctx)
}
