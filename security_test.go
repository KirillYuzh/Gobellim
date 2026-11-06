package gobellim_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/testkit"
)

type secOut struct{ OK bool }

type nonGroundIn struct {
	Secret string `gobellim:"required,sensitivity=secret"`
}
type nonGroundArgs struct {
	Query string `gobellim:"required"`
}

func TestHandleIsRejectedOutsideGroundField(t *testing.T) {
	var executed int
	tool := gobellim.Tool[nonGroundArgs, secOut]{
		Name:      "kb.search",
		Operation: gobellim.Read,
		Target:    testResource[nonGroundArgs],
		Run: func(context.Context, gobellim.Principal, nonGroundArgs) (secOut, error) {
			executed++
			return secOut{OK: true}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("kb.search", map[string]any{"Query": "<h1>"}),
		testkit.Reply(`{"OK":true}`),
	)
	task, err := gobellim.NewTask[nonGroundIn, secOut](newEngine(t, model, testkit.AllowAll, nil), gobellim.TaskConfig{
		Name: "p", Instruction: "x", Tools: []gobellim.ToolDef{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := task.Run(context.Background(), principal(), nonGroundIn{Secret: "TOP-SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 0 {
		t.Fatal("a handle in a non-ground field reached the tool")
	}
	if got := res.ToolCalls[0].Decision; got.Kind != gobellim.Deny || got.RuleIDs[0] != "ungrounded" {
		t.Fatalf("decision = %+v, want Deny ungrounded", got)
	}
}

type sourceIn struct {
	Email   string `gobellim:"required,sensitivity=secret"`
	OrderID string `gobellim:"required,source=application,sensitivity=secret"`
}
type sourceArgs struct {
	OrderID string `gobellim:"required,ground"`
}

func TestGroundRejectsHandleFromAnotherField(t *testing.T) {
	var executed int
	tool := gobellim.Tool[sourceArgs, secOut]{
		Name:      "orders.apply",
		Operation: gobellim.Write,
		Target:    testResource[sourceArgs],
		Run: func(context.Context, gobellim.Principal, sourceArgs) (secOut, error) {
			executed++
			return secOut{OK: true}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.apply", map[string]any{"OrderID": "<h1>"}),
		testkit.Reply(`{"OK":true}`),
	)
	task, err := gobellim.NewTask[sourceIn, secOut](newEngine(t, model, testkit.AllowAll, nil), gobellim.TaskConfig{
		Name: "p", Instruction: "x", Tools: []gobellim.ToolDef{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := task.Run(context.Background(), principal(), sourceIn{
		Email: "alice@example.com", OrderID: "A-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 0 {
		t.Fatal("a handle from Email satisfied the OrderID ground field")
	}
	if got := res.ToolCalls[0].Decision; got.Kind != gobellim.Deny || got.RuleIDs[0] != "ungrounded" {
		t.Fatalf("decision = %+v, want Deny ungrounded", got)
	}
}

type targetIn struct {
	OrderID string `gobellim:"required,source=application,sensitivity=secret"`
}
type targetArgs struct {
	OrderID string `gobellim:"required,ground"`
}

func TestWriteWithEmptyTargetIsDenied(t *testing.T) {
	var executed int
	tool := gobellim.Tool[targetArgs, secOut]{
		Name:      "orders.apply",
		Operation: gobellim.Write,
		Target:    func(gobellim.Principal, targetArgs) gobellim.Resource { return gobellim.Resource{} },
		Run: func(context.Context, gobellim.Principal, targetArgs) (secOut, error) {
			executed++
			return secOut{OK: true}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.apply", map[string]any{"OrderID": "<h1>"}),
		testkit.Reply(`{"OK":true}`),
	)
	task, err := gobellim.NewTask[targetIn, secOut](newEngine(t, model, testkit.AllowAll, nil), gobellim.TaskConfig{
		Name: "p", Instruction: "x", Tools: []gobellim.ToolDef{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := task.Run(context.Background(), principal(), targetIn{OrderID: "A-1"})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 0 {
		t.Fatal("a write without a target executed")
	}
	if got := res.ToolCalls[0].Decision; got.Kind != gobellim.Deny || got.RuleIDs[0] != "target-unbound" {
		t.Fatalf("decision = %+v, want Deny target-unbound", got)
	}
}

type redactArgs struct {
	Query string `gobellim:"required"`
}
type redactFields struct {
	M map[string]string `gobellim:"sensitivity=secret"`
	B []byte            `gobellim:"sensitivity=secret"`
	S string            `gobellim:"sensitivity=secret"`
}

func TestResultRedactionCoversMapBytesAndString(t *testing.T) {
	tool := gobellim.Tool[redactArgs, redactFields]{
		Name:      "customer.read",
		Operation: gobellim.Read,
		Target:    testResource[redactArgs],
		Run: func(context.Context, gobellim.Principal, redactArgs) (redactFields, error) {
			return redactFields{
				M: map[string]string{"key": "MAP-SECRET"},
				B: []byte("BYTE-SECRET"),
				S: "STRING-SECRET",
			}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("customer.read", map[string]any{"Query": "x"}),
		testkit.Reply(`{"OK":true}`),
	)
	task, err := gobellim.NewTask[redactArgs, secOut](newEngine(t, model, testkit.AllowAll, nil), gobellim.TaskConfig{
		Name: "p", Instruction: "x", Tools: []gobellim.ToolDef{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := task.Run(context.Background(), principal(), redactArgs{Query: "x"}); err != nil {
		t.Fatal(err)
	}
	calls := model.Calls()
	payload := calls[len(calls)-1].Messages[len(calls[len(calls)-1].Messages)-1].Content
	for _, secret := range []string{"MAP-SECRET", "BYTE-SECRET", "STRING-SECRET"} {
		if strings.Contains(payload, secret) {
			t.Fatalf("%s reached the model: %s", secret, payload)
		}
	}
}

type badSecretResult struct {
	N int `gobellim:"sensitivity=secret"`
}

func TestSensitivityOnScalarResultIsRejected(t *testing.T) {
	tool := gobellim.Tool[redactArgs, badSecretResult]{
		Name:      "customer.read",
		Operation: gobellim.Read,
		Target:    testResource[redactArgs],
		Run: func(context.Context, gobellim.Principal, redactArgs) (badSecretResult, error) {
			return badSecretResult{N: 42}, nil
		},
	}
	_, err := gobellim.NewTask[redactArgs, secOut](newEngine(t, testkit.NewFakeModel(), testkit.AllowAll, nil), gobellim.TaskConfig{
		Name: "p", Instruction: "x", Tools: []gobellim.ToolDef{tool},
	})
	if !errors.Is(err, gobellim.ErrUnsupportedType) {
		t.Fatalf("err = %v, want ErrUnsupportedType", err)
	}
}

type cancelIn struct {
	OrderID string `gobellim:"required,source=application,sensitivity=secret"`
}
type cancelArgs struct {
	OrderID string `gobellim:"required,ground"`
	Cents   int    `gobellim:"min=1,max=5000"`
}

func TestCanceledApprovalDoesNotConsumeTheNonce(t *testing.T) {
	var executed int
	tool := gobellim.Tool[cancelArgs, secOut]{
		Name:      "orders.refund",
		Operation: gobellim.Write,
		Target:    testResource[cancelArgs],
		Run: func(ctx context.Context, _ gobellim.Principal, _ cancelArgs) (secOut, error) {
			if err := ctx.Err(); err != nil {
				return secOut{}, err
			}
			executed++
			return secOut{OK: true}, nil
		},
	}
	model := testkit.NewFakeModel(testkit.Call("orders.refund", map[string]any{
		"OrderID": "<h1>", "Cents": 500,
	}))
	task, err := gobellim.NewTask[cancelIn, secOut](newEngine(t, model, testkit.AllowAll, nil), gobellim.TaskConfig{
		Name: "p", Instruction: "x", Tools: []gobellim.ToolDef{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := task.Run(context.Background(), principal(), cancelIn{OrderID: "A-1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending == nil {
		t.Fatalf("no pending action: %+v", res.ToolCalls)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := task.ExecuteApproved(canceled, principal(), principal(), res.Pending); err == nil {
		t.Fatal("a canceled request executed the action")
	}
	if executed != 0 {
		t.Fatal("a canceled request executed the action")
	}
	if _, _, err := task.ExecuteApproved(context.Background(), principal(), principal(), res.Pending); err != nil {
		t.Fatalf("retry after cancellation: %v", err)
	}
	if executed != 1 {
		t.Fatalf("executed = %d, want 1", executed)
	}
}

func TestRequestCarriesBoundedMaxTokens(t *testing.T) {
	model := testkit.NewFakeModel(testkit.Reply(`{"Category":"billing","Reply":"ok"}`))
	task := newTriageTask(t, newEngine(t, model, testkit.AllowAll, nil), nil)
	if _, err := task.Run(context.Background(), principal(), ticketIn{
		TenantID: "acme", TicketID: "t-1", Body: "hello",
	}); err != nil {
		t.Fatal(err)
	}
	if got := model.Calls()[0].MaxTokens; got <= 0 {
		t.Fatalf("MaxTokens = %d, want a bounded positive value", got)
	}
}

func TestReadWithoutTargetIsAConfigError(t *testing.T) {
	tool := gobellim.Tool[readArgs, readResult]{
		Name:      "kb.search",
		Operation: gobellim.Read,
		Run: func(context.Context, gobellim.Principal, readArgs) (readResult, error) {
			return readResult{}, nil
		},
	}
	_, err := gobellim.NewTask[ticketIn, triageOut](newEngine(t, testkit.NewFakeModel(), testkit.AllowAll, nil), gobellim.TaskConfig{
		Name: "p", Instruction: "x", Tools: []gobellim.ToolDef{tool},
	})
	if !errors.Is(err, gobellim.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestMemClaimsIsBounded(t *testing.T) {
	m := gobellim.NewMemClaims()
	now := time.Unix(1_700_000_000, 0)
	m.Now = func() time.Time { return now }
	m.MaxEntries = 1
	ctx := context.Background()
	if ok, err := m.Claim(ctx, "a", now.Add(time.Hour)); err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if ok, err := m.Claim(ctx, "b", now.Add(time.Hour)); !errors.Is(err, gobellim.ErrClaimStoreFull) || ok {
		t.Fatalf("second claim: ok=%v err=%v, want full", ok, err)
	}
}

// TestTaskRunIsConcurrentSafe exercises one Engine and one Task from many
// goroutines: a data race in the schema cache, the handle set or the claim
// store is a production incident in a control plane, not a flaky test. Run it
// with -race.
func TestTaskRunIsConcurrentSafe(t *testing.T) {
	const n = 100
	responses := make([]gobellim.Response, n)
	for i := range responses {
		responses[i] = testkit.Reply(validTriage)
	}
	task := newTriageTask(t, newEngine(t, testkit.NewFakeModel(responses...), testkit.AllowAll, nil), nil)

	var wg sync.WaitGroup
	errs := make(chan error, n)
	ids := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := task.Run(context.Background(), principal(), ticketIn{
				TenantID: "acme", TicketID: "A-100", Body: "hello",
			})
			if err != nil {
				errs <- err
				return
			}
			if res.Status != gobellim.TaskCompleted {
				errs <- fmt.Errorf("status = %v, want completed", res.Status)
				return
			}
			ids <- res.RunID
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Fatal(err)
	}
	seen := make(map[string]bool, n)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate run id %q", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d run ids, want %d", len(seen), n)
	}
}

// A source tag on a tool argument declares trust the Gate cannot honour: the
// runtime labels every model-supplied argument Derived. Accepting it silently
// would let a reader believe an unattended write was declared safe.
type argSourceArgs struct {
	OrderID string `gobellim:"required,ground"`
	Note    string `gobellim:"source=application"`
}

func TestToolArgumentRejectsSourceTag(t *testing.T) {
	tool := gobellim.Tool[argSourceArgs, secOut]{
		Name:      "orders.apply",
		Operation: gobellim.Write,
		Target:    testResource[argSourceArgs],
		Run: func(context.Context, gobellim.Principal, argSourceArgs) (secOut, error) {
			return secOut{OK: true}, nil
		},
	}
	_, err := gobellim.NewTask[targetIn, secOut](
		newEngine(t, testkit.NewFakeModel(), testkit.AllowAll, nil),
		gobellim.TaskConfig{Name: "p", Instruction: "x", Tools: []gobellim.ToolDef{tool}},
	)
	if !errors.Is(err, gobellim.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

// ground on a task input field is the mirror image: only an argument is
// grounded, so the tag would be a no-op protection.
type groundInput struct {
	Body string `gobellim:"required,ground"`
}

func TestInputFieldRejectsGroundTag(t *testing.T) {
	_, err := gobellim.NewTask[groundInput, secOut](
		newEngine(t, testkit.NewFakeModel(), testkit.AllowAll, nil),
		gobellim.TaskConfig{Name: "p", Instruction: "x"},
	)
	if !errors.Is(err, gobellim.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

// A target without a tenant cannot be checked against the caller's tenant. It
// must be denied before the operation ceiling, even when the arguments are trusted
// enough for an unattended write.
type tenantlessArgs struct {
	OrderID string `gobellim:"required,ground"`
}

func TestTargetWithoutTenantIsDenied(t *testing.T) {
	var executed int
	tool := gobellim.Tool[tenantlessArgs, secOut]{
		Name:      "orders.apply",
		Operation: gobellim.Write,
		Target: func(gobellim.Principal, tenantlessArgs) gobellim.Resource {
			return gobellim.Resource{Type: "order", ID: "A-1"}
		},
		Run: func(context.Context, gobellim.Principal, tenantlessArgs) (secOut, error) {
			executed++
			return secOut{OK: true}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("orders.apply", map[string]any{"OrderID": "<h1>"}),
		testkit.Reply(`{"OK":true}`),
	)
	task, err := gobellim.NewTask[targetIn, secOut](newEngine(t, model, testkit.AllowAll, nil), gobellim.TaskConfig{
		Name: "p", Instruction: "x", Tools: []gobellim.ToolDef{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := task.Run(context.Background(), principal(), targetIn{OrderID: "A-1"})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 0 {
		t.Fatal("a write with an unbound target executed")
	}
	if got := res.ToolCalls[0].Decision; got.Kind != gobellim.Deny || got.RuleIDs[0] != "target-unbound" {
		t.Fatalf("decision = %+v, want Deny target-unbound", got)
	}
}

// A shared signing key lets another replica accept a PendingAction. Without a
// shared claim store the same approval would then execute once per replica, so
// the combination is a configuration error at New, not a runtime surprise.
func TestSharedApprovalKeyRequiresASharedClaimStore(t *testing.T) {
	key := make([]byte, 32)
	_, err := gobellim.New(
		gobellim.WithModel(testkit.NewFakeModel()),
		gobellim.WithAuthorizer(testkit.AllowAll),
		gobellim.WithAudit(testkit.DiscardAudit()),
		gobellim.WithApprovalKey(key),
	)
	if !errors.Is(err, gobellim.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}

	if _, err := gobellim.New(
		gobellim.WithModel(testkit.NewFakeModel()),
		gobellim.WithAuthorizer(testkit.AllowAll),
		gobellim.WithAudit(testkit.DiscardAudit()),
		gobellim.WithApprovalKey(key),
		gobellim.WithClaimStore(gobellim.NewMemClaims()),
	); err != nil {
		t.Fatalf("key with an explicit claim store: %v", err)
	}
}

// A zero TTL means "the default". A negative one was written on purpose and is
// a typo, not a request for the default.
func TestNegativeApprovalTTLIsRejected(t *testing.T) {
	_, err := gobellim.New(
		gobellim.WithModel(testkit.NewFakeModel()),
		gobellim.WithAuthorizer(testkit.AllowAll),
		gobellim.WithAudit(testkit.DiscardAudit()),
		gobellim.WithApprovalTTL(-time.Second),
	)
	if !errors.Is(err, gobellim.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}
