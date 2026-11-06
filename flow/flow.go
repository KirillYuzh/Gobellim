package flow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
)

var (
	// ErrExpired reports an approval whose lifetime is over.
	ErrExpired = errors.New("flow: approval expired")
	// ErrReplay reports an approval nonce that was already used.
	ErrReplay = errors.New("flow: approval already used")
	// ErrFlowDrift reports a card or step that no longer matches the flow.
	ErrFlowDrift = errors.New("flow: flow or step changed")
	// ErrUnauthorized reports a missing principal or a policy denial.
	ErrUnauthorized = errors.New("flow: unauthorized")
	// ErrDecision reports a Decision that is neither Approve nor Reject.
	ErrDecision = errors.New("flow: unknown decision")
)

// Status is the explicit outcome of a flow run.
type Status uint8

const (
	// Running is the zero value: the flow did not reach a terminal outcome.
	Running Status = iota
	// Suspended means an approval step stopped the flow.
	Suspended
	// Done means every step completed.
	Done
	// Rejected means an approver rejected the approval.
	Rejected
	// Expired means the approval window closed.
	Expired
	// Failed means a step returned an error.
	Failed
)

// String returns the tag name of the status.
func (s Status) String() string {
	switch s {
	case Running:
		return "running"
	case Suspended:
		return "suspended"
	case Done:
		return "done"
	case Rejected:
		return "rejected"
	case Expired:
		return "expired"
	case Failed:
		return "failed"
	default:
		return "unknown"
	}
}

// Decision is an approver's answer to an approval card. The zero value is not an
// approval.
type Decision uint8

const (
	// DecisionUnknown is the zero value. It never approves anything.
	DecisionUnknown Decision = iota
	// Approve accepts the action.
	Approve
	// Reject refuses the action. A rejection is final for this nonce.
	Reject
)

// Suspension describes the approval a flow is waiting for. The application
// stores Nonce, Card and State server-side and returns only an opaque id to the
// client. There is no sealed bearer token.
type Suspension struct {
	Nonce     string
	Card      gobellim.ApprovalCard
	ExpiresAt time.Time
	Requester gobellim.Principal
}

// Result is the outcome of Run or Resume. An error is returned separately, not
// hidden in Result, so a caller has one place to check.
type Result[S any] struct {
	Status     Status
	State      S
	Suspension *Suspension
}

type config struct {
	version       int
	policyVersion string
	authz         gobellim.Authorizer
	audit         gobellim.Sink
	store         gobellim.ClaimStore
	ttl           time.Duration
	maxWallTime   time.Duration
	now           func() time.Time
}

// Option configures a Flow.
type Option func(*config)

// Version sets the workflow version. A token from another version is rejected
// by ErrFlowDrift.
func Version(v int) Option { return func(c *config) { c.version = v } }

// WithPolicyVersion stamps audit events with the active policy version.
func WithPolicyVersion(v string) Option { return func(c *config) { c.policyVersion = v } }

// WithAuthorizer sets the RBAC/ACL adapter. It is required.
func WithAuthorizer(a gobellim.Authorizer) Option { return func(c *config) { c.authz = a } }

// WithAudit sets the audit sink. It is required.
func WithAudit(s gobellim.Sink) Option { return func(c *config) { c.audit = s } }

// WithStore sets the claim store that makes a resume single-use. The default is
// a bounded in-process store; use store/sql for cross-replica deployments.
func WithStore(s gobellim.ClaimStore) Option { return func(c *config) { c.store = s } }

// WithTTL sets the default approval lifetime. The default is 1 hour.
func WithTTL(d time.Duration) Option { return func(c *config) { c.ttl = d } }

// WithMaxWallTime bounds one Run or Resume call.
func WithMaxWallTime(d time.Duration) Option { return func(c *config) { c.maxWallTime = d } }

// WithClock overrides the clock. It exists for tests.
func WithClock(fn func() time.Time) Option { return func(c *config) { c.now = fn } }

type step[S any] struct {
	id      string
	run     func(ctx context.Context, p gobellim.Principal, s *S) error
	approve *approve[S]
}

type approve[S any] struct {
	when             func(ctx context.Context, p gobellim.Principal, s S) (bool, error)
	permission       string
	actionPermission string
	fourEyes         bool
	ttl              time.Duration
	card             func(ctx context.Context, p gobellim.Principal, s S) (gobellim.ApprovalCard, error)
	target           func(ctx context.Context, p gobellim.Principal, s S) (gobellim.Resource, error)
}

// Flow is a linear, typed workflow. Steps run in order. An approval step stops
// the flow; the application stores the Suspension and the state, then calls
// Resume after an approver decides.
type Flow[S any] struct {
	name  string
	cfg   config
	steps []*step[S]
	ids   map[string]struct{}
	err   error
}

// New starts a flow. Configuration errors are returned immediately.
func New[S any](name string, opts ...Option) (*Flow[S], error) {
	cfg := config{
		version:     1,
		ttl:         time.Hour,
		maxWallTime: 60 * time.Second,
		now:         time.Now,
	}
	for _, o := range opts {
		o(&cfg)
	}
	switch {
	case name == "":
		return nil, fmt.Errorf("%w: flow name is required", gobellim.ErrInvalidConfig)
	case cfg.authz == nil:
		return nil, fmt.Errorf("%w: authorizer is required", gobellim.ErrInvalidConfig)
	case cfg.audit == nil:
		return nil, fmt.Errorf("%w: audit sink is required", gobellim.ErrInvalidConfig)
	case cfg.version < 1:
		return nil, fmt.Errorf("%w: flow version must be positive", gobellim.ErrInvalidConfig)
	case cfg.ttl <= 0:
		return nil, fmt.Errorf("%w: TTL must be positive", gobellim.ErrInvalidConfig)
	case cfg.maxWallTime <= 0:
		return nil, fmt.Errorf("%w: max wall time must be positive", gobellim.ErrInvalidConfig)
	}
	if cfg.store == nil {
		store := NewMemStore()
		store.Now = cfg.now
		cfg.store = store
	}
	return &Flow[S]{name: name, cfg: cfg, ids: make(map[string]struct{})}, nil
}

func (f *Flow[S]) fail(err error) {
	if f.err == nil {
		f.err = err
	}
}

func (f *Flow[S]) addID(id string) bool {
	if _, dup := f.ids[id]; dup {
		f.fail(fmt.Errorf("%w: duplicate step id %q", gobellim.ErrInvalidConfig, id))
		return false
	}
	f.ids[id] = struct{}{}
	return true
}

// Step adds a deterministic step. A step must be idempotent up to the next
// approval boundary.
func (f *Flow[S]) Step(id string, fn func(ctx context.Context, p gobellim.Principal, s *S) error) *Flow[S] {
	if f.err != nil {
		return f
	}
	if id == "" || fn == nil {
		f.fail(fmt.Errorf("%w: step id and function are required", gobellim.ErrInvalidConfig))
		return f
	}
	if !f.addID(id) {
		return f
	}
	f.steps = append(f.steps, &step[S]{id: id, run: fn})
	return f
}

// ApproveConfig configures an approval boundary. Card is required.
type ApproveConfig[S any] struct {
	// When skips the approval when it returns false.
	When func(ctx context.Context, p gobellim.Principal, s S) (bool, error)
	// Permission is the approver's permission. It defaults to
	// "approve:<flow>.<step>".
	Permission string
	// ActionPermission is the permission re-checked for the requester on resume.
	ActionPermission string
	// FourEyes requires the approver to differ from the requester.
	FourEyes bool
	// TTL overrides the flow's approval lifetime for this step.
	TTL time.Duration
	// Card builds the approval card from the workflow state. It must be
	// deterministic: Resume rebuilds it and compares the action digest.
	Card func(ctx context.Context, p gobellim.Principal, s S) (gobellim.ApprovalCard, error)
	// Target binds the approved action to an object and tenant.
	Target func(ctx context.Context, p gobellim.Principal, s S) (gobellim.Resource, error)
}

func noTarget[S any](context.Context, gobellim.Principal, S) (gobellim.Resource, error) {
	return gobellim.Resource{}, nil
}

// Approve adds an approval boundary. It does not execute anything: the next
// step runs only after a checked approval.
func (f *Flow[S]) Approve(id string, cfg ApproveConfig[S]) *Flow[S] {
	if f.err != nil {
		return f
	}
	if id == "" {
		f.fail(fmt.Errorf("%w: approve id is required", gobellim.ErrInvalidConfig))
		return f
	}
	if cfg.Card == nil {
		f.fail(fmt.Errorf("%w: approve %s: Card is required", gobellim.ErrInvalidConfig, id))
		return f
	}
	if !f.addID(id) {
		return f
	}
	permission := cfg.Permission
	if permission == "" {
		permission = "approve:" + f.name + "." + id
	}
	a := &approve[S]{
		when:             cfg.When,
		permission:       permission,
		actionPermission: cfg.ActionPermission,
		fourEyes:         cfg.FourEyes,
		ttl:              cfg.TTL,
		card:             cfg.Card,
		target:           noTarget[S],
	}
	if cfg.Target != nil {
		a.target = cfg.Target
	}
	f.steps = append(f.steps, &step[S]{id: id, approve: a})
	return f
}

// Run executes from the first step. It returns Suspended at an approval
// boundary and Done when every step completed.
func (f *Flow[S]) Run(ctx context.Context, p gobellim.Principal, s S) (Result[S], error) {
	if f.err != nil {
		return Result[S]{Status: Failed, State: s}, f.err
	}
	if err := p.Validate(); err != nil {
		return Result[S]{Status: Failed, State: s}, err
	}
	ctx, cancel := context.WithTimeout(ctx, f.cfg.maxWallTime)
	defer cancel()
	return f.runFrom(ctx, p, s, 0)
}

func (f *Flow[S]) runFrom(ctx context.Context, p gobellim.Principal, s S, from int) (Result[S], error) {
	for i := from; i < len(f.steps); i++ {
		st := f.steps[i]

		if st.approve == nil {
			if err := st.run(ctx, p, &s); err != nil {
				f.emit(ctx, gobellim.Event{
					Kind: gobellim.EventWorkflowFailed, Flow: f.name, Step: st.id,
					PrincipalID: p.UserID, Tenant: p.Tenant, Error: "step-failed",
				})
				return Result[S]{Status: Failed, State: s}, err
			}
			continue
		}

		if st.approve.when != nil {
			ok, err := st.approve.when(ctx, p, s)
			if err != nil {
				f.emit(ctx, gobellim.Event{
					Kind: gobellim.EventWorkflowFailed, Flow: f.name, Step: st.id,
					PrincipalID: p.UserID, Tenant: p.Tenant, Error: "when-failed",
				})
				return Result[S]{Status: Failed, State: s}, err
			}
			if !ok {
				continue
			}
		}

		card, err := st.approve.card(ctx, p, s)
		if err != nil {
			f.emit(ctx, gobellim.Event{
				Kind: gobellim.EventWorkflowFailed, Flow: f.name, Step: st.id,
				PrincipalID: p.UserID, Tenant: p.Tenant, Error: "card-failed",
			})
			return Result[S]{Status: Failed, State: s}, err
		}
		card.RunID = newID("r_")
		card.Flow = f.name
		card.Step = st.id
		card.RequiredPermission = st.approve.permission
		card.FourEyes = st.approve.fourEyes
		card.ExpiresAt = f.cfg.now().Add(f.ttlFor(st.approve))
		if card.Digest == "" {
			card.Digest = card.ActionDigest()
		}
		suspension := &Suspension{
			Nonce:     newID("n_"),
			Card:      card,
			ExpiresAt: card.ExpiresAt,
			Requester: p,
		}

		f.emit(ctx, gobellim.Event{
			Kind: gobellim.EventWorkflowSuspended, RunID: card.RunID, Flow: f.name, Step: st.id,
			PrincipalID: p.UserID, Tenant: p.Tenant, Decision: gobellim.Escalate,
		})
		f.emit(ctx, gobellim.Event{
			Kind: gobellim.EventApprovalRequested, RunID: card.RunID, Flow: f.name, Step: st.id,
			PrincipalID: p.UserID, Tenant: p.Tenant, Decision: gobellim.Escalate,
		})
		return Result[S]{Status: Suspended, State: s, Suspension: suspension}, nil
	}
	return Result[S]{Status: Done, State: s}, nil
}

// Resume checks an approval and continues from the step after the approval
// boundary. The application passes the requester, the approver, the nonce and
// the card it stored from Run, plus the state it stored. Nothing here is a
// bearer token: the approval endpoint must authenticate the approver and load
// the suspension server-side.
func (f *Flow[S]) Resume(
	ctx context.Context,
	approver gobellim.Principal,
	susp Suspension,
	state S,
	d Decision,
) (Result[S], error) {
	var zero Result[S]
	if f.err != nil {
		return zero, f.err
	}
	requester := susp.Requester
	card := susp.Card
	nonce := susp.Nonce
	if err := requester.Validate(); err != nil {
		return zero, err
	}
	if err := approver.Validate(); err != nil {
		return zero, err
	}
	if nonce == "" {
		return zero, fmt.Errorf("%w: nonce is required", ErrFlowDrift)
	}
	if requester.Tenant != approver.Tenant {
		return zero, fmt.Errorf("%w: approver tenant does not match requester", ErrUnauthorized)
	}
	ctx, cancel := context.WithTimeout(ctx, f.cfg.maxWallTime)
	defer cancel()

	if card.Flow != f.name {
		return zero, fmt.Errorf("%w: card flow is %q, want %q", ErrFlowDrift, card.Flow, f.name)
	}
	if card.ExpiresAt.IsZero() {
		return zero, fmt.Errorf("%w: card has no expiry", ErrFlowDrift)
	}
	if f.cfg.now().After(card.ExpiresAt) {
		f.emit(ctx, gobellim.Event{
			Kind: gobellim.EventWorkflowExpired, RunID: card.RunID, Flow: f.name, Step: card.Step,
			PrincipalID: approver.UserID, Tenant: approver.Tenant,
		})
		return Result[S]{Status: Expired, State: state}, fmt.Errorf("%w: expired at %s", ErrExpired, card.ExpiresAt.Format(time.RFC3339))
	}

	idx, a, err := f.approvalAt(card.Step)
	if err != nil {
		return zero, err
	}
	if card.RequiredPermission != a.permission || card.FourEyes != a.fourEyes {
		return zero, fmt.Errorf("%w: approval policy changed for step %q", ErrFlowDrift, card.Step)
	}

	target, err := a.target(ctx, requester, state)
	if err != nil {
		return Result[S]{Status: Failed, State: state}, err
	}
	// A target bound to another tenant would let the approver authorize a
	// foreign object. The Gate makes this check for Tool; flow repeats it
	// because Target here is application code, not a Tool declaration.
	if target.TenantID == "" {
		target.TenantID = requester.Tenant
	}
	if target.TenantID != requester.Tenant {
		return Result[S]{Status: Failed, State: state},
			fmt.Errorf("%w: target is bound to another tenant", ErrUnauthorized)
	}
	ok, err := f.cfg.authz.Allowed(ctx, approver, a.permission, target)
	if err != nil || !ok {
		f.emit(ctx, gobellim.Event{
			Kind: gobellim.EventApprovalDenied, RunID: card.RunID, Flow: f.name, Step: card.Step,
			PrincipalID: approver.UserID, Tenant: approver.Tenant, Error: "approver-permission",
		})
		return Result[S]{Status: Failed, State: state},
			fmt.Errorf("%w: approver lacks %s", ErrUnauthorized, a.permission)
	}

	switch d {
	case Reject:
		claimed, err := f.cfg.store.Claim(ctx, nonce, card.ExpiresAt)
		if err != nil {
			return Result[S]{Status: Failed, State: state}, fmt.Errorf("flow: store claim: %w", err)
		}
		if !claimed {
			return Result[S]{Status: Failed, State: state}, ErrReplay
		}
		f.emit(ctx, gobellim.Event{
			Kind: gobellim.EventApprovalDenied, RunID: card.RunID, Flow: f.name, Step: card.Step,
			PrincipalID: approver.UserID, Tenant: approver.Tenant,
		})
		return Result[S]{Status: Rejected, State: state}, nil
	case Approve:
	default:
		return Result[S]{Status: Failed, State: state}, fmt.Errorf("%w: %d", ErrDecision, d)
	}

	if a.fourEyes && approver.UserID == requester.UserID {
		return Result[S]{Status: Failed, State: state},
			fmt.Errorf("%w: four-eyes requires a different approver", ErrUnauthorized)
	}

	if a.actionPermission != "" {
		ok, err := f.cfg.authz.Allowed(ctx, requester, a.actionPermission, target)
		if err != nil || !ok {
			f.emit(ctx, gobellim.Event{
				Kind: gobellim.EventApprovalDenied, RunID: card.RunID, Flow: f.name, Step: card.Step,
				PrincipalID: approver.UserID, Tenant: approver.Tenant, Error: "action-permission",
			})
			return Result[S]{Status: Failed, State: state},
				fmt.Errorf("%w: requester lacks %s", ErrUnauthorized, a.actionPermission)
		}
	}

	// Rebuild the card from the stored state and compare the action digest.
	// This catches a non-deterministic builder or a mutated state.
	rebuilt, err := a.card(ctx, requester, state)
	if err != nil {
		return Result[S]{Status: Failed, State: state}, err
	}
	if rebuilt.ActionDigest() != card.ActionDigest() {
		return Result[S]{Status: Failed, State: state},
			fmt.Errorf("%w: pending action changed", ErrFlowDrift)
	}

	// Claim before the remaining steps: a crash loses the approval instead of
	// running the post-approval steps twice.
	claimed, err := f.cfg.store.Claim(ctx, nonce, card.ExpiresAt)
	if err != nil {
		return Result[S]{Status: Failed, State: state}, fmt.Errorf("flow: store claim: %w", err)
	}
	if !claimed {
		return Result[S]{Status: Failed, State: state}, ErrReplay
	}

	f.emit(ctx, gobellim.Event{
		Kind: gobellim.EventApprovalGranted, RunID: card.RunID, Flow: f.name, Step: card.Step,
		PrincipalID: approver.UserID, Tenant: approver.Tenant,
	})
	f.emit(ctx, gobellim.Event{
		Kind: gobellim.EventWorkflowResumed, RunID: card.RunID, Flow: f.name, Step: card.Step,
		PrincipalID: requester.UserID, Tenant: requester.Tenant,
	})

	// The remaining steps run as the requester; ApproverFrom carries the
	// authenticated approver to the step that executes the action.
	return f.runFrom(withApprover(ctx, approver), requester, state, idx+1)
}

func (f *Flow[S]) approvalAt(id string) (int, *approve[S], error) {
	for i, st := range f.steps {
		if st.id == id && st.approve != nil {
			return i, st.approve, nil
		}
	}
	return 0, nil, fmt.Errorf("%w: step %q is not an approval boundary", ErrFlowDrift, id)
}

// approverKey carries the authenticated approver from Resume to the steps that
// run after the approval. It is a request-scoped fact, not configuration.
type approverKey struct{}

func withApprover(ctx context.Context, p gobellim.Principal) context.Context {
	return context.WithValue(ctx, approverKey{}, p)
}

// ApproverFrom returns the principal that approved the resume in flight. It is
// absent during Run and during the steps before an approval boundary.
func ApproverFrom(ctx context.Context) (gobellim.Principal, bool) {
	p, ok := ctx.Value(approverKey{}).(gobellim.Principal)
	return p, ok
}

func (f *Flow[S]) ttlFor(a *approve[S]) time.Duration {
	if a.ttl > 0 {
		return a.ttl
	}
	return f.cfg.ttl
}

func (f *Flow[S]) emit(ctx context.Context, e gobellim.Event) {
	e.Time = f.cfg.now()
	e.PolicyVersion = f.cfg.policyVersion
	f.cfg.audit.Emit(ctx, e)
}

func newID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// Err reports the first configuration error recorded by Step or Approve. Run and
// Resume return it as well; the accessor lets a caller fail at startup instead
// of on the first request.
func (f *Flow[S]) Err() error { return f.err }
