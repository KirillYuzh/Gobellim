package gobellim_test

import (
	"context"
	"fmt"
	"log/slog"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/testkit"
)

// The types below are the ones in the README quickstart. This example exists so
// the documented call path is compiled and executed by `go test`: a rename that
// breaks the quickstart breaks the build instead of the reader.
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

func Example() {
	// Your existing business function, with the operation it performs.
	refund := gobellim.Tool[RefundArgs, RefundResult]{
		Name:       "orders.refund",
		Operation:  gobellim.Write,
		Permission: "orders:refund",
		Target: func(p gobellim.Principal, a RefundArgs) gobellim.Resource {
			return gobellim.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
		},
		Run: func(context.Context, gobellim.Principal, RefundArgs) (RefundResult, error) {
			return RefundResult{RefundID: "rf_1", Status: "ok"}, nil
		},
	}

	// A scripted model stands in for the provider: it proposes the refund using
	// the handle it was shown, not the order number.
	model := testkit.NewFakeModel(
		testkit.Call("orders.refund", map[string]any{"OrderID": "<h1>", "Cents": 4900}),
	)

	engine, err := gobellim.New(
		gobellim.WithModel(model),
		gobellim.WithAuthorizer(testkit.AllowAll),
		gobellim.WithAudit(gobellim.SlogSink{Logger: slog.New(slog.DiscardHandler)}),
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

	ctx := context.Background()
	requester := gobellim.Principal{UserID: "u-1", Tenant: "acme"}

	res, err := task.Run(ctx, requester, TicketInput{
		TenantID: "acme",
		TicketID: "T-100",
		Body:     "please refund order A-100",
		OrderID:  "A-100",
	})
	if err != nil {
		panic(err)
	}

	// The order number came from an untrusted input field, so a write is not
	// executed: it becomes a card for a human.
	fmt.Println("approval required:", res.Status == gobellim.TaskApprovalRequired)
	fmt.Println("card:", res.Pending.Card.Tool, res.Pending.Card.Operation)

	// After a real approval the Gate runs again and the handler executes.
	_, rec, err := task.ExecuteApproved(ctx, requester, gobellim.Principal{UserID: "m-1", Tenant: "acme"}, res.Pending)
	fmt.Println("executed:", rec.Executed, "err:", err)

	// Output:
	// approval required: true
	// card: orders.refund write
	// executed: true err: <nil>
}
