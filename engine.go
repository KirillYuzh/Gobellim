package gobellim

import (
	"context"
	"fmt"
	"reflect"
	"time"
)

// config holds the Engine's dependencies. It is unexported because every field
// has an option: a caller that builds one directly bypasses the validation in
// New, and a caller that needs a new knob needs an option that can validate it.
type config struct {
	model         Model
	authorizer    Authorizer
	audit         Sink
	limits        Limits
	policyVersion string
	// claims makes an approved action single-use. A nil value uses an
	// in-process store; a shared store is needed between replicas.
	claims      ClaimStore
	approvalTTL time.Duration
	// approvalKey signs a PendingAction. A nil value generates a random
	// per-process key, so an action escalated on one replica can be approved
	// only on that replica. Set a shared 32-byte key together with a shared
	// ClaimStore; the key alone lets one approval execute once per replica.
	approvalKey []byte
	now         func() time.Time
}

// Option configures an Engine.
type Option func(*config)

// WithModel sets the model adapter. It is required.
func WithModel(m Model) Option { return func(c *config) { c.model = m } }

// WithAuthorizer sets the RBAC/ACL adapter. It is required.
func WithAuthorizer(a Authorizer) Option { return func(c *config) { c.authorizer = a } }

// WithAudit sets the audit sink. It is required.
func WithAudit(s Sink) Option { return func(c *config) { c.audit = s } }

// WithLimits overrides the default budgets.
func WithLimits(l Limits) Option { return func(c *config) { c.limits = l } }

// WithPolicyVersion stamps audit events with the active policy version.
func WithPolicyVersion(v string) Option { return func(c *config) { c.policyVersion = v } }

// WithClaimStore makes approved actions single-use across replicas.
func WithClaimStore(s ClaimStore) Option { return func(c *config) { c.claims = s } }

// WithApprovalTTL sets how long a PendingAction stays executable. A zero value
// uses the default of 1 hour; a negative value is ErrInvalidConfig. An approval
// that sits unused for a day is a stale decision.
func WithApprovalTTL(d time.Duration) Option { return func(c *config) { c.approvalTTL = d } }

// WithApprovalKey sets the HMAC key that authenticates a PendingAction. It must
// be 32 bytes. Use it together with WithClaimStore when an action escalated on
// one replica may be approved on another; a shared key without a shared claim
// store is ErrInvalidConfig.
func WithApprovalKey(key []byte) Option {
	return func(c *config) { c.approvalKey = key }
}

// WithClock overrides the clock. It exists for tests.
func WithClock(fn func() time.Time) Option { return func(c *config) { c.now = fn } }

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

// isNil reports whether v is nil or a nil pointer, map, slice, channel or
// function wrapped in an interface. A dependency declared as
// `var m *MyModel` and passed through WithModel is not nil as an interface, and
// the difference shows up as a panic on the first call instead of a
// configuration error at startup.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

// New validates the configuration and builds an Engine.
func New(opts ...Option) (*Engine, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	if isNil(cfg.model) {
		return nil, fmt.Errorf("%w: model is required", ErrInvalidConfig)
	}
	if isNil(cfg.authorizer) {
		return nil, fmt.Errorf("%w: authorizer is required", ErrInvalidConfig)
	}
	if isNil(cfg.audit) {
		// A run without an audit trail cannot be explained afterwards, and a
		// forgotten option is not a decision to have no trail.
		return nil, fmt.Errorf("%w: audit sink is required", ErrInvalidConfig)
	}
	limits := cfg.limits.withFallback(DefaultLimits())
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if len(cfg.approvalKey) > 0 && cfg.claims == nil {
		// A shared key moves a PendingAction between replicas, but the default
		// claim store is per-process: the same approval could then execute once
		// on every replica. The pair belongs together.
		return nil, fmt.Errorf("%w: WithApprovalKey needs WithClaimStore: a shared key without a shared claim store allows one approval to run on every replica", ErrInvalidConfig)
	}
	audit := cfg.audit
	ttl := cfg.approvalTTL
	if ttl < 0 {
		// A missing option is a zero value, but a negative duration was written on
		// purpose. Defaulting it would hide a typo behind a working approval.
		return nil, fmt.Errorf("%w: approval TTL must not be negative", ErrInvalidConfig)
	}
	if ttl == 0 {
		ttl = defaultApprovalTTL
	}
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	claims := cfg.claims
	if claims == nil {
		mem := NewMemClaims()
		mem.Now = now
		claims = mem
	}
	key := cfg.approvalKey
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
		model:         cfg.model,
		authz:         cfg.authorizer,
		audit:         audit,
		limits:        limits,
		policyVersion: cfg.policyVersion,
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
