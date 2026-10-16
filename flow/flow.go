package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	invopop "github.com/invopop/jsonschema"
	stjs "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/gobellim/kit/internal/core"
)

type State map[string]any

type Status string

const (
	StatusSuccess   Status = "success"
	StatusFailed    Status = "failed"
	StatusSuspended Status = "suspended"
)

type Suspension struct {
	Payload any `json:"payload,omitempty"`
}

func Suspend(payload any) *Suspension { return &Suspension{Payload: payload} }

func (s *Suspension) Error() string { return "flow: suspended" }

type StepResult struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
}

type Result[Out any] struct {
	Status     Status
	Output     Out
	Error      error
	Suspension *Suspension
	Snapshot   []byte
	State      State
	Steps      []StepResult
}

type Step[In, Out any] struct {
	r runnable
}

func New[In, Out any](id string, fn func(ctx context.Context, st State, in In) (Out, error)) (*Step[In, Out], error) {
	if id == "" {
		return nil, fmt.Errorf("flow: step id is required: %w", core.ErrInvalidConfig)
	}
	if fn == nil {
		return nil, fmt.Errorf("flow: step %s: run function is required: %w", id, core.ErrInvalidConfig)
	}
	in, err := schemaFor[In](id + ":in")
	if err != nil {
		return nil, err
	}
	out, err := schemaFor[Out](id + ":out")
	if err != nil {
		return nil, err
	}
	return &Step[In, Out]{r: &runner[In, Out]{stepID: id, in: in, out: out, fn: fn}}, nil
}

func NewResumable[In, Out, Resume any](
	id string,
	fn func(ctx context.Context, st State, in In) (Out, error),
	onResume func(ctx context.Context, st State, in In, payload Resume) (Out, error),
) (*Step[In, Out], error) {
	if onResume == nil {
		return nil, fmt.Errorf("flow: step %s: resume function is required: %w", id, core.ErrInvalidConfig)
	}
	step, err := New(id, fn)
	if err != nil {
		return nil, err
	}
	resumeSchema, err := schemaFor[Resume](id + ":resume")
	if err != nil {
		return nil, err
	}
	r := step.r.(*runner[In, Out])
	r.resumeSchema = resumeSchema
	r.resumeFn = func(ctx context.Context, st State, in In, payload json.RawMessage) (Out, error) {
		var p Resume
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &p); err != nil {
				var zero Out
				return zero, fmt.Errorf("flow: step %s: decode resume payload: %w: %w", id, err, core.ErrInvalidArgs)
			}
		}
		return onResume(ctx, st, in, p)
	}
	return step, nil
}

func (s *Step[In, Out]) ID() string { return s.r.id() }

func (s *Step[In, Out]) Pipeline() *Pipeline[In, Out] {
	return &Pipeline[In, Out]{steps: []runnable{s.r}, in: s.r.inputSchema(), out: s.r.outputSchema()}
}

type Pipeline[In, Out any] struct {
	steps []runnable
	in    *compiled
	out   *compiled
}

func Then[In, Mid, Out any](p *Pipeline[In, Mid], next *Step[Mid, Out]) *Pipeline[In, Out] {
	steps := make([]runnable, 0, len(p.steps)+1)
	steps = append(steps, p.steps...)
	steps = append(steps, next.r)
	return &Pipeline[In, Out]{steps: steps, in: p.in, out: next.r.outputSchema()}
}

func Chain[A, B, C any](first *Step[A, B], second *Step[B, C]) *Pipeline[A, C] {
	return Then(first.Pipeline(), second)
}

func Chain3[A, B, C, D any](s1 *Step[A, B], s2 *Step[B, C], s3 *Step[C, D]) *Pipeline[A, D] {
	return Then(Then(s1.Pipeline(), s2), s3)
}

func (p *Pipeline[In, Out]) Run(ctx context.Context, in In) Result[Out] {
	return p.RunWithState(ctx, nil, in)
}

func (p *Pipeline[In, Out]) RunWithState(ctx context.Context, st State, in In) Result[Out] {
	raw, err := json.Marshal(in)
	if err != nil {
		return Result[Out]{Status: StatusFailed, Error: fmt.Errorf("flow: encode input: %w: %w", err, core.ErrInvalidArgs)}
	}
	return p.runFrom(ctx, st, raw, 0, nil, nil)
}

func (p *Pipeline[In, Out]) Resume(ctx context.Context, snapshotBytes []byte, data any) Result[Out] {
	var snap snapshot
	if err := json.Unmarshal(snapshotBytes, &snap); err != nil {
		return Result[Out]{Status: StatusFailed, Error: fmt.Errorf("flow: decode snapshot: %w: %w", err, core.ErrInvalidArgs)}
	}
	if snap.StepIndex < 0 || snap.StepIndex >= len(p.steps) {
		return Result[Out]{Status: StatusFailed, Error: fmt.Errorf("flow: snapshot step index %d is out of range: %w", snap.StepIndex, core.ErrInvalidArgs)}
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return Result[Out]{Status: StatusFailed, Error: fmt.Errorf("flow: encode resume payload: %w: %w", err, core.ErrInvalidArgs)}
	}
	st := State(snap.State)
	if st == nil {
		st = State{}
	}
	return p.runFrom(ctx, st, snap.StepInput, snap.StepIndex, payload, snap.Steps)
}

type snapshot struct {
	State     map[string]any  `json:"state"`
	StepIndex int             `json:"step_index"`
	StepInput json.RawMessage `json:"step_input"`
	Steps     []StepResult    `json:"steps"`
}

func (p *Pipeline[In, Out]) runFrom(ctx context.Context, st State, input json.RawMessage, from int, payload json.RawMessage, prior []StepResult) Result[Out] {
	if st == nil {
		st = State{}
	}
	steps := make([]StepResult, 0, len(p.steps))
	steps = append(steps, prior...)

	if err := ctx.Err(); err != nil {
		return Result[Out]{Status: StatusFailed, Error: err, State: st, Steps: steps}
	}
	if from == 0 {
		if err := validate(p.in, input, "input"); err != nil {
			return Result[Out]{Status: StatusFailed, Error: err, State: st, Steps: steps}
		}
	}

	current := input
	for i := from; i < len(p.steps); i++ {
		step := p.steps[i]

		var (
			raw json.RawMessage
			err error
		)
		if i == from && len(payload) > 0 {
			raw, err = step.resume(ctx, st, current, payload)
		} else {
			raw, err = step.run(ctx, st, current)
		}
		if err != nil {
			var susp *Suspension
			if errors.As(err, &susp) {
				snap, serr := json.Marshal(snapshot{State: st, StepIndex: i, StepInput: current, Steps: steps})
				if serr != nil {
					return Result[Out]{Status: StatusFailed, Error: fmt.Errorf("flow: encode snapshot: %w", serr), State: st, Steps: steps}
				}
				seen := append(append([]StepResult(nil), steps...), StepResult{ID: step.id(), Status: StatusSuspended})
				return Result[Out]{Status: StatusSuspended, Suspension: susp, Snapshot: snap, State: st, Steps: seen}
			}
			steps = append(steps, StepResult{ID: step.id(), Status: StatusFailed, Error: err.Error()})
			return Result[Out]{Status: StatusFailed, Error: err, State: st, Steps: steps}
		}
		steps = append(steps, StepResult{ID: step.id(), Status: StatusSuccess})
		current = raw
	}

	if err := validate(p.out, current, "output"); err != nil {
		return Result[Out]{Status: StatusFailed, Error: err, State: st, Steps: steps}
	}
	var out Out
	if len(current) > 0 {
		if err := json.Unmarshal(current, &out); err != nil {
			return Result[Out]{Status: StatusFailed, Error: fmt.Errorf("flow: decode output: %w: %w", err, core.ErrInvalidArgs), State: st, Steps: steps}
		}
	}
	return Result[Out]{Status: StatusSuccess, Output: out, State: st, Steps: steps}
}

type runnable interface {
	id() string
	inputSchema() *compiled
	outputSchema() *compiled
	run(ctx context.Context, st State, in json.RawMessage) (json.RawMessage, error)
	resume(ctx context.Context, st State, in, payload json.RawMessage) (json.RawMessage, error)
}

type runner[In, Out any] struct {
	stepID       string
	in           *compiled
	out          *compiled
	resumeSchema *compiled
	fn           func(ctx context.Context, st State, in In) (Out, error)
	resumeFn     func(ctx context.Context, st State, in In, payload json.RawMessage) (Out, error)
}

func (r *runner[In, Out]) id() string              { return r.stepID }
func (r *runner[In, Out]) inputSchema() *compiled  { return r.in }
func (r *runner[In, Out]) outputSchema() *compiled { return r.out }

func (r *runner[In, Out]) run(ctx context.Context, st State, in json.RawMessage) (json.RawMessage, error) {
	if err := validate(r.in, in, r.stepID+" input"); err != nil {
		return nil, err
	}
	decoded, err := decodeIn[In](in)
	if err != nil {
		return nil, fmt.Errorf("flow: step %s: %w: %w", r.stepID, err, core.ErrInvalidArgs)
	}
	out, err := r.fn(ctx, st, decoded)
	if err != nil {
		return nil, err
	}
	return r.encodeOut(out)
}

func (r *runner[In, Out]) resume(ctx context.Context, st State, in, payload json.RawMessage) (json.RawMessage, error) {
	if r.resumeFn == nil {
		return nil, fmt.Errorf("flow: step %s is not resumable: %w", r.stepID, core.ErrInvalidArgs)
	}
	if err := validate(r.resumeSchema, payload, r.stepID+" resume"); err != nil {
		return nil, err
	}
	decoded, err := decodeIn[In](in)
	if err != nil {
		return nil, fmt.Errorf("flow: step %s: %w: %w", r.stepID, err, core.ErrInvalidArgs)
	}
	out, err := r.resumeFn(ctx, st, decoded, payload)
	if err != nil {
		return nil, err
	}
	return r.encodeOut(out)
}

func (r *runner[In, Out]) encodeOut(out Out) (json.RawMessage, error) {
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("flow: step %s: encode output: %w", r.stepID, err)
	}
	if err := validate(r.out, raw, r.stepID+" output"); err != nil {
		return nil, err
	}
	return raw, nil
}

func decodeIn[In any](raw json.RawMessage) (In, error) {
	var in In
	if len(raw) == 0 {
		return in, nil
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, fmt.Errorf("decode input: %w", err)
	}
	return in, nil
}

type compiled struct {
	raw json.RawMessage
	sch *stjs.Schema
}

func schemaFor[T any](name string) (*compiled, error) {
	var v T
	var value any = v
	rv := reflect.ValueOf(v)
	switch {
	case !rv.IsValid():
		return nil, nil
	case rv.Kind() == reflect.Pointer && rv.IsNil():
		value = reflect.New(rv.Type().Elem()).Interface()
	}
	raw, err := reflectSchema(value)
	if err != nil {
		return nil, fmt.Errorf("flow: %s: %w", name, err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("flow: %s: %w", name, err)
	}
	c := stjs.NewCompiler()
	if err := c.AddResource(name, doc); err != nil {
		return nil, fmt.Errorf("flow: %s: %w", name, err)
	}
	sch, err := c.Compile(name)
	if err != nil {
		return nil, fmt.Errorf("flow: %s: %w", name, err)
	}
	return &compiled{raw: raw, sch: sch}, nil
}

func reflectSchema(v any) (json.RawMessage, error) {
	r := &invopop.Reflector{DoNotReference: true, ExpandedStruct: true}
	raw, err := json.Marshal(r.Reflect(v))
	if err != nil {
		return nil, fmt.Errorf("reflect schema: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("normalize schema: %w", err)
	}
	delete(m, "$schema")
	delete(m, "$id")
	if _, isObject := m["properties"]; isObject || m["type"] == "object" {
		m["additionalProperties"] = false
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode schema: %w", err)
	}
	return out, nil
}

func validate(c *compiled, raw json.RawMessage, label string) error {
	if c == nil {
		return nil
	}
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("flow: %s: %w: %w", label, err, core.ErrInvalidArgs)
	}
	if err := c.sch.Validate(v); err != nil {
		return fmt.Errorf("flow: %s: %w: %w", label, err, core.ErrInvalidArgs)
	}
	return nil
}
