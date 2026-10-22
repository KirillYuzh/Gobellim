// Package workflow — детерминированная типизированная машина состояний: сценарий
// выполняет шаги в объявленном порядке независимо от того, что делает модель, а
// ветка выбирает подпоток по состоянию.
//
// Шаг получает тот же типизированный указатель на состояние и меняет его явно,
// поэтому опечатка в поле — ошибка компиляции, а не нулевое значение из map.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrInvalid отмечает битый поток: пустое или повторяющееся имя шага, nil-колбэк,
// отсутствующая ветка, цикл или nil-состояние.
var ErrInvalid = errors.New("workflow: invalid")

// ErrObserver отмечает панику в наблюдателе. Паника перехватывается и
// возвращается ошибкой, а не роняет процесс.
var ErrObserver = errors.New("workflow: observer failed")

const (
	// maxDepth ограничивает вложенность веток.
	maxDepth = 32
	// maxSteps ограничивает число шагов одного Run вместе с вложенными потоками.
	maxSteps = 1000
)

// Step — одно именованное изменение состояния.
type Step[S any] struct {
	Name string
	Run  func(ctx context.Context, st *S) error
}

// Branch выбирает подпоток по состоянию. Decide возвращает имя маршрута.
type Branch[S any] struct {
	Name   string
	Decide func(st *S) string
	Routes map[string]*Flow[S]
}

// Event — то, что наблюдатель видит после шага.
type Event[S any] struct {
	Flow     string
	Step     string
	State    *S
	Err      error
	Duration time.Duration
}

// Observer вызывается после каждого шага. Паника в нём не роняет поток:
// она превращается в ErrObserver.
type Observer[S any] interface {
	Step(ctx context.Context, ev Event[S])
}

// ObserverFunc превращает функцию в Observer.
type ObserverFunc[S any] func(ctx context.Context, ev Event[S])

// Step вызывает исходную функцию.
func (f ObserverFunc[S]) Step(ctx context.Context, ev Event[S]) { f(ctx, ev) }

type node[S any] struct {
	step   *Step[S]
	branch *Branch[S]
}

// Flow — упорядоченный список шагов и веток над одним типом состояния.
type Flow[S any] struct {
	name  string
	nodes []node[S]
	obs   Observer[S]
}

// New возвращает пустой поток с заданным именем. Имя попадает в тексты ошибок
// и в события наблюдателя.
func New[S any](name string) *Flow[S] {
	return &Flow[S]{name: name}
}

// Name возвращает имя потока.
func (f *Flow[S]) Name() string { return f.name }

// Then добавляет шаг и возвращает поток, чтобы вызовы сцеплялись.
func (f *Flow[S]) Then(name string, run func(context.Context, *S) error) *Flow[S] {
	f.nodes = append(f.nodes, node[S]{step: &Step[S]{Name: name, Run: run}})
	return f
}

// Branch добавляет ветку и возвращает поток, чтобы вызовы сцеплялись.
func (f *Flow[S]) Branch(name string, decide func(*S) string, routes map[string]*Flow[S]) *Flow[S] {
	f.nodes = append(f.nodes, node[S]{branch: &Branch[S]{Name: name, Decide: decide, Routes: routes}})
	return f
}

// WithObserver задаёт наблюдателя, вызываемого после каждого шага, и возвращает
// поток.
func (f *Flow[S]) WithObserver(o Observer[S]) *Flow[S] {
	f.obs = o
	return f
}

// Validate сообщает о первой структурной проблеме дерева потоков.
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

// Run проверяет поток и выполняет его над st. Ошибка шага останавливает цепочку
// и несёт путь вида "поток/шаг".
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

// decide изолирует пользовательский колбэк: паника в нём — ошибка, а не падение.
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
