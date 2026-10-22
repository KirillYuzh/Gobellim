// Package workflow is a deterministic, typed state machine: a scenario runs its
// steps in the declared order regardless of what the model does, and a branch
// chooses a sub-flow from the state.
//
// A step receives the same typed state pointer and mutates it explicitly, so a
// typo in a field is a compile error rather than a zero value from a map.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrInvalid marks a malformed flow: an empty or duplicate step name, a nil
// callback, a missing branch, a cycle, or a nil state.
var ErrInvalid = errors.New("workflow: invalid")

// ErrObserver marks an observer that panicked. The panic is recovered and
// returned as an error instead of crashing the process.
var ErrObserver = errors.New("workflow: observer failed")

const (
	// maxDepth bounds nested branch flows.
	maxDepth = 32
	// maxSteps bounds the steps of a single Run, including nested flows.
	maxSteps = 1000
)

// Step is one named mutation of the state.
type Step[S any] struct {
	Name string
	Run  func(ctx context.Context, st *S) error
}

// Branch chooses one sub-flow from the state. Decide returns a route name.
type Branch[S any] struct {
	Name   string
	Decide func(st *S) string
	Routes map[string]*Flow[S]
}

// Event is what an observer sees after one step.
type Event[S any] struct {
	Flow     string
	Step     string
	State    *S
	Err      error
	Duration time.Duration
}

// Observer is called after every step. A panicking observer does not crash the
// flow: its panic becomes ErrObserver.
type Observer[S any] interface {
	Step(ctx context.Context, ev Event[S])
}

// ObserverFunc adapts a function to Observer.
type ObserverFunc[S any] func(ctx context.Context, ev Event[S])

// Step calls the underlying function.
func (f ObserverFunc[S]) Step(ctx context.Context, ev Event[S]) { f(ctx, ev) }

type node[S any] struct {
	step   *Step[S]
	branch *Branch[S]
}

// Flow is an ordered list of steps and branches over one state type.
type Flow[S any] struct {
	name  string
	nodes []node[S]
	obs   Observer[S]
}

// New returns an empty flow with the given name. The name is used in error
// paths and observer events.
func New[S any](name string) *Flow[S] {
	return &Flow[S]{name: name}
}

// Name returns the flow name.
func (f *Flow[S]) Name() string { return f.name }

// Then appends a step and returns the flow, so calls chain.
func (f *Flow[S]) Then(name string, run func(context.Context, *S) error) *Flow[S] {
	f.nodes = append(f.nodes, node[S]{step: &Step[S]{Name: name, Run: run}})
	return f
}

// Branch appends a branch and returns the flow, so calls chain.
func (f *Flow[S]) Branch(name string, decide func(*S) string, routes map[string]*Flow[S]) *Flow[S] {
	f.nodes = append(f.nodes, node[S]{branch: &Branch[S]{Name: name, Decide: decide, Routes: routes}})
	return f
}

// WithObserver sets the observer called after every step and returns the flow.
func (f *Flow[S]) WithObserver(o Observer[S]) *Flow[S] {
	f.obs = o
	return f
}

// Validate reports the first structural problem of the flow tree.
func (f *Flow[S]) Validate() error {
	if f == nil {
		return fmt.Errorf("%w: nil flow", ErrInvalid)
	}
	return f.validate(map[*Flow[S]]bool{})
}

func (f *Flow[S]) validate(path map[*Flow[S]]bool) error {
	if path[f] {
		return fmt.Errorf("%w: %s: cycle", ErrInvalid, f.name)
	}
	path[f] = true
	defer delete(path, f)

	seen := make(map[string]struct{}, len(f.nodes))
	for _, n := range f.nodes {
		switch {
		case n.step != nil:
			if n.step.Name == "" {
				return fmt.Errorf("%w: %s: step name is required", ErrInvalid, f.name)
			}
			if n.step.Run == nil {
				return fmt.Errorf("%w: %s/%s: run function is required", ErrInvalid, f.name, n.step.Name)
			}
			if _, dup := seen[n.step.Name]; dup {
				return fmt.Errorf("%w: %s: step %q is declared twice", ErrInvalid, f.name, n.step.Name)
			}
			seen[n.step.Name] = struct{}{}
		case n.branch != nil:
			b := n.branch
			if b.Name == "" {
				return fmt.Errorf("%w: %s: branch name is required", ErrInvalid, f.name)
			}
			if b.Decide == nil {
				return fmt.Errorf("%w: %s/%s: decide function is required", ErrInvalid, f.name, b.Name)
			}
			if len(b.Routes) == 0 {
				return fmt.Errorf("%w: %s/%s: no routes", ErrInvalid, f.name, b.Name)
			}
			if _, dup := seen[b.Name]; dup {
				return fmt.Errorf("%w: %s: step %q is declared twice", ErrInvalid, f.name, b.Name)
			}
			seen[b.Name] = struct{}{}
			for route, sub := range b.Routes {
				if route == "" {
					return fmt.Errorf("%w: %s/%s: empty route name", ErrInvalid, f.name, b.Name)
				}
				if sub == nil {
					return fmt.Errorf("%w: %s/%s: route %q has no flow", ErrInvalid, f.name, b.Name, route)
				}
				if err := sub.validate(path); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("%w: %s: empty node", ErrInvalid, f.name)
		}
	}
	return nil
}

// Suspension pauses a flow at a step. Payload is typed by the caller: it is the
// question a human-in-the-loop flow is waiting for.
type Suspension struct {
	Flow    string
	Step    string
	Payload any
}

// Error implements error, so a step pauses the flow by returning it.
func (s *Suspension) Error() string {
	return fmt.Sprintf("workflow: suspended at %s/%s", s.Flow, s.Step)
}

// Suspend pauses the current step. Start returns the execution with the
// suspension, Run returns the *Suspension error, and Resume continues right
// after the step that suspended.
func Suspend(ctx context.Context, payload any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return &Suspension{Payload: payload}
}

// Execution is a started flow: the state, the position of every frame and the
// pending suspension.
type Execution[S any] struct {
	st    *S
	stack []frame[S]
	steps int
	susp  *Suspension
	done  bool
}

type frame[S any] struct {
	flow *Flow[S]
	next int
	obs  Observer[S]
}

// Start validates the flow and runs it until it suspends or ends. A suspension
// is not a failure: it is visible through Suspension.
func (f *Flow[S]) Start(ctx context.Context, st *S) (*Execution[S], error) {
	if st == nil {
		return nil, fmt.Errorf("%w: nil state", ErrInvalid)
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	e := &Execution[S]{st: st, stack: []frame[S]{{flow: f, obs: f.obs}}}
	if err := e.run(ctx); err != nil {
		var susp *Suspension
		if !errors.As(err, &susp) {
			return nil, err
		}
	}
	return e, nil
}

// Run executes the flow. A suspended flow is reported as a *Suspension error.
func (f *Flow[S]) Run(ctx context.Context, st *S) error {
	e, err := f.Start(ctx, st)
	if err != nil {
		return err
	}
	if susp := e.Suspension(); susp != nil {
		return susp
	}
	return nil
}

// State returns the state of the execution.
func (e *Execution[S]) State() *S { return e.st }

// Done reports that the flow reached its end.
func (e *Execution[S]) Done() bool { return e.done }

// Suspension returns the pending suspension or nil.
func (e *Execution[S]) Suspension() *Suspension { return e.susp }

// Resume continues the flow right after the step that suspended. apply runs
// exactly once, while the flow is suspended; an error from apply aborts the
// resume and leaves the flow suspended.
func (e *Execution[S]) Resume(ctx context.Context, apply func(*S) error) error {
	if e.done || e.susp == nil {
		return fmt.Errorf("%w: nothing to resume", ErrInvalid)
	}
	if apply == nil {
		return fmt.Errorf("%w: apply is required", ErrInvalid)
	}
	if err := apply(e.st); err != nil {
		return err
	}
	e.susp = nil
	if err := e.run(ctx); err != nil {
		var susp *Suspension
		if !errors.As(err, &susp) {
			return err
		}
	}
	return nil
}

// run executes nodes down the frame stack until the stack is empty. A pause is
// returned as a *Suspension and remembered in e.susp.
func (e *Execution[S]) run(ctx context.Context) error {
	for len(e.stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(e.stack) > maxDepth {
			return fmt.Errorf("%w: nesting deeper than %d", ErrInvalid, maxDepth)
		}
		top := &e.stack[len(e.stack)-1]
		if top.next >= len(top.flow.nodes) {
			e.stack = e.stack[:len(e.stack)-1]
			continue
		}
		n := top.flow.nodes[top.next]
		top.next++
		e.steps++
		if e.steps > maxSteps {
			return fmt.Errorf("%w: %s: more than %d steps", ErrInvalid, top.flow.name, maxSteps)
		}
		if n.step != nil {
			if err := e.runStep(ctx, top, n.step); err != nil {
				return err
			}
			continue
		}
		route, err := decide(top.flow.name, n.branch, e.st)
		if err != nil {
			return err
		}
		sub, ok := n.branch.Routes[route]
		if !ok || sub == nil {
			return fmt.Errorf("%w: %s/%s: no branch %q", ErrInvalid, top.flow.name, n.branch.Name, route)
		}
		obs := top.obs
		if sub.obs != nil {
			obs = sub.obs
		}
		e.stack = append(e.stack, frame[S]{flow: sub, obs: obs})
	}
	e.done = true
	return nil
}

// decide isolates a user callback: a panic in it is an error, not a crash.
func decide[S any](flowName string, b *Branch[S], st *S) (route string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%s/%s: panic: %v", flowName, b.Name, rec)
		}
	}()
	return b.Decide(st), nil
}

func (e *Execution[S]) runStep(ctx context.Context, fr *frame[S], s *Step[S]) (err error) {
	start := time.Now()
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%s/%s: panic: %v", fr.flow.name, s.Name, rec)
		} else if err != nil {
			var susp *Suspension
			if errors.As(err, &susp) {
				susp.Flow, susp.Step = fr.flow.name, s.Name
				e.susp = susp
			} else {
				err = fmt.Errorf("%s/%s: %w", fr.flow.name, s.Name, err)
			}
		}
		if oerr := observe(ctx, fr.obs, fr.flow.name, s.Name, e.st, err, time.Since(start)); oerr != nil && err == nil {
			err = oerr
		}
	}()
	return s.Run(ctx, e.st)
}

func observe[S any](ctx context.Context, obs Observer[S], flowName, stepName string, st *S, err error, d time.Duration) (oerr error) {
	if obs == nil {
		return nil
	}
	defer func() {
		if rec := recover(); rec != nil {
			oerr = fmt.Errorf("%w: %s/%s: panic: %v", ErrObserver, flowName, stepName, rec)
		}
	}()
	obs.Step(ctx, Event[S]{Flow: flowName, Step: stepName, State: st, Err: err, Duration: d})
	return nil
}
