# Gobellim

**Слой исполнения и политики для одного LLM use-case внутри существующего Go-сервиса.**

Gobellim — библиотека, а не платформа. Она не поднимает сервер, БД или брокер,
а core-модуль не имеет обязательных зависимостей.

Главный инвариант:

> Модель может предложить текст, аргументы или действие. Она не может выдать
> себе права, изменить policy, зарегистрировать инструмент или выполнить side
> effect в обход Gate.

## Статус

P0 и P1 из [`DESIGN.md`](./DESIGN.md) реализованы: типизированные Task и Tool,
schema по тегам, labels и handles, Gate, привязка к tenant, audit, лимиты,
линейный workflow с approval card, sealed continuation, проверки Resume и
`database/sql` store.

P2 не реализован: replay/diff, детекторы, HTTP approval handlers,
OpenTelemetry, quorum, компенсации и редактирование одобренных аргументов.

Запускаемые ноутбуки — [`examples/notebooks`](./examples/notebooks/README.md).

## Быстрый старт

Приложение сохраняет свой handler, `context.Context` и бизнес-функции.
Gobellim фиксирует вокруг них модельный цикл.

```go
type TicketInput struct {
	TenantID string `gobellim:"source=principal,required"`
	TicketID string `gobellim:"source=application,required"`
	Body     string `gobellim:"required,sensitivity=pii"`
	OrderID  string `gobellim:"sensitivity=secret"`
}

type TriageOutput struct {
	Category string `gobellim:"required,oneof=billing|technical|other"`
	Reply    string
}

type RefundArgs struct {
	OrderID string `gobellim:"required,ground"`
	Cents   int    `gobellim:"min=1,max=5000"`
}

refund := gobellim.Tool[RefundArgs, RefundResult]{
	Name:       "orders.refund",
	Effect:     gobellim.Write, // обязателен: zero value отклоняется
	Permission: "orders:refund",
	Resource: func(p gobellim.Principal, a RefundArgs) gobellim.Resource {
		return gobellim.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
	},
	Validate: validateRefundAgainstBusinessRules,
	Run: func(ctx context.Context, p gobellim.Principal, a RefundArgs) (RefundResult, error) {
		return orders.Refund(ctx, p.Tenant, a.OrderID, a.Cents)
	},
}

engine, err := gobellim.New(
	gobellim.WithModel(model),               // ваш адаптер; см. plugins/gigachat
	gobellim.WithAuthorizer(existing),       // ваш RBAC/ACL
	gobellim.WithAudit(gobellim.SlogSink{}), // или plugins/langfuse
)
task, err := gobellim.NewTask[TicketInput, TriageOutput](engine, gobellim.TaskConfig{
	Name:        "ticket.triage",
	Instruction: "Classify the ticket and propose a reply.",
	Tools:       []gobellim.ToolDef{refund},
})
res, err := task.Run(ctx, principalFromRequest(r), in)
```

`res.Status == gobellim.TaskApprovalRequired` означает, что модель предложила
write-действие, но оно **не выполнено**. В `res.Pending` лежит сериализуемое
описание действия: карточка с аргументами, digest, provenance и expiry. После
реального одобрения приложение вызывает `task.ExecuteApproved(ctx, requester,
res.Pending)`: Gate заново проверяет tenant, RBAC/ACL, `Validate` и digest.
Для возобновляемого approval с sealed-токеном, TTL и four-eyes используйте
`flow`.

## Gate

Каждый вызов инструмента от модели проходит через Gate: tenant binding,
RBAC/ACL, grounding, бизнес-валидация, потолок эффекта.

| Худшая целостность аргументов | Read  | Write    | Egress | Destructive |
| ----------------------------- | ----- | -------- | ------ | ----------- |
| `Trusted` / `Validated`       | allow | allow    | allow  | escalate    |
| `Derived`                     | allow | escalate | deny   | deny        |
| `Untrusted` / нет меток       | allow | escalate | deny   | deny        |

Дополнительно:

- `Effect` обязателен. Zero value — `EffectUnknown`, `NewTask` его отклоняет:
  мутирующий инструмент не может молча стать `Read`.
- пустой набор меток — `Untrusted`, а не `Trusted`;
- `Resource.TenantID` должен совпадать с `Principal.Tenant` (пустой
  заполняется из принципала);
- ошибка `Authorizer` — `Deny`;
- неизвестный инструмент и невалидные аргументы — `Deny`;
- `ground`-поле принимает только существующий handle; сырая строка — `Deny`;
- ошибка `Validate` — `Deny`;
- заявления модели о правах не учитываются.

`Validated` достижим только одним путём: аргумент пришёл через handle этого
запуска и `Tool.Validate` вернул `nil`. Свободный текст модели остаётся
`Derived`, инструмент без `Validate` повышения не получает. `worst` считается
по всем аргументам, поэтому один model-derived аргумент в write-вызове всё
равно требует человека.

## Теги schema

| Тег                    | Смысл                                                |
| ---------------------- | ---------------------------------------------------- |
| `required`             | значение не должно быть нулевым (нельзя на `bool`)   |
| `source=principal`     | строка должна совпадать с полем `Principal`          |
| `source=application`   | значение из состояния приложения, получает `Trusted` |
| `untrusted`            | явно недоверенный вход (дефолт)                      |
| `sensitivity=pii`      | e-mail/телефон/карта заменяются на handles           |
| `sensitivity=secret`   | всё значение заменяется одним handle                 |
| `sensitivity=internal` | только метка, без маскирования                       |
| `oneof=a\|b`           | строковый enum                                       |
| `min`, `max`           | числовой диапазон или количество элементов           |
| `ground`               | аргумент обязан быть существующим handle             |

`source` и `ground` применимы только к `string`; `sensitivity` — к `string` и
`[]string`. `time.Time`, `[]byte`, `map`, указатели, вложенность глубже двух
уровней и встроенные struct без явного `json`-имени отклоняются на `NewTask`:
лучше fail на старте, чем schema, которая врёт модели.

## Grounding — это handle

`ground`-поле принимает только handle, созданный для этого запуска. Модель не
может выдумать ID или скопировать чужое trusted-значение. Чтобы модель могла
ссылаться на значение, пометьте входное поле `sensitivity=secret` (или
`sensitivity=pii`). `source=application` даёт метку `Trusted`, но само по себе
handle не создаёт.

```go
type TicketInput struct {
	OrderID string `gobellim:"sensitivity=secret,required"`
}
type RefundArgs struct {
	OrderID string `gobellim:"required,ground"`
}
// Модель видит {"OrderID":"<h1>"} и может передать "<h1>" в orders.refund.
```

## Human approval

```go
sealer, err := flow.NewSealer("v1", key) // AES-GCM; ключ — секрет приложения
store, err := sqlstore.New(db)          // опционально: replay между репликами
if err != nil {
	return err
}
wf, err := flow.New[RefundCase]("refund",
	flow.WithAuthorizer(authz),
	flow.WithSealer(sealer), // обязателен
	flow.WithStore(store),
	flow.WithTTL(24*time.Hour),
)

wf.Step("prepare", prepareRefund).
	Approve("manager", flow.ApproveConfig[RefundCase]{
		Permission:       "refund:approve",
		ActionPermission: "orders:refund",
		FourEyes:         true,
		Card:             refundApprovalCard,
		Resource:         refundResource,
	}).
	Step("execute", executeRefund)

res, err := wf.Run(ctx, requester, state) // res.Status == flow.Suspended
out, err := wf.Resume(ctx, approver, res.Suspension.Token, flow.Approve)
```

Resume проверяет AEAD, tenant, expiry, версию flow, право апрувера, Four-Eyes,
право requester'а, digest действия и атомарный claim nonce. `flow.Store` имеет
один метод `Claim`; SQL-адаптер также умеет `Prune` просроченных nonce.

Шаг после `Approve` — это само действие. Сделайте его идемпотентным: падение
после внешнего вызова нельзя откатить, exactly-once не обещается.

## Observability

```go
sink, err := langfuse.New(langfuse.Config{
	Host:      "https://cloud.langfuse.com",
	PublicKey: os.Getenv("LANGFUSE_PUBLIC_KEY"),
	SecretKey: os.Getenv("LANGFUSE_SECRET_KEY"),
})
defer sink.Close(ctx)

engine, err := gobellim.New(
	gobellim.WithModel(model),
	gobellim.WithAuthorizer(authz),
	gobellim.WithAudit(sink),
)
```

Sink батчами отправляет `trace-create`, `generation-create` и `span-create`.
Уходят только labels, digest, decision, latency и токены; prompt text, сырые
аргументы и секреты — нет. Для локального просмотра:
`langfuse.Config{DryRun: true, Writer: os.Stdout}`.

## Гарантии и non-goals

Гарантируется при корректной интеграции:

- вызов инструмента от модели не доходит до handler без Gate;
- unknown tool, невалидные аргументы, ошибка authorizer и чужой tenant не
  становятся `Allow`;
- пустой набор аргументов не считается доверенным;
- `ground`-аргумент нельзя удовлетворить сырой строкой модели;
- свободный текст модели не становится trusted из-за JSON schema;
- approval привязан к инструменту и аргументам, которые видел человек;
- audit содержит labels и digest, а не prompt text и секреты.

Не гарантируется:

- отсутствие вредного или ложного текста модели;
- точный data-flow внутри модели;
- exactly-once для внешнего side effect;
- одноразовость Resume между репликами без `Store`;
- durable recovery произвольной Go-функции;
- retrieval/RAG и data ACL — это ответственность приложения;
- `ReaderSet` / per-reader ACL не реализованы.

## Установка

```sh
go get github.com/KirillYuzh/Gobellim
```

## Документация

- [`DESIGN.md`](./DESIGN.md) — спецификация.
- [`docs/`](./docs) — how-to.
- [`examples/notebooks`](./examples/notebooks/README.md) — запускаемые сценарии,
  моки и реальный GigaChat free tier.
- [`plugins/langfuse`](./plugins/langfuse) — audit sink.
- [`plugins/gigachat`](./plugins/gigachat) — model adapter.

## Лицензия

[`MIT`](LICENSE)
