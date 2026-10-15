package domain

import (
	"context"
	"errors"
	"time"
)

type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

type User struct {
	ID    string
	Login string
	Role  Role
}

type Principal struct {
	UserID string
	Login  string
	Role   Role
	Tenant string
}

type Session struct {
	Token     string
	Principal Principal
	ExpiresAt time.Time
}

type Answer struct {
	Content string
}

type Weather struct {
	City         string
	TemperatureC float64
	WindKmh      float64
	Description  string
}

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrInvalidInput = errors.New("invalid input")
	ErrNotFound     = errors.New("not found")
	ErrUnavailable  = errors.New("upstream unavailable")
)

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
