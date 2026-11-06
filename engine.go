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
	// only on that replica. Set a shared 32-byte key together with a shared
	// ClaimStore; the key alone lets one approval execute once per replica.
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

// WithApprovalTTL sets how long a PendingAction stays executable. A zero value
// uses the default of 1 hour; a negative value is ErrInvalidConfig. An approval
// that sits unused for a day is a stale decision.
func WithApprovalTTL(d time.Duration) Option { return func(c *Config) { c.ApprovalTTL = d } }

// WithApprovalKey sets the HMAC key that authenticates a PendingAction. It must
// be 32 bytes. Use it together with WithClaimStore when an action escalated on
// one replica may be approved on another; a shared key without a shared claim
// store is ErrInvalidConfig.
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
	sealer        *Sealer
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
	if len(cfg.ApprovalKey) > 0 && cfg.Claims == nil {
		// A shared key moves a PendingAction between replicas, but the default
		// claim store is per-process: the same approval could then execute once
		// on every replica. The pair belongs together.
		return nil, fmt.Errorf("%w: WithApprovalKey needs WithClaimStore: a shared key without a shared claim store allows one approval to run on every replica", ErrInvalidConfig)
	}
	audit := cfg.Audit
	ttl := cfg.ApprovalTTL
	if ttl < 0 {
		// A missing option is a zero value, but a negative duration was written on
		// purpose. Defaulting it would hide a typo behind a working approval.
		return nil, fmt.Errorf("%w: approval TTL must not be negative", ErrInvalidConfig)
	}
	if ttl == 0 {
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
		// randomHex(16) is 32 bytes of ASCII, which is the length NewSealer
		// requires: a default key and an explicit one behave the same way.
		key = []byte(randomHex(16))
	}
	sealer, err := NewSealer(key)
	if err != nil {
		return nil, err
	}
	return &Engine{
		model:         cfg.Model,
		authz:         cfg.Authorizer,
		audit:         audit,
		limits:        limits,
		policyVersion: cfg.PolicyVersion,
		claims:        claims,
		approvalTTL:   ttl,
		sealer:        sealer,
		now:           now,
	}, nil
}

// Deps is a snapshot of the Engine's collaborators. An optional package such as
// flow uses it to share one authorizer, one audit sink, one claim store, one
// clock and one approval key with the Engine, instead of asking the application
// to configure a second set that can silently disagree.
//
// Two authorizers for one use case is a security gap rather than a preference:
// the Gate would check a caller against one ACL and the approval against
// another.
type Deps struct {
	Authorizer  Authorizer
	Audit       Sink
	Claims      ClaimStore
	Now         func() time.Time
	ApprovalTTL time.Duration
	Sealer      *Sealer
}

// Deps returns the Engine's collaborators.
func (e *Engine) Deps() Deps {
	return Deps{
		Authorizer:  e.authz,
		Audit:       e.audit,
		Claims:      e.claims,
		Now:         e.now,
		ApprovalTTL: e.approvalTTL,
		Sealer:      e.sealer,
	}
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
