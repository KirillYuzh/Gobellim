package call

import (
	"context"
	"errors"
	"testing"

	"github.com/gobellim/kit/guard"
	"github.com/gobellim/kit/internal/core"
	"github.com/gobellim/kit/trace"
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

type fakeTracer struct{ obs []trace.Observation }

func (f *fakeTracer) Observe(_ context.Context, o trace.Observation) { f.obs = append(f.obs, o) }

func authed() context.Context {
	return core.WithPrincipal(context.Background(), core.Principal{UserID: "u", Roles: []string{"r"}, Tenant: "t"})
}

func TestDoAllowsAndObserves(t *testing.T) {
	tr := &fakeTracer{}
	deps := Deps{Authz: fakeAuthz{}, Guard: fakeGuard{verdict: guard.Verdict{Action: guard.Allow, Rail: guard.RailInput, Text: "hi"}}, Tracer: tr}

	called := false
	out, err := Do(authed(), deps, Req{Kind: KindLLM, Name: "m", Rail: guard.RailInput, Text: "hi"}, func(_ context.Context, span *Span) (string, error) {
		called = true
		if span.Text != "hi" {
			t.Fatalf("span text = %q", span.Text)
		}
		return "ok", nil
	})
	if err != nil || out != "ok" || !called {
		t.Fatalf("out=%q err=%v called=%v", out, err, called)
	}
	if len(tr.obs) != 1 || tr.obs[0].GuardAction != "allow" || tr.obs[0].Level != trace.LevelDefault {
		t.Fatalf("obs = %+v", tr.obs)
	}
}

func TestDoPassesMaskedText(t *testing.T) {
	tr := &fakeTracer{}
	masked := guard.Verdict{Action: guard.Mask, Rail: guard.RailInput, Text: "hi [PII:EMAIL]"}
	deps := Deps{Guard: fakeGuard{verdict: masked}, Tracer: tr}

	var got string
	if _, err := Do(authed(), deps, Req{Kind: KindLLM, Name: "m", Rail: guard.RailInput, Text: "hi a@b.co"}, func(_ context.Context, span *Span) (string, error) {
		got = span.Text
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	if got != masked.Text {
		t.Fatalf("operation saw %q, want %q", got, masked.Text)
	}
	if tr.obs[0].GuardAction != "mask" {
		t.Fatalf("obs = %+v", tr.obs[0])
	}
}

func TestDoFailsClosedOnRejectAndStillObserves(t *testing.T) {
	tr := &fakeTracer{}
	reject := guard.Verdict{
		Action:   guard.Reject,
		Rail:     guard.RailInput,
		Findings: []guard.Finding{{Category: guard.CatInjection}, {Category: guard.CatInjection}},
	}
	deps := Deps{Guard: fakeGuard{verdict: reject}, Tracer: tr}

	called := false
	_, err := Do(authed(), deps, Req{Kind: KindLLM, Name: "m", Rail: guard.RailInput, Text: "bad"}, func(context.Context, *Span) (string, error) {
		called = true
		return "", nil
	})
	if !errors.Is(err, core.ErrGuardReject) {
		t.Fatalf("err = %v, want ErrGuardReject", err)
	}
	if called {
		t.Fatal("operation must not run after a reject")
	}
	if len(tr.obs) != 1 {
		t.Fatalf("want one observation, got %d", len(tr.obs))
	}
	obs := tr.obs[0]
	if obs.ErrorKind != "guard_reject" || obs.GuardAction != "reject" || obs.GuardRail != "input" {
		t.Fatalf("obs = %+v", obs)
	}
	if len(obs.GuardCategories) != 1 || obs.GuardCategories[0] != "INJECTION" {
		t.Fatalf("categories = %v", obs.GuardCategories)
	}
}

func TestDoDeniesWithoutPermission(t *testing.T) {
	tr := &fakeTracer{}
	deps := Deps{Authz: fakeAuthz{err: core.ErrUnauthorized}, Tracer: tr}

	_, err := Do(authed(), deps, Req{Kind: KindTool, Name: "t", Permission: "tool:t"}, func(context.Context, *Span) (string, error) {
		t.Fatal("operation must not run when authz denies")
		return "", nil
	})
	if !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if len(tr.obs) != 1 || tr.obs[0].ErrorKind != "unauthorized" {
		t.Fatalf("obs = %+v", tr.obs)
	}
}

func TestDoRequiresPrincipal(t *testing.T) {
	tr := &fakeTracer{}
	_, err := Do(context.Background(), Deps{Tracer: tr}, Req{Kind: KindLLM, Name: "m"}, func(context.Context, *Span) (string, error) {
		t.Fatal("operation must not run without a principal")
		return "", nil
	})
	if !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if len(tr.obs) != 1 || tr.obs[0].ErrorKind != "unauthorized" {
		t.Fatalf("obs = %+v", tr.obs)
	}
}
