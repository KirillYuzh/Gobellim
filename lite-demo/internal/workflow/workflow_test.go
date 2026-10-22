package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type state struct {
	order []string
	code  string
}

func TestRunThenAndBranch(t *testing.T) {
	st := &state{}
	flow := New[state]("research").
		Then("kb", func(context.Context, *state) error {
			st.order = append(st.order, "kb")
			return nil
		}).
		Then("remote", func(context.Context, *state) error {
			st.order = append(st.order, "remote")
			st.code = "b"
			return nil
		}).
		Branch("route", func(s *state) string { return s.code }, map[string]*Flow[state]{
			"a": New[state]("a").Then("relay", func(context.Context, *state) error {
				st.order = append(st.order, "relay")
				return nil
			}),
			"b": New[state]("b").Then("ask", func(context.Context, *state) error {
				st.order = append(st.order, "ask")
				return nil
			}),
		})

	if err := flow.Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := strings.Join(st.order, ","), "kb,remote,ask"; got != want {
		t.Fatalf("order = %q, want %q", got, want)
	}
}

func TestRunStopsOnErrorWithPath(t *testing.T) {
	boom := errors.New("boom")
	flow := New[state]("f").Then("step", func(context.Context, *state) error { return boom })
	err := flow.Run(context.Background(), &state{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if !strings.Contains(err.Error(), "f/step") {
		t.Fatalf("err = %q, want path f/step", err)
	}
}

func TestRunHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	flow := New[state]("f").Then("step", func(context.Context, *state) error {
		ran = true
		return nil
	})
	if err := flow.Run(ctx, &state{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if ran {
		t.Fatal("step ran after cancellation")
	}
}

func TestStepPanicBecomesError(t *testing.T) {
	flow := New[state]("f").Then("step", func(context.Context, *state) error { panic("kaboom") })
	err := flow.Run(context.Background(), &state{})
	if err == nil || !strings.Contains(err.Error(), "panic: kaboom") {
		t.Fatalf("err = %v, want recovered panic", err)
	}
}

func TestObserverReceivesEventAndPanicIsContained(t *testing.T) {
	var seen []string
	obs := ObserverFunc[state](func(_ context.Context, ev Event[state]) {
		seen = append(seen, ev.Step)
		if ev.Step == "boom" {
			panic("observer")
		}
	})
	flow := New[state]("f").
		WithObserver(obs).
		Then("ok", func(context.Context, *state) error { return nil }).
		Then("boom", func(context.Context, *state) error { return nil })

	err := flow.Run(context.Background(), &state{})
	if !errors.Is(err, ErrObserver) {
		t.Fatalf("err = %v, want ErrObserver", err)
	}
	if strings.Join(seen, ",") != "ok,boom" {
		t.Fatalf("observer saw %v", seen)
	}
}

func TestObserverGetsDuration(t *testing.T) {
	var d time.Duration
	flow := New[state]("f").
		WithObserver(ObserverFunc[state](func(_ context.Context, ev Event[state]) { d = ev.Duration })).
		Then("step", func(context.Context, *state) error { return nil })
	if err := flow.Run(context.Background(), &state{}); err != nil {
		t.Fatal(err)
	}
	if d < 0 {
		t.Fatalf("duration = %v", d)
	}
}

func TestValidateRejectsBadShapes(t *testing.T) {
	cases := map[string]*Flow[state]{
		"empty step name": New[state]("f").Then("", func(context.Context, *state) error { return nil }),
		"nil run":         New[state]("f").Then("s", nil),
		"duplicate":       New[state]("f").Then("s", func(context.Context, *state) error { return nil }).Then("s", func(context.Context, *state) error { return nil }),
		"missing branch": New[state]("f").Branch("b", func(*state) string { return "x" }, map[string]*Flow[state]{
			"a": New[state]("a"),
		}),
	}
	for name, flow := range cases {
		t.Run(name, func(t *testing.T) {
			st := &state{}
			err := flow.Run(context.Background(), st)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateRejectsCycle(t *testing.T) {
	flow := New[state]("f")
	flow.Branch("loop", func(*state) string { return "self" }, map[string]*Flow[state]{"self": flow})
	if err := flow.Validate(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err = %v, want cycle", err)
	}
}

func TestRunRejectsNilState(t *testing.T) {
	if err := New[state]("f").Run(context.Background(), nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRunRejectsNilFlow(t *testing.T) {
	var flow *Flow[state]
	if err := flow.Run(context.Background(), &state{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestBranchDecidePanicBecomesError(t *testing.T) {
	flow := New[state]("f").Branch("route", func(*state) string { panic("decide") }, map[string]*Flow[state]{
		"x": New[state]("x"),
	})
	err := flow.Run(context.Background(), &state{})
	if err == nil || !strings.Contains(err.Error(), "panic: decide") {
		t.Fatalf("err = %v, want recovered decide panic", err)
	}
}

func TestObserverIsInheritedBySubFlows(t *testing.T) {
	var seen []string
	flow := New[state]("root").
		WithObserver(ObserverFunc[state](func(_ context.Context, ev Event[state]) {
			seen = append(seen, ev.Flow+"/"+ev.Step)
		})).
		Branch("route", func(*state) string { return "x" }, map[string]*Flow[state]{
			"x": New[state]("sub").Then("step", func(context.Context, *state) error { return nil }),
		})
	if err := flow.Run(context.Background(), &state{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(seen, ","); got != "sub/step" {
		t.Fatalf("observer saw %q, want sub/step", got)
	}
}

func TestSuspendAndResume(t *testing.T) {
	ctx := context.Background()
	flow := New[state]("ask").
		Then("first", func(ctx context.Context, st *state) error {
			if st.code != "" {
				return nil
			}
			return Suspend(ctx, "q1")
		}).
		Then("second", func(ctx context.Context, st *state) error {
			if len(st.order) > 0 {
				return nil
			}
			return Suspend(ctx, "q2")
		})

	st := &state{}
	exec, err := flow.Start(ctx, st)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if exec.Done() || exec.Suspension() == nil || exec.Suspension().Payload != "q1" {
		t.Fatalf("first pause = %+v", exec.Suspension())
	}
	if exec.Suspension().Flow != "ask" || exec.Suspension().Step != "first" {
		t.Fatalf("pause path = %s/%s", exec.Suspension().Flow, exec.Suspension().Step)
	}

	if err := exec.Resume(ctx, func(s *state) error {
		s.code = "answer"
		return nil
	}); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if exec.Suspension() == nil || exec.Suspension().Payload != "q2" {
		t.Fatalf("second pause = %+v", exec.Suspension())
	}

	if err := exec.Resume(ctx, func(s *state) error {
		s.order = append(s.order, "ready")
		return nil
	}); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !exec.Done() || exec.Suspension() != nil {
		t.Fatalf("execution is not done: done=%v pause=%+v", exec.Done(), exec.Suspension())
	}
	if err := exec.Resume(ctx, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("resume after done: err = %v, want ErrInvalid", err)
	}
}

func TestRunReturnsSuspension(t *testing.T) {
	flow := New[state]("ask").Then("q", func(ctx context.Context, _ *state) error {
		return Suspend(ctx, "q")
	})
	err := flow.Run(context.Background(), &state{})
	var susp *Suspension
	if !errors.As(err, &susp) || susp.Payload != "q" {
		t.Fatalf("err = %v, want suspension", err)
	}
}
