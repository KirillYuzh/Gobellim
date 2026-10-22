package call

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/trace"
)

type Kind string

const (
	KindLLM    Kind = "llm"
	KindTool   Kind = "tool"
	KindEmbed  Kind = "embed"
	KindSearch Kind = "search"
	KindIngest Kind = "ingest"
	KindDelete Kind = "delete"
	KindMemory Kind = "memory"
	KindGuard  Kind = "guard"
	KindRerank Kind = "rerank"
)

type Authorizer interface {
	Authorize(pr core.Principal, permission string) error
}

type Guarder interface {
	Check(ctx context.Context, rail guard.Rail, text string) (guard.Verdict, error)
}

type Tracer interface {
	Observe(ctx context.Context, span trace.Span)
}

type Deps struct {
	Authz  Authorizer
	Guard  Guarder
	Tracer Tracer
	// Provider creates the span of the call and of the operations it covers.
	// A nil provider records nothing.
	Provider trace.Provider

	GuardVersion string
	GuardModel   string
	PolicyHash   string
	TextPolicy   string
}

type Req struct {
	Kind Kind
	Name string
	// Permission is the RBAC permission the caller must hold. Every Do call
	// names one: an empty permission is a configuration error, never a free
	// pass. A guard probe uses Guard, which authorizes nothing because it runs
	// inside an operation that already did.
	Permission string
	Rail       guard.Rail
	Text       string
	Model      string
	TraceID    string
	ParentID   string
	Tags       []string
}

type GuardRejectError struct {
	Kind     Kind
	Name     string
	Rail     guard.Rail
	Tripwire *guard.Tripwire
	Version  string
}

func (e *GuardRejectError) Error() string {
	reason := ""
	if e.Tripwire != nil {
		reason = e.Tripwire.Reason
	}
	return fmt.Sprintf("call: %s %s: guard rejected rail %s (%s)", e.Kind, e.Name, e.Rail, reason)
}

func (e *GuardRejectError) Unwrap() error { return core.ErrGuardReject }

func Do[T any](ctx context.Context, deps Deps, req Req, fn func(ctx context.Context, span trace.Span) (T, error)) (T, error) {
	ctx, span := startSpan(ctx, deps.Provider, req.Name, trace.Attr("call.kind", string(req.Kind)))
	defer span.End()

	start := time.Now()

	out, err := run(ctx, deps, req, fn, span)

	span.SetAttributes(
		trace.Int("call.duration_ms", int(time.Since(start).Milliseconds())),
		trace.String("call.model", req.Model),
	)

	if err != nil {
		span.RecordError(err)
		span.SetError()
		span.SetAttributes(trace.String("call.error_kind", errorKindOf(err)))
	}

	if deps.Tracer != nil {
		span.Event("call.complete",
			trace.Bool("call.success", err == nil),
		)
		deps.Tracer.Observe(ctx, span)
	}

	return out, err
}

func run[T any](ctx context.Context, deps Deps, req Req, fn func(ctx context.Context, span trace.Span) (T, error), span trace.Span) (T, error) {
	var zero T

	if req.Permission == "" {
		return zero, fmt.Errorf("call: %s %s: permission is required: %w", req.Kind, req.Name, core.ErrInvalidConfig)
	}
	pr, ok := core.PrincipalFrom(ctx)
	if !ok {
		return zero, fmt.Errorf("call: %s %s: no principal: %w", req.Kind, req.Name, core.ErrUnauthorized)
	}
	if deps.Authz == nil {
		return zero, fmt.Errorf("call: %s %s: no authorizer configured: %w", req.Kind, req.Name, core.ErrUnauthorized)
	}
	if err := deps.Authz.Authorize(pr, req.Permission); err != nil {
		return zero, err
	}

	ctx, _, err := checkRail(ctx, deps, req)
	if err != nil {
		return zero, err
	}

	return fn(ctx, span)
}

// checkRail runs the guard rail of req and returns the context carrying the
// masked text together with the verdict.
func checkRail(ctx context.Context, deps Deps, req Req) (context.Context, guard.Verdict, error) {
	if req.Rail == "" {
		return ctx, guard.Verdict{Action: guard.Allow}, nil
	}
	if deps.Guard == nil {
		return ctx, guard.Verdict{}, fmt.Errorf("call: %s %s: rail %s without a guard: %w", req.Kind, req.Name, req.Rail, core.ErrInvalidConfig)
	}
	verdict, err := deps.Guard.Check(ctx, req.Rail, req.Text)
	if err != nil {
		return ctx, guard.Verdict{}, err
	}

	_, guardSpan := startSpan(ctx, deps.Provider, "guard.check",
		trace.Attr("guard.rail", string(req.Rail)),
	)

	if verdict.Action == guard.Reject {
		guardSpan.Event("guard.rejected",
			trace.String("guard.action", "reject"),
			trace.String("guard.categories", categoriesToString(verdict.Findings)),
		)
		guardSpan.End()
		return ctx, guard.Verdict{}, &GuardRejectError{
			Kind: req.Kind, Name: req.Name, Rail: verdict.Rail,
			Tripwire: verdict.Tripwire, Version: verdict.Version,
		}
	}

	if verdict.Tripwire != nil {
		guardSpan.SetAttributes(
			trace.String("tripwire_rule", verdict.Tripwire.RuleID),
			trace.String("tripwire_reason", verdict.Tripwire.Reason),
		)
	}

	guardSpan.End()
	return ctx, verdict, nil
}

// startSpan creates a span with the provider, or a span that records nothing
// when no provider is configured.
func startSpan(ctx context.Context, p trace.Provider, name string, attrs ...trace.Attribute) (context.Context, trace.Span) {
	if p == nil {
		return trace.Noop().Start(ctx, name, attrs...)
	}
	return p.Start(ctx, name, attrs...)
}

// Guard checks text against a guard rail and returns the verdict. It runs no
// RBAC check of its own: a guard probe always sits inside an operation that
// already authorized the caller, and it exposes no capability on its own. Use
// Do for anything the caller must be authorized for.
func Guard(ctx context.Context, deps Deps, req Req) (guard.Verdict, error) {
	if _, ok := core.PrincipalFrom(ctx); !ok {
		return guard.Verdict{}, fmt.Errorf("call: %s %s: no principal: %w", req.Kind, req.Name, core.ErrUnauthorized)
	}
	ctx, span := startSpan(ctx, deps.Provider, req.Name, trace.Attr("call.kind", string(req.Kind)))
	defer span.End()

	_, verdict, err := checkRail(ctx, deps, req)
	if err != nil {
		span.RecordError(err)
		span.SetError()
		return guard.Verdict{}, err
	}
	if deps.Tracer != nil {
		span.Event("call.complete",
			trace.Bool("call.success", true),
		)
		deps.Tracer.Observe(ctx, span)
	}
	return verdict, nil
}

func errorKindOf(err error) string {
	switch {
	case errors.Is(err, core.ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, core.ErrGuardReject):
		return "guard_reject"
	case errors.Is(err, core.ErrGuardUnavailable):
		return "guard_unavailable"
	case errors.Is(err, core.ErrInvalidArgs):
		return "invalid_args"
	case errors.Is(err, core.ErrToolNotFound):
		return "tool_not_found"
	case errors.Is(err, core.ErrTool):
		return "tool"
	case errors.Is(err, core.ErrMaxSteps):
		return "max_steps"
	case errors.Is(err, core.ErrBudget):
		return "budget"
	case errors.Is(err, core.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, core.ErrApprovalRequired):
		return "approval_required"
	case errors.Is(err, core.ErrNotFound):
		return "not_found"
	case errors.Is(err, core.ErrProvider):
		return "provider"
	default:
		return "internal"
	}
}

func categories(findings []guard.Finding) []string {
	seen := make(map[string]struct{}, len(findings))
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		cat := string(f.Category)
		if _, ok := seen[cat]; ok {
			continue
		}
		seen[cat] = struct{}{}
		out = append(out, cat)
	}
	return out
}

func categoriesToString(findings []guard.Finding) string {
	return strings.Join(categories(findings), ",")
}
