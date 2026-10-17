package kit

import (
	"context"
	"errors"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// Audit outcomes written to the audit log.
const (
	auditOK     = "ok"
	auditDenied = "denied"
	auditError  = "error"
)

// auditOp accumulates one operation-level audit record. It writes nothing when
// Config.AuditLog is nil.
type auditOp struct {
	app     *App
	op      string
	started time.Time
	pr      core.Principal
	traceID string
	fields  []any
}

// beginAudit starts an audit record for op. The principal is read best-effort so
// a request that fails before authentication still produces a record.
func (a *App) beginAudit(ctx context.Context, op string) *auditOp {
	ao := &auditOp{app: a, op: op, started: time.Now()}
	if a.cfg.AuditLog == nil {
		return ao
	}
	if pr, err := principal(ctx); err == nil {
		ao.pr = pr
	}
	return ao
}

// trace records the trace ID so the audit line joins the trace.
func (ao *auditOp) trace(id string) { ao.traceID = id }

// field adds an operation-specific detail such as a document or thread ID. Pass
// identifiers, never user text: the audit log is not masked.
func (ao *auditOp) field(key string, value any) { ao.fields = append(ao.fields, key, value) }

// end writes the record. A nil error is ok; a policy denial, guard rejection or
// missing approval is denied; everything else is an error.
func (ao *auditOp) end(err error) {
	if ao == nil || ao.app.cfg.AuditLog == nil {
		return
	}
	kind := auditErrorKind(err)
	outcome := auditOK
	switch {
	case err == nil:
	case kind == "unauthorized", kind == "guard_reject", kind == "approval_required":
		outcome = auditDenied
	default:
		outcome = auditError
	}
	roles := ao.pr.Roles
	if roles == nil {
		roles = []string{}
	}
	args := []any{
		"operation", ao.op,
		"user_id", ao.pr.UserID,
		"tenant", ao.pr.Tenant,
		"roles", roles,
		"outcome", outcome,
		"error_kind", kind,
		"trace_id", ao.traceID,
		"duration_ms", time.Since(ao.started).Milliseconds(),
	}
	args = append(args, ao.fields...)
	ao.app.cfg.AuditLog.Info("kit: audit", args...)
}

// auditErrorKind maps an error to a stable, text-free label. Raw error strings
// may carry user data, so they never reach the audit log.
func auditErrorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, core.ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, core.ErrGuardReject):
		return "guard_reject"
	case errors.Is(err, core.ErrGuardUnavailable):
		return "guard_unavailable"
	case errors.Is(err, core.ErrApprovalRequired):
		return "approval_required"
	case errors.Is(err, core.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, core.ErrBudget):
		return "budget"
	case errors.Is(err, core.ErrInvalidArgs):
		return "invalid_args"
	case errors.Is(err, core.ErrInvalidConfig):
		return "invalid_config"
	case errors.Is(err, core.ErrToolNotFound):
		return "tool_not_found"
	case errors.Is(err, core.ErrNotFound):
		return "not_found"
	case errors.Is(err, core.ErrTool):
		return "tool"
	case errors.Is(err, core.ErrMaxSteps):
		return "max_steps"
	case errors.Is(err, core.ErrProvider):
		return "provider"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "error"
	}
}
