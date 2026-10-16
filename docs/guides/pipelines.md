# Пайплайны (`flow`): шаги, контракт и приостановки

Это how-to по пакету `flow`: как объявить шаг, как проверить контракт и как
приостановить и продолжить процесс. Объяснение идеи — в
[Пайплайны (концепция)](../concepts/flow.md).

## Когда `flow`, а когда — обычная функция

`flow` стоит брать, когда нужно **одно** из трёх:

- вход и выход шага должны проверяться по JSON Schema в рантайме (данные приходят
  извне: HTTP, очередь, чужие типы);
- шаг должен уметь **приостановиться** и продолжиться позже по снимку;
- нужен JSON-сериализуемый `State`, который переживает приостановку.

Во всех остальных случаях пишите обычную функцию: типы Go уже дают
compile-time-контракт, а `flow` добавит только схему и снимок.

## Шаги

```go
func New[In, Out any](id string, fn func(ctx context.Context, st State, in In) (Out, error)) (*Step[In, Out], error)

func NewResumable[In, Out, Resume any](
	id string,
	fn func(ctx context.Context, st State, in In) (Out, error),
	onResume func(ctx context.Context, st State, in In, payload Resume) (Out, error),
) (*Step[In, Out], error)
```

`New` объявляет обычный шаг; `NewResumable` — шаг, который может приостановиться и
затем продолжиться с payload типа `Resume`. Оба возвращают `*Step[In, Out]`.

Последовательности как типа нет: шаги вызываются по порядку, а `State` переносит
контекст между ними. Компилятор проверяет стык типов на каждом вызове:

```go
first := check.Run(ctx, nil, in)          // Result[Draft]
if first.Status != flow.StatusSuccess {
	return first.Error
}
second := review.Run(ctx, first.State, first.Output) // Result[Review]
```

Именно поэтому в `flow` нет `Pipeline`/`Then`/`Chain` — это была бы абстракция над
обычным вызовом функции (`D31`, `D47`).

## Схемы из типов

Схема входа, выхода и payload продолжения выводится из Go-типов
(`invopop/jsonschema`): без `$ref`, без `$schema`/`$id`, у объектов
`additionalProperties: false`. Для типизированного шага это защита в глубину —
компилятор уже проверил типы; проверка окупается на payload продолжения (сырой
JSON) и на интерфейсных типах (для них схемы нет). Нарушение схемы —
`core.ErrInvalidArgs`.

## Запуск: `Run`, `Resume`, `Result`

```go
func (s *Step[In, Out]) Run(ctx context.Context, st State, in In) Result[Out]
func (s *Step[In, Out]) Resume(ctx context.Context, snapshot []byte, data any) Result[Out]

type Result[Out any] struct {
	Status     Status // StatusSuccess | StatusFailed | StatusSuspended
	Output     Out
	Error      error
	Suspension *Suspension
	Snapshot   []byte // только при приостановке
	State      State
}
```

- `Run` проверяет вход по схеме `In`, вызывает `fn`, проверяет выход по схеме `Out`.
- `st` может быть `nil` — тогда заводится пустой `State`.
- Отменённый `ctx` даёт `StatusFailed` с `context.Canceled`.

## Приостановка и возобновление

Шаг приостанавливается, вернув `flow.Suspend(payload)` как ошибку:

```go
func Suspend(payload any) *Suspension
```

`Run` вернёт `Status = StatusSuspended`, `Suspension` и `Snapshot` — JSON с
`{"state": …, "step_input": …}`. Снимок — **единственное**, что нужно для
продолжения:

```go
resumed := review.Resume(ctx, result.Snapshot, Review{Approved: true, Notes: "ок"})
```

`Resume` разбирает снимок (испорченный — `ErrInvalidArgs`), берёт payload из
`data`, проверяет его по схеме `Resume` и вызывает `onResume`. Для не-resumable
шага `Resume` даёт `ErrInvalidArgs`.

## `State`: JSON-round-trip и его следствие

`State` — `map[string]any`, который маршалится в снимок. После `Resume` числа
возвращаются как `float64` (JSON), а не тем типом, что вы положили. Если состояние
нужно читать типизированно, храните срез со строковыми ключами или декодируйте
через `json.Decoder.UseNumber()`.

## Рецепты

### Шаг-approval: приостановиться и продолжить

```go
type Draft struct {
	Title string `json:"title"`
}
type Review struct {
	Approved bool   `json:"approved"`
	Notes    string `json:"notes"`
}
type Decision struct {
	Approved bool `json:"approved"`
}

review, err := flow.NewResumable[Draft, Review, Decision]("review",
	func(_ context.Context, _ flow.State, in Draft) (Review, error) {
		return Review{}, flow.Suspend(map[string]any{"question": "approve " + in.Title + "?"})
	},
	func(_ context.Context, _ flow.State, in Draft, d Decision) (Review, error) {
		return Review{Approved: d.Approved, Notes: in.Title}, nil
	},
)
if err != nil {
	return err
}

res := review.Run(ctx, nil, Draft{Title: "Черновик"})
if res.Status == flow.StatusSuspended {
	// ... показать res.Suspension.Payload и дождаться ответа ...
	final := review.Resume(ctx, res.Snapshot, Decision{Approved: true})
	_ = final
}
```

### Несколько шагов с аккумулятором в `State`

```go
res := fetch.Run(ctx, flow.State{}, in)
if res.Status != flow.StatusSuccess {
	return res.Error
}
res = normalize.Run(ctx, res.State, res.Output) // res.State переносится дальше
```

### Возобновление после перезапуска процесса

`Snapshot` — обычные байты: сохраните их в БД и восстановите после рестарта.
`Resume` не требует ничего, кроме снимка и объекта шага, поэтому порядок
восстановления — ваш: соберите тот же `*Step` (та же функция, тот же id) и вызовите
`Resume`. Id и функция должны совпадать с тем прогоном, что сделал снимок.

### Шаг, который валидирует собственный выход

Схема выхода строится из `Out`, так что «валидация собственного выхода» — это
просто тип: положите в `Out` то, что действительно обязано быть на выходе. Если
выход — интерфейс (`any`), схемы нет, и проверка не выполняется.

### `flow` изнутри `kit`-инструмента

`flow` не знает про `kit` — вызовите шаги внутри тела инструмента:

```go
type RefundIn struct {
	OrderID string `json:"order_id"`
	Reason  string `json:"reason"`
}

kit.Tool(app, "refund", func(ctx context.Context, in RefundIn) (string, error) {
	res := refundStep.Run(ctx, nil, in)
	switch res.Status {
	case flow.StatusSuspended:
		return "", fmt.Errorf("refund needs approval (snapshot saved)")
	case flow.StatusFailed:
		return "", res.Error
	}
	return "refund queued", nil
})
```

Инструмент, который шлёт данные наружу, объявляйте эффектом:
`kit.Effects(kit.EffectEgress)` + `kit.EgressHosts(...)`.

## Частые ошибки

- **Забыть про `float64` после `Resume`.** `State` идёт через JSON.
- **Считать, что `Resume` повторит шаги до приостановки.** Нет: продолжается ровно
  шаг, который приостановился; предыдущие шаги уже отработали.
- **Ждать `Result.Steps`.** Их нет: результат — про один шаг. Исход цепочки — это
  исход её шагов по отдельности.
- **Игнорировать `Status`.** `StatusSuspended` — не ошибка; проверяйте его первым.

## Ограничения

- Нет branch/parallel/foreach/loop DSL — это обычный Go.
- Нет объекта `Pipeline`: последовательность — вызовы `Run` друг за другом.
- Схема — просьба-проверка, а не гарантия: она не «ремонтирует» невалидные данные.
- Снимок версионируется id шага, а не автоматически переносится между версиями
  кода: восстанавливайте совместимой версией шага.

## Смотрите также

- [Пайплайны (концепция)](../concepts/flow.md) — зачем это и как устроено
- [Решения](../internals/decisions.md) — `D31`, `D47`
- [`flow/flow.go`](../../flow/flow.go) — исходник
