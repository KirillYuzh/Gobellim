package kit

import (
	"context"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// Principal identifies the caller for RBAC and data scoping; UserID is required
// by memory operations and Tenant is required by ingest, search, and delete.
type Principal = core.Principal

// WithPrincipal returns a context carrying p, which operations read back with
// PrincipalFrom.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return core.WithPrincipal(ctx, p)
}

// PrincipalFrom returns the principal stored by WithPrincipal and whether one was
// present.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	return core.PrincipalFrom(ctx)
}

// WithTraceMetadata attaches request-scoped metadata that is copied into every
// observation produced from this context; call-level values override same-named
// keys.
func WithTraceMetadata(ctx context.Context, md map[string]any) context.Context {
	return core.WithSpanMetadata(ctx, md)
}

// TraceMetadataFrom returns the metadata attached by WithTraceMetadata, or nil.
func TraceMetadataFrom(ctx context.Context) map[string]any {
	return core.SpanMetadataFrom(ctx)
}
