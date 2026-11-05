package gobellim_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/testkit"
)

type ticketIn struct {
	TenantID       string `gobellim:"source=principal,required"`
	TicketID       string `gobellim:"source=application,required"`
	Body           string `gobellim:"required,sensitivity=pii"`
	RequestedOrder string `gobellim:"sensitivity=secret"`
}

type triageOut struct {
	Category string `gobellim:"required,oneof=billing|technical|other"`
	Reply    string
}

type refundArgs struct {
	OrderID string `gobellim:"required,ground"`
	Cents   int    `gobellim:"min=1,max=5000"`
}

type refundResult struct {
	RefundID string
	Status   string
}

type readArgs struct {
	Query string `gobellim:"required"`
}

type readResult struct {
	Hits []string
}

const validTriage = `{"Category":"billing","Reply":"ok"}`

func principal() gobellim.Principal {
	return gobellim.Principal{UserID: "u-1", Tenant: "acme"}
}

func newEngine(t *testing.T, model gobellim.Model, authz gobellim.Authorizer, sink gobellim.Sink) *gobellim.Engine {
	t.Helper()
	e, err := gobellim.New(
		gobellim.WithModel(model),
		gobellim.WithAuthorizer(authz),
		gobellim.WithAudit(sink),
	)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func newTriageTask(t *testing.T, e *gobellim.Engine, tools []gobellim.ToolDef) *gobellim.Task[ticketIn, triageOut] {
	t.Helper()
	task, err := gobellim.NewTask[ticketIn, triageOut](e, gobellim.TaskConfig{
		Name:        "ticket.triage",
		Instruction: "Classify the ticket and propose a reply.",
		Tools:       tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestWriteEscalatesAndDoesNotExecute(t *testing.T) {
	var executed int
	refund := gobellim.Tool[refundArgs, refundResult]{
		Name:       "orders.refund",
		Effect:     gobellim.Write,
		Permission: "orders:refund",
		Resource: func(p gobellim.Principal, a refundArgs) gobellim.Resource {
			return gobellim.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
		},
		Run: func(context.Context, gobellim.Principal, refundArgs) (refundResult, error) {
			executed++
			return refundResult{RefundID: "rf_1", Status: "ok"}, nil
		},
	}

	model := testkit.NewFakeModel(testkit.Call("orders.refund", map[string]any{"OrderID": "<h1>", "Cents": 500}))
	rec := &testkit.Recorder{}
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, rec), []gobellim.ToolDef{refund})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "A-100", Body: "please refund", RequestedOrder: "A-100",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != gobellim.TaskApprovalRequired {
		t.Fatalf("status = %v, want approval required", res.Status)
	}
	if executed != 0 {
		t.Fatalf("tool executed %d times, want 0", executed)
	}
	if res.Pending == nil || res.Pending.Card.Tool != "orders.refund" {
		t.Fatalf("pending = %+v", res.Pending)
	}
	if res.Pending.Card.Digest == "" {
		t.Fatal("approval card has no digest")
	}
	if rec.Count(gobellim.EventGateEscalated) != 1 {
		t.Fatalf("escalation events = %d, want 1", rec.Count(gobellim.EventGateEscalated))
	}
}

func TestUngroundedArgumentIsDenied(t *testing.T) {
	var executed int
	refund := gobellim.Tool[refundArgs, refundResult]{
		Name:       "orders.refund",
		Effect:     gobellim.Write,
		Permission: "orders:refund",
		Run: func(context.Context, gobellim.Principal, refundArgs) (refundResult, error) {
			executed++
			return refundResult{}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.refund", map[string]any{"OrderID": "A-100", "Cents": 500}),
		testkit.Reply(validTriage),
	)
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), []gobellim.ToolDef{refund})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "A-100", Body: "please refund", RequestedOrder: "A-100",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 0 {
		t.Fatal("ungrounded action executed")
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Decision.Kind != gobellim.Deny {
		t.Fatalf("tool calls = %+v", res.ToolCalls)
	}
	if res.Status != gobellim.TaskCompleted {
		t.Fatalf("status = %v", res.Status)
	}
}

func TestRBACDenyDoesNotExecute(t *testing.T) {
	var executed int
	refund := gobellim.Tool[refundArgs, refundResult]{
		Name:       "orders.refund",
		Effect:     gobellim.Write,
		Permission: "orders:refund",
		Run: func(context.Context, gobellim.Principal, refundArgs) (refundResult, error) {
			executed++
			return refundResult{}, nil
		},
	}
	authz := gobellim.AuthorizerFunc(func(_ context.Context, _ gobellim.Principal, permission string, _ gobellim.Resource) (bool, error) {
		return permission != "orders:refund", nil
	})
	model := testkit.NewFakeModel(
		testkit.Call("orders.refund", refundArgs{OrderID: "A-100", Cents: 500}),
		testkit.Reply(validTriage),
	)
	task := newTriageTask(t, newEngine(t, model, authz, nil), []gobellim.ToolDef{refund})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "A-100", Body: "please refund",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 0 {
		t.Fatal("denied action executed")
	}
	if got := res.ToolCalls[0].Decision.RuleIDs; len(got) == 0 || got[0] != "rbac-acl" {
		t.Fatalf("rule ids = %v", got)
	}
}

func TestValidOutputCompletes(t *testing.T) {
	model := testkit.NewFakeModel(testkit.Reply(validTriage))
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), nil)

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != gobellim.TaskCompleted {
		t.Fatalf("status = %v", res.Status)
	}
	if res.Output.Category != "billing" {
		t.Fatalf("category = %q", res.Output.Category)
	}
}

func TestInvalidOutputIsRejected(t *testing.T) {
	model := testkit.NewFakeModel(testkit.Reply(`{"Category":"nope"}`))
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), nil)

	if _, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello",
	}); !errors.Is(err, gobellim.ErrInvalidArgs) {
		t.Fatalf("err = %v, want ErrInvalidArgs", err)
	}
}

func TestInputPIIIsMaskedBeforeTheModel(t *testing.T) {
	model := testkit.NewFakeModel(testkit.Reply(validTriage))
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), nil)

	if _, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "write to alice@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("model calls = %d", len(calls))
	}
	prompt := calls[0].Messages[1].Content
	if strings.Contains(prompt, "alice@example.com") {
		t.Fatalf("PII leaked to the model: %q", prompt)
	}
	if !strings.Contains(prompt, "<h") {
		t.Fatalf("prompt has no handle: %q", prompt)
	}
}

func TestFabricatedHandleIsDenied(t *testing.T) {
	var executed int
	refund := gobellim.Tool[refundArgs, refundResult]{
		Name:       "orders.refund",
		Effect:     gobellim.Write,
		Permission: "orders:refund",
		Run: func(context.Context, gobellim.Principal, refundArgs) (refundResult, error) {
			executed++
			return refundResult{}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.refund", map[string]any{"OrderID": "<h999>", "Cents": 500}),
		testkit.Reply(validTriage),
	)
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), []gobellim.ToolDef{refund})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "A-100", Body: "please refund",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 0 {
		t.Fatal("fabricated handle executed")
	}
	if res.ToolCalls[0].Decision.Kind != gobellim.Deny {
		t.Fatalf("decision = %v", res.ToolCalls[0].Decision.Kind)
	}
}

type orderIn struct {
	TenantID string `gobellim:"source=principal,required"`
	OrderID  string `gobellim:"source=application,sensitivity=secret,required"`
}

type trustedArgs struct {
	OrderID string `gobellim:"required,ground"`
}

type trustedResult struct {
	OK bool
}

func TestTrustedHandleAllowsWrite(t *testing.T) {
	var executed int
	apply := gobellim.Tool[trustedArgs, trustedResult]{
		Name:       "orders.apply",
		Effect:     gobellim.Write,
		Permission: "orders:apply",
		Run: func(context.Context, gobellim.Principal, trustedArgs) (trustedResult, error) {
			executed++
			return trustedResult{OK: true}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.apply", map[string]any{"OrderID": "<h1>"}),
		testkit.Reply(validTriage),
	)
	e := newEngine(t, model, testkit.AllowAll, nil)
	task, err := gobellim.NewTask[orderIn, triageOut](e, gobellim.TaskConfig{
		Name:        "order.apply",
		Instruction: "Apply the order.",
		Tools:       []gobellim.ToolDef{apply},
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := task.Run(context.Background(), principal(), orderIn{TenantID: "acme", OrderID: "A-1042"})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 1 {
		t.Fatalf("executed = %d, want 1; status=%v calls=%+v prompt=%s", executed, res.Status, res.ToolCalls, model.Calls()[0].Messages[1].Content)
	}
	if res.ToolCalls[0].Decision.Kind != gobellim.Allow {
		t.Fatalf("decision = %v", res.ToolCalls[0].Decision.Kind)
	}
	if res.Status != gobellim.TaskCompleted {
		t.Fatalf("status = %v", res.Status)
	}
}

func TestUnknownToolIsDenied(t *testing.T) {
	model := testkit.NewFakeModel(
		testkit.Call("orders.refund", map[string]any{"OrderID": "A-100", "Cents": 500}),
		testkit.Reply(validTriage),
	)
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), nil)

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "A-100", Body: "please refund",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ToolCalls[0].Decision.Kind != gobellim.Deny {
		t.Fatalf("decision = %v", res.ToolCalls[0].Decision.Kind)
	}
	if res.ToolCalls[0].Decision.RuleIDs[0] != "unknown-tool" {
		t.Fatalf("rules = %v", res.ToolCalls[0].Decision.RuleIDs)
	}
}

func TestMaxToolCallsIsEnforced(t *testing.T) {
	read := gobellim.Tool[readArgs, readResult]{
		Name:       "kb.search",
		Effect:     gobellim.Read,
		Permission: "kb:search",
		Run: func(context.Context, gobellim.Principal, readArgs) (readResult, error) {
			return readResult{Hits: []string{"a"}}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.CallWithID("c1", "kb.search", readArgs{Query: "a"}),
		testkit.CallWithID("c2", "kb.search", readArgs{Query: "b"}),
	)
	e, err := gobellim.New(
		gobellim.WithModel(model),
		gobellim.WithAuthorizer(testkit.AllowAll),
		gobellim.WithLimits(gobellim.Limits{MaxToolCalls: 1}),
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := gobellim.NewTask[ticketIn, triageOut](e, gobellim.TaskConfig{
		Name: "ticket.triage", Instruction: "Search.", Tools: []gobellim.ToolDef{read},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello",
	}); !errors.Is(err, gobellim.ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
}

func TestReadToolResultFeedsTheModel(t *testing.T) {
	read := gobellim.Tool[readArgs, readResult]{
		Name:       "kb.search",
		Effect:     gobellim.Read,
		Permission: "kb:search",
		Run: func(context.Context, gobellim.Principal, readArgs) (readResult, error) {
			return readResult{Hits: []string{"doc-1"}}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("kb.search", readArgs{Query: "billing"}),
		testkit.Reply(validTriage),
	)
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), []gobellim.ToolDef{read})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != gobellim.TaskCompleted {
		t.Fatalf("status = %v", res.Status)
	}
	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(calls))
	}
	last := calls[1].Messages[len(calls[1].Messages)-1]
	if last.Role != gobellim.RoleTool || !strings.Contains(last.Content, "doc-1") {
		t.Fatalf("tool message = %+v", last)
	}
}

func TestToolRequiresExplicitEffect(t *testing.T) {
	tool := gobellim.Tool[readArgs, readResult]{
		Name: "orders.read",
		Run: func(context.Context, gobellim.Principal, readArgs) (readResult, error) {
			return readResult{}, nil
		},
	}
	e := newEngine(t, testkit.NewFakeModel(), testkit.AllowAll, nil)
	_, err := gobellim.NewTask[ticketIn, triageOut](e, gobellim.TaskConfig{
		Name: "x", Instruction: "x", Tools: []gobellim.ToolDef{tool},
	})
	if !errors.Is(err, gobellim.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestTaskCompletionIsAudited(t *testing.T) {
	rec := &testkit.Recorder{}
	model := testkit.NewFakeModel(testkit.Reply(validTriage))
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, rec), nil)
	if _, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello",
	}); err != nil {
		t.Fatal(err)
	}
	if got := rec.Count(gobellim.EventTaskCompleted); got != 1 {
		t.Fatalf("task.completed events = %d, events=%+v", got, rec.Events())
	}
}

func TestEmptyArgsWriteIsEscalated(t *testing.T) {
	type noArgs struct{}
	var executed int
	wipe := gobellim.Tool[noArgs, refundResult]{
		Name:   "danger.wipe",
		Effect: gobellim.Write,
		Run: func(context.Context, gobellim.Principal, noArgs) (refundResult, error) {
			executed++
			return refundResult{}, nil
		},
	}
	model := testkit.NewFakeModel(testkit.Call("danger.wipe", map[string]any{}))
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), []gobellim.ToolDef{wipe})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 0 {
		t.Fatal("empty-args write executed without approval")
	}
	if res.Status != gobellim.TaskApprovalRequired {
		t.Fatalf("status = %v, want approval required", res.Status)
	}
	if res.ToolCalls[0].Decision.Kind != gobellim.Escalate {
		t.Fatalf("decision = %v, want escalate", res.ToolCalls[0].Decision.Kind)
	}
}

func TestNestedGroundRequiresAHandle(t *testing.T) {
	type inner struct {
		OrderID string `gobellim:"required,ground"`
	}
	type outer struct {
		Nested inner
	}
	var executed int
	tool := gobellim.Tool[outer, readResult]{
		Name:   "orders.read",
		Effect: gobellim.Read,
		Run: func(context.Context, gobellim.Principal, outer) (readResult, error) {
			executed++
			return readResult{}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.read", map[string]any{"Nested": map[string]any{"OrderID": "A-999"}}),
		testkit.Reply(validTriage),
	)
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), []gobellim.ToolDef{tool})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 0 {
		t.Fatal("nested ungrounded read executed")
	}
	if res.ToolCalls[0].Decision.Kind != gobellim.Deny {
		t.Fatalf("decision = %v, want deny", res.ToolCalls[0].Decision.Kind)
	}
}

func TestResourceTenantMismatchIsDenied(t *testing.T) {
	var executed int
	tool := gobellim.Tool[readArgs, readResult]{
		Name:       "orders.read",
		Effect:     gobellim.Read,
		Permission: "orders:read",
		Resource: func(gobellim.Principal, readArgs) gobellim.Resource {
			return gobellim.Resource{Type: "order", ID: "A-1", TenantID: "other-tenant"}
		},
		Run: func(context.Context, gobellim.Principal, readArgs) (readResult, error) {
			executed++
			return readResult{}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.read", readArgs{Query: "A-1"}),
		testkit.Reply(validTriage),
	)
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), []gobellim.ToolDef{tool})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 0 {
		t.Fatal("cross-tenant action executed")
	}
	got := res.ToolCalls[0].Decision
	if got.Kind != gobellim.Deny || len(got.RuleIDs) == 0 || got.RuleIDs[0] != "tenant-mismatch" {
		t.Fatalf("decision = %+v, want Deny tenant-mismatch", got)
	}
}

type handleOnlyArgs struct {
	OrderID string `gobellim:"required,ground"`
}

type secretResult struct {
	Card  string `gobellim:"sensitivity=secret"`
	Email string `gobellim:"sensitivity=pii"`
	Note  string
}

// A grounded handle plus a passing business validator is Validated: the runtime
// minted the handle and the application confirmed the rule.
func TestValidatedHandleAllowsWrite(t *testing.T) {
	var executed int
	apply := gobellim.Tool[handleOnlyArgs, refundResult]{
		Name:     "orders.apply",
		Effect:   gobellim.Write,
		Validate: func(context.Context, gobellim.Principal, handleOnlyArgs) error { return nil },
		Run: func(context.Context, gobellim.Principal, handleOnlyArgs) (refundResult, error) {
			executed++
			return refundResult{RefundID: "r-1"}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.apply", map[string]any{"OrderID": "<h1>"}),
		testkit.Reply(validTriage),
	)
	e := newEngine(t, model, testkit.AllowAll, nil)
	task := newTriageTask(t, e, []gobellim.ToolDef{apply})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "refund", RequestedOrder: "A-1042",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 1 {
		t.Fatalf("executed = %d, want 1; decision=%+v", executed, res.ToolCalls)
	}
	if got := res.ToolCalls[0].Decision.Kind; got != gobellim.Allow {
		t.Fatalf("decision = %v, want Allow", got)
	}
}

// Without a validator the same handle is only Derived, so a write still needs a
// human. Promotion is not a free pass.
func TestGroundedHandleWithoutValidatorEscalates(t *testing.T) {
	var executed int
	apply := gobellim.Tool[handleOnlyArgs, refundResult]{
		Name:   "orders.apply",
		Effect: gobellim.Write,
		Run: func(context.Context, gobellim.Principal, handleOnlyArgs) (refundResult, error) {
			executed++
			return refundResult{}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.apply", map[string]any{"OrderID": "<h1>"}),
		testkit.Reply(validTriage),
	)
	e := newEngine(t, model, testkit.AllowAll, nil)
	task := newTriageTask(t, e, []gobellim.ToolDef{apply})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "refund", RequestedOrder: "A-1042",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 0 || res.Status != gobellim.TaskApprovalRequired || res.Pending == nil {
		t.Fatalf("executed=%d status=%v pending=%v", executed, res.Status, res.Pending)
	}
}

func TestValidatedHandleAllowsEgress(t *testing.T) {
	var executed int
	send := gobellim.Tool[handleOnlyArgs, refundResult]{
		Name:     "mail.send",
		Effect:   gobellim.Egress,
		Validate: func(context.Context, gobellim.Principal, handleOnlyArgs) error { return nil },
		Run: func(context.Context, gobellim.Principal, handleOnlyArgs) (refundResult, error) {
			executed++
			return refundResult{}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("mail.send", map[string]any{"OrderID": "<h1>"}),
		testkit.Reply(validTriage),
	)
	e := newEngine(t, model, testkit.AllowAll, nil)
	task := newTriageTask(t, e, []gobellim.ToolDef{send})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "send", RequestedOrder: "A-1042",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 1 || res.ToolCalls[0].Decision.Kind != gobellim.Allow {
		t.Fatalf("executed=%d decision=%+v", executed, res.ToolCalls)
	}
}

func TestToolResultIsRedacted(t *testing.T) {
	read := gobellim.Tool[readArgs, secretResult]{
		Name:   "customer.read",
		Effect: gobellim.Read,
		Run: func(context.Context, gobellim.Principal, readArgs) (secretResult, error) {
			return secretResult{Card: "4111 1111 1111 1111", Email: "a@b.co", Note: "ok"}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("customer.read", map[string]any{"Query": "x"}),
		testkit.Reply(validTriage),
	)
	e := newEngine(t, model, testkit.AllowAll, nil)
	task := newTriageTask(t, e, []gobellim.ToolDef{read})

	if _, err := task.Run(context.Background(), principal(), ticketIn{TenantID: "acme", TicketID: "t-1", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	calls := model.Calls()
	toolMsg := calls[1].Messages[len(calls[1].Messages)-1]
	if strings.Contains(toolMsg.Content, "4111") {
		t.Fatalf("a secret reached the model: %s", toolMsg.Content)
	}
	if strings.Contains(toolMsg.Content, "a@b.co") {
		t.Fatalf("PII reached the model: %s", toolMsg.Content)
	}
	if !strings.Contains(toolMsg.Content, "<h1>") || !strings.Contains(toolMsg.Content, "ok") {
		t.Fatalf("tool result lost its shape: %s", toolMsg.Content)
	}
}

func TestExecuteApprovedRunsTheAction(t *testing.T) {
	var executed int
	refund := gobellim.Tool[refundArgs, refundResult]{
		Name:   "orders.refund",
		Effect: gobellim.Write,
		Run: func(context.Context, gobellim.Principal, refundArgs) (refundResult, error) {
			executed++
			return refundResult{RefundID: "r-1"}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.refund", map[string]any{"OrderID": "<h1>", "Cents": 100}),
		testkit.Reply(validTriage),
	)
	e := newEngine(t, model, testkit.AllowAll, nil)
	task := newTriageTask(t, e, []gobellim.ToolDef{refund})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "refund", RequestedOrder: "A-1042",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending == nil {
		t.Fatalf("no pending action: %+v", res.ToolCalls)
	}
	if executed != 0 {
		t.Fatal("the escalated action ran before approval")
	}

	out, rec, err := task.ExecuteApproved(context.Background(), principal(), res.Pending)
	if err != nil {
		t.Fatal(err)
	}
	if executed != 1 || !rec.Executed {
		t.Fatalf("executed=%d rec=%+v", executed, rec)
	}
	if got := out.(refundResult).RefundID; got != "r-1" {
		t.Fatalf("result = %q", got)
	}
}

func TestExecuteApprovedRejectsADifferentAction(t *testing.T) {
	refund := gobellim.Tool[refundArgs, refundResult]{
		Name:   "orders.refund",
		Effect: gobellim.Write,
		Run: func(context.Context, gobellim.Principal, refundArgs) (refundResult, error) {
			t.Fatal("a tampered action was executed")
			return refundResult{}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.refund", map[string]any{"OrderID": "<h1>", "Cents": 100}),
		testkit.Reply(validTriage),
	)
	e := newEngine(t, model, testkit.AllowAll, nil)
	task := newTriageTask(t, e, []gobellim.ToolDef{refund})

	res, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "refund", RequestedOrder: "A-1042",
	})
	if err != nil {
		t.Fatal(err)
	}
	tampered := *res.Pending
	tampered.Args = []byte(`{"OrderID":"A-999","Cents":100}`)
	if _, _, err := task.ExecuteApproved(context.Background(), principal(), &tampered); !errors.Is(err, gobellim.ErrApprovalInvalid) {
		t.Fatalf("err = %v, want ErrApprovalInvalid", err)
	}

	other := *res.Pending
	other.Tenant = "other"
	if _, _, err := task.ExecuteApproved(context.Background(), principal(), &other); !errors.Is(err, gobellim.ErrApprovalInvalid) {
		t.Fatalf("err = %v, want ErrApprovalInvalid", err)
	}
}

func TestPanicIsAuditedAsFailure(t *testing.T) {
	boom := gobellim.Tool[readArgs, readResult]{
		Name:   "boom",
		Effect: gobellim.Read,
		Run: func(context.Context, gobellim.Principal, readArgs) (readResult, error) {
			panic("tool blew up")
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("boom", map[string]any{"Query": "x"}),
		testkit.Reply(validTriage),
	)
	rec := &testkit.Recorder{}
	e := newEngine(t, model, testkit.AllowAll, rec)
	task := newTriageTask(t, e, []gobellim.ToolDef{boom})

	func() {
		defer func() { _ = recover() }()
		_, _ = task.Run(context.Background(), principal(), ticketIn{TenantID: "acme", TicketID: "t-1", Body: "b"})
	}()

	if rec.Count(gobellim.EventTaskCompleted) != 0 {
		t.Fatal("a panicking run was audited as completed")
	}
	if rec.Count(gobellim.EventTaskFailed) != 1 {
		t.Fatalf("events = %+v", rec.Events())
	}
}
