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
	// ApprovalKey signs a PendingAction. A nil value generates a random
	// per-process key, so an action escalated on one replica can be approved
	// only on that replica; set a shared 32-byte key to move approvals between
	// replicas.
	ApprovalKey []byte
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
// is 1 hour; an approval that sits unused for a day is a stale decision.
func WithApprovalTTL(d time.Duration) Option { return func(c *Config) { c.ApprovalTTL = d } }

// WithApprovalKey sets the HMAC key that authenticates a PendingAction. It must
// be 32 bytes. Use it when an action escalated on one replica may be approved
// on another.
func WithApprovalKey(key []byte) Option { return func(c *Config) { c.ApprovalKey = key } }

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
	approvalKey   []byte
	now           func() time.Time
}

const defaultApprovalTTL = time.Hour

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
	if cfg.Audit == nil {
		// A run without an audit trail cannot be explained afterwards, and a
		// forgotten option is not a decision to have no trail.
		return nil, fmt.Errorf("%w: audit sink is required", ErrInvalidConfig)
	}
	limits := cfg.Limits.withFallback(DefaultLimits())
	if err := limits.validate(); err != nil {
		return nil, err
	}
	audit := cfg.Audit
	ttl := cfg.ApprovalTTL
	if ttl <= 0 {
		ttl = defaultApprovalTTL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	claims := cfg.Claims
	if claims == nil {
		mem := NewMemClaims()
		mem.Now = now
		claims = mem
	}
	key := cfg.ApprovalKey
	if len(key) == 0 {
		key = []byte(randomHex(32))
	} else if len(key) != 32 {
		return nil, fmt.Errorf("%w: approval key must be 32 bytes", ErrInvalidConfig)
	}
	return &Engine{
		model:         cfg.Model,
		authz:         cfg.Authorizer,
		audit:         audit,
		limits:        limits,
		policyVersion: cfg.PolicyVersion,
		claims:        claims,
		approvalTTL:   ttl,
		approvalKey:   key,
		now:           now,
	}, nil
}

// Limits returns the effective budgets.
func (e *Engine) Limits() Limits { return e.limits }

// elapsed measures a step on the same clock that stamps events and checks
// expiry, so an injected clock cannot report a latency of several years.
func (e *Engine) elapsed(start time.Time) time.Duration { return e.now().Sub(start) }

func (e *Engine) emit(ctx context.Context, ev Event) {
	ev.Time = e.now()
	ev.PolicyVersion = e.policyVersion
	e.audit.Emit(ctx, ev)
}
