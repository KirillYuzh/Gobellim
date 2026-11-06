// Notebook 01: one use case, typed input and output, PII masking, and the Gate.
// Run: go run ./examples/notebooks/01_task_mock
package main

import (
	"context"
	"fmt"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/examples/notebooks/notebook"
	"github.com/KirillYuzh/Gobellim/testkit"
)

type TicketInput struct {
	TenantID string `gobellim:"source=principal,required"`
	TicketID string `gobellim:"source=application,required"`
	Body     string `gobellim:"required,sensitivity=patterns"`
	OrderID  string `gobellim:"sensitivity=secret"`
}

type TriageOutput struct {
	Category string `gobellim:"required,oneof=billing|technical|other"`
	Reply    string `gobellim:"resolve"`
}

type RefundArgs struct {
	OrderID string `gobellim:"required,ground"`
	Cents   int    `gobellim:"min=1,max=5000"`
}

type RefundResult struct {
	RefundID string
	Status   string
}

func main() {
	ctx := context.Background()
	principal := gobellim.Principal{UserID: "u-1", Tenant: "acme"}

	notebook.Cell("1. Вход — это данные, а не инструкции", `
Body размечен как sensitivity=patterns: e-mail заменяется на handle <h1>.
OrderID размечен как sensitivity=secret: модель видит <h2>, а не сам номер.
source=principal требует, чтобы TenantID совпадал с Principal.`)

	input := TicketInput{
		TenantID: "acme",
		TicketID: "T-100",
		Body:     "Please refund my order for alice@example.com",
		OrderID:  "A-100",
	}

	var executed int
	refund := gobellim.Tool[RefundArgs, RefundResult]{
		Name:       "orders.refund",
		Operation:  gobellim.Write,
		Permission: "orders:refund",
		Target: func(p gobellim.Principal, a RefundArgs) gobellim.Resource {
			return gobellim.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
		},
		Run: func(context.Context, gobellim.Principal, RefundArgs) (RefundResult, error) {
			executed++
			return RefundResult{RefundID: "rf_1", Status: "ok"}, nil
		},
	}

	// The mock model plays exactly the script an attacker would try: use the
	// handle it was shown for the order and ask for a refund.
	model := testkit.NewFakeModel(
		testkit.Call("orders.refund", map[string]any{"OrderID": "<h2>", "Cents": 4900}),
	)
	rec := &testkit.Recorder{}
	engine, err := gobellim.New(
		gobellim.WithModel(model),
		gobellim.WithAuthorizer(testkit.AllowAll),
		gobellim.WithAudit(rec),
	)
	if err != nil {
		panic(err)
	}

	task, err := gobellim.NewTask[TicketInput, TriageOutput](engine, gobellim.TaskConfig{
		Name:        "ticket.triage",
		Instruction: "Classify the ticket and propose a reply.",
		Tools:       []gobellim.ToolDef{refund},
	})
	if err != nil {
		panic(err)
	}

	res, err := task.Run(ctx, principal, input)
	if err != nil {
		panic(err)
	}

	notebook.Result("prompt sent to the model:\n%s", model.Calls()[0].Messages[1].Content)
	notebook.Result("tool executed: %d", executed)
	notebook.Result("task status: %v", res.Status)
	if res.Pending != nil {
		notebook.Cell("2. Gate остановил write", `
Аргумент OrderID пришёл из untrusted-поля, поэтому Write не разрешён молча.
Возвращается approval card; tool handler не вызывался.`)
		notebook.Result("tool: %s, operation: %s", res.Pending.Card.Tool, res.Pending.Card.Operation)
		notebook.Result("args (resolved for the approver): %s", string(res.Pending.Card.Args))
		notebook.Result("digest: %s", res.Pending.Card.Digest)
		for _, p := range res.Pending.Card.Provenance {
			notebook.Result("provenance: %s ← %s (%s)", p.Arg, p.Source, p.Integrity)
		}
	}

	notebook.Cell("3. Что произошло", `
Модель не получила ни e-mail, ни номер заказа в открытом виде.
Даже если она подставит handle в write-инструмент, Gate не выполнит действие
без одобрения из доверенного источника. После одобрения приложение вызывает
Task.ExecuteApproved с той же карточкой: Gate перепроверит tenant, ACL,
бизнес-валидацию и digest. Следующий notebook показывает остальные отказы.`)
	fmt.Println()
}
