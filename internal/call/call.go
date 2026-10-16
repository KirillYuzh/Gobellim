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

type Kind string

const (
	KindLLM    Kind = "llm"
	KindTool   Kind = "tool"
	KindEmbed  Kind = "embed"
	KindSearch Kind = "search"
	KindIngest Kind = "ingest"
	KindDelete Kind = "delete"
	KindGuard  Kind = "guard"
)

type Authorizer interface {
	Authorize(pr core.Principal, permission string) error
}

type Guarder interface {
	Check(ctx context.Context, rail guard.Rail, text string) (guard.Verdict, error)
}

type Tracer interface {
	Observe(ctx context.Context, obs trace.Observation)
}

type Deps struct {
	Authz  Authorizer
	Guard  Guarder
	Tracer Tracer

	GuardVersion string
	GuardModel   string
	PolicyHash   string
	TextPolicy   trace.TextPolicy
}

type Req struct {
	Kind       Kind
	Name       string
	Permission string
	Rail       guard.Rail
	Text       string
	Model      string
	TraceID    string
	ParentID   string
	Tags       []string
}

type Span struct {
	Verdict  guard.Verdict
	Text     string
	Kind     trace.Kind
	Output   string
	Usage    trace.Usage
	Metadata map[string]any
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
			Input:           obsInput(deps, req, span),
			Output:          span.Output,
			StatusMessage:   status,
			StartTime:       start,
			EndTime:         time.Now(),
			Usage:           span.Usage,
			GuardRail:       string(span.Verdict.Rail),
			GuardAction:     span.Verdict.Action.String(),
			GuardCategories: categories(span.Verdict.Findings),
			Tags:            req.Tags,
			Metadata:        observationMetadata(ctx, span),
			GuardVersion:    deps.GuardVersion,
			GuardModel:      deps.GuardModel,
			PolicyHash:      deps.PolicyHash,
			TextPolicy:      deps.TextPolicy,
		})
	}

	return out, err
}

func observationMetadata(ctx context.Context, span *Span) map[string]any {
	base := core.SpanMetadataFrom(ctx)
	if len(base) == 0 {
		return span.Metadata
	}
	if len(span.Metadata) == 0 {
		out := make(map[string]any, len(base))
		for k, v := range base {
			out[k] = v
		}
		return out
	}
	out := make(map[string]any, len(base)+len(span.Metadata))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range span.Metadata {
		out[k] = v
	}
	return out
}

func obsInput(deps Deps, req Req, span *Span) string {
	if deps.TextPolicy.IsRaw() {
		return req.Text
	}
	return span.Text
}

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
			return zero, &GuardRejectError{
				Kind: req.Kind, Name: req.Name, Rail: verdict.Rail,
				Tripwire: verdict.Tripwire, Version: verdict.Version,
			}
		}
		if verdict.Tripwire != nil {
			if span.Metadata == nil {
				span.Metadata = map[string]any{}
			}
			span.Metadata["tripwire_rule"] = verdict.Tripwire.RuleID
			span.Metadata["tripwire_reason"] = verdict.Tripwire.Reason
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
