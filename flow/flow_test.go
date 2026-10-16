package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gobellim/kit/internal/core"
)

func TestPipelineRunsTypedChain(t *testing.T) {
	double, err := New("double", func(_ context.Context, _ State, in int) (int, error) {
		return in * 2, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	label, err := New("label", func(_ context.Context, st State, in int) (string, error) {
		st["value"] = in
		return fmt.Sprintf("v=%d", in), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	res := Chain(double, label).Run(context.Background(), 3)
	if res.Status != StatusSuccess {
		t.Fatalf("status = %s, err = %v", res.Status, res.Error)
	}
	if res.Output != "v=6" {
		t.Fatalf("output = %q", res.Output)
	}
	if len(res.Steps) != 2 || res.Steps[0].ID != "double" || res.Steps[1].ID != "label" {
		t.Fatalf("steps = %+v", res.Steps)
	}
	if res.State["value"] != 6 {
		t.Fatalf("state = %+v", res.State)
	}
}

func TestPipelineKeepsStepOrderAcrossThreeSteps(t *testing.T) {
	a, err := New("a", func(_ context.Context, _ State, in int) (int, error) { return in + 1, nil })
	if err != nil {
		t.Fatal(err)
	}
	b, err := New("b", func(_ context.Context, _ State, in int) (int, error) { return in * 10, nil })
	if err != nil {
		t.Fatal(err)
	}
	c, err := New("c", func(_ context.Context, _ State, in int) (string, error) {
		return fmt.Sprintf("%d", in), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	res := Chain3(a, b, c).Run(context.Background(), 1)
	if res.Status != StatusSuccess || res.Output != "20" {
		t.Fatalf("res = %+v", res)
	}
	ids := []string{res.Steps[0].ID, res.Steps[1].ID, res.Steps[2].ID}
	if strings.Join(ids, ",") != "a,b,c" {
		t.Fatalf("ids = %v", ids)
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
	p := newApprovalStep(t).Pipeline()

	res := p.Run(context.Background(), approvalIn{Amount: 7})
	if res.Status != StatusSuspended {
		t.Fatalf("status = %s, err = %v", res.Status, res.Error)
	}
	if res.Suspension == nil || len(res.Snapshot) == 0 {
		t.Fatalf("res = %+v, want a suspension and a snapshot", res)
	}

	resumed := p.Resume(context.Background(), res.Snapshot, approvalResume{Approved: true})
	if resumed.Status != StatusSuccess {
		t.Fatalf("resume status = %s, err = %v", resumed.Status, resumed.Error)
	}
	if !resumed.Output.Approved {
		t.Fatalf("output = %+v", resumed.Output)
	}
	if resumed.State["amount"] != float64(7) {
		t.Fatalf("state did not survive the resume: %+v", resumed.State)
	}
	if len(resumed.Steps) != 1 || resumed.Steps[0].Status != StatusSuccess {
		t.Fatalf("steps = %+v", resumed.Steps)
	}
}

func TestResumeRejectsWrongPayloadShape(t *testing.T) {
	p := newApprovalStep(t).Pipeline()

	res := p.Run(context.Background(), approvalIn{Amount: 1})
	if res.Status != StatusSuspended {
		t.Fatalf("status = %s", res.Status)
	}

	resumed := p.Resume(context.Background(), res.Snapshot, map[string]any{"approved": "yes"})
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
	snap, err := json.Marshal(snapshot{
		State:     map[string]any{},
		StepIndex: 0,
		StepInput: json.RawMessage("1"),
	})
	if err != nil {
		t.Fatal(err)
	}

	res := step.Pipeline().Resume(context.Background(), snap, nil)
	if res.Status != StatusFailed || !errors.Is(res.Error, core.ErrInvalidArgs) {
		t.Fatalf("res = %+v, want a failure wrapping core.ErrInvalidArgs", res)
	}
}

func TestStepFailureStopsThePipeline(t *testing.T) {
	boom, err := New("boom", func(_ context.Context, _ State, _ int) (int, error) {
		return 0, errors.New("kaboom")
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := New("after", func(_ context.Context, _ State, in int) (int, error) { return in, nil })
	if err != nil {
		t.Fatal(err)
	}

	res := Then(boom.Pipeline(), after).Run(context.Background(), 1)
	if res.Status != StatusFailed || !strings.Contains(res.Error.Error(), "kaboom") {
		t.Fatalf("res = %+v", res)
	}
	if len(res.Steps) != 1 || res.Steps[0].ID != "boom" || res.Steps[0].Status != StatusFailed {
		t.Fatalf("steps = %+v, want only the failing step", res.Steps)
	}
}

func TestCanceledContextFailsTheRun(t *testing.T) {
	step, err := New("id", func(_ context.Context, _ State, in int) (int, error) { return in, nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := step.Pipeline().Run(ctx, 1)
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
	res := step.Pipeline().Run(context.Background(), map[string]any{"k": "v"})
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
