// Package call is the single call primitive: authz, then the guard rail, then a
// trace observation, and only then the operation.
package call

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gobellim/kit/guard"
	"github.com/gobellim/kit/internal/core"
	"github.com/gobellim/kit/internal/id"
	"github.com/gobellim/kit/trace"
)

// Kind labels a call.
type Kind string

const (
	KindLLM    Kind = "llm"
	KindTool   Kind = "tool"
	KindEmbed  Kind = "embed"
	KindSearch Kind = "search"
	KindIngest Kind = "ingest"
	KindGuard  Kind = "guard"
)

// Authorizer is satisfied by authz.Policy.
type Authorizer interface {
	Authorize(pr core.Principal, permission string) error
}

// Guarder is satisfied by guard.Pipeline.
type Guarder interface {
	Check(ctx context.Context, rail guard.Rail, text string) (guard.Verdict, error)
}

// Tracer is satisfied by anything whose Observe method matches.
type Tracer interface {
	Observe(ctx context.Context, obs trace.Observation)
}

// Deps are the collaborators of a call.
type Deps struct {
	Authz  Authorizer
	Guard  Guarder
	Tracer Tracer
}

// Req describes one call.
type Req struct {
	Kind Kind
	// Name is the operation name (tool name, model, index).
	Name string
	// Permission is the RBAC permission to check; "" skips the check.
	Permission string
	// Rail, when set, runs the guard over Text before the operation.
	Rail guard.Rail
	// Text is the guarded text (message content, chunk text, tool result).
	Text string
	// Model is recorded on the observation.
	Model string
	// TraceID and ParentID link the observation to its trace.
	TraceID  string
	ParentID string
}

// Span is the mutable context handed to the operation.
type Span struct {
	// Verdict is the guard verdict (zero when no rail was applied).
	Verdict guard.Verdict
	// Text is the guarded text: masked when the guard masked it.
	Text string
	// Kind is the observation kind; set GENERATION for LLM calls.
	Kind trace.Kind
	// Output, Usage and Metadata are filled by the operation for tracing.
	Output   string
	Usage    trace.Usage
	Metadata map[string]any
}

// Do runs one operation behind authz, the guard and tracing.
func Do[T any](ctx context.Context, deps Deps, req Req, fn func(ctx context.Context, span *Span) (T, error)) (T, error) {
	span := &Span{Text: req.Text}
	start := time.Now()

	out, err := run(ctx, deps, req, span, fn)

	if deps.Tracer != nil {
		kind := span.Kind
		if kind == "" {
			kind = trace.KindSpan
			if req.Kind == KindGuard {
				kind = trace.KindEvent
			}
		}
		level := trace.LevelDefault
		status := ""
		errorKind := ""
		if err != nil {
			level = trace.LevelError
			status = err.Error()
			errorKind = errorKindOf(err)
		}
		deps.Tracer.Observe(ctx, trace.Observation{
			ID:              id.New(),
			TraceID:         req.TraceID,
			ParentID:        req.ParentID,
			Name:            req.Name,
			Kind:            kind,
			Level:           level,
			ErrorKind:       errorKind,
			Model:           req.Model,
			Input:           req.Text,
			Output:          span.Output,
			StatusMessage:   status,
			StartTime:       start,
			EndTime:         time.Now(),
			Usage:           span.Usage,
			GuardRail:       string(span.Verdict.Rail),
			GuardAction:     span.Verdict.Action.String(),
			GuardCategories: categories(span.Verdict.Findings),
			Metadata:        span.Metadata,
		})
	}

	return out, err
}

// Guard runs only the guard rail of a call and records the observation, so a
// rejected request is still visible in the trace.
func Guard(ctx context.Context, deps Deps, req Req) (guard.Verdict, error) {
	var verdict guard.Verdict
	_, err := Do(ctx, deps, req, func(_ context.Context, span *Span) (struct{}, error) {
		verdict = span.Verdict
		return struct{}{}, nil
	})
	if err != nil {
		return guard.Verdict{}, err
	}
	return verdict, nil
}

func run[T any](ctx context.Context, deps Deps, req Req, span *Span, fn func(ctx context.Context, span *Span) (T, error)) (T, error) {
	var zero T

	pr, ok := core.PrincipalFrom(ctx)
	if !ok {
		return zero, fmt.Errorf("call: %s %s: no principal: %w", req.Kind, req.Name, core.ErrUnauthorized)
	}
	if deps.Authz != nil {
		if err := deps.Authz.Authorize(pr, req.Permission); err != nil {
			return zero, err
		}
	}

	if req.Rail != "" && deps.Guard != nil {
		verdict, err := deps.Guard.Check(ctx, req.Rail, req.Text)
		if err != nil {
			return zero, err
		}
		span.Verdict = verdict
		span.Text = verdict.Text
		if verdict.Action == guard.Reject {
			return zero, fmt.Errorf("call: %s %s: %w", req.Kind, req.Name, core.ErrGuardReject)
		}
	}

	return fn(ctx, span)
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
	case errors.Is(err, core.ErrMaxSteps):
		return "max_steps"
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
