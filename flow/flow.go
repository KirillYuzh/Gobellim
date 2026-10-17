package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	invopop "github.com/invopop/jsonschema"
	stjs "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// State is the JSON-serialisable key/value bag shared by steps and carried
// through suspension and resume.
type State map[string]any

// Status is the outcome of a step run.
type Status string

const (
	// StatusSuccess means the step produced an output.
	StatusSuccess Status = "success"
	// StatusFailed means the step returned an error.
	StatusFailed Status = "failed"
	// StatusSuspended means the step suspended and can be resumed from Result.Snapshot.
	StatusSuspended Status = "suspended"
)

// Suspension is the error value a step returns to pause; it carries an optional
// payload for the caller and Resume.
type Suspension struct {
	Payload any `json:"payload,omitempty"`
}

// Suspend returns a *Suspension carrying payload, which Run reports as
// StatusSuspended together with a snapshot.
func Suspend(payload any) *Suspension { return &Suspension{Payload: payload} }

// Error implements error so a step can return a Suspension from its run function.
func (s *Suspension) Error() string { return "flow: suspended" }

// Result is the outcome of a step run: status, output or error, the state, and,
// when suspended, the suspension and the snapshot needed to resume.
type Result[Out any] struct {
	Status     Status
	Output     Out
	Error      error
	Suspension *Suspension
	Snapshot   []byte
	State      State
}

// Step is a typed flow step that validates its input and output against the
// JSON schemas derived from In and Out.
type Step[In, Out any] struct {
	r runnable
}

// New builds a step with the given id that runs fn. It derives input and output
// schemas from In and Out and rejects an empty id or nil fn with
// core.ErrInvalidConfig.
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

// NewResumable builds a resumable step: a suspended Run can be continued with
// Resume, whose payload is validated against the schema of Resume.
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

// ID returns the step's identifier.
func (s *Step[In, Out]) ID() string { return s.r.id() }

// Run validates in against the schema of In, invokes the step, and validates the
// output; a Suspend error becomes StatusSuspended. A nil st is treated as an
// empty State.
func (s *Step[In, Out]) Run(ctx context.Context, st State, in In) Result[Out] {
	if st == nil {
		st = State{}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return Result[Out]{Status: StatusFailed, Error: fmt.Errorf("flow: encode input: %w: %w", err, core.ErrInvalidArgs), State: st}
	}
	if err := ctx.Err(); err != nil {
		return Result[Out]{Status: StatusFailed, Error: err, State: st}
	}
	out, err := s.r.run(ctx, st, raw)
	if err != nil {
		return failure[Out](err, st, raw)
	}
	return success[Out](out, st)
}

// Resume decodes a snapshot from a suspended Run, validates data against the
// Resume schema, and continues the step through its onResume function. It fails
// with core.ErrInvalidArgs when the step is not resumable or the snapshot is
// malformed.
func (s *Step[In, Out]) Resume(ctx context.Context, snapshotBytes []byte, data any) Result[Out] {
	var snap snapshot
	if err := json.Unmarshal(snapshotBytes, &snap); err != nil {
		return Result[Out]{Status: StatusFailed, Error: fmt.Errorf("flow: decode snapshot: %w: %w", err, core.ErrInvalidArgs)}
	}
	st := State(snap.State)
	if st == nil {
		st = State{}
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return Result[Out]{Status: StatusFailed, Error: fmt.Errorf("flow: encode resume payload: %w: %w", err, core.ErrInvalidArgs), State: st}
	}
	out, err := s.r.resume(ctx, st, snap.StepInput, payload)
	if err != nil {
		return failure[Out](err, st, snap.StepInput)
	}
	return success[Out](out, st)
}

func failure[Out any](err error, st State, input json.RawMessage) Result[Out] {
	var susp *Suspension
	if errors.As(err, &susp) {
		snap, serr := json.Marshal(snapshot{State: st, StepInput: input})
		if serr != nil {
			return Result[Out]{Status: StatusFailed, Error: fmt.Errorf("flow: encode snapshot: %w", serr), State: st}
		}
		return Result[Out]{Status: StatusSuspended, Suspension: susp, Snapshot: snap, State: st}
	}
	return Result[Out]{Status: StatusFailed, Error: err, State: st}
}

func success[Out any](out json.RawMessage, st State) Result[Out] {
	var value Out
	if len(out) > 0 {
		if err := json.Unmarshal(out, &value); err != nil {
			return Result[Out]{Status: StatusFailed, Error: fmt.Errorf("flow: decode output: %w: %w", err, core.ErrInvalidArgs), State: st}
		}
	}
	return Result[Out]{Status: StatusSuccess, Output: value, State: st}
}

type snapshot struct {
	State     map[string]any  `json:"state"`
	StepInput json.RawMessage `json:"step_input"`
}

type runnable interface {
	id() string
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

func (r *runner[In, Out]) id() string { return r.stepID }

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
