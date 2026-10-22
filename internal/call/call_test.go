package call

import (
	"context"
	"errors"
	"testing"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/trace"
)

type fakeAuthz struct{ err error }

func (f fakeAuthz) Authorize(core.Principal, string) error { return f.err }

type fakeGuard struct {
	verdict guard.Verdict
	err     error
}

func (f fakeGuard) Check(context.Context, guard.Rail, string) (guard.Verdict, error) {
	return f.verdict, f.err
}

type fakeTracer struct{}

func (f *fakeTracer) Observe(_ context.Context, span trace.Span) {}

func authed() context.Context {
	return core.WithPrincipal(context.Background(), core.Principal{UserID: "u", Roles: []string{"r"}, Tenant: "t"})
}

func TestDoAllowsAndObserves(t *testing.T) {
	deps := Deps{Authz: fakeAuthz{}, Guard: fakeGuard{verdict: guard.Verdict{Action: guard.Allow, Rail: guard.RailInput, Text: "hi"}}, Tracer: &fakeTracer{}}

	called := false
	out, err := Do(authed(), deps, Req{Kind: KindLLM, Name: "m", Permission: "chat", Rail: guard.RailInput, Text: "hi"}, func(_ context.Context, span trace.Span) (string, error) {
		called = true
		return "ok", nil
	})
	if err != nil || out != "ok" || !called {
		t.Fatalf("out=%q err=%v called=%v", out, err, called)
	}
}

func TestDoFailsClosedOnRejectAndStillObserves(t *testing.T) {
	reject := guard.Verdict{
		Action:   guard.Reject,
		Rail:     guard.RailInput,
		Findings: []guard.Finding{{Category: guard.CatInjection}, {Category: guard.CatInjection}},
	}

	called := false
	_, err := Do(authed(), Deps{Authz: fakeAuthz{}, Guard: fakeGuard{verdict: reject}}, Req{Kind: KindLLM, Name: "m", Permission: "chat", Rail: guard.RailInput, Text: "bad"}, func(_ context.Context, span trace.Span) (string, error) {
		called = true
		return "", nil
	})
	if !errors.Is(err, core.ErrGuardReject) {
		t.Fatalf("err = %v, want ErrGuardReject", err)
	}
	if called {
		t.Fatal("operation must not run after a reject")
	}
}

func TestDoDeniesWithoutPermission(t *testing.T) {
	_, err := Do(authed(), Deps{Authz: fakeAuthz{err: core.ErrUnauthorized}}, Req{Kind: KindTool, Name: "t", Permission: "tool:t"}, func(context.Context, trace.Span) (string, error) {
		t.Fatal("operation must not run when authz denies")
		return "", nil
	})
	if !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}

// An operation without a named permission is a configuration error, never a
// free pass: the call layer must not fall through to "allowed".
func TestDoRequiresANamedPermission(t *testing.T) {
	called := false
	_, err := Do(authed(), Deps{Authz: fakeAuthz{}}, Req{Kind: KindLLM, Name: "m"}, func(context.Context, trace.Span) (string, error) {
		called = true
		return "", nil
	})
	if !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
	if called {
		t.Fatal("operation must not run without a permission")
	}
}

// A guard probe sits inside an operation that already authorized the caller,
// so it names no permission of its own and authorizes nothing.
func TestGuardNeedsNoPermission(t *testing.T) {
	verdict, err := Guard(authed(), Deps{Guard: fakeGuard{verdict: guard.Verdict{Action: guard.Allow, Rail: guard.RailInput, Text: "hi"}}},
		Req{Kind: KindGuard, Name: "guard:chunk", Rail: guard.RailInput, Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Action != guard.Allow {
		t.Fatalf("action = %v, want allow", verdict.Action)
	}
}

func TestDoRequiresPrincipal(t *testing.T) {
	_, err := Do(context.Background(), Deps{}, Req{Kind: KindLLM, Name: "m", Permission: "chat"}, func(context.Context, trace.Span) (string, error) {
		t.Fatal("operation must not run without a principal")
		return "", nil
	})
	if !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}
