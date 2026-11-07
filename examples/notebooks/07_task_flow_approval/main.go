// Notebook 07: a Task escalates inside a Flow. The model proposes, the Gate
// stops the write, a human approves, and the same action executes exactly once.
// Run: go run ./examples/notebooks/07_task_flow_approval
package main

import (
	"context"
	"errors"
	"fmt"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/examples/notebooks/notebook"
	"github.com/KirillYuzh/Gobellim/flow"
	"github.com/KirillYuzh/Gobellim/testkit"
)

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
	Status   string
}

// refundCase is the workflow state the application stores between Run and
// Resume. PendingAction is plain JSON, so it travels with the state.
type refundCase struct {
	OrderID  string
	Cents    int
	Pending  gobellim.PendingAction
	Executed bool
}

func main() {
	ctx := context.Background()
	requester := gobellim.Principal{UserID: "u-1", Tenant: "acme"}
	approver := gobellim.Principal{UserID: "m-1", Tenant: "acme"}

	var runs int
	refund := gobellim.Tool[refundArgs, refundResult]{
		Name:       "orders.refund",
		Operation:  gobellim.Write,
		Permission: "orders:refund",
		Target: func(p gobellim.Principal, a refundArgs) gobellim.Resource {
			return gobellim.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
		},
		Run: func(context.Context, gobellim.Principal, refundArgs) (refundResult, error) {
			runs++
			fmt.Println("   ↳ tool Run executed")
			return refundResult{RefundID: "rf_1", Status: "ok"}, nil
		},
	}

	model := testkit.NewFakeModel(
		testkit.Call("orders.refund", map[string]any{"OrderID": "<h1>", "Cents": 4900}),
	)
	engine, err := gobellim.New(
		gobellim.WithModel(model),
		gobellim.WithAuthorizer(testkit.AllowAll),
		gobellim.WithAudit(testkit.DiscardAudit()),
	)
	if err != nil {
		panic(err)
	}
	task, err := gobellim.NewTask[refundIn, refundOut](engine, gobellim.TaskConfig{
		Name:        "refund",
		Instruction: "Propose a refund when the caller asks for one.",
		Tools:       []gobellim.ToolDef{refund},
	})
	if err != nil {
		panic(err)
	}

	notebook.Cell("1. Flow делит с Engine всё, чем проверяет", `
flow.FromEngine берёт authorizer, audit, claim store, часы и ключ подписи у
Engine. Двух разных authorizer в одном use-case быть не должно: Gate проверял бы
вызывающего по одному ACL, а одобрение — по другому.`)

	wf, err := flow.FromEngine[refundCase](engine, "refund")
	if err != nil {
		panic(err)
	}
	wf.Step("propose", func(ctx context.Context, p gobellim.Principal, s *refundCase) error {
		res, err := task.Run(ctx, p, refundIn{TenantID: p.Tenant, OrderID: "A-1042"})
		if err != nil {
			return err
		}
		if res.Pending == nil {
			return errors.New("the task did not escalate")
		}
		// Карточка задачи становится карточкой шага: апрувер видит ровно то,
		// что Gate остановил.
		s.Pending = *res.Pending
		s.OrderID = "A-1042"
		s.Cents = 4900
		return nil
	})
	wf.Approve("manager", flow.ApproveConfig[refundCase]{
		Permission:       "refund:approve",
		ActionPermission: "orders:refund",
		FourEyes:         true,
		Card: func(_ context.Context, _ gobellim.Principal, s refundCase) (gobellim.ApprovalCard, error) {
			return s.Pending.Card, nil
		},
		Target: func(_ context.Context, p gobellim.Principal, s refundCase) (gobellim.Resource, error) {
			return gobellim.Resource{Type: "order", ID: s.OrderID, TenantID: p.Tenant}, nil
		},
	})
	wf.Step("execute", func(ctx context.Context, p gobellim.Principal, s *refundCase) error {
		// p — requester: шаги после Resume идут от его имени.
		approvedBy, ok := flow.ApproverFrom(ctx)
		if !ok {
			return errors.New("no approver in the context")
		}
		_, rec, err := task.ExecuteApproved(ctx, p, approvedBy, &s.Pending)
		if err != nil {
			return err
		}
		s.Executed = rec.Executed
		return nil
	})

	notebook.Cell("2. Run → Suspended", `
Модель предложила write с аргументом из untrusted-поля. Task вернул
PendingAction, Flow остановился на границе «manager», tool не вызывался.`)
	res, err := wf.Run(ctx, requester, refundCase{})
	if err != nil {
		panic(err)
	}
	notebook.Result("status: %v", res.Status)
	notebook.Result("tool runs before approval: %d", runs)
	notebook.Result("signed suspension: %v", len(res.Suspension.Proof) > 0)
	notebook.Result("card: %s / %s / digest=%s",
		res.Suspension.Card.Tool, res.Suspension.Card.Operation, res.Suspension.Card.Digest)
	notebook.Result("task digest == card digest: %v",
		res.Suspension.Card.Digest == res.State.Pending.Digest)

	notebook.Cell("3. Resume → Done, действие выполнено один раз", `
Апрувер m-1 отличается от requester u-1, права проверены, Suspension подписан,
nonce занят. ExecuteApproved заново прогоняет Gate и сверяет digest.`)
	out, err := wf.Resume(ctx, approver, *res.Suspension, res.State, flow.VerdictApprove)
	if err != nil {
		panic(err)
	}
	notebook.Result("status: %v, executed: %v, tool runs: %d", out.Status, out.State.Executed, runs)

	notebook.Cell("4. Повторный Resume → ErrReplay", `
Тот же nonce второй раз не выполнит шаг.`)
	_, err = wf.Resume(ctx, approver, *res.Suspension, res.State, flow.VerdictApprove)
	notebook.Result("replay detected: %v (errors.Is(ErrReplay)=%v)", err, errors.Is(err, flow.ErrReplay))

	notebook.Cell("5. Подделанная Suspension → ErrFlowDrift", `
Suspension подписан ключом Engine. Объект, который flow не выпускал, не
считается одобрением: иначе он был бы bearer-токеном, а nonce внутри него решает,
одноразово ли действие.`)
	forged := *res.Suspension
	forged.Proof = nil
	_, err = wf.Resume(ctx, approver, forged, out.State, flow.VerdictApprove)
	notebook.Result("forged suspension: %v", err)

	fmt.Println()
}
