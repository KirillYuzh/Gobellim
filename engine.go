package gobellim

import (
	"context"
	"fmt"
	"time"
)

// timeNow is a variable so tests can pin the clock.
var timeNow = time.Now

// Config holds the Engine's dependencies.
type Config struct {
	Model         Model
	Authorizer    Authorizer
	Audit         Sink
	Limits        Limits
	PolicyVersion string
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

// Engine is the configured runtime: one model, one authorizer, one audit sink
// and the default budgets.
type Engine struct {
	model         Model
	authz         Authorizer
	audit         Sink
	limits        Limits
	policyVersion string
}

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
	return &Engine{
		model:         cfg.Model,
		authz:         cfg.Authorizer,
		audit:         audit,
		limits:        limits,
		policyVersion: cfg.PolicyVersion,
	}, nil
}

// Limits returns the effective budgets.
func (e *Engine) Limits() Limits { return e.limits }

func (e *Engine) emit(ctx context.Context, ev Event) {
	ev.Time = timeNow()
	ev.PolicyVersion = e.policyVersion
	e.audit.Emit(ctx, ev)
}
