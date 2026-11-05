package flow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/testkit"
)

type refundState struct {
	OrderID  string
	Cents    int
	Executed bool
}

type authzFunc func(ctx context.Context, p gobellim.Principal, permission string, resource gobellim.Resource) (bool, error)

func (f authzFunc) Allowed(ctx context.Context, p gobellim.Principal, permission string, resource gobellim.Resource) (bool, error) {
	return f(ctx, p, permission, resource)
}

func perms(m map[string]bool) gobellim.Authorizer {
	return authzFunc(func(_ context.Context, _ gobellim.Principal, permission string, _ gobellim.Resource) (bool, error) {
		v, ok := m[permission]
		return ok && v, nil
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

func testSealer(t *testing.T) *Sealer {
	t.Helper()
	s, err := NewSealer("test-key", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func buildFlow(authz gobellim.Authorizer, sealer *Sealer, store Store, opts ...Option) *Flow[refundState] {
	base := []Option{
		WithAuthorizer(authz),
		WithSealer(sealer),
		WithStore(store),
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
				Tool:   "orders.refund",
				Effect: gobellim.Write,
				Args:   args,
			}, nil
		},
		Resource: func(_ context.Context, p gobellim.Principal, s refundState) (gobellim.Resource, error) {
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
	return buildFlow(authz, mustSealer(), NewMemStore(), opts...)
}

func mustSealer() *Sealer {
	s, err := NewSealer("test-key", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		panic(err)
	}
	return s
}

func TestSuspendAndResume(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Suspended {
		t.Fatalf("status = %v, want Suspended", res.Status)
	}
	if res.Suspension == nil || res.Suspension.Token == "" {
		t.Fatal("suspension has no token")
	}
	card := res.Suspension.Card
	if card.Tool != "orders.refund" || card.Effect != gobellim.Write {
		t.Fatalf("card = %+v", card)
	}
	if card.RequiredPermission != "refund:approve" {
		t.Fatalf("required permission = %q", card.RequiredPermission)
	}
	if !card.FourEyes || card.Digest == "" {
		t.Fatalf("card = %+v", card)
	}
	if res.State.Executed {
		t.Fatal("the post-approval step ran before approval")
	}

	out, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != Done {
		t.Fatalf("status = %v, want Done", out.Status)
	}
	if !out.State.Executed {
		t.Fatal("the post-approval step did not run")
	}
}

func TestReject(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Reject)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != Rejected {
		t.Fatalf("status = %v, want Rejected", out.Status)
	}
	if out.State.Executed {
		t.Fatal("rejected action executed")
	}
}

func TestTamperedToken(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	token := res.Suspension.Token
	tampered := token[:len(token)-1] + "A"
	if tampered == token {
		tampered = token + "A"
	}
	if _, err := f.Resume(context.Background(), approver("m-1"), tampered, Approve); !errors.Is(err, ErrContinuation) {
		t.Fatalf("err = %v, want ErrContinuation", err)
	}
}

func TestOtherTenant(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	other := gobellim.Principal{UserID: "m-1", Tenant: "other"}
	if _, err := f.Resume(context.Background(), other, res.Suspension.Token, Approve); !errors.Is(err, ErrContinuation) {
		t.Fatalf("err = %v, want ErrContinuation", err)
	}
}

func TestExpired(t *testing.T) {
	now := time.Now()
	f := newFlow(perms(defaultPerms()), WithClock(func() time.Time { return now }))
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	out, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
	if out.Status != Expired {
		t.Fatalf("status = %v, want Expired", out.Status)
	}
}

func TestRevokedActionPermission(t *testing.T) {
	f := newFlow(perms(map[string]bool{"refund:approve": true, "orders:refund": false}))
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if out.State.Executed {
		t.Fatal("revoked action executed")
	}
}

func TestApproverPermission(t *testing.T) {
	f := newFlow(perms(map[string]bool{"refund:approve": false, "orders:refund": true}))
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestRejectRequiresApproverPermission(t *testing.T) {
	f := newFlow(perms(map[string]bool{"refund:approve": false, "orders:refund": true}))
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Reject); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestFourEyes(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Resume(context.Background(), requester(), res.Suspension.Token, Approve); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestReplay(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve); !errors.Is(err, ErrReplay) {
		t.Fatalf("err = %v, want ErrReplay", err)
	}
}

func TestParallelResume(t *testing.T) {
	store := NewMemStore()
	sealer := testSealer(t)
	var executions atomic.Int32
	f, err := New[refundState]("refund",
		WithAuthorizer(perms(defaultPerms())),
		WithSealer(sealer),
		WithStore(store),
		WithTTL(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.Step("prepare", func(_ context.Context, _ gobellim.Principal, s *refundState) error {
		s.OrderID = "A-1042"
		s.Cents = 4900
		return nil
	})
	f.Approve("manager", ApproveConfig[refundState]{
		Permission:       "refund:approve",
		ActionPermission: "orders:refund",
		TTL:              time.Hour,
		Card: func(_ context.Context, _ gobellim.Principal, s refundState) (gobellim.ApprovalCard, error) {
			return gobellim.ApprovalCard{
				Tool:   "orders.refund",
				Effect: gobellim.Write,
				Args:   json.RawMessage(`{"order_id":"A-1042","cents":4900}`),
			}, nil
		},
		Resource: func(_ context.Context, p gobellim.Principal, s refundState) (gobellim.Resource, error) {
			return gobellim.Resource{Type: "order", ID: s.OrderID, TenantID: p.Tenant}, nil
		},
	})
	f.Step("execute", func(_ context.Context, _ gobellim.Principal, _ *refundState) error {
		executions.Add(1)
		return nil
	})

	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	statuses := make([]Status, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve)
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
}

func TestFlowDrift(t *testing.T) {
	sealer := testSealer(t)
	f := buildFlow(perms(defaultPerms()), sealer, NewMemStore(), Version(1))
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	f2, err := New[refundState]("refund",
		WithAuthorizer(perms(defaultPerms())),
		WithSealer(sealer),
		Version(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f2.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve); !errors.Is(err, ErrFlowDrift) {
		t.Fatalf("err = %v, want ErrFlowDrift", err)
	}
}

func TestContinuationTooLarge(t *testing.T) {
	f := newFlow(perms(defaultPerms()), WithMaxTokenBytes(10))
	if _, err := f.Run(context.Background(), requester(), refundState{}); !errors.Is(err, ErrContinuationTooLarge) {
		t.Fatalf("err = %v, want ErrContinuationTooLarge", err)
	}
}

func TestConditionalApprovalSkipped(t *testing.T) {
	f, err := New[refundState]("refund",
		WithAuthorizer(perms(defaultPerms())),
		WithSealer(mustSealer()),
		WithTTL(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.Approve("manager", ApproveConfig[refundState]{
		When:       func(_ context.Context, _ gobellim.Principal, _ refundState) (bool, error) { return false, nil },
		Permission: "refund:approve",
		Card: func(_ context.Context, _ gobellim.Principal, _ refundState) (gobellim.ApprovalCard, error) {
			return gobellim.ApprovalCard{Tool: "orders.refund"}, nil
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
		t.Fatalf("status=%v executed=%v", res.Status, res.State.Executed)
	}
}

func TestSealer(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	s, err := NewSealer("k1", key)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Seal([]byte("hello"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := s.Open(token, []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "hello" {
		t.Fatalf("plain = %q", plain)
	}
	if _, err := s.Open(token, []byte("other")); err == nil {
		t.Fatal("opened a token with the wrong AAD")
	}

	rotated, err := NewSealer("k2", bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.WithKey("k1", key); err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.Open(token, []byte("aad")); err != nil {
		t.Fatalf("rotation failed: %v", err)
	}

	if _, err := NewSealer("short", []byte("short")); err == nil {
		t.Fatal("accepted a short key")
	}
}

func TestMalformedToken(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	if _, err := f.Resume(context.Background(), approver("m-1"), "", Approve); !errors.Is(err, ErrContinuation) {
		t.Fatalf("err = %v, want ErrContinuation", err)
	}
}

func TestUnknownDecisionIsNotApproval(t *testing.T) {
	f := newFlow(perms(defaultPerms()))
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Decision(9)); !errors.Is(err, ErrDecision) {
		t.Fatalf("err = %v, want ErrDecision", err)
	}
	if _, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, DecisionUnknown); !errors.Is(err, ErrDecision) {
		t.Fatalf("zero decision approved an action: %v", err)
	}
}

func TestDefaultStorePreventsReplay(t *testing.T) {
	f := buildFlow(perms(defaultPerms()), mustSealer(), nil)
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve); !errors.Is(err, ErrReplay) {
		t.Fatalf("err = %v, want ErrReplay", err)
	}
}

func TestStepDrift(t *testing.T) {
	sealer := mustSealer()
	f := buildFlow(perms(defaultPerms()), sealer, NewMemStore())
	res, err := f.Run(context.Background(), requester(), refundState{})
	if err != nil {
		t.Fatal(err)
	}

	other, err := New[refundState]("refund",
		WithAuthorizer(perms(defaultPerms())),
		WithSealer(sealer),
		WithTTL(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	other.Step("prepare", func(context.Context, gobellim.Principal, *refundState) error { return nil })
	other.Approve("renamed", ApproveConfig[refundState]{
		Card: func(context.Context, gobellim.Principal, refundState) (gobellim.ApprovalCard, error) {
			return gobellim.ApprovalCard{Tool: "orders.refund"}, nil
		},
	})

	if _, err := other.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve); !errors.Is(err, ErrFlowDrift) {
		t.Fatalf("err = %v, want ErrFlowDrift", err)
	}
}

func TestMemStoreExpiredNonceCanBeClaimedAgain(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	if ok, err := store.Claim(ctx, "n", time.Now().Add(-time.Second)); err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if ok, err := store.Claim(ctx, "n", time.Now().Add(time.Hour)); err != nil || !ok {
		t.Fatalf("expired nonce was not reusable: ok=%v err=%v", ok, err)
	}
	if ok, _ := store.Claim(ctx, "n", time.Now().Add(time.Hour)); ok {
		t.Fatal("live nonce was claimed twice")
	}
	store.Forget(time.Now().Add(2 * time.Hour))
	if ok, _ := store.Claim(ctx, "n", time.Now().Add(time.Hour)); !ok {
		t.Fatal("Forget did not drop the expired nonce")
	}
}

type bridgeIn struct {
	TenantID string `gobellim:"source=principal,required"`
	OrderID  string `gobellim:"sensitivity=secret,required"`
}

type bridgeOut struct {
	OK bool
}

type bridgeArgs struct {
	OrderID string `gobellim:"required,ground"`
}

type bridgeResult struct {
	RefundID string
}

type bridgeState struct {
	Pending  gobellim.PendingAction
	Executed bool
}

// TestTaskApprovalInsideFlow is the end-to-end contract: a Task escalates, flow
// seals the PendingAction, a human approves, and the exact action executes
// through the Gate once.
func TestTaskApprovalInsideFlow(t *testing.T) {
	var executed int
	refund := gobellim.Tool[bridgeArgs, bridgeResult]{
		Name:   "orders.refund",
		Effect: gobellim.Write,
		Resource: func(p gobellim.Principal, _ bridgeArgs) gobellim.Resource {
			return gobellim.Resource{Type: "order", ID: "A-1042", TenantID: p.Tenant}
		},
		Permission: "orders:refund",
		Run: func(context.Context, gobellim.Principal, bridgeArgs) (bridgeResult, error) {
			executed++
			return bridgeResult{RefundID: "r-1"}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.refund", map[string]any{"OrderID": "<h1>"}),
		testkit.Reply(`{"OK":true}`),
	)
	engine, err := gobellim.New(
		gobellim.WithModel(model),
		gobellim.WithAuthorizer(perms(defaultPerms())),
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := gobellim.NewTask[bridgeIn, bridgeOut](engine, gobellim.TaskConfig{
		Name:        "refund.propose",
		Instruction: "propose a refund",
		Tools:       []gobellim.ToolDef{refund},
	})
	if err != nil {
		t.Fatal(err)
	}

	f, err := New[bridgeState]("refund-bridge",
		WithAuthorizer(perms(defaultPerms())),
		WithSealer(testSealer(t)),
		WithTTL(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.Step("propose", func(ctx context.Context, p gobellim.Principal, s *bridgeState) error {
		res, err := task.Run(ctx, p, bridgeIn{TenantID: p.Tenant, OrderID: "A-1042"})
		if err != nil {
			return err
		}
		if res.Pending == nil {
			return errors.New("the model did not propose an action")
		}
		s.Pending = *res.Pending
		return nil
	})
	f.ApprovePending("manager", func(s bridgeState) *gobellim.PendingAction { return &s.Pending }, ApproveConfig[bridgeState]{
		Permission:       "refund:approve",
		ActionPermission: "orders:refund",
		Resource: func(_ context.Context, p gobellim.Principal, _ bridgeState) (gobellim.Resource, error) {
			return gobellim.Resource{Type: "order", ID: "A-1042", TenantID: p.Tenant}, nil
		},
	})
	f.Step("execute", func(ctx context.Context, p gobellim.Principal, s *bridgeState) error {
		approver, ok := ApproverFrom(ctx)
		if !ok {
			return errors.New("no approver in the context")
		}
		_, rec, err := task.ExecuteApproved(ctx, p, approver, &s.Pending)
		if err != nil {
			return err
		}
		if !rec.Executed {
			return errors.New("the approved action did not run")
		}
		s.Executed = true
		return nil
	})

	res, err := f.Run(context.Background(), requester(), bridgeState{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Suspended {
		t.Fatalf("status = %v", res.Status)
	}
	if executed != 0 {
		t.Fatal("the escalated action ran before approval")
	}

	out, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != Done || !out.State.Executed || executed != 1 {
		t.Fatalf("status=%v state=%+v executed=%d", out.Status, out.State, executed)
	}
	if _, err := f.Resume(context.Background(), approver("m-1"), res.Suspension.Token, Approve); !errors.Is(err, ErrReplay) {
		t.Fatalf("err = %v, want ErrReplay", err)
	}
	if executed != 1 {
		t.Fatalf("executed = %d, want 1", executed)
	}
}

func TestResumeRejectsOversizedToken(t *testing.T) {
	f, err := New[refundState]("refund",
		WithAuthorizer(perms(defaultPerms())),
		WithSealer(mustSealer()),
		WithMaxTokenBytes(64),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.Step("prepare", func(context.Context, gobellim.Principal, *refundState) error { return nil })
	f.Approve("manager", ApproveConfig[refundState]{
		Card: func(context.Context, gobellim.Principal, refundState) (gobellim.ApprovalCard, error) {
			return gobellim.ApprovalCard{Tool: "orders.refund"}, nil
		},
	})

	token := strings.Repeat("A", 1<<20)
	if _, err := f.Resume(context.Background(), approver("m-1"), token, Approve); !errors.Is(err, ErrContinuation) {
		t.Fatalf("err = %v, want ErrContinuation", err)
	}
}
