// Package workflow — детерминированная типизированная машина состояний: сценарий
// выполняет шаги в объявленном порядке независимо от того, что делает модель, а
// ветка выбирает подпоток по состоянию.
//
// Шаг умеет приостановить сценарий: Suspend отдаёт наружу типизированный вопрос,
// Run возвращает *Suspension, а состояние и позиция остаются в Execution. Resume
// продолжает с той же точки, где сценарий остановился.
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
	// maxSteps ограничивает число шагов одного запуска вместе с вложенными потоками.
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

// Suspension — пауза сценария. Payload типизирован вызывающим кодом: это вопрос,
// на который ждут ответ, а не инструкция модели.
type Suspension struct {
	Flow    string
	Step    string
	Payload any
}

// Error позволяет возвращать паузу как ошибку шага.
func (s *Suspension) Error() string {
	return fmt.Sprintf("workflow: suspended at %s/%s", s.Flow, s.Step)
}

// Suspend останавливает шаг. Run вернёт *Suspension, а состояние останется у
// вызывающего, пока он не вызовет Resume.
func Suspend(ctx context.Context, payload any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return &Suspension{Payload: payload}
}

// Execution — запущенный сценарий: состояние, позиция стека и текущая пауза.
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

// Start запускает поток и останавливается либо на первом Suspend, либо в конце.
// Пауза видна через Suspension, ошибка означает настоящий отказ.
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

// Run прогоняет поток целиком. Пауза возвращается как *Suspension, чтобы
// вызывающий код без resume-логики её заметил.
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

// State возвращает состояние запуска.
func (e *Execution[S]) State() *S { return e.st }

// Done сообщает, что сценарий дошёл до конца.
func (e *Execution[S]) Done() bool { return e.done }

// Suspension возвращает текущую паузу или nil.
func (e *Execution[S]) Suspension() *Suspension { return e.susp }

// Resume продолжает сценарий с того шага, который вызвал Suspend. Ответ
// применяет apply: workflow гарантирует, что он вызовется один раз и только
// пока сценарий действительно ждёт ответа. Ошибка apply отменяет продолжение.
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

// run исполняет узлы сверху вниз, пока стек не опустеет. Пауза возвращается
// ошибкой *Suspension и запоминается в e.susp.
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

// decide изолирует пользовательский колбэк: паника в нём — ошибка, а не падение.
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
