# Обзор

Детерминированные цепочки действий: Workflows.

Два уровня:

| Пакет           | Что                                                                    |
| --------------- | ---------------------------------------------------------------------- |
| `flow/workflow` | `Flow[S]`: детерминированный сценарий из шагов и веток                 |
| `flow`          | `Step[In, Out]`: один типизированный шаг с JSON Schema и приостановкой |

`Flow` нужен, когда сценарий задаёт разработчик: порядок шагов не зависит от
модели. `Step` нужен, когда шаг вызывается отдельно и его вход/выход нужно
проверить по схеме.

## Flow: сценарий

Состояние типизировано. Шаг получает `*S` и меняет его явно, поэтому опечатка в
поле — ошибка компиляции, а не нулевое значение из map.

```go
package main

import (
	"context"
	"fmt"

	"github.com/KirillYuzh/Gobellim/flow/workflow"
)

type researchState struct {
	Query  string
	KB     string
	Code   string
	Result string
}

func main() {
	flow := workflow.New[researchState]("research").
		Then("kb", func(ctx context.Context, st *researchState) error {
			st.KB = "chunks for " + st.Query
			return nil
		}).
		Then("lookup", func(ctx context.Context, st *researchState) error {
			st.Code = "a"
			return nil
		}).
		Branch("route", func(st *researchState) string {
			return st.Code
		}, map[string]*workflow.Flow[researchState]{
			"a": workflow.New[researchState]("a").Then("relay", func(_ context.Context, st *researchState) error {
				st.Result = st.KB + " + body A"
				return nil
			}),
			"other": workflow.New[researchState]("other").Then("report", func(_ context.Context, st *researchState) error {
				st.Result = "unknown code"
				return nil
			}),
		})

	st := researchState{Query: "go modules"}
	if err := flow.Run(context.Background(), &st); err != nil {
		panic(err)
	}
	fmt.Println(st.Result)
}
```

## Ветки

`Branch` получает состояние и возвращает имя маршрута. Соответствующий под-flow
запускается с тем же состоянием. Нет маршрута — ошибка `ErrInvalid`, а не тихий
пропуск.

## Ошибки и границы

- Ошибка шага останавливает цепочку и возвращается с путём `research/kb`.
- `Validate` ловит пустые имена, дубли, nil-шаги и циклы.
- `Run` проверяет `ctx`, ограничивает глубину (32) и число шагов (1000).
- Паника шага возвращается как ошибка, а не роняет процесс.
- Паника observer возвращается как `ErrObserver`.

```go
var (
	workflow.ErrInvalid  // структурная ошибка flow
	workflow.ErrObserver // observer запаниковал
)
```

## Observer

```go
flow.WithObserver(workflow.ObserverFunc[researchState](
	func(ctx context.Context, ev workflow.Event[researchState]) {
		fmt.Println(ev.Flow, ev.Step, ev.Duration, ev.Err)
	},
))
```

Observer вызывается после каждого шага, получает длительность и ошибку. Он не
может остановить flow и не роняет его паникой.

## Публичный API `flow/workflow`

```go
func New[S any](name string) *Flow[S]
func (f *Flow[S]) Name() string
func (f *Flow[S]) Then(name string, run func(context.Context, *S) error) *Flow[S]
func (f *Flow[S]) Branch(name string, decide func(*S) string, routes map[string]*Flow[S]) *Flow[S]
func (f *Flow[S]) WithObserver(o Observer[S]) *Flow[S]
func (f *Flow[S]) Validate() error
func (f *Flow[S]) Run(ctx context.Context, st *S) error

type Event[S any] struct {
	Flow     string
	Step     string
	State    *S
	Err      error
	Duration time.Duration
}

type Observer[S any] interface {
	Step(ctx context.Context, ev Event[S])
}
type ObserverFunc[S any] func(ctx context.Context, ev Event[S])
```

## Step: один шаг

`flow.Step` выводит JSON Schema из `In` и `Out`, проверяет границу и умеет
приостановиться:

```go
step, err := flow.New("charge", func(ctx context.Context, st flow.State, in Order) (Receipt, error) {
	return Receipt{OrderID: in.ID}, nil
})
if err != nil {
	return err
}

res := step.Run(ctx, nil, Order{ID: "ord-1"})
if res.Status == flow.StatusSuspended {
	// res.Snapshot можно передать в Resume после внешнего решения.
}
```

`Suspend(payload)` возвращает `*flow.Suspension`; `Run` отдаёт
`StatusSuspended` и snapshot, а `NewResumable` добавляет `Resume`.

## Ограничения

- Нет `Parallel`: шаги выполняются последовательно. Параллельность — на стороне
  вызывающего кода.
- Нет автоматических ретраев.
- Схема — проверка границы, а не «ремонт» невалидных данных.

## Устройство

Как это устроено → [implementation.md](./implementation.md)

## Смотрите также

- [Быстрый старт](../overview.md)
- [Инструменты](../tools/README.md) — инструмент как шаг сценария
- [Справочник API](../reference/gobellim.md)
