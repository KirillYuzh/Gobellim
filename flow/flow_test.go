package flow

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gobellim/kit/internal/core"
)

func TestStepRunsTypedFunction(t *testing.T) {
	step, err := New("double", func(_ context.Context, st State, in int) (int, error) {
		st["value"] = in
		return in * 2, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	res := step.Run(context.Background(), nil, 3)
	if res.Status != StatusSuccess {
		t.Fatalf("status = %s, err = %v", res.Status, res.Error)
	}
	if res.Output != 6 {
		t.Fatalf("output = %d", res.Output)
	}
	if res.State["value"] != 3 {
		t.Fatalf("state = %+v", res.State)
	}
}

func TestStepsComposeByCallingThem(t *testing.T) {
	double, err := New("double", func(_ context.Context, _ State, in int) (int, error) { return in * 2, nil })
	if err != nil {
		t.Fatal(err)
	}
	label, err := New("label", func(_ context.Context, _ State, in int) (string, error) {
		return fmt.Sprintf("v=%d", in), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	first := double.Run(context.Background(), nil, 3)
	if first.Status != StatusSuccess {
		t.Fatalf("first = %+v", first)
	}
	second := label.Run(context.Background(), first.State, first.Output)
	if second.Status != StatusSuccess || second.Output != "v=6" {
		t.Fatalf("second = %+v", second)
	}
}

type approvalIn struct {
	Amount int `json:"amount"`
}

type approvalOut struct {
	Approved bool `json:"approved"`
}

type approvalResume struct {
	Approved bool `json:"approved"`
}

func newApprovalStep(t *testing.T) *Step[approvalIn, approvalOut] {
	t.Helper()
	step, err := NewResumable[approvalIn, approvalOut, approvalResume]("approve",
		func(_ context.Context, st State, in approvalIn) (approvalOut, error) {
			st["amount"] = in.Amount
			return approvalOut{}, Suspend(map[string]any{"question": "approve?"})
		},
		func(_ context.Context, _ State, in approvalIn, p approvalResume) (approvalOut, error) {
			return approvalOut{Approved: p.Approved && in.Amount > 0}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return step
}

func TestSuspendThenResume(t *testing.T) {
	step := newApprovalStep(t)

	res := step.Run(context.Background(), nil, approvalIn{Amount: 7})
	if res.Status != StatusSuspended {
		t.Fatalf("status = %s, err = %v", res.Status, res.Error)
	}
	if res.Suspension == nil || len(res.Snapshot) == 0 {
		t.Fatalf("res = %+v, want a suspension and a snapshot", res)
	}
	if res.Suspension.Payload == nil {
		t.Fatalf("suspension payload = %+v", res.Suspension)
	}

	resumed := step.Resume(context.Background(), res.Snapshot, approvalResume{Approved: true})
	if resumed.Status != StatusSuccess {
		t.Fatalf("resume status = %s, err = %v", resumed.Status, resumed.Error)
	}
	if !resumed.Output.Approved {
		t.Fatalf("output = %+v", resumed.Output)
	}
	if resumed.State["amount"] != float64(7) {
		t.Fatalf("state did not survive the resume: %+v", resumed.State)
	}
}

func TestResumeRejectsWrongPayloadShape(t *testing.T) {
	step := newApprovalStep(t)

	res := step.Run(context.Background(), nil, approvalIn{Amount: 1})
	if res.Status != StatusSuspended {
		t.Fatalf("status = %s", res.Status)
	}

	resumed := step.Resume(context.Background(), res.Snapshot, map[string]any{"approved": "yes"})
	if resumed.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", resumed.Status)
	}
	if !errors.Is(resumed.Error, core.ErrInvalidArgs) {
		t.Fatalf("err = %v, want core.ErrInvalidArgs", resumed.Error)
	}
}

func TestResumeOnNonResumableStepFails(t *testing.T) {
	step, err := New("plain", func(_ context.Context, _ State, in int) (int, error) { return in, nil })
	if err != nil {
		t.Fatal(err)
	}

	snap := []byte(`{"state":{},"step_input":1}`)
	res := step.Resume(context.Background(), snap, nil)
	if res.Status != StatusFailed || !errors.Is(res.Error, core.ErrInvalidArgs) {
		t.Fatalf("res = %+v, want a failure wrapping core.ErrInvalidArgs", res)
	}
}

func TestResumeRejectsCorruptSnapshot(t *testing.T) {
	step := newApprovalStep(t)
	res := step.Resume(context.Background(), []byte("not json"), nil)
	if res.Status != StatusFailed || !errors.Is(res.Error, core.ErrInvalidArgs) {
		t.Fatalf("res = %+v", res)
	}
}

func TestStepFailureIsReturned(t *testing.T) {
	step, err := New("boom", func(_ context.Context, _ State, _ int) (int, error) {
		return 0, errors.New("kaboom")
	})
	if err != nil {
		t.Fatal(err)
	}

	res := step.Run(context.Background(), nil, 1)
	if res.Status != StatusFailed || res.Error == nil || res.Error.Error() != "kaboom" {
		t.Fatalf("res = %+v", res)
	}
}

func TestCanceledContextFailsTheRun(t *testing.T) {
	step, err := New("id", func(_ context.Context, _ State, in int) (int, error) { return in, nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := step.Run(ctx, nil, 1)
	if res.Status != StatusFailed || !errors.Is(res.Error, context.Canceled) {
		t.Fatalf("res = %+v, want a failure wrapping context.Canceled", res)
	}
}

func TestInterfaceTypesHaveNoSchema(t *testing.T) {
	step, err := New[any, any]("passthrough", func(_ context.Context, _ State, in any) (any, error) {
		return in, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res := step.Run(context.Background(), nil, map[string]any{"k": "v"})
	if res.Status != StatusSuccess {
		t.Fatalf("status = %s, err = %v", res.Status, res.Error)
	}
	got, ok := res.Output.(map[string]any)
	if !ok || got["k"] != "v" {
		t.Fatalf("output = %#v", res.Output)
	}
}

func TestConstructorValidation(t *testing.T) {
	if _, err := New("", func(_ context.Context, _ State, in int) (int, error) { return in, nil }); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("empty id err = %v, want core.ErrInvalidConfig", err)
	}
	if _, err := New[int, int]("x", nil); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("nil run err = %v, want core.ErrInvalidConfig", err)
	}
	if _, err := NewResumable[approvalIn, approvalOut, approvalResume]("x",
		func(_ context.Context, _ State, in approvalIn) (approvalOut, error) { return approvalOut{}, nil }, nil); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("nil onResume err = %v, want core.ErrInvalidConfig", err)
	}
}
