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

// Run validates the flow and executes it against st. A step error stops the
// chain and carries the path as "flow/step".
func (f *Flow[S]) Run(ctx context.Context, st *S) error {
	if st == nil {
		return fmt.Errorf("%w: nil state", ErrInvalid)
	}
	if err := f.Validate(); err != nil {
		return err
	}
	n := 0
	return f.run(ctx, st, map[*Flow[S]]bool{}, 0, &n, f.obs)
}

func (f *Flow[S]) run(ctx context.Context, st *S, path map[*Flow[S]]bool, depth int, steps *int, obs Observer[S]) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: %s: nesting deeper than %d", ErrInvalid, f.name, maxDepth)
	}
	if path[f] {
		return fmt.Errorf("%w: %s: cycle", ErrInvalid, f.name)
	}
	path[f] = true
	defer delete(path, f)

	if f.obs != nil {
		obs = f.obs
	}
	for _, n := range f.nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		*steps++
		if *steps > maxSteps {
			return fmt.Errorf("%w: %s: more than %d steps", ErrInvalid, f.name, maxSteps)
		}
		if n.step != nil {
			if err := f.runStep(ctx, n.step, st, obs); err != nil {
				return err
			}
			continue
		}
		b := n.branch
		route, err := decide(f.name, b, st)
		if err != nil {
			return err
		}
		sub, ok := b.Routes[route]
		if !ok || sub == nil {
			return fmt.Errorf("%w: %s/%s: no branch %q", ErrInvalid, f.name, b.Name, route)
		}
		if err := sub.run(ctx, st, path, depth+1, steps, obs); err != nil {
			return err
		}
	}
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

func (f *Flow[S]) runStep(ctx context.Context, s *Step[S], st *S, obs Observer[S]) (err error) {
	start := time.Now()
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%s/%s: panic: %v", f.name, s.Name, rec)
		} else if err != nil {
			err = fmt.Errorf("%s/%s: %w", f.name, s.Name, err)
		}
		if oerr := observe(ctx, obs, f.name, s.Name, st, err, time.Since(start)); oerr != nil && err == nil {
			err = oerr
		}
	}()
	return s.Run(ctx, st)
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
