package gobellim_test

import (
	"context"
	"errors"
	"testing"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/testkit"
)

// trustedIn has no untrusted field: every value comes from the application or
// the principal.
type trustedIn struct {
	TenantID string `gobellim:"source=principal,required"`
	OrderID  string `gobellim:"source=application,required"`
}

func TestAnswerOfTrustedInputIsDerived(t *testing.T) {
	model := testkit.NewFakeModel(testkit.Reply(validTriage))
	e := newEngine(t, model, testkit.AllowAll, nil)
	task, err := gobellim.NewTask[trustedIn, triageOut](e, gobellim.TaskConfig{
		Name: "trusted", Instruction: "Classify.",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := task.Run(context.Background(), principal(), trustedIn{TenantID: "acme", OrderID: "A-1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Label.Integrity != gobellim.Derived {
		t.Fatalf("integrity = %v, want derived", res.Label.Integrity)
	}
	if res.Label.Sensitivity != gobellim.Public {
		t.Fatalf("sensitivity = %v, want public", res.Label.Sensitivity)
	}
	if len(res.Label.Sources) == 0 {
		t.Fatal("the answer has no provenance at all")
	}
}

// A ticket body is caller-supplied text. The answer that was written after
// reading it is not evidence, no matter how valid its JSON is.
func TestAnswerOfUntrustedInputIsUntrusted(t *testing.T) {
	model := testkit.NewFakeModel(testkit.Reply(validTriage))
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), nil)

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Label.Integrity != gobellim.Untrusted {
		t.Fatalf("integrity = %v, want untrusted", res.Label.Integrity)
	}
	if res.Label.Sensitivity != gobellim.Patterns {
		t.Fatalf("sensitivity = %v, want patterns, the declared level of Body", res.Label.Sensitivity)
	}
}

// The model only ever sees a handle, but the answer can paste the value back in
// a resolve field, so the answer is as sensitive as the value behind the handle.
func TestSecretInputLabelsTheAnswer(t *testing.T) {
	model := testkit.NewFakeModel(testkit.Reply(validTriage))
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), nil)

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello", RequestedOrder: "4111 1111 1111 1111",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Label.Sensitivity != gobellim.Secret {
		t.Fatalf("sensitivity = %v, want secret", res.Label.Sensitivity)
	}
}

// A read tool puts external data into the context. The answer inherits both its
// sensitivity and its untrusted integrity, which is what egress needs to know.
func TestReadResultLabelsTheAnswer(t *testing.T) {
	read := gobellim.Tool[readArgs, secretResult]{
		Name:      "customer.read",
		Operation: gobellim.Read,
		Target:    testResource[readArgs],
		Run: func(context.Context, gobellim.Principal, readArgs) (secretResult, error) {
			return secretResult{Card: "4111 1111 1111 1111", Email: "a@b.co"}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("customer.read", readArgs{Query: "order"}),
		testkit.Reply(validTriage),
	)
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), []gobellim.ToolDef{read})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Label.Sensitivity != gobellim.Secret {
		t.Fatalf("sensitivity = %v, want secret", res.Label.Sensitivity)
	}
	var toolSource bool
	for _, s := range res.Label.Sources {
		if s.Kind == "tool" && s.ID == "customer.read" {
			toolSource = true
		}
	}
	if !toolSource {
		t.Fatalf("the answer does not name the tool it read: %+v", res.Label.Sources)
	}
}

func TestEgressCanRefuseTheAnswer(t *testing.T) {
	var calls int
	model := testkit.NewFakeModel(testkit.Reply(validTriage))
	rec := &testkit.Recorder{}
	e := newEngine(t, model, testkit.AllowAll, rec)
	task, err := gobellim.NewTask[ticketIn, triageOut](e, gobellim.TaskConfig{
		Name:        "ticket.triage",
		Instruction: "Classify.",
		Egress: func(_ context.Context, _ gobellim.Principal, l gobellim.Label) error {
			calls++
			if l.Sensitivity > gobellim.Public {
				return errors.New("the answer may repeat a sensitive value")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello",
	})
	if !errors.Is(err, gobellim.ErrEgressDenied) {
		t.Fatalf("err = %v, want ErrEgressDenied", err)
	}
	if calls != 1 {
		t.Fatalf("egress calls = %d, want 1", calls)
	}
	if res.Status != gobellim.TaskFailed {
		t.Fatalf("status = %v, want failed", res.Status)
	}
	if res.Output.Reply != "" {
		t.Fatalf("a refused answer reached the caller: %q", res.Output.Reply)
	}
	if rec.Count(gobellim.EventTaskFailed) != 1 {
		t.Fatalf("a refused answer was not audited: %+v", rec.Events())
	}
}

func TestEgressRunsOnAnAnswerItAccepts(t *testing.T) {
	var got gobellim.Label
	model := testkit.NewFakeModel(testkit.Reply(validTriage))
	e := newEngine(t, model, testkit.AllowAll, nil)
	task, err := gobellim.NewTask[trustedIn, triageOut](e, gobellim.TaskConfig{
		Name:        "trusted",
		Instruction: "Classify.",
		Egress: func(_ context.Context, _ gobellim.Principal, l gobellim.Label) error {
			got = l
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := task.Run(context.Background(), principal(), trustedIn{TenantID: "acme", OrderID: "A-1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != gobellim.TaskCompleted {
		t.Fatalf("status = %v", res.Status)
	}
	if got.Integrity != res.Label.Integrity || got.Sensitivity != res.Label.Sensitivity {
		t.Fatalf("egress saw %+v, result says %+v", got, res.Label)
	}
}

// Egress vets an answer. A rejected tool call or an escalated action produces
// no answer, so the hook must not run and must not be able to veto a run that
// stopped before writing one.
func TestEgressDoesNotRunWithoutAnAnswer(t *testing.T) {
	var calls int
	refund := gobellim.Tool[refundArgs, refundResult]{
		Name:       "orders.refund",
		Operation:  gobellim.Write,
		Target:     testResource[refundArgs],
		Permission: "orders:refund",
		Run: func(context.Context, gobellim.Principal, refundArgs) (refundResult, error) {
			return refundResult{}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.refund", map[string]any{"OrderID": "<h1>", "Cents": 500}),
	)
	e := newEngine(t, model, testkit.AllowAll, nil)
	task, err := gobellim.NewTask[ticketIn, triageOut](e, gobellim.TaskConfig{
		Name:        "ticket.triage",
		Instruction: "Classify.",
		Tools:       []gobellim.ToolDef{refund},
		Egress: func(context.Context, gobellim.Principal, gobellim.Label) error {
			calls++
			return errors.New("must not be called")
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "refund", RequestedOrder: "A-1042",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != gobellim.TaskApprovalRequired || res.Pending == nil {
		t.Fatalf("status = %v, pending = %v", res.Status, res.Pending)
	}
	if calls != 0 {
		t.Fatalf("egress ran %d times on a suspended run", calls)
	}
}
