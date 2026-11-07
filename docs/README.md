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
	Body     string `gobellim:"required,sensitivity=patterns"`
	OrderID  string `gobellim:"sensitivity=secret"`
}

type TriageOutput struct {
	Category string `gobellim:"required,oneof=billing|technical|other"`
	Reply    string `gobellim:"resolve"`
}

type RefundArgs struct {
	OrderID string `gobellim:"required,ground"`
	Cents   int    `gobellim:"min=1,max=5000"`
}

refund := gobellim.Tool[RefundArgs, RefundResult]{
	Name:       "orders.refund",
	Operation:     gobellim.Write, // обязателен: zero value отклоняется
	Permission: "orders:refund",
	Target: func(p gobellim.Principal, a RefundArgs) gobellim.Resource {
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
описание действия: digest, provenance, expiry, nonce, HMAC-подпись engine и
карточка `res.Pending.Card`. `Card.Args` — маскированное представление: `secret`
уже заменён handle, `patterns` замаскирован, а `Provenance` несёт `Sensitivity`
каждого поля. Исполняемые значения лежат в `PendingAction.Args` и остаются на
сервере.

Храните `PendingAction` на сервере: если endpoint одобрения декодирует его из
тела запроса, клиент сам выбирает аргументы и digest, и engine отвергнет такой
объект как `ErrApprovalDrift`. Решение принимает кто-то вне модели: человек или
ваша же бизнес-логика, действующая как аутентифицированный `Principal`. После реального одобрения приложение вызывает
`task.ExecuteApproved(ctx, requester, approver, res.Pending)`: Gate заново
проверяет tenant, RBAC/ACL, `Validate`, tool/operation и digest, а `approver`
попадает в audit как `approval.granted`. Одобрение одноразовое: nonce уходит в
`ClaimStore` (по умолчанию — процессный, `gobellim.WithClaimStore` — общий).
`WithApprovalKey` без `WithClaimStore` — `ErrInvalidConfig`: общий ключ с
процессным claim store дал бы одно исполнение на каждую реплику.

Для многошагового workflow с TTL и four-eyes используйте `flow`: он
останавливается на границе шага и отдаёт вам `Suspension` и состояние для
хранения (шаг после `Approve`, approver берётся из `flow.ApproverFrom(ctx)`).
Sealed-токена нет. `flow.VerdictReject` тоже забирает nonce: отклонённое действие
нельзя позже провести как одобренное.

## Теги schema

Поддерживается маленькое подмножество; неизвестный тег — ошибка на `NewTask`.

| Тег                    | Смысл                                                        |
| ---------------------- | ------------------------------------------------------------ |
| `required`             | значение не должно быть нулевым (нельзя на `bool`)           |
| `source=principal`     | строка должна совпадать с полем `Principal`                  |
| `source=application`   | значение пришло из состояния приложения и получает `Trusted` |
| `ref`                  | выпустить handle, оставив значение публичным                 |
| `resolve`              | (только выход) handle в строке снова становится значением    |
| `sensitivity=patterns` | совпадения e-mail/телефона/карты заменяются на handles       |
| `sensitivity=secret`   | всё значение заменяется одним handle                         |
| `sensitivity=internal` | метка без маскирования                                       |
| `oneof=a\|b`           | строковый enum                                               |
| `min`, `max`           | числовой диапазон или количество элементов среза             |
| `ground`               | аргумент обязан быть handle для своего же пути               |
| `ground=TicketID`      | аргумент обязан быть handle для этого поля входа             |
| `ground=any`           | аргумент обязан быть handle для любого поля входа            |

`source` имеет смысл только на **входном** поле задачи, `ground` — только на
**аргументе** инструмента. Тег не с той стороны — `ErrInvalidConfig` на
`NewTask`, а не молчаливый no-op.

`source`, `ref` и `ground` применимы только к `string`. `sensitivity` — к `string` и
`[]string`. `time.Time`, `[]byte`, `map`, указатели, вложенные структуры глубже
двух уровней и встроенные struct без явного `json`-имени отклоняются на
`NewTask`: лучше fail на старте, чем schema, которая врёт модели.

Те же `sensitivity`-теги применяются к **результату** инструмента перед тем,
как он попадёт обратно в контекст модели: `secret` становится handle, `patterns`
маскируется. Неизвестный `gobellim`-тег на результирующей структуре — ошибка
`NewTask`, чтобы опечатка не отключала маскирование молча.

## Gate

Каждый вызов инструмента от модели проходит через Gate. Порядок:

1. `Resource.TenantID` обязателен для всего, кроме `Compute`, и должен
   совпадать с tenant принципала. Пустой tenant — `Deny` с `target-unbound`, а
   не повод подставить значение из принципала.
2. `ground`-поля: handle, созданный в этом запуске, и только для объявленного поля входа.
3. `Authorizer` (ошибка — `Deny`).
4. бизнес-валидация `Tool.Validate`.
5. потолок доступа.

Grounding идёт раньше `Authorizer` намеренно: id, придуманный моделью, не должен
доходить до ACL приложения и попадать в его аудит.

| Худшая целостность аргументов | Read, Compute | Write    | Send     | Delete   |
| ----------------------------- | ------------- | -------- | -------- | -------- |
| `Trusted`                     | allow         | allow    | allow    | escalate |
| `Derived`                     | allow         | escalate | escalate | deny     |
| `Untrusted` / нет меток       | allow         | escalate | deny     | deny     |

Нулевой `Operation` — `OperationUnknown`, а не `Read`: забыть объявление нельзя.
Для `Write`/`Send`/`Delete` `Target` обязателен и должен вернуть непустые
`Type`, `ID` и `TenantID`: без цели ACL проверялся бы только по RBAC.
Неизвестный инструмент, невалидные аргументы, ошибка authorizer и чужой
tenant — `Deny`, никогда `Allow`. Такой отказ не валит запуск: он пишется в
audit как `gate.denied` и считается в `TaskResult.Denied`, включая вызовы,
которые не дошли до Gate.

`Send` с текстом, который написал модель, уходит в `Escalate`: `Deny` там был бы
решением, которое не может отменить даже человек, а сгенерированный ответ —
обычный случай этой операции. Без одобрения извне он не выполняется.

**Как аргумент становится `Trusted`.** Доверие не зарабатывается внутри Gate.
Аргумент `Trusted` только тогда, когда значение пришло через handle, выпущенный
для поля входа с `source=principal` или `source=application`. Свободный текст
модели всегда `Derived`, и `Tool.Validate` его не повышает: валидатор
подтверждает бизнес-смысл, а не происхождение.

Бюджеты можно уточнить на задачу: `TaskConfig.Limits` перекрывает значения
`Engine`, включая `MaxContextBytes` — потолок на реальный размер контекста
(текст сообщений и аргументы tool calls), а не на число сообщений.

## Grounding — это handles

Модель не может «подтвердить» ID строкой. `ground`-поле принимает только
handle, созданный рантаймом для этого запуска. Пометьте входное поле `ref`, если
значение публичное, но модель должна ссылаться на него; `sensitivity=secret`
нужен для конфиденциальных данных, `sensitivity=patterns` — для полей вроде e-mail.
`source=application` само по себе даёт метку `Trusted`, но не создаёт handle.

```go
type TicketInput struct {
	OrderID string `gobellim:"ref,required"`
}
type RefundArgs struct {
	OrderID string `gobellim:"required,ground"`
}
// Model sees {"OrderID":"<h1>"} and may pass "<h1>" to orders.refund.
```

## Human approval

Для одного действия хватает `task.Run` → `res.Pending` → `ExecuteApproved`:
храните `PendingAction` на сервере, после одобрения передайте его обратно.
`flow` — для нескольких шагов и границ одобрения.

```go
wf, err := flow.FromEngine[RefundCase](engine, "refund",
	flow.WithTTL(24*time.Hour), // опционально; по умолчанию 1h из Engine
)

wf.Step("prepare", prepareRefund).
	Approve("manager", flow.ApproveConfig[RefundCase]{
		Permission:       "refund:approve",
		ActionPermission: "orders:refund",
		FourEyes:         true,
		Card:             refundApprovalCard, // строит карточку из состояния
		Target:           refundResource,     // tenant binding для ACL апрувера
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

res, err := wf.Run(ctx, requester, RefundCase{})
// res.Status == flow.Suspended. Сохраните res.Suspension и res.State.
out, err := wf.Resume(ctx, approver, *res.Suspension, res.State, flow.VerdictApprove)
```

`flow.FromEngine` берёт authorizer, audit, claim store, часы и ключ подписи у
`Engine`: flow проверяет апрувера тем же адаптером, что и Gate.

`Resume` проверяет tenant, expiry, версию flow, id шага, право апрувера,
Four-Eyes, право requester'а, digest действия, пересчитанный из сохранённого
состояния, и атомарный claim nonce. `VerdictUnknown` (нулевое значение) никогда
не считается одобрением. Sealed-токена нет: `Suspension` и состояние хранит
приложение, и `Suspension` подписан ключом выпустившего flow — объект, который
процесс не подписывал, это `ErrFlowDrift` (`DESIGN.md` §8.5). Без `WithStore`
flow использует process-local `MemStore`: повторный resume в том же процессе
невозможен, между репликами нужен общий `Store`. SQL-адаптер также умеет
`Prune` просроченных nonce, а для MySQL — `InsertPrefix("INSERT IGNORE")`
с пустым `ConflictClause` (с `ON DUPLICATE KEY UPDATE` флаг `clientFoundRows`
превращает дубликат в выигранный claim).
Claim ставится до внешнего эффекта: падение в этом окне теряет одобрение, а не
рискует двойным исполнением; восстановление — сверка с провайдером.

## Выход

`res.Label` — худшая целостность и худшая чувствительность всего, что видел
модель: входных полей, результатов инструментов и её собственного текста. Поле
с `sensitivity=secret` делает ответ `Secret` даже когда модель видела только
handle: поле с `resolve` вернёт само значение. Недоверенный ввод делает ответ
`Untrusted`, потому что его текст мог быть инструкцией. Пустые значения в метку
не входят.

`TaskConfig.Egress` вызывается один раз перед возвратом готового ответа и может
его отклонить:

```go
Egress: func(ctx context.Context, p gobellim.Principal, l gobellim.Label) error {
	if l.Sensitivity > gobellim.Internal && !p.HasRole("support") {
		return errors.New("answer may contain personal data")
	}
	return nil
},
```

Отказ — это `ErrEgressDenied`: запуск получает `TaskFailed`, а `Out` не
возвращается. Хук не видит текст ответа: он есть в `Out`, и приложению хватает
метки, чтобы решить про получателя. `nil` сохраняет прежнее поведение, метка
всё равно остаётся в `TaskResult`.

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
