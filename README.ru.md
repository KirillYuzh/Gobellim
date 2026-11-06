# Gobellim

**[Русский](README.ru.md) · [English](README.md)**

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

![Gobellim](./site/public/gobellim-compact-white.svg)

Слой исполнения и политики для одного LLM use-case внутри существующего
Go-сервиса. Gobellim — библиотека, а не платформа: она не поднимает сервер, БД
или брокер, а core-модуль не имеет обязательных зависимостей.

![Архитектура Gobellim](docs/architecture.svg)

Главный инвариант:

> Модель может предложить текст, аргументы или действие. Она не может выдать
> себе права, изменить policy, зарегистрировать инструмент или выполнить side
> effect в обход Gate.

## Дельта: один хендлер, сделанный безопасным

Обычный HTTP-хендлер вызывает LLM-клиент напрямую:

```go
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	answer, err := h.llm.Complete(r.Context(), req.Prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"answer": answer})
}
```

Тот же хендлер с Gobellim:

```diff
 func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
     var req chatRequest
     if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
         http.Error(w, err.Error(), http.StatusBadRequest)
         return
     }
-    answer, err := h.llm.Complete(r.Context(), req.Prompt)
+    res, err := h.task.Run(r.Context(), h.principal(r), TicketInput{
+        TenantID: principalFromRequest(r).Tenant,
+        TicketID: req.TicketID,
+        Body:     req.Prompt,
+    })
     if err != nil {
         http.Error(w, err.Error(), http.StatusBadGateway)
         return
     }
-    writeJSON(w, map[string]string{"answer": answer})
+    if res.Status == gobellim.TaskApprovalRequired {
+        // res.Pending — остановленное действие. Ничего не выполнено.
+        writeJSON(w, map[string]any{"status": "approval_required", "action": res.Pending.Card})
+        return
+    }
+    writeJSON(w, map[string]string{"answer": res.Output.Reply})
 }
```

Хендлер сохраняет свой `ctx`, свои инструменты и бизнес-функции. Gobellim
добавляет одно место, где решается судьба каждого модельного вызова, и одно
место, откуда одобренное действие продолжается.

## Что добавляет Gobellim

```mermaid
flowchart LR
    H["ваш handler"] --> P["Principal"]
    P --> T["Task: типизированный вход<br/>labels, handles, лимиты"]
    T --> M["модель предлагает<br/>ответ или tool call"]
    M --> G{"Gate"}
    G -->|"tenant · RBAC/ACL · grounding · Validate · access"| A["Allow"]
    G --> D["Deny"]
    G --> E["Escalate"]
    A --> X["ваш tool handler"]
    X --> R["отредактированный результат<br/>обратно в модель"]
    E --> C["PendingAction<br/>digest · nonce · expiry"]
    C -->|"доверенное одобрение"| EA["ExecuteApproved<br/>Gate прогоняется снова"]
    EA --> X
    G --> AU["audit: labels и digest"]
```

- **Principal** — кто вызывает: пользователь, tenant, роли. Приходит из вашего
  слоя аутентификации, никогда из текста модели.
- **Gate** — одна функция, превращающая предложенное действие в `Allow`,
  `Deny` или `Escalate`. Неизвестный инструмент, невалидные аргументы, ошибка
  authorizer и чужой tenant никогда не становятся `Allow`.
- **PendingAction** — сериализуемая эскалация: разрезолвленные аргументы,
  digest, nonce, expiry и HMAC, привязывающий объект к выпустившему его engine.
  Это единственный способ исполнить принятое апрувером действие, он
  одноразовый, и endpoint одобрения не должен принимать его поля от клиента.
- **Foreign approval** — решение об эскалированном действии приходит вне
  модели: от человека или от вашей же бизнес-логики, действующей как
  аутентифицированный `Principal`. Gate проверяет его одинаково.
- **Audit** — labels, digest, решения и счётчики. Без prompt text, сырых
  аргументов и секретов.

Каждый — это гейт на пути вызова, а не хук, который можно забыть.

## Почему Gobellim

Gobellim — библиотека, а не сервис: вы сохраняете свои хендлеры, конкурентность
и инфраструктуру. Каждое утверждение ниже структурно, и за каждым есть код и
тесты.

- **Один путь вызова, без обхода.** Каждый модельный tool call решается в
  [`gate.go`](./gate.go); цикл в [`task.go`](./task.go) не может дойти до
  handler'а мимо него. [`internal/importrules`](./internal/importrules) — это
  build-time проверка графа пакетов.
- **Модель не участвует в доступе.** Permission и эффект объявлены на
  инструменте ([`tool.go`](./tool.go)), проверяются вашим `Authorizer` и
  перепроверяются, когда одобренное действие продолжается.
- **Сырые секреты не попадают в prompt.** Входные поля и результаты
  инструментов маскируются или заменяются handle'ами ([`handles.go`](./handles.go),
  [`redact.go`](./redact.go)); модель может ссылаться только на handle.
- **Одобрение привязано и одноразово.** Digest покрывает инструмент и его
  аргументы; nonce фиксируется до запуска handler'а ([`claims.go`](./claims.go),
  [`flow/`](./flow)); HMAC engine отвергает объект, который он не выпускал
  ([`approval.go`](./approval.go)).
- **Audit — не лог всего.** События несут labels и digest, а не значения
  ([`audit.go`](./audit.go)); незавершённый запуск аудируется как ошибка, а не
  как успех.

Универсальные Go-фреймворки для LLM дают адаптеры. Вы можете обернуть один из
них policy и guard'ом сами — и тогда владеете этой обёрткой и её тестами.
Gobellim — это такая обёртка: Gate, approval и security-наборы идут вместе с
ней.

## Установка

```sh
go get github.com/KirillYuzh/Gobellim
```

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

type RefundResult struct {
	RefundID string
	Status   string
}

refund := gobellim.Tool[RefundArgs, RefundResult]{
	Name:       "orders.refund",
	Access:     gobellim.Write, // обязателен: zero value отклоняется
	Permission: "orders:refund",
	Target: func(p gobellim.Principal, a RefundArgs) gobellim.Resource {
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
	gobellim.WithAudit(gobellim.SlogSink{}), // обязателен; или plugins/langfuse
	gobellim.WithLimits(gobellim.Limits{
		MaxInputBytes:  32 << 10,
		MaxOutputBytes: 16 << 10,
		MaxToolCalls:   3,
	}),
)
if err != nil {
	return err
}

task, err := gobellim.NewTask[TicketInput, TriageOutput](engine, gobellim.TaskConfig{
	Name:        "ticket.triage",
	Instruction: "Classify the ticket and propose a reply.",
	Tools:       []gobellim.ToolDef{refund},
})
if err != nil {
	return err
}

res, err := task.Run(ctx, principalFromRequest(r), in)
if err != nil {
	return err
}
if res.Status == gobellim.TaskApprovalRequired {
	// res.Pending — остановленное действие. После реального одобрения:
	//   out, rec, err := task.ExecuteApproved(ctx, requester, approver, res.Pending)
}
```

`gobellim.New` использует functional options. Nil-модель, nil-authorizer или
nil-audit sink — `ErrInvalidConfig`; разрешающего дефолта нет, и запуска без
audit-следа тоже. В тесте, где audit не проверяется, передайте
`gobellim.SlogSink{Logger: slog.New(slog.DiscardHandler)}`. Бюджеты можно
уточнить на задачу через `TaskConfig.Limits`; нулевое поле наследует значение
`Engine`.

## Grounding — это handle

`ground`-поле принимает только handle, созданный для этого запуска, и только из
объявленного источника. Голый `ground` использует путь аргумента: `OrderID`
принимает handle из входного поля `OrderID`. Если входное поле называется иначе,
укажите `ground=RequestedOrder`. Handle в non-ground поле — `Deny`, даже если
токен существует. `ground=any` — явное ослабление, не дефолт. Чтобы модель могла
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
	flow.WithAuthorizer(authz), // обязателен
	flow.WithAudit(auditSink),  // обязателен
	flow.WithStore(store),
	flow.WithTTL(24*time.Hour),
)
if err != nil {
	return err
}

wf.Step("prepare", prepareRefund).
	Approve("manager", flow.ApproveConfig[RefundCase]{
		Permission:       "refund:approve",
		ActionPermission: "orders:refund",
		FourEyes:         true,
		Card:             refundApprovalCard, // детерминированная: Resume пересобирает её
		Target:           refundResource,
	}).
	Step("execute", func(ctx context.Context, p gobellim.Principal, s *RefundCase) error {
		// p — requester: Resume уже проверил одобрение перед этим шагом.
		return orders.Refund(ctx, p.Tenant, s.OrderID, s.Cents)
	})

res, err := wf.Run(ctx, requester, state) // res.Status == flow.Suspended
out, err := wf.Resume(ctx, approver, *res.Suspension, res.State, flow.Approve)
```

`flow` одобряет шаг workflow. Если одобрять нужно именно tool call модели,
используйте `Task` с `res.Pending` и `ExecuteApproved`, не оборачивая это в
`flow`: один путь — один approval engine.

`Run` возвращает `Suspension{Nonce, Card, ExpiresAt, Requester}` и состояние.
Зашифрованного bearer-токена нет, поэтому нет и ключа, который надо ротировать:
единственное доверие здесь — к аутентификации пользователей и к хранилищу
приложения.

`Resume` принимает решение только от аутентифицированного approver'а и
проверяет tenant, expiry, `Card.Flow`, реальность `Card.Step` для этого flow,
совпадение `RequiredPermission`/`FourEyes` с текущей конфигурацией (`ErrFlowDrift`
при смене политики), право апрувера, Four-Eyes, право requester'а, tenant цели,
пересобранный `ActionDigest` и атомарный claim nonce. `Reject` тоже забирает
nonce: сказанное «нет» нельзя позже повторить как «да». `DecisionUnknown`
(нулевое значение) и неизвестное решение — `ErrDecision`, никогда не одобрение.

Без `WithStore` используется process-local `MemStore`: одноразовость только
внутри процесса, между репликами нужен общий `Store`. SQL-адаптер умеет `Prune`
просроченных nonce и принимает `ConflictClause` для MySQL. Nonce занимается до
внешнего эффекта: падение в этом окне теряет одобрение, а не рискует двойным
исполнением.

`Task.ExecuteApproved` исполняет `PendingAction` после реального одобрения.
Gate заново проверяет tenant, RBAC/ACL, `Validate`, tool/access и digest ровно
тех аргументов, которые видел апрувер; апрувер пишется в audit как
`approval.granted`. Nonce фиксируется до запуска handler'а: второй вызов —
`ErrApprovalReplay`, а падение в этом окне теряет одобрение, а не рискует
двойным исполнением.

У `PendingAction` есть HMAC по всей карточке с ключом engine. Храните его на
сервере: endpoint одобрения, который декодирует `PendingAction` из тела
запроса, отдаёт клиенту аргументы, digest и nonce — engine отвергнет такой
объект как `ErrApprovalDrift`. По умолчанию ключ случайный и живёт в процессе;
`gobellim.WithApprovalKey` задаёт общий 32-байтовый ключ, если эскалация и
исполнение происходят на разных репликах.

## Observability

```go
sink, err := langfuse.New(langfuse.Config{
	Host:      "https://cloud.langfuse.com",
	PublicKey: os.Getenv("LANGFUSE_PUBLIC_KEY"),
	SecretKey: os.Getenv("LANGFUSE_SECRET_KEY"),
	OnDrop:    func(e gobellim.Event) { metrics.Inc("audit_dropped", string(e.Kind)) },
})
if err != nil {
	return err
}
defer sink.Close(ctx)

engine, err := gobellim.New(
	gobellim.WithModel(model),
	gobellim.WithAuthorizer(authz),
	gobellim.WithAudit(sink),
)
```

Sink батчами отправляет `trace-create`, `generation-create` и `span-create`.
Уходят только labels, digest, decision, latency и токены; prompt text, сырые
аргументы и секреты — нет. Principal id и tenant по умолчанию не отправляются;
`SendPrincipalID` / `SendTenant` включают эту корреляцию явно. Для локального
просмотра: `langfuse.Config{DryRun: true, Writer: os.Stdout}`.

Адаптеры моделей — в отдельных пакетах. `plugins/gigachat` реализует freemium
OAuth и маппинг `functions`/`function_call`. Запускаемые сценарии — в notebook'ах.

## Гарантии и non-goals

Гарантируется при корректной интеграции:

- вызов инструмента от модели не доходит до handler без Gate;
- unknown tool, невалидные аргументы, ошибка authorizer и чужой tenant не
  становятся `Allow`;
- пустой набор аргументов не считается доверенным;
- handle в non-ground поле и handle из чужого входного поля отклоняются;
- `ground`-аргумент нельзя удовлетворить сырой строкой модели;
- свободный текст модели не становится trusted из-за JSON schema или `Validate`;
- approval привязан к инструменту и аргументам, которые видел апрувер,
  `ExecuteApproved` прогоняет Gate заново, одобрение одноразово, а объект,
  который engine не выпускал, отклоняется;
- отменённый запрос не сжигает одобрение: `ctx.Err()` проверяется до `Claim`;
- `sensitivity` на результате инструмента не пускает секрет в контекст модели,
  включая `map` и `[]byte`;
- audit содержит labels и digest, а не prompt text и секреты;
- незавершённый запуск аудируется как `task.failed`, никогда как
  `task.completed`.

Не гарантируется:

- exactly-once для внешнего side effect: общий `Store` даёт одноразовый claim,
  идемпотентность — на стороне провайдера;
- редактирование внутри self-marshalling типа в результате (`time.Time`,
  `json.Marshaler`); `sensitivity` на таком типе — ошибка `NewTask`;
- отсутствие вредного или ложного текста модели;
- точный data-flow внутри модели;
- одноразовость Resume между репликами без общего `Store`;
- durable recovery произвольной Go-функции;
- retrieval/RAG и data ACL — это ответственность приложения;
- `Readers` / per-reader ACL не реализованы.

## Документация

`docs/` — обычный Markdown, читается прямо на GitHub. `site/` рендерит те же
файлы через Next.js и Fumadocs:

```sh
cd site && npm install && npm run dev
```

- [`DESIGN.md`](./DESIGN.md) — спецификация.
- [`docs/README.md`](./docs/README.md) — how-to.
- [`examples/notebooks`](./examples/notebooks/README.md) — запускаемые сценарии,
  моки и реальный GigaChat free tier.
- [`plugins/langfuse`](./plugins/langfuse) — audit sink.
- [`plugins/gigachat`](./plugins/gigachat) — model adapter.

## Участие

Gobellim принимает issues, форки и pull requests. Начните с
[CONTRIBUTING.md](.github/CONTRIBUTING.md). Отчёты об уязвимостях — через
[SECURITY.md](SECURITY.md), не в публичный трекер.

## Лицензия

[`MIT`](LICENSE)
