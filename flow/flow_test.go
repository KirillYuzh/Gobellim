package flow

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
)

type refundState struct {
	OrderID  string
	Cents    int
	Executed bool
}

type authzFunc func(context.Context, gobellim.Principal, string, gobellim.Resource) (bool, error)

func (f authzFunc) Allowed(ctx context.Context, p gobellim.Principal, permission string, r gobellim.Resource) (bool, error) {
	return f(ctx, p, permission, r)
}

func perms(granted map[string]bool) gobellim.Authorizer {
	return authzFunc(func(_ context.Context, _ gobellim.Principal, permission string, _ gobellim.Resource) (bool, error) {
		return granted[permission], nil
	})
}

func defaultPerms() map[string]bool {
	return map[string]bool{"refund:approve": true, "orders:refund": true}
}

func requester() gobellim.Principal {
	return gobellim.Principal{UserID: "u-1", Tenant: "acme"}
}

func approver(id string) gobellim.Principal {
	return gobellim.Principal{UserID: id, Tenant: "acme"}
}

type silentAudit struct{}

func (silentAudit) Emit(context.Context, gobellim.Event) {}

func buildFlow(authz gobellim.Authorizer, store gobellim.ClaimStore, opts ...Option) *Flow[refundState] {
	base := []Option{
		WithAuthorizer(authz),
		WithStore(store),
		WithAudit(silentAudit{}),
		WithTTL(time.Hour),
	}
	base = append(base, opts...)
	f, err := New[refundState]("refund", base...)
	if err != nil {
		panic(err)
	}
	f.Step("prepare", func(_ context.Context, _ gobellim.Principal, s *refundState) error {
		s.OrderID = "A-1042"
		s.Cents = 4900
		return nil
	})
	f.Approve("manager", ApproveConfig[refundState]{
		When: func(_ context.Context, _ gobellim.Principal, s refundState) (bool, error) {
			return s.Cents > 0, nil
		},
		Permission:       "refund:approve",
		ActionPermission: "orders:refund",
		FourEyes:         true,
		TTL:              time.Hour,
		Card: func(_ context.Context, _ gobellim.Principal, s refundState) (gobellim.ApprovalCard, error) {
			args, err := json.Marshal(map[string]any{"order_id": s.OrderID, "cents": s.Cents})
			if err != nil {
				return gobellim.ApprovalCard{}, err
			}
			return gobellim.ApprovalCard{
				Tool:      "orders.refund",
				Operation: gobellim.Write,
				Args:      args,
			}, nil
		},
		Target: func(_ context.Context, p gobellim.Principal, s refundState) (gobellim.Resource, error) {
			return gobellim.Resource{Type: "order", ID: s.OrderID, TenantID: p.Tenant}, nil
		},
	})
	f.Step("execute", func(_ context.Context, _ gobellim.Principal, s *refundState) error {
		s.Executed = true
		return nil
	})
	return f
}

func newFlow(authz gobellim.Authorizer, opts ...Option) *Flow[refundState] {
	return buildFlow(authz, NewMemStore(), opts...)
}

func suspended(t *testing.T, f *Flow[refundState]) Result[refundState] {
	t.Helper()
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Suspended || res.Suspension == nil {
		t.Fatalf("status = %v, suspension = %+v", res.Status, res.Suspension)
	}
	return res
}

func resume(ctx context.Context, f *Flow[refundState], res Result[refundState], p gobellim.Principal, d Decision) (Result[refundState], error) {
	return f.Resume(ctx, p, *res.Suspension, res.State, d)
}

func TestSuspendAndResume(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res := suspended(t, f)
	card := res.Suspension.Card
	if card.Tool != "orders.refund" || card.Operation != gobellim.Write {
		t.Fatalf("card = %+v", card)
	}
	if card.RequiredPermission != "refund:approve" || !card.FourEyes || card.Digest == "" {
		t.Fatalf("card = %+v", card)
	}
	if res.State.Executed {
		t.Fatal("the post-approval step ran before approval")
	}

	out, err := resume(context.Background(), f, res, approver("m-1"), Approve)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != Done || !out.State.Executed {
		t.Fatalf("status = %v executed = %v", out.Status, out.State.Executed)
	}
}

func TestReject(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res := suspended(t, f)
	out, err := resume(context.Background(), f, res, approver("m-1"), Reject)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != Rejected || out.State.Executed {
		t.Fatalf("status = %v executed = %v", out.Status, out.State.Executed)
	}
}

func TestRejectConsumesTheNonce(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res := suspended(t, f)
	if _, err := resume(context.Background(), f, res, approver("m-1"), Reject); err != nil {
		t.Fatal(err)
	}
	if _, err := resume(context.Background(), f, res, approver("m-2"), Approve); !errors.Is(err, ErrReplay) {
		t.Fatalf("err = %v, want ErrReplay", err)
	}
}

func TestTamperedCard(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res := suspended(t, f)
	card := res.Suspension.Card
	card.Args = json.RawMessage(`{"order_id":"A-999","cents":4900}`)
	susp := *res.Suspension
	susp.Card = card
	_, err := f.Resume(context.Background(), approver("m-1"), susp, res.State, Approve)
	if !errors.Is(err, ErrFlowDrift) {
		t.Fatalf("err = %v, want ErrFlowDrift", err)
	}
}

func TestOtherTenant(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res := suspended(t, f)
	other := gobellim.Principal{UserID: "m-1", Tenant: "other"}
	_, err := resume(context.Background(), f, res, other, Approve)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	f := newFlow(perms(defaultPerms()), WithClock(func() time.Time { return now }), WithTTL(time.Minute))
	res := suspended(t, f)
	now = now.Add(2 * time.Hour)
	out, err := resume(context.Background(), f, res, approver("m-1"), Approve)
	if !errors.Is(err, ErrExpired) || out.Status != Expired {
		t.Fatalf("status = %v err = %v, want Expired/ErrExpired", out.Status, err)
	}
}

func TestRevokedActionPermission(t *testing.T) {
	f := newFlow(perms(map[string]bool{"refund:approve": true}))
	res := suspended(t, f)
	if _, err := resume(context.Background(), f, res, approver("m-1"), Approve); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestApproverPermission(t *testing.T) {
	f := newFlow(perms(map[string]bool{"orders:refund": true}))
	res := suspended(t, f)
	if _, err := resume(context.Background(), f, res, approver("m-1"), Approve); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestRejectRequiresApproverPermission(t *testing.T) {
	f := newFlow(perms(map[string]bool{"orders:refund": true}))
	res := suspended(t, f)
	if _, err := resume(context.Background(), f, res, approver("m-1"), Reject); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestFourEyes(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res := suspended(t, f)
	if _, err := resume(context.Background(), f, res, requester(), Approve); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestReplay(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res := suspended(t, f)
	if _, err := resume(context.Background(), f, res, approver("m-1"), Approve); err != nil {
		t.Fatal(err)
	}
	if _, err := resume(context.Background(), f, res, approver("m-2"), Approve); !errors.Is(err, ErrReplay) {
		t.Fatalf("err = %v, want ErrReplay", err)
	}
}

func TestParallelResume(t *testing.T) {
	store := NewMemStore()
	f := buildFlow(perms(defaultPerms()), store)
	res := suspended(t, f)

	var executions atomic.Int32
	// The execute step is the only one that writes, so count it by replacing
	// the flow with a fresh one whose last step counts.
	f2, err := New[refundState]("refund",
		WithAuthorizer(perms(defaultPerms())),
		WithStore(store),
		WithAudit(silentAudit{}),
		WithTTL(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	f2.Step("prepare", func(_ context.Context, _ gobellim.Principal, s *refundState) error {
		s.OrderID = "A-1042"
		s.Cents = 4900
		return nil
	})
	f2.Approve("manager", ApproveConfig[refundState]{
		Permission:       "refund:approve",
		ActionPermission: "orders:refund",
		TTL:              time.Hour,
		Card: func(_ context.Context, _ gobellim.Principal, s refundState) (gobellim.ApprovalCard, error) {
			return gobellim.ApprovalCard{
				Tool:      "orders.refund",
				Operation: gobellim.Write,
				Args:      json.RawMessage(`{"order_id":"A-1042","cents":4900}`),
			}, nil
		},
		Target: func(_ context.Context, p gobellim.Principal, s refundState) (gobellim.Resource, error) {
			return gobellim.Resource{Type: "order", ID: s.OrderID, TenantID: p.Tenant}, nil
		},
	})
	f2.Step("execute", func(_ context.Context, _ gobellim.Principal, _ *refundState) error {
		executions.Add(1)
		return nil
	})
	res2 := suspended(t, f2)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	statuses := make([]Status, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := resume(context.Background(), f2, res2, approver("m-1"), Approve)
			errs[i], statuses[i] = err, out.Status
		}()
	}
	wg.Wait()

	dones, replays := 0, 0
	for i := range 2 {
		switch {
		case errs[i] == nil && statuses[i] == Done:
			dones++
		case errors.Is(errs[i], ErrReplay):
			replays++
		default:
			t.Fatalf("unexpected result %d: status=%v err=%v", i, statuses[i], errs[i])
		}
	}
	if dones != 1 || replays != 1 {
		t.Fatalf("dones=%d replays=%d, want 1/1", dones, replays)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("executions = %d, want 1", got)
	}
	_ = res
}

func TestResumeRejectsForeignTenantTarget(t *testing.T) {
	f, err := New[refundState]("refund",
		WithAuthorizer(perms(defaultPerms())),
		WithStore(NewMemStore()),
		WithAudit(silentAudit{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.Approve("manager", ApproveConfig[refundState]{
		Permission: "refund:approve",
		FourEyes:   true,
		Card: func(context.Context, gobellim.Principal, refundState) (gobellim.ApprovalCard, error) {
			return gobellim.ApprovalCard{Tool: "orders.refund", Operation: gobellim.Write}, nil
		},
		Target: func(context.Context, gobellim.Principal, refundState) (gobellim.Resource, error) {
			return gobellim.Resource{Type: "order", ID: "A-1", TenantID: "other"}, nil
		},
	})
	res := suspended(t, f)
	if _, err := f.Resume(context.Background(), approver("m-1"), *res.Suspension, res.State, Approve); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("foreign tenant target err = %v, want ErrUnauthorized", err)
	}
}

func TestFlowDrift(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res := suspended(t, f)

	badFlow := res.Suspension.Card
	badFlow.Flow = "other"
	suspFlow := *res.Suspension
	suspFlow.Card = badFlow
	if _, err := f.Resume(context.Background(), approver("m-1"), suspFlow, res.State, Approve); !errors.Is(err, ErrFlowDrift) {
		t.Fatalf("flow drift err = %v", err)
	}

	badStep := res.Suspension.Card
	badStep.Step = "missing"
	suspStep := *res.Suspension
	suspStep.Card = badStep
	if _, err := f.Resume(context.Background(), approver("m-1"), suspStep, res.State, Approve); !errors.Is(err, ErrFlowDrift) {
		t.Fatalf("step drift err = %v", err)
	}
}

func TestConditionalApprovalSkipped(t *testing.T) {
	f, err := New[refundState]("refund",
		WithAuthorizer(perms(defaultPerms())),
		WithStore(NewMemStore()),
		WithAudit(silentAudit{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.Step("prepare", func(_ context.Context, _ gobellim.Principal, s *refundState) error {
		s.Cents = 0
		return nil
	})
	f.Approve("manager", ApproveConfig[refundState]{
		When: func(_ context.Context, _ gobellim.Principal, s refundState) (bool, error) { return s.Cents > 0, nil },
		Card: func(context.Context, gobellim.Principal, refundState) (gobellim.ApprovalCard, error) {
			return gobellim.ApprovalCard{Tool: "orders.refund", Operation: gobellim.Write}, nil
		},
	})
	f.Step("execute", func(_ context.Context, _ gobellim.Principal, s *refundState) error {
		s.Executed = true
		return nil
	})
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Done || !res.State.Executed {
		t.Fatalf("status = %v executed = %v", res.Status, res.State.Executed)
	}
}

func TestUnknownDecisionIsNotApproval(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res := suspended(t, f)
	if _, err := resume(context.Background(), f, res, approver("m-1"), DecisionUnknown); !errors.Is(err, ErrDecision) {
		t.Fatalf("err = %v, want ErrDecision", err)
	}
	if out, err := resume(context.Background(), f, res, approver("m-1"), Approve); err != nil || out.Status != Done {
		t.Fatalf("a valid approval after DecisionUnknown: status=%v err=%v", out.Status, err)
	}
}

func TestDefaultStorePreventsReplay(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res := suspended(t, f)
	if _, err := resume(context.Background(), f, res, approver("m-1"), Approve); err != nil {
		t.Fatal(err)
	}
	if _, err := resume(context.Background(), f, res, approver("m-2"), Approve); !errors.Is(err, ErrReplay) {
		t.Fatalf("err = %v, want ErrReplay", err)
	}
}

func TestDuplicateStepIDIsRejected(t *testing.T) {
	f, err := New[refundState]("refund",
		WithAuthorizer(perms(defaultPerms())),
		WithAudit(silentAudit{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.Step("same", func(context.Context, gobellim.Principal, *refundState) error { return nil })
	f.Step("same", func(context.Context, gobellim.Principal, *refundState) error { return nil })
	if f.Err() == nil {
		t.Fatal("duplicate step id was accepted")
	}
}

// A suspension is a bearer object unless it is signed: whoever supplies one
// chooses the nonce, and the nonce is what makes an approval single-use.
func TestSuspensionIsSigned(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res := suspended(t, f)

	forged := *res.Suspension
	forged.Requester = gobellim.Principal{UserID: "u-2", Tenant: "acme"}
	if _, err := f.Resume(context.Background(), approver("m-1"), forged, res.State, Approve); !errors.Is(err, ErrFlowDrift) {
		t.Fatalf("forged requester err = %v, want ErrFlowDrift", err)
	}

	stripped := *res.Suspension
	stripped.Proof = nil
	if _, err := f.Resume(context.Background(), approver("m-1"), stripped, res.State, Approve); !errors.Is(err, ErrFlowDrift) {
		t.Fatalf("missing proof err = %v, want ErrFlowDrift", err)
	}

	if _, err := f.Resume(context.Background(), approver("m-1"), *res.Suspension, res.State, Approve); err != nil {
		t.Fatalf("an untouched suspension must resume: %v", err)
	}
}

// A declared target must name its tenant. Filling it from the requester would
// make the tenant comparison true by construction.
func TestApprovalTargetWithoutTenantIsRejected(t *testing.T) {
	f, err := New[refundState]("refund",
		WithAuthorizer(perms(defaultPerms())),
		WithStore(NewMemStore()),
		WithAudit(silentAudit{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.Step("prepare", func(_ context.Context, _ gobellim.Principal, s *refundState) error {
		s.OrderID = "A-1"
		return nil
	})
	f.Approve("manager", ApproveConfig[refundState]{
		Permission: "refund:approve",
		Card: func(context.Context, gobellim.Principal, refundState) (gobellim.ApprovalCard, error) {
			return gobellim.ApprovalCard{Tool: "orders.refund", Operation: gobellim.Write}, nil
		},
		Target: func(context.Context, gobellim.Principal, refundState) (gobellim.Resource, error) {
			return gobellim.Resource{Type: "order", ID: "A-1"}, nil
		},
	})
	res := suspended(t, f)
	if _, err := resume(context.Background(), f, res, approver("m-1"), Approve); !errors.Is(err, ErrFlowDrift) {
		t.Fatalf("err = %v, want ErrFlowDrift", err)
	}
}

// stubModel satisfies gobellim.Model for tests that never reach the model.
type stubModel struct{}

func (stubModel) Chat(context.Context, gobellim.Request) (gobellim.Response, error) {
	return gobellim.Response{}, errors.New("stub model")
}

// FromEngine exists so a flow that resumes a Task authorizes the approver with
// the same adapter the Gate uses. A second, weaker authorizer is the kind of
// mistake that ships quietly.
func TestFromEngineSharesTheAuthorizer(t *testing.T) {
	engine, err := gobellim.New(
		gobellim.WithModel(stubModel{}),
		gobellim.WithAuthorizer(perms(map[string]bool{})),
		gobellim.WithAudit(silentAudit{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	f, err := FromEngine[refundState](engine, "refund")
	if err != nil {
		t.Fatal(err)
	}
	f.Step("prepare", func(_ context.Context, _ gobellim.Principal, s *refundState) error {
		s.OrderID = "A-1"
		s.Cents = 1
		return nil
	})
	f.Approve("manager", ApproveConfig[refundState]{
		Permission: "refund:approve",
		Card: func(context.Context, gobellim.Principal, refundState) (gobellim.ApprovalCard, error) {
			return gobellim.ApprovalCard{Tool: "orders.refund", Operation: gobellim.Write}, nil
		},
	})
	res := suspended(t, f)
	if _, err := resume(context.Background(), f, res, approver("m-1"), Approve); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized from the engine's authorizer", err)
	}
}
