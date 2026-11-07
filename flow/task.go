package flow

import (
	"context"
	"fmt"

	gobellim "github.com/KirillYuzh/Gobellim"
)

// PendingExecutor is the part of gobellim.Task an approval step needs: it runs a
// PendingAction after an authenticated approver accepted it, re-running the
// Gate. *gobellim.Task[In, Out] satisfies it.
type PendingExecutor interface {
	ExecuteApproved(ctx context.Context, requester, approvedBy gobellim.Principal, pending *gobellim.PendingAction) (any, gobellim.ToolCallRecord, error)
}

// ApproveTaskConfig configures an approval boundary bound to an action a Task
// escalated.
type ApproveTaskConfig[S any] struct {
	// Pending returns the escalated action the workflow state holds. Required: a
	// state without one is a configuration error, not an approval of nothing.
	Pending func(S) *gobellim.PendingAction
	// Permission is the approver's permission. It defaults to
	// "approve:<flow>.<step>".
	Permission string
	// ActionPermission is the permission re-checked for the requester on resume.
	ActionPermission string
	// FourEyes requires the approver to differ from the requester. ExecuteApproved
	// re-checks whichever requirement the Task wrote into the PendingAction, so a
	// stricter Task is never loosened here; set this when the flow itself should
	// be the stricter one.
	FourEyes bool
	// Target binds the approval for the approver's ACL. The default is the pending
	// action itself: type "task", its run id and its tenant. The tool is
	// re-checked against its own target by ExecuteApproved, so the default is a
	// step-level decision, not a substitute for the tool's ACL.
	Target func(ctx context.Context, p gobellim.Principal, s S) (gobellim.Resource, error)
	// OnResult receives the tool output after the approved action runs. Optional:
	// a step that does not need the result leaves it nil.
	OnResult func(ctx context.Context, s *S, out any, rec gobellim.ToolCallRecord) error
}

// ApproveTask adds an approval boundary for an action a Task escalated, and the
// step that executes it afterwards.
//
// Manual wiring repeats facts the Gate already produced: the approval card, the
// target, and the resolved arguments copied into the workflow state. When the
// copy drifts, the approver decides on one action and ExecuteApproved receives
// another. Keeping the card and the arguments in the PendingAction removes that
// class of mistake.
func (f *Flow[S]) ApproveTask(id string, executor PendingExecutor, cfg ApproveTaskConfig[S]) *Flow[S] {
	if f.err != nil {
		return f
	}
	if id == "" || executor == nil || cfg.Pending == nil {
		f.fail(fmt.Errorf("%w: ApproveTask needs an id, an executor and Pending", gobellim.ErrInvalidConfig))
		return f
	}

	missing := func() error {
		return fmt.Errorf("%w: approve %s: the state holds no pending action", gobellim.ErrInvalidConfig, id)
	}
	pendingCard := func(_ context.Context, _ gobellim.Principal, s S) (gobellim.ApprovalCard, error) {
		pa := cfg.Pending(s)
		if pa == nil {
			return gobellim.ApprovalCard{}, missing()
		}
		return pa.Card, nil
	}
	target := cfg.Target
	if target == nil {
		target = func(_ context.Context, _ gobellim.Principal, s S) (gobellim.Resource, error) {
			pa := cfg.Pending(s)
			if pa == nil {
				return gobellim.Resource{}, missing()
			}
			return gobellim.Resource{Type: "task", ID: pa.RunID, TenantID: pa.Tenant}, nil
		}
	}

	f.Approve(id, ApproveConfig[S]{
		Permission:       cfg.Permission,
		ActionPermission: cfg.ActionPermission,
		FourEyes:         cfg.FourEyes,
		Card:             pendingCard,
		Target:           target,
	})
	if f.err != nil {
		return f
	}

	return f.Step(id+"_execute", func(ctx context.Context, p gobellim.Principal, s *S) error {
		approvedBy, ok := ApproverFrom(ctx)
		if !ok {
			// Without an approval in flight there is nothing to execute. The
			// boundary always suspends, so this is a programming error rather than
			// a branch an application can hit.
			return fmt.Errorf("%w: step %s runs without an approval", gobellim.ErrUnauthorized, id)
		}
		pa := cfg.Pending(*s)
		if pa == nil {
			return missing()
		}
		out, rec, err := executor.ExecuteApproved(ctx, p, approvedBy, pa)
		if err != nil {
			return err
		}
		if cfg.OnResult != nil {
			return cfg.OnResult(ctx, s, out, rec)
		}
		return nil
	})
}
