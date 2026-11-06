package flow_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	gb "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/flow"
	"github.com/KirillYuzh/Gobellim/testkit"
)

// The scenario this file exists for: the model proposes a refund inside a Task,
// the Task escalates, a human approves through a Flow, and the approved action
// executes exactly once. Before this file the composition was described in the
// README and in DESIGN §8.3, and neither was backed by a test.

type refundIn struct {
	TenantID string `gobellim:"source=principal,required"`
	OrderID  string `gobellim:"sensitivity=secret,required"`
}

type refundOut struct {
	Answer string
}

type refundArgs struct {
	OrderID string `gobellim:"required,ground"`
	Cents   int    `gobellim:"min=1,max=5000"`
}

type refundResult struct {
	RefundID string
}

type caseState struct {
	OrderID  string
	Cents    int
	Pending  gb.PendingAction
	Executed bool
}

type harness struct {
	engine *gb.Engine
	task   *gb.Task[refundIn, refundOut]
	runs   *atomic.Int32
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	runs := new(atomic.Int32)
	refund := gb.Tool[refundArgs, refundResult]{
		Name:       "orders.refund",
		Operation:  gb.Write,
		Permission: "orders:refund",
		Target: func(p gb.Principal, a refundArgs) gb.Resource {
			return gb.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
		},
		Run: func(context.Context, gb.Principal, refundArgs) (refundResult, error) {
			runs.Add(1)
			return refundResult{RefundID: "rf_1"}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.refund", map[string]any{"OrderID": "<h1>", "Cents": 4900}),
	)
	engine, err := gb.New(
		gb.WithModel(model),
		gb.WithAuthorizer(testkit.AllowAll),
		gb.WithAudit(testkit.DiscardAudit()),
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := gb.NewTask[refundIn, refundOut](engine, gb.TaskConfig{
		Name:        "refund",
		Instruction: "Propose a refund when the caller asks for one.",
		Tools:       []gb.ToolDef{refund},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{engine: engine, task: task, runs: runs}
}

// flow builds the workflow around the Task. Everything the flow authorizes with
// comes from the Engine, so the approver is checked against the same ACL as the
// Gate.
func (h *harness) flow(t *testing.T) *flow.Flow[caseState] {
	t.Helper()
	wf, err := flow.FromEngine[caseState](h.engine, "refund")
	if err != nil {
		t.Fatal(err)
	}
	wf.Step("propose", func(ctx context.Context, p gb.Principal, s *caseState) error {
		res, err := h.task.Run(ctx, p, refundIn{TenantID: p.Tenant, OrderID: "A-1042"})
		if err != nil {
			return err
		}
		if res.Pending == nil {
			return errors.New("the task did not escalate")
		}
		s.Pending = *res.Pending
		s.OrderID = "A-1042"
		s.Cents = 4900
		return nil
	})
	wf.Approve("manager", flow.ApproveConfig[caseState]{
		Permission:       "refund:approve",
		ActionPermission: "orders:refund",
		FourEyes:         true,
		Card: func(_ context.Context, _ gb.Principal, s caseState) (gb.ApprovalCard, error) {
			return s.Pending.Card, nil
		},
		Target: func(_ context.Context, p gb.Principal, s caseState) (gb.Resource, error) {
			return gb.Resource{Type: "order", ID: s.OrderID, TenantID: p.Tenant}, nil
		},
	})
	wf.Step("execute", func(ctx context.Context, p gb.Principal, s *caseState) error {
		approver, ok := flow.ApproverFrom(ctx)
		if !ok {
			return errors.New("no approver in the context")
		}
		_, rec, err := h.task.ExecuteApproved(ctx, p, approver, &s.Pending)
		if err != nil {
			return err
		}
		s.Executed = rec.Executed
		return nil
	})
	return wf
}

func requester() gb.Principal { return gb.Principal{UserID: "u-1", Tenant: "acme"} }

func approver() gb.Principal { return gb.Principal{UserID: "m-1", Tenant: "acme"} }

func TestTaskEscalatesInsideFlowAndExecutesOnce(t *testing.T) {
	h := newHarness(t)
	wf := h.flow(t)
	ctx := context.Background()

	res, err := wf.Run(ctx, requester(), caseState{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != flow.Suspended || res.Suspension == nil {
		t.Fatalf("status = %v, want Suspended", res.Status)
	}
	if got := h.runs.Load(); got != 0 {
		t.Fatalf("the tool ran %d times before approval", got)
	}
	if res.State.Pending.Digest == "" || res.State.Pending.Digest != res.Suspension.Card.ActionDigest() {
		t.Fatal("the approver would decide on a different action than the one the task escalated")
	}

	out, err := wf.Resume(ctx, approver(), *res.Suspension, res.State, flow.Approve)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != flow.Done || !out.State.Executed {
		t.Fatalf("status = %v executed = %v, want Done/true", out.Status, out.State.Executed)
	}
	if got := h.runs.Load(); got != 1 {
		t.Fatalf("the tool ran %d times, want 1", got)
	}

	if _, err := wf.Resume(ctx, approver(), *res.Suspension, res.State, flow.Approve); !errors.Is(err, flow.ErrReplay) {
		t.Fatalf("second resume err = %v, want ErrReplay", err)
	}
	if got := h.runs.Load(); got != 1 {
		t.Fatalf("replay ran the tool: %d executions", got)
	}
}

// An approval escalated on one replica and decided on another replica works,
// because both flows share the Engine's sealer and claim store.
func TestSuspensionResumesOnAnotherFlowWithTheSameEngine(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	res, err := h.flow(t).Run(ctx, requester(), caseState{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := h.flow(t).Resume(ctx, approver(), *res.Suspension, res.State, flow.Approve)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != flow.Done || !out.State.Executed {
		t.Fatalf("status = %v executed = %v", out.Status, out.State.Executed)
	}
	if got := h.runs.Load(); got != 1 {
		t.Fatalf("the tool ran %d times, want 1", got)
	}
}

func TestFromEngineRequiresAnEngine(t *testing.T) {
	if _, err := flow.FromEngine[caseState](nil, "refund"); !errors.Is(err, gb.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}
