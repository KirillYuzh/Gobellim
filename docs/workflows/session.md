# Сессия: сценарий с вопросом пользователю

`flow/workflow` задаёт порядок шагов, `flow/session` добавляет паузу: сценарий
останавливается, отдаёт типизированный вопрос клиенту и продолжается, когда
приходит ответ. Разработчик описывает вопросы и переходы, библиотека держит
запуски, владельца и защиту от повторного ответа.

Это единственный слой, где решается, что спросить и куда пойти дальше. Модель к
этому не привлекается: она может начать сценарий инструментом, но не выбирает
ветку и не видит `run_id`.

## Пример

Сценарий из демо: сразу выбор пути A или B, а в пути B ещё один вопрос.

```go
type researchState struct {
	Query  string
	Param  string // свободный вопрос, если он нужен сценарию
	Path   string
	Topic  string
	Result string
}

research := session.New[researchState]("research").
	AskChoice("path", "Что сделать: путь A или путь B?", []string{"A", "B"},
		func(st *researchState, answer string) error {
			st.Path = answer
			return nil
		}).
	Branch(func(st *researchState) string { return st.Path }).
	Case("A", func(f *session.Session[researchState]) {
		f.Do("finish_a", func(_ context.Context, st *researchState) error {
			st.Result = "Путь A: " + st.Query
			return nil
		})
	}).
	Case("B", func(f *session.Session[researchState]) {
		f.AskChoice("topic", "Что показать?", []string{"X", "Y"},
			func(st *researchState, answer string) error {
				st.Topic = answer
				return nil
			}).
			Do("finish_b", func(_ context.Context, st *researchState) error {
				st.Result = "Путь B: " + st.Topic
				return nil
			})
	})
```

## Start: начать и остановиться на вопросе

```go
res, err := research.Start(ctx, session.Principal{UserID: "u-1", Tenant: "acme"}, &st)
if err != nil {
	return err
}

switch res.Status {
case session.StatusAwaitingUser:
	// res.Question: Stage, Prompt, Input, Options
	// res.RunID нужен приложению, а не модели
case session.StatusCompleted:
	// res.State — итоговое состояние
}
```

Свободный ответ объявляется так же: `AskText`. Если ответ уже есть в
состоянии, вопрос можно пропустить:

```go
AskText("param", "Какой параметр использовать?",
	func(st *researchState, answer string) error {
		st.Param = answer
		return nil
	},
	session.SkipIf(func(st *researchState) bool { return st.Param != "" }))
```

`SkipIf` не создаёт паузу: шаг сразу идёт дальше.

## Resume: продолжить с ответом

```go
res, err := research.Resume(ctx, session.ResumeRequest{
	RunID:     runID,
	ResumeID:  "answer-42",          // повтор с тем же id вернёт тот же ответ
	Principal: session.Principal{UserID: "u-1", Tenant: "acme"},
	Text:      "go modules",         // для InputText
	Choice:    "A",                  // для InputChoice
})
```

`Resume` сам проверяет ответ по объявлению вопроса, применяет его к состоянию и
продолжает поток с того шага, который остановился. Повторный вопрос, чужой запуск
и ответ не по вариантам отсекаются до продолжения.

## Транспорт

`Start` обычно вызывается из инструмента, который модель вызвала сама:

```go
res, err := u.research.Start(ctx, principal(user), &st)
return entity.Result{
	Status:  string(res.Status),
	RunID:   res.RunID,   // приложение вырезает его из ответа модели
	Prompt:  res.Question.Prompt,
	Input:   string(res.Question.Input),
	Options: res.Question.Options,
}, nil
```

`Resume` вызывается отдельным эндпоинтом, а не инструментом. Пользователь
отвечает приложению, приложение вызывает use case, use case вызывает `Resume`.

```text
POST /v1/chat
  → модель вызывает start_research
  → сценарий остановился
  → {"status":"awaiting_user","run_id":"run-…","question":{…}}

POST /v1/research/{run_id}/resume
  {"user":{…},"text":"go modules"}
  → {"status":"awaiting_user","question":{…}}

POST /v1/research/{run_id}/resume
  {"user":{…},"choice":"A"}
  → {"status":"completed","answer":"Путь A: go modules"}
```

## Хранилище запусков

По умолчанию это `MemoryStore`:

```go
store := session.NewMemoryStore[researchState](
	session.TTL(15 * time.Minute),
	session.MaxRuns(1000),
	session.MaxRunsPerPrincipal(10),
)
research := session.New[researchState]("research").WithStore(store)
```

| Свойство        | Поведение                                                   |
| --------------- | ----------------------------------------------------------- |
| TTL             | по умолчанию 15 минут, затем `ErrNotFound`                  |
| Лимиты          | 1000 запусков всего, 10 на владельца; вытесняется старейший |
| Владение        | чужой `UserID`, `Tenant` или `ChatID` даёт `ErrNotFound`    |
| Перезапуск      | процесс упал — незавершённые запуски теряются               |
| Идемпотентность | повтор с тем же `ResumeID` возвращает сохранённый результат |

Чужой запуск отдаётся как «не найдено» намеренно: сервис не подтверждает, что
такой запуск существует.

## Ошибки

```go
var (
	session.ErrNotFound // нет запуска, истёк TTL, чужой владелец
	session.ErrInvalid  // битое объявление, ответ не по вариантам, запуск завершён
)
```

`Resume` на завершённом запуске возвращает `ErrInvalid`. Ответ не из списка
`Options` тоже `ErrInvalid`, и запуск остаётся ждать правильный ответ.

## Ограничения

- Хранилище только в памяти. Для нескольких инстансов нужен внешний store.
- Нет параллельных веток: поток последовательный, как и у `Flow`.
- Состояние копируется в `Result.State`; если в нём есть указатели, копия
  поверхностная.
- `ResumeID` не обязателен, но без него повторный ответ на завершённый запуск
  вернёт `ErrInvalid`, а не прежний результат.

## Публичный API

```go
func New[S any](name string) *Session[S]
func (s *Session[S]) WithStore(*MemoryStore[S]) *Session[S]
func (s *Session[S]) Do(name string, run func(context.Context, *S) error) *Session[S]
func (s *Session[S]) AskText(stage, prompt string, apply func(*S, string) error, opts ...AskOption[S]) *Session[S]
func (s *Session[S]) AskChoice(stage, prompt string, options []string, apply func(*S, string) error, opts ...AskOption[S]) *Session[S]
func SkipIf[S any](func(*S) bool) AskOption[S]
func (s *Session[S]) Branch(decide func(*S) string) *Session[S]
func (s *Session[S]) Case(route string, build func(*Session[S])) *Session[S]
func (s *Session[S]) Start(ctx context.Context, p Principal, st *S) (Result[S], error)
func (s *Session[S]) Resume(ctx context.Context, req ResumeRequest) (Result[S], error)

type Status string   // awaiting_user | completed
type Input string    // text | choice

type Question struct {
	Stage   string
	Prompt  string
	Input   Input
	Options []string
}

type Result[S any] struct {
	Status   Status
	RunID    string
	Question *Question
	State    S
}

type ResumeRequest struct {
	RunID     string
	ResumeID  string
	Principal Principal
	Text      string
	Choice    string
}

type Principal struct {
	UserID string
	Tenant string
	ChatID string
}
```

## Смотрите также

- [Рабочие процессы](./README.md) — `Flow[S]` и `Branch`
- [Как устроены рабочие процессы](./implementation.md) — пауза в движке
- [Инструменты](../tools/README.md) — инструмент, который начинает сценарий
