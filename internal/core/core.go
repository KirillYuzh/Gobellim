package core

import (
	"context"
	"errors"
)

var (
	ErrInvalidConfig    = errors.New("kit: invalid configuration")
	ErrUnauthorized     = errors.New("kit: unauthorized")
	ErrGuardReject      = errors.New("kit: guard rejected")
	ErrGuardUnavailable = errors.New("kit: guard unavailable")
	ErrProvider         = errors.New("kit: provider error")
	ErrToolNotFound     = errors.New("kit: tool not found")
	ErrInvalidArgs      = errors.New("kit: invalid tool arguments")
	ErrMaxSteps         = errors.New("kit: max steps exceeded")
	ErrNotFound         = errors.New("kit: not found")
	ErrTool             = errors.New("kit: tool error")
	ErrBudget           = errors.New("kit: budget exceeded")
	ErrRateLimited      = errors.New("kit: rate limited")
	ErrApprovalRequired = errors.New("kit: approval required")
)

type Principal struct {
	UserID string
	Roles  []string
	Tenant string
}

func (p Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

func (p Principal) IsZero() bool {
	return p.UserID == "" && p.Tenant == "" && len(p.Roles) == 0
}

func (p Principal) Validate() error {
	if p.IsZero() {
		return ErrUnauthorized
	}
	if p.Tenant == "" {
		return errors.New("kit: principal tenant is required")
	}
	return nil
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

type spanMetadataKey struct{}

func WithSpanMetadata(ctx context.Context, md map[string]any) context.Context {
	if len(md) == 0 {
		return ctx
	}
	return context.WithValue(ctx, spanMetadataKey{}, md)
}

func SpanMetadataFrom(ctx context.Context) map[string]any {
	md, _ := ctx.Value(spanMetadataKey{}).(map[string]any)
	return md
}
