package gobellim

import (
	"context"
	"fmt"
	"time"
)

// Config holds the Engine's dependencies.
type Config struct {
	Model         Model
	Authorizer    Authorizer
	Audit         Sink
	Limits        Limits
	PolicyVersion string
	// Claims makes an approved action single-use. A nil value uses an
	// in-process store; a shared store is needed between replicas.
	Claims      ClaimStore
	ApprovalTTL time.Duration
	Now         func() time.Time
}

// Option configures an Engine.
type Option func(*Config)

// WithModel sets the model adapter. It is required.
func WithModel(m Model) Option { return func(c *Config) { c.Model = m } }

// WithAuthorizer sets the RBAC/ACL adapter. It is required.
func WithAuthorizer(a Authorizer) Option { return func(c *Config) { c.Authorizer = a } }

// WithAudit sets the audit sink.
func WithAudit(s Sink) Option { return func(c *Config) { c.Audit = s } }

// WithLimits overrides the default budgets.
func WithLimits(l Limits) Option { return func(c *Config) { c.Limits = l } }

// WithPolicyVersion stamps audit events with the active policy version.
func WithPolicyVersion(v string) Option { return func(c *Config) { c.PolicyVersion = v } }

// WithClaimStore makes approved actions single-use across replicas.
func WithClaimStore(s ClaimStore) Option { return func(c *Config) { c.Claims = s } }

// WithApprovalTTL sets how long a PendingAction stays executable. The default
// is 24 hours.
func WithApprovalTTL(d time.Duration) Option { return func(c *Config) { c.ApprovalTTL = d } }

// WithClock overrides the clock. It exists for tests.
func WithClock(fn func() time.Time) Option { return func(c *Config) { c.Now = fn } }

// Engine is the configured runtime: one model, one authorizer, one audit sink
// and the default budgets.
type Engine struct {
	model         Model
	authz         Authorizer
	audit         Sink
	limits        Limits
	policyVersion string
	claims        ClaimStore
	approvalTTL   time.Duration
	now           func() time.Time
}

const defaultApprovalTTL = 24 * time.Hour

// New validates the configuration and builds an Engine.
func New(opts ...Option) (*Engine, error) {
	var cfg Config
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.Model == nil {
		return nil, fmt.Errorf("%w: model is required", ErrInvalidConfig)
	}
	if cfg.Authorizer == nil {
		return nil, fmt.Errorf("%w: authorizer is required", ErrInvalidConfig)
	}
	limits := cfg.Limits.withDefaults()
	if err := limits.validate(); err != nil {
		return nil, err
	}
	audit := cfg.Audit
	if audit == nil {
		audit = nopSink{}
	}
	claims := cfg.Claims
	if claims == nil {
		claims = NewMemClaims()
	}
	ttl := cfg.ApprovalTTL
	if ttl <= 0 {
		ttl = defaultApprovalTTL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Engine{
		model:         cfg.Model,
		authz:         cfg.Authorizer,
		audit:         audit,
		limits:        limits,
		policyVersion: cfg.PolicyVersion,
		claims:        claims,
		approvalTTL:   ttl,
		now:           now,
	}, nil
}

// Limits returns the effective budgets.
func (e *Engine) Limits() Limits { return e.limits }

func (e *Engine) emit(ctx context.Context, ev Event) {
	ev.Time = e.now()
	ev.PolicyVersion = e.policyVersion
	e.audit.Emit(ctx, ev)
}
