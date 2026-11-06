# Gobellim: как встроить один LLM use-case

`gobellim` — это небольшой слой внутри Go-сервиса: типизированный вход/выход,
закрытый набор инструментов, Gate перед каждым side effect и опциональное
одобрение из доверенного источника вне модели.
Спецификация — [`DESIGN.md`](../DESIGN.md). Запускаемые примеры —
[`examples/notebooks`](../examples/notebooks/README.md).

## Установка

```sh
go get github.com/KirillYuzh/Gobellim
```

Core не требует внешних зависимостей. `flow`, `store/sql`, `plugins/*` —
опциональные пакеты того же модуля.

## Один use-case

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
	Access:     gobellim.Write, // обязателен: zero value отклоняется
	Permission: "orders:refund",
	Target: func(p gobellim.Principal, a RefundArgs) gobellim.Resource {
		return gobellim.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
	},
	// Validate — проверка на отказ; nil не повышает доверие.
	Validate: validateRefund,
	Run: func(ctx context.Context, p gobellim.Principal, a RefundArgs) (RefundResult, error) {
		return orders.Refund(ctx, p.Tenant, a.OrderID, a.Cents)
	},
}

engine, err := gobellim.New(
	gobellim.WithModel(model),          // ваш адаптер; есть plugins/gigachat
	gobellim.WithAuthorizer(existing),  // ваш RBAC/ACL
	gobellim.WithAudit(auditSink),      // например, plugins/langfuse
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
описание действия: карточка с аргументами, digest, provenance, expiry, nonce и
HMAC-подпись engine. Храните его на сервере: если endpoint одобрения декодирует
`PendingAction` из тела запроса, клиент сам выбирает аргументы и digest, и
engine отвергнет такой объект как `ErrApprovalDrift`. После реального одобрения
приложение вызывает `task.ExecuteApproved(ctx, requester, approver,
res.Pending)`: Gate заново проверяет tenant, RBAC/ACL, `Validate`, tool/access и
digest, а `approver` попадает в audit как `approval.granted`. Одобрение
одноразовое: nonce уходит в `ClaimStore` (по умолчанию — процессный,
`gobellim.WithClaimStore` — общий). Для шагов с TTL, four-eyes и хранением
состояния используйте `flow`: он возвращает `Suspension`, приложение хранит его
на сервере, а approver берётся из `flow.ApproverFrom(ctx)` после `Resume`.
`flow.Reject` тоже забирает nonce: отклонённое действие нельзя позже провести
как одобренное.

## Теги schema

Поддерживается маленькое подмножество; неизвестный тег — ошибка на `NewTask`.

| Тег                    | Смысл                                                        |
| ---------------------- | ------------------------------------------------------------ |
| `required`             | значение не должно быть нулевым (нельзя на `bool`)           |
| `source=principal`     | строка должна совпадать с полем `Principal`                  |
| `source=application`   | значение пришло из состояния приложения и получает `Trusted` |
| `sensitivity=pii`      | совпадения e-mail/телефона/карты заменяются на handles       |
| `sensitivity=secret`   | всё значение заменяется одним handle                         |
| `sensitivity=internal` | метка без маскирования                                       |
| `oneof=a\|b`           | строковый enum                                               |
| `min`, `max`           | числовой диапазон или количество элементов среза             |
| `ground`               | handle из поля с тем же путём                                |
| `ground=Source`        | handle из указанного входного поля                           |
| `ground=any`           | любой handle (слабее, явно)                                  |

`source` и `ground` применимы только к `string`. `sensitivity` — к `string` и
`[]string`. `time.Time`, `[]byte`, `map`, указатели, вложенные структуры глубже
двух уровней и встроенные struct без явного `json`-имени отклоняются на
`NewTask`: лучше fail на старте, чем schema, которая врёт модели.

Те же `sensitivity`-теги применяются к **результату** инструмента перед тем,
как он попадёт обратно в контекст модели: tagged `string` становится handle,
`[]string` обрабатывается поэлементно, `[]byte` считается одной строкой, а
`map[string]string` — по значениям. Тег на другом типе — ошибка `NewTask`, а не
молчаливое отключение маскирования. `time.Time` и `json.Marshaler` непрозрачны
и не могут нести sensitivity-тег.

## Gate

Каждый вызов инструмента от модели проходит через Gate. Порядок:

1. `Target.TenantID` (пустой заполняется из `Principal.Tenant`) должен
   совпадать с tenant принципала.
2. Непустая цель для всего, кроме `AccessCompute` (`target-unbound`).
3. `ground`-поля, включая совместимость источника handle.
4. `Authorizer` (ошибка — `Deny`).
5. бизнес-валидация `Tool.Validate`.
6. политика `Access`.

| Худшая целостность аргументов | Read  | Write    | Send  | Delete   | Compute |
| ----------------------------- | ----- | -------- | ----- | -------- | ------- |
| `Trusted`                     | allow | allow    | allow | escalate | allow   |
| `Derived`                     | allow | escalate | deny  | deny     | allow   |
| `Untrusted` / нет меток       | allow | escalate | deny  | deny     | allow   |

Нулевой `Access` — `AccessUnknown`, а не `Read`: забыть access нельзя.
Для `Read`/`Write`/`Send`/`Delete` поле `Target` обязательно: без цели ACL
проверялся бы только по RBAC. `AccessCompute` — чистая работа без данных и без
цели. `Send` требует, чтобы `Target` связывал получателя, а не только имя
инструмента. Неизвестный инструмент, невалидные аргументы, ошибка authorizer и
чужой tenant — `Deny`, никогда `Allow`.

`Validate` — проверка на отказ. Успешный `Validate` не повышает handle до
`Trusted`: недоверенный handle остаётся `Derived`/`Untrusted`, write идёт в
`Escalate`, send — в `Deny`. Необслуживаемый `Write`/`Send` возможен только с
`Trusted` handle из `source=application,sensitivity=secret`; `Delete` всегда
требует одобрения.

Бюджеты можно уточнить на задачу: `TaskConfig.Limits` перекрывает значения
`Engine`, включая `MaxContextBytes` — потолок на реальный размер контекста
(текст сообщений и аргументы tool calls), а не на число сообщений.

## Grounding — это handles

Модель не может «подтвердить» ID строкой. `ground`-поле принимает только
handle, созданный рантаймом для этого запуска, и только из объявленного
источника. Голый `ground` использует путь аргумента: `OrderID` принимает handle
из входного поля `OrderID`. Если входное поле называется иначе, укажите
`ground=RequestedOrder`. Handle в non-ground поле — `Deny`, даже если токен
существует. `ground=any` — явное ослабление, не дефолт.

```go
type TicketInput struct {
	OrderID string `gobellim:"sensitivity=secret,required"`
}
type RefundArgs struct {
	OrderID string `gobellim:"required,ground"`
}
// Model sees {"OrderID":"<h1>"} and may pass "<h1>" to orders.refund.
```

## Foreign approval (одобрение вне модели)

Одобрение не обязано приходить от человека. Это любое решение об
эскалированном действии, принятое вне модели: менеджер в UI или ваша же
бизнес-логика, действующая как аутентифицированный `Principal`. Gate проверяет
такое решение одинаково, и модель не может одобрить сама себя.

Два пути, не смешивайте их в одном действии:

- `Task` превращает эскалированный tool call в `PendingAction`, приложение
  хранит его, доверенный апрувер решает, а `ExecuteApproved` исполняет после
  повторной проверки tenant, RBAC/ACL, `Validate`, access и точного digest.
- `flow` останавливает шаг workflow и возвращает серверный `Suspension`;
  `Resume` проверяет решение и право `ActionPermission` у requester'а.

`FourEyes` — опциональная проверка разделения обязанностей: апрувер обязан быть
другим `Principal`, чем requester.

```go
store, err := sqlstore.New(db) // опционально: одноразовость между репликами
if err != nil {
	return err
}
wf, err := flow.New[RefundCase]("refund",
	flow.WithAuthorizer(authz),   // обязателен
	flow.WithAudit(auditSink),    // обязателен
	flow.WithStore(store),
	flow.WithTTL(24*time.Hour),
)

wf.Step("prepare", prepareRefund).
	Approve("manager", flow.ApproveConfig[RefundCase]{
		Permission:       "refund:approve",
		ActionPermission: "orders:refund",
		FourEyes:         true,
		Card:             refundApprovalCard, // детерминированная карточка
		Target:           refundResource,     // Resource для ACL
	}).
	Step("execute", func(ctx context.Context, p gobellim.Principal, s *RefundCase) error {
		// p — requester: Resume уже проверил одобрение перед этим шагом.
		return orders.Refund(ctx, p.Tenant, s.OrderID, s.Cents)
	})

res, err := wf.Run(ctx, requester, state) // res.Status == flow.Suspended
// Suspension и state сохраняет приложение; клиенту уходит только их id.
out, err := wf.Resume(ctx, approver, *res.Suspension, res.State, flow.Approve)
```

`flow` одобряет шаг workflow. Если одобрять нужно именно tool call модели,
используйте `Task` с `res.Pending` и `ExecuteApproved`, не оборачивая это в
`flow`: один путь — один approval engine.

Приостановка — это `Suspension{Nonce, Card, ExpiresAt, Requester}` и состояние
workflow. Приложение хранит их на сервере и отдаёт клиенту непрозрачный id;
никакого bearer-токена с зашифрованным состоянием нет, поэтому и ключа шифрования
не нужно.

`Resume` принимает решение только от аутентифицированного `approver` и проверяет:
tenant совпадает с requester'ом, срок не истёк, `Card.Flow` и `Card.Step`
принадлежат этому flow, `RequiredPermission`/`FourEyes` в карточке равны текущей
конфигурации (`ErrFlowDrift` при смене политики), у апрувера есть право
`Permission`, Four-Eyes соблюдён, у requester'а всё ещё есть `ActionPermission`,
карточка, пересобранная из состояния, даёт тот же `ActionDigest`. Только после
всех проверок nonce атомарно занимается в `Store`.

`DecisionUnknown` (нулевое значение) и любое неизвестное решение — `ErrDecision`,
никогда не одобрение. `Reject` тоже занимает nonce: отказ финален для этой
приостановки, повторное решение требует нового `Run`.

Без `WithStore` используется process-local `MemStore`; это одноразовость только
внутри процесса, между репликами нужен общий `Store`. SQL-адаптер умеет `Prune`
просроченных nonce и принимает `ConflictClause` для MySQL. Nonce занимается до
внешнего эффекта: падение в этом окне теряет одобрение, а не рискует двойным
исполнением; восстановление — сверка с провайдером.

## Observability

```go
sink, err := langfuse.New(langfuse.Config{
	Host:      "https://cloud.langfuse.com",
	PublicKey: os.Getenv("LANGFUSE_PUBLIC_KEY"),
	SecretKey: os.Getenv("LANGFUSE_SECRET_KEY"),
})
defer sink.Close(ctx)
engine, _ := gobellim.New(..., gobellim.WithAudit(sink))
```

Sink отправляет батчами trace / generation / span и **не** отправляет prompt
text, сырые аргументы или секреты — только labels, digest, decision, latency и
токены. Для локального просмотра: `langfuse.Config{DryRun: true, Writer: os.Stdout}`.

## Ограничения

- Нет durable recovery произвольной Go-функции.
- Нет exactly-once для внешнего эффекта: шаг после approval делайте идемпотентным.
- `flow` не заменяет полноценный durable execution engine.
- Retrieval/RAG и data ACL остаются ответственностью приложения.
- Regex-маскирование PII — не DLP; это best-effort слой.
