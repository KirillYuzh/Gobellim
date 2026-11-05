// Package flow is the sequential, code-driven workflow with optional human
// approval. It never resumes a Go stack: it suspends at a step boundary, seals
// the minimal state into a token, and continues from the next step after a
// checked approval.
package flow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
)

var (
	// ErrContinuation signals a malformed, tampered or foreign token.
	ErrContinuation = errors.New("flow: invalid continuation")
	// ErrExpired signals a token past its expiry.
	ErrExpired = errors.New("flow: continuation expired")
	// ErrFlowDrift signals a token from another workflow or version.
	ErrFlowDrift = errors.New("flow: workflow version drift")
	// ErrReplay signals a token that was already consumed.
	ErrReplay = errors.New("flow: continuation replay")
	// ErrUnauthorized signals an approver without the required permission.
	ErrUnauthorized = errors.New("flow: approver is not authorized")
	// ErrContinuationTooLarge signals a sealed state above the configured cap.
	ErrContinuationTooLarge = errors.New("flow: continuation exceeds the size limit")
	// ErrDecision signals an unknown approval decision. The zero Decision is
	// never treated as an approval.
	ErrDecision = errors.New("flow: unknown approval decision")
)

// Status is the outcome of a Run or Resume.
type Status uint8

const (
	// Running means the flow is executing.
	Running Status = iota
	// Suspended means the flow stopped at an approval boundary.
	Suspended
	// Done means the flow finished.
	Done
	// Rejected means the approver rejected the action.
	Rejected
	// Expired means the approval expired.
	Expired
	// Failed means a step or a check failed.
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

// Decision is the approver's answer. Editing arguments is deliberately not
// supported: a changed action must go through a new run.
type Decision uint8

const (
	// DecisionUnknown is the zero value. It is never a valid answer and Resume
	// rejects it, so an uninitialised variable cannot approve an action.
	DecisionUnknown Decision = iota
	// Approve lets the workflow continue.
	Approve
	// Reject terminates the workflow without executing the action.
	Reject
)

// Suspension is returned when a flow needs a human decision.
type Suspension struct {
	ID        string                `json:"id"`
	Card      gobellim.ApprovalCard `json:"card"`
	Token     string                `json:"token"`
	ExpiresAt time.Time             `json:"expires_at"`
}

// Result is the typed outcome of Run and Resume.
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
	sealer        *Sealer
	store         Store
	ttl           time.Duration
	maxTokenBytes int
	maxWallTime   time.Duration
	now           func() time.Time
}

// Option configures a Flow.
type Option func(*config)

// Version pins the workflow version checked on resume.
func Version(v int) Option { return func(c *config) { c.version = v } }

// WithPolicyVersion stamps audit events with the active policy version.
func WithPolicyVersion(v string) Option { return func(c *config) { c.policyVersion = v } }

// WithAuthorizer sets the RBAC/ACL adapter. It is required.
func WithAuthorizer(a gobellim.Authorizer) Option { return func(c *config) { c.authz = a } }

// WithAudit sets the audit sink.
func WithAudit(s gobellim.Sink) Option { return func(c *config) { c.audit = s } }

// WithSealer sets the continuation sealer. Without one the flow uses an
// ephemeral per-process key, which cannot resume on another replica.
func WithSealer(s *Sealer) Option { return func(c *config) { c.sealer = s } }

// WithStore enables single-use resume across replicas.
func WithStore(s Store) Option { return func(c *config) { c.store = s } }

// WithTTL sets the default approval lifetime.
func WithTTL(d time.Duration) Option { return func(c *config) { c.ttl = d } }

// WithMaxTokenBytes caps the sealed continuation size.
func WithMaxTokenBytes(n int) Option { return func(c *config) { c.maxTokenBytes = n } }

// WithMaxWallTime caps one Run or Resume, including all steps.
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
	resource         func(ctx context.Context, p gobellim.Principal, s S) (gobellim.Resource, error)
}

// Flow is a linear, typed workflow.
type Flow[S any] struct {
	name  string
	cfg   config
	steps []*step[S]
	err   error
}

type nopSink struct{}

func (nopSink) Emit(context.Context, gobellim.Event) {}

// New starts a flow. Configuration errors are returned immediately.
func New[S any](name string, opts ...Option) (*Flow[S], error) {
	cfg := config{
		version:       1,
		ttl:           24 * time.Hour,
		maxTokenBytes: 64 << 10,
		maxWallTime:   60 * time.Second,
		now:           time.Now,
	}
	for _, o := range opts {
		o(&cfg)
	}
	switch {
	case name == "":
		return nil, fmt.Errorf("%w: flow name is required", gobellim.ErrInvalidConfig)
	case cfg.authz == nil:
		return nil, fmt.Errorf("%w: authorizer is required", gobellim.ErrInvalidConfig)
	case cfg.sealer == nil:
		return nil, fmt.Errorf("%w: sealer is required", gobellim.ErrInvalidConfig)
	case cfg.version < 1:
		return nil, fmt.Errorf("%w: flow version must be positive", gobellim.ErrInvalidConfig)
	case cfg.ttl <= 0:
		return nil, fmt.Errorf("%w: TTL must be positive", gobellim.ErrInvalidConfig)
	case cfg.maxTokenBytes <= 0:
		return nil, fmt.Errorf("%w: max token bytes must be positive", gobellim.ErrInvalidConfig)
	case cfg.maxWallTime <= 0:
		return nil, fmt.Errorf("%w: max wall time must be positive", gobellim.ErrInvalidConfig)
	}
	if cfg.audit == nil {
		cfg.audit = nopSink{}
	}
	if cfg.store == nil {
		// A per-process store is the safe default: without it a token could be
		// resumed an unlimited number of times inside one replica.
		cfg.store = NewMemStore()
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &Flow[S]{name: name, cfg: cfg}, nil
}

func (f *Flow[S]) fail(err error) {
	if f.err == nil {
		f.err = err
	}
}

// Step adds a deterministic step. Steps must be pure or idempotent up to the
// next approval boundary.
func (f *Flow[S]) Step(id string, fn func(ctx context.Context, p gobellim.Principal, s *S) error) *Flow[S] {
	if f.err != nil {
		return f
	}
	if id == "" || fn == nil {
		f.fail(fmt.Errorf("%w: step id and function are required", gobellim.ErrInvalidConfig))
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
	// Card builds the approval card from the workflow state.
	Card func(ctx context.Context, p gobellim.Principal, s S) (gobellim.ApprovalCard, error)
	// Resource binds the approved action to an object and tenant.
	Resource func(ctx context.Context, p gobellim.Principal, s S) (gobellim.Resource, error)
}

func noResource[S any](context.Context, gobellim.Principal, S) (gobellim.Resource, error) {
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
		resource:         noResource[S],
	}
	if cfg.Resource != nil {
		a.resource = cfg.Resource
	}
	f.steps = append(f.steps, &step[S]{id: id, approve: a})
	return f
}

// ApprovePending adds an approval boundary whose card is the PendingAction a
// previous step produced. It removes only the card closure: permission,
// resource and action permission stay explicit application policy.
func (f *Flow[S]) ApprovePending(id string, pending func(S) *gobellim.PendingAction, cfg ApproveConfig[S]) *Flow[S] {
	if cfg.Card == nil {
		cfg.Card = func(_ context.Context, _ gobellim.Principal, s S) (gobellim.ApprovalCard, error) {
			p := pending(s)
			if p == nil {
				return gobellim.ApprovalCard{}, fmt.Errorf("%w: approve %s: no pending action in the state", gobellim.ErrInvalidConfig, id)
			}
			return p.Card, nil
		}
	}
	return f.Approve(id, cfg)
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

		needed := true
		if st.approve.when != nil {
			ok, err := st.approve.when(ctx, p, s)
			if err != nil {
				f.emit(ctx, gobellim.Event{
					Kind: gobellim.EventWorkflowFailed, Flow: f.name, Step: st.id,
					PrincipalID: p.UserID, Tenant: p.Tenant, Error: "when-failed",
				})
				return Result[S]{Status: Failed, State: s}, err
			}
			needed = ok
		}
		if !needed {
			continue
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

		state, err := json.Marshal(s)
		if err != nil {
			return Result[S]{Status: Failed, State: s}, fmt.Errorf("flow: encode state: %w", err)
		}
		cont := continuation{
			V:           1,
			Flow:        f.name,
			FlowVersion: f.cfg.version,
			Tenant:      p.Tenant,
			Requester:   p,
			Step:        i,
			StepID:      st.id,
			ExpiresAt:   card.ExpiresAt,
			Nonce:       newID(""),
			State:       state,
			Card:        card,
		}
		plain, err := json.Marshal(cont)
		if err != nil {
			return Result[S]{Status: Failed, State: s}, fmt.Errorf("flow: encode continuation: %w", err)
		}
		token, err := f.cfg.sealer.Seal(plain, f.aad(p.Tenant))
		if err != nil {
			return Result[S]{Status: Failed, State: s}, err
		}
		if len(token) > f.cfg.maxTokenBytes {
			err := fmt.Errorf("%w: %d bytes, cap is %d", ErrContinuationTooLarge, len(token), f.cfg.maxTokenBytes)
			return Result[S]{Status: Failed, State: s}, err
		}

		f.emit(ctx, gobellim.Event{
			Kind: gobellim.EventWorkflowSuspended, RunID: card.RunID, Flow: f.name, Step: st.id,
			PrincipalID: p.UserID, Tenant: p.Tenant, Decision: gobellim.Escalate,
		})
		f.emit(ctx, gobellim.Event{
			Kind: gobellim.EventApprovalRequested, RunID: card.RunID, Flow: f.name, Step: st.id,
			PrincipalID: p.UserID, Tenant: p.Tenant, Decision: gobellim.Escalate,
		})

		return Result[S]{
			Status: Suspended,
			State:  s,
			Suspension: &Suspension{
				ID:        card.RunID,
				Card:      card,
				Token:     token,
				ExpiresAt: card.ExpiresAt,
			},
		}, nil
	}

	return Result[S]{Status: Done, State: s}, nil
}

// Resume checks a token and an approval, then continues from the step after
// the approval boundary. The checks are listed in DESIGN.md §8.6; every one is
// fail-closed.
func (f *Flow[S]) Resume(ctx context.Context, approver gobellim.Principal, token string, d Decision) (Result[S], error) {
	var zero Result[S]
	if f.err != nil {
		return zero, f.err
	}
	if err := approver.Validate(); err != nil {
		return zero, err
	}
	if len(token) > f.cfg.maxTokenBytes {
		// The cap applies to tokens we accepted, not only to tokens we issued:
		// an unbounded token would be decoded on a public approval endpoint.
		return zero, fmt.Errorf("%w: token is %d bytes, cap is %d", ErrContinuation, len(token), f.cfg.maxTokenBytes)
	}
	ctx, cancel := context.WithTimeout(ctx, f.cfg.maxWallTime)
	defer cancel()

	plain, err := f.cfg.sealer.Open(token, f.aad(approver.Tenant))
	if err != nil {
		return zero, fmt.Errorf("%w: %w", ErrContinuation, err)
	}
	var c continuation
	if err := json.Unmarshal(plain, &c); err != nil {
		return zero, fmt.Errorf("%w: %w", ErrContinuation, err)
	}
	if c.V != 1 {
		return zero, fmt.Errorf("%w: unknown format version %d", ErrContinuation, c.V)
	}
	if c.Flow != f.name || c.FlowVersion != f.cfg.version {
		f.emit(ctx, gobellim.Event{
			Kind: gobellim.EventWorkflowFailed, RunID: c.Card.RunID, Flow: f.name, Step: c.StepID,
			PrincipalID: approver.UserID, Tenant: approver.Tenant, Error: "flow-drift",
		})
		return zero, fmt.Errorf("%w: token is %q v%d, expected %q v%d",
			ErrFlowDrift, c.Flow, c.FlowVersion, f.name, f.cfg.version)
	}
	if f.cfg.now().After(c.ExpiresAt) {
		f.emit(ctx, gobellim.Event{
			Kind: gobellim.EventWorkflowExpired, RunID: c.Card.RunID, Flow: f.name, Step: c.StepID,
			PrincipalID: approver.UserID, Tenant: approver.Tenant,
		})
		return Result[S]{Status: Expired}, fmt.Errorf("%w: expired at %s", ErrExpired, c.ExpiresAt.Format(time.RFC3339))
	}
	if c.Tenant != approver.Tenant {
		return zero, fmt.Errorf("%w: tenant mismatch", ErrContinuation)
	}

	var state S
	if err := json.Unmarshal(c.State, &state); err != nil {
		return zero, fmt.Errorf("%w: state: %w", ErrContinuation, err)
	}
	if c.Step < 0 || c.Step >= len(f.steps) || f.steps[c.Step].approve == nil {
		return zero, fmt.Errorf("%w: step %d is not an approval boundary", ErrFlowDrift, c.Step)
	}
	if f.steps[c.Step].id != c.StepID {
		return zero, fmt.Errorf("%w: step %d is %q, token is %q", ErrFlowDrift, c.Step, f.steps[c.Step].id, c.StepID)
	}
	a := f.steps[c.Step].approve

	resource, err := a.resource(ctx, c.Requester, state)
	if err != nil {
		return Result[S]{Status: Failed, State: state}, err
	}
	ok, err := f.cfg.authz.Allowed(ctx, approver, a.permission, resource)
	if err != nil || !ok {
		f.emit(ctx, gobellim.Event{
			Kind: gobellim.EventApprovalDenied, RunID: c.Card.RunID, Flow: f.name, Step: c.StepID,
			PrincipalID: approver.UserID, Tenant: approver.Tenant, Error: "approver-permission",
		})
		return Result[S]{Status: Failed, State: state},
			fmt.Errorf("%w: approver lacks %s", ErrUnauthorized, a.permission)
	}

	switch d {
	case Reject:
		// A rejection consumes the token: a human who said no must not be
		// replayed as a yes by the same continuation. A retry needs a new Run.
		claimed, err := f.cfg.store.Claim(ctx, c.Nonce, c.ExpiresAt)
		if err != nil {
			return Result[S]{Status: Failed, State: state}, fmt.Errorf("flow: store claim: %w", err)
		}
		if !claimed {
			return Result[S]{Status: Failed, State: state}, ErrReplay
		}
		f.emit(ctx, gobellim.Event{
			Kind: gobellim.EventApprovalDenied, RunID: c.Card.RunID, Flow: f.name, Step: c.StepID,
			PrincipalID: approver.UserID, Tenant: approver.Tenant,
		})
		return Result[S]{Status: Rejected, State: state}, nil
	case Approve:
	default:
		return Result[S]{Status: Failed, State: state}, fmt.Errorf("%w: %d", ErrDecision, d)
	}

	if a.fourEyes && approver.UserID == c.Requester.UserID {
		return Result[S]{Status: Failed, State: state},
			fmt.Errorf("%w: four-eyes requires a different approver", ErrUnauthorized)
	}

	if a.actionPermission != "" {
		ok, err := f.cfg.authz.Allowed(ctx, c.Requester, a.actionPermission, resource)
		if err != nil || !ok {
			f.emit(ctx, gobellim.Event{
				Kind: gobellim.EventApprovalDenied, RunID: c.Card.RunID, Flow: f.name, Step: c.StepID,
				PrincipalID: approver.UserID, Tenant: approver.Tenant, Error: "action-permission",
			})
			return Result[S]{Status: Failed, State: state},
				fmt.Errorf("%w: requester lacks %s", ErrUnauthorized, a.actionPermission)
		}
	}

	// Rebuild the card from the sealed state and compare the action digest.
	// The token is authentic, but this also catches a non-deterministic builder
	// or an application-level mutation of the pending action.
	rebuilt, err := a.card(ctx, c.Requester, state)
	if err != nil {
		return Result[S]{Status: Failed, State: state}, err
	}
	if rebuilt.ActionDigest() != c.Card.ActionDigest() {
		return Result[S]{Status: Failed, State: state},
			fmt.Errorf("%w: pending action changed", ErrFlowDrift)
	}

	// Claim before the effect: a crash in the window between the claim and the
	// external call loses the approval instead of risking a second execution.
	// Recovery is reconciliation with the provider, not a retry.
	claimed, err := f.cfg.store.Claim(ctx, c.Nonce, c.ExpiresAt)
	if err != nil {
		return Result[S]{Status: Failed, State: state}, fmt.Errorf("flow: store claim: %w", err)
	}
	if !claimed {
		return Result[S]{Status: Failed, State: state}, ErrReplay
	}

	f.emit(ctx, gobellim.Event{
		Kind: gobellim.EventApprovalGranted, RunID: c.Card.RunID, Flow: f.name, Step: c.StepID,
		PrincipalID: approver.UserID, Tenant: approver.Tenant,
	})
	f.emit(ctx, gobellim.Event{
		Kind: gobellim.EventWorkflowResumed, RunID: c.Card.RunID, Flow: f.name, Step: c.StepID,
		PrincipalID: c.Requester.UserID, Tenant: c.Requester.Tenant,
	})

	// The remaining steps run as the requester and can read the approver from
	// the context to record who approved an action.
	return f.runFrom(withApprover(ctx, approver), c.Requester, state, c.Step+1)
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

func (f *Flow[S]) aad(tenant string) []byte {
	// Tenant and flow are bound outside the ciphertext. The workflow version is
	// inside the authenticated payload, so a mismatch yields ErrFlowDrift
	// instead of looking like tampering.
	return []byte(tenant + "|" + f.name)
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

type continuation struct {
	V           int                   `json:"v"`
	Flow        string                `json:"flow"`
	FlowVersion int                   `json:"flow_version"`
	Tenant      string                `json:"tenant"`
	Requester   gobellim.Principal    `json:"requester"`
	Step        int                   `json:"step"`
	StepID      string                `json:"step_id"`
	ExpiresAt   time.Time             `json:"expires_at"`
	Nonce       string                `json:"nonce"`
	State       json.RawMessage       `json:"state"`
	Card        gobellim.ApprovalCard `json:"card"`
}

// Err reports the first configuration error recorded by Step or Approve. Run and
// Resume return it as well; the accessor lets a caller fail at startup instead
// of on the first request.
func (f *Flow[S]) Err() error { return f.err }
