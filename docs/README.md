# Gobellim: как встроить один LLM use-case

`gobellim` — это небольшой слой внутри Go-сервиса: типизированный вход/выход,
закрытый набор инструментов, Gate перед каждым side effect и опциональный HITL.
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
	Effect:     gobellim.Write, // обязателен: zero value отклоняется
	Permission: "orders:refund",
	Resource: func(p gobellim.Principal, a RefundArgs) gobellim.Resource {
		return gobellim.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
	},
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
res.Pending)`: Gate заново проверяет tenant, RBAC/ACL, `Validate`, tool/effect и
digest, а `approver` попадает в audit как `approval.granted`. Одобрение
одноразовое: nonce уходит в `ClaimStore` (по умолчанию — процессный,
`gobellim.WithClaimStore` — общий). Для возобновляемого approval с
sealed-токеном, TTL и four-eyes используйте `flow` (шаг после `Approve`,
approver берётся из `flow.ApproverFrom(ctx)`). `flow.Reject` тоже забирает
nonce: отклонённое действие нельзя позже провести как одобренное.

## Теги schema

Поддерживается маленькое подмножество; неизвестный тег — ошибка на `NewTask`.

| Тег                    | Смысл                                                        |
| ---------------------- | ------------------------------------------------------------ |
| `required`             | значение не должно быть нулевым (нельзя на `bool`)           |
| `source=principal`     | строка должна совпадать с полем `Principal`                  |
| `source=application`   | значение пришло из состояния приложения и получает `Trusted` |
| `untrusted`            | явно недоверенный вход (дефолт для входного поля)            |
| `sensitivity=pii`      | совпадения e-mail/телефона/карты заменяются на handles       |
| `sensitivity=secret`   | всё значение заменяется одним handle                         |
| `sensitivity=internal` | метка без маскирования                                       |
| `oneof=a\|b`           | строковый enum                                               |
| `min`, `max`           | числовой диапазон или количество элементов среза             |
| `ground`               | аргумент обязан быть существующим handle                     |

`source` и `ground` применимы только к `string`. `sensitivity` — к `string` и
`[]string`. `time.Time`, `[]byte`, `map`, указатели, вложенные структуры глубже
двух уровней и встроенные struct без явного `json`-имени отклоняются на
`NewTask`: лучше fail на старте, чем schema, которая врёт модели.

Те же `sensitivity`-теги применяются к **результату** инструмента перед тем,
как он попадёт обратно в контекст модели: `secret` становится handle, `pii`
маскируется. Неизвестный `gobellim`-тег на результирующей структуре — ошибка
`NewTask`, чтобы опечатка не отключала маскирование молча.

## Gate

Каждый вызов инструмента от модели проходит через Gate. Порядок:

1. `Resource.TenantID` (пустой заполняется из `Principal.Tenant`) должен
   совпадать с tenant принципала.
2. `Authorizer` (ошибка — `Deny`).
3. `ground`-поля.
4. бизнес-валидация `Tool.Validate`.
5. потолок эффекта.

| Худшая целостность аргументов | Read  | Write    | Egress | Destructive |
| ----------------------------- | ----- | -------- | ------ | ----------- |
| `Trusted` / `Validated`       | allow | allow    | allow  | escalate    |
| `Derived`                     | allow | escalate | deny   | deny        |
| `Untrusted` / нет меток       | allow | escalate | deny   | deny        |

Нулевой `Effect` — `EffectUnknown`, а не `Read`: забыть эффект нельзя.
Для `Write`/`Egress`/`Destructive` `Resource` обязателен: без цели ACL
проверялся бы только по RBAC. Неизвестный инструмент, невалидные аргументы,
ошибка authorizer и чужой tenant — `Deny`, никогда `Allow`.

**Как аргумент получает `Validated`.** Handle-аргумент, бизнес-смысл которого
подтвердил `Tool.Validate`, становится `Validated`: handle выпущен рантаймом для
этого запуска, правило проверено приложением. Свободный текст модели остаётся
`Derived`. Без `Validate` повышения нет — write по-прежнему требует человека.

Бюджеты можно уточнить на задачу: `TaskConfig.Limits` перекрывает значения
`Engine`, включая `MaxContextBytes` — потолок на реальный размер контекста
(текст сообщений и аргументы tool calls), а не на число сообщений.

## Grounding — это handles

Модель не может «подтвердить» ID строкой. `ground`-поле принимает только
handle, созданный рантаймом для этого запуска. Чтобы модель могла ссылаться на
значение, пометьте входное поле `sensitivity=secret` (или `sensitivity=pii`);
`source=application` само по себе даёт метку `Trusted`, но не создаёт handle.

```go
type TicketInput struct {
	OrderID string `gobellim:"sensitivity=secret,required"`
}
type RefundArgs struct {
	OrderID string `gobellim:"required,ground"`
}
// Model sees {"OrderID":"<h1>"} and may pass "<h1>" to orders.refund.
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
	Step("execute", func(ctx context.Context, p gobellim.Principal, s *RefundCase) error {
		// p — requester: после Resume шаги идут от его имени.
		approver, ok := flow.ApproverFrom(ctx)
		if !ok {
			return errors.New("no approver")
		}
		_, _, err := task.ExecuteApproved(ctx, p, approver, &s.Pending)
		return err
	})

res, err := wf.Run(ctx, requester, state)          // res.Status == flow.Suspended
out, err := wf.Resume(ctx, approver, token, flow.Approve)
```

`Resume` проверяет AEAD, tenant, expiry, версию flow, id шага, право апрувера,
Four-Eyes, право requester'а, digest действия и атомарный claim nonce. Размер
входного токена ограничен `WithMaxTokenBytes` до декодирования.
`DecisionUnknown` (нулевое значение) никогда не считается одобрением.
Без `WithStore` flow использует process-local `MemStore`: повторный resume в том
же процессе невозможен, между репликами нужен общий `Store`. SQL-адаптер также
умеет `Prune` просроченных nonce и принимает `ConflictClause` для MySQL.
Claim ставится до внешнего эффекта: падение в этом окне теряет одобрение, а не
рискует двойным исполнением; восстановление — сверка с провайдером.

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
