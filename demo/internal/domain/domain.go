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

type KnowledgeDoc struct {
	ID     string
	Title  string
	Text   string
	ACL    []string
	Chunks int
}

type KnowledgeHit struct {
	DocID    string
	Title    string
	Text     string
	Position int
}

// Ticket is the typed answer of the triage use case. kit derives its JSON Schema
// from these fields, so the model is asked for exactly this shape.
type Ticket struct {
	Severity int    `json:"severity"`
	Summary  string `json:"summary"`
}

// AuditEvent is one recorded use of the bot. It lives in the demo's own store,
// not in kit: the library never owns your analytics.
type AuditEvent struct {
	Tenant string
	UserID string
	Role   Role
	Action string
}

// AuditStats is the aggregate the mock audit store reports.
type AuditStats struct {
	Events   int
	ByRole   map[string]int
	ByAction map[string]int
}

const Tenant = "bot"

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
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
