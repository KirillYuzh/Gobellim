# Обработка ошибок: таксономия, ретраи и HTTP

Этот гид — для вызывающего: как различать ошибки `kit`, что ретраить, что фатально
для хода и как превратить ошибку в HTTP-ответ. Список sentinel-ошибок и их поля —
[Справочник: Ошибки](../reference/errors.md); здесь — решения, рецепты и грабли.

## Таксономия sentinel-ошибок

Все sentinel-ошибки объявлены в `internal/core` и реэкспортированы из `kit`
(`errors.go`). Оборачиваются они с сохранением причины (`%w`, `errors.Join`),
поэтому проверяются через `errors.Is`.

| Sentinel              | Откуда приходит                                                                                                           | Слой              |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------- | ----------------- |
| `ErrInvalidConfig`    | `kit.New`, сборка guard/чанкера/писателя, невалидный `policy.yaml`, неполный конфиг провайдера, нет `GuardL2`             | старт             |
| `ErrUnauthorized`     | нет/пуст принципал; `Ingest`/`Search`/`Delete` без `Tenant`; RBAC-отказ; эгресс заблокирован                              | примитив / ops    |
| `ErrGuardReject`      | rail `input` вернул `reject` (fast-fail)                                                                                  | guard             |
| `ErrGuardUnavailable` | guard недоступен (fail-closed) или таймаут очереди ORT                                                                    | guard             |
| `ErrProvider`         | провайдер LLM/эмбеддера/ES вернул не-2xx; ответ `Generate` не декодировался; ошибка item `_bulk`                          | провайдер         |
| `ErrInvalidArgs`      | args не JSON или не прошли схему; пустой `SearchRequest.Query`; нет `user`/`assistant`; пустой `doc_id`; пустой `Doc.ACL` | детерминированный |
| `ErrMaxSteps`         | исчерпан `MaxSteps` цикла                                                                                                 | цикл              |
| `ErrBudget`           | превышен `Config.MaxTotalTokens` за ход                                                                                   | цикл              |
| `ErrRateLimited`      | исчерпан лимит на тенанта (`Config.RateLimit`, `BR-86`)                                                                   | примитив / ops    |
| `ErrApprovalRequired` | инструмент с `RequireApproval` без `Approver`, отказ или повтор fingerprint                                               | approval          |
| `ErrToolNotFound`     | `App.CallTool` для неизвестного инструмента (в `Chat` — результат-ошибка модели)                                          | CallTool          |
| `ErrTool`             | сбой, помеченный как падение внутри инструмента: ошибка хука или сбой MCP-инструмента                                     | инструмент        |
| `ErrNotFound`         | в `eval` — трейс, который рекордер не получил                                                                             | eval              |

> [!NOTE]
> Эти три sentinel’а объявлены и размечаются (`error_kind`), и все три имеют
> реальные сайты возврата, но **операции фасада их не возвращают**: `ErrToolNotFound`
> приходит из `CallTool` (не из `Chat`), `ErrTool` — из обёртки сбоя инструмента, а
> `ErrNotFound` — из пакета `eval`. На них нельзя опираться в коде, который ждёт их
> от `App.Chat`/`App.Generate`.

## `errors.Is` против `errors.As`

`errors.Is` — для «что это за класс ошибки»; `errors.As` — когда нужны поля.
Отказ guard несёт `*kit.GuardRejectError` (алиас типа из `internal/call`), и он же
разворачивается в `core.ErrGuardReject`:

```go
if errors.Is(err, kit.ErrGuardReject) {
	// класс: отклонил guard
}

var reject *kit.GuardRejectError
if errors.As(err, &reject) {
	_ = reject.Rail      // guard.Rail: input, retrieval, execution, output, tool_result
	_ = reject.Version   // версия guard (I-08)
	if reject.Tripwire != nil {
		_ = reject.Tripwire.Reason   // например "BANK_CARD,SECRET"
		_ = reject.Tripwire.RuleID   // "BANK_CARD/L1"
		_ = reject.Tripwire.Metadata // {"rail": "...", "categories": [...]}
	}
}
```

`Tripwire` заполняется только на `reject`; при `mask`/`detect` его нет.

## `error_kind` в трейсах

Примитив `internal/call` при ошибке выставляет observation `level=ERROR`,
`status_message=err.Error()` и `error_kind` по таблице `errorKindOf`:

| `error_kind`        | Sentinel                |
| ------------------- | ----------------------- |
| `unauthorized`      | `ErrUnauthorized`       |
| `guard_reject`      | `ErrGuardReject`        |
| `guard_unavailable` | `ErrGuardUnavailable`   |
| `invalid_args`      | `ErrInvalidArgs`        |
| `tool_not_found`    | `ErrToolNotFound`       |
| `tool`              | `ErrTool`               |
| `max_steps`         | `ErrMaxSteps`           |
| `budget`            | `ErrBudget`             |
| `rate_limited`      | `ErrRateLimited`        |
| `approval_required` | `ErrApprovalRequired`   |
| `not_found`         | `ErrNotFound`           |
| `provider`          | `ErrProvider`           |
| `internal`          | всё остальное (default) |

На отказе guard в observation есть ещё `guard_rail`, `guard_action=reject` и
`guard_categories`. Запросы к ES остаются data-driven: панель «ошибки по
`error_kind`» строится прямо по этим полям.

> [!NOTE]
> `tripwire_rule`/`tripwire_reason` в `metadata` observation присваивает тот же
> примитив — но при `reject` он возвращает ошибку раньше присваивания, поэтому на
> отказе полагайтесь на `guard_action`/`guard_categories` и на сам
> `GuardRejectError`, а метаданные считайте best-effort (`проверьте у себя` на
> вашей версии).

## Что фатально для хода, а что — результат для модели

Ошибка инструмента — **данные для модели** (`I-11`), а не падение хода. Фатальным
ход делает только ограниченный набор:

| Фатально (ход прерывается, ошибка наружу)         | Возвращается модели как `{"error": …}`                    |
| ------------------------------------------------- | --------------------------------------------------------- |
| RBAC-отказ (нет права на инструмент)              | неизвестный инструмент                                    |
| эгресс-отказ (Rule of Two / host-allowlist)       | превышен `max_calls` по инструменту                       |
| ошибка провайдера (`ErrProvider`)                 | аргументы больше `ArgsMaxBytes`                           |
| отмена/дедлайн контекста                          | **первый** невалидный набор args (BR-23)                  |
| отсутствие подтверждения (`ErrApprovalRequired`)  | сбой внутри инструмента                                   |
| **второй** невалидный args по тому же инструменту | reject guard на `tool_result` (`result blocked by guard`) |
| reject guard на `input` (`ErrGuardReject`)        |                                                           |

Практическая разница: `{"error": "…"}` уходит модели, и она может среагировать
(переспросить, попробовать иначе). Фатальный отказ модели не виден — ход просто
завершается ошибкой.

## Ретраи: где живут повторы

Повторы — на уровне HTTP-транспорта, внутри клиентов провайдеров. `kit` не
ретраит операции фасада.

```go
type StatusError struct {
	StatusCode int
	RetryAfter time.Duration
	Body       string
}

func (e *StatusError) IsRetryable() bool // 408, 425, 429, 5xx
func IsRetryable(err error) bool          // false для cancel/deadline
func RetryAfter(err error) time.Duration  // значение Retry-After
```

- `internal/httpjson.NewClient(Config{Timeout, MaxRetries, Backoff, MaxResponseBytes})`
  даёт `*http.Client` с ретраями: повторяет `StatusError.IsRetryable()`, уважает
  `Retry-After` (секунды или HTTP-дата) и делает jitter-бэкофф.
- `model.Chain(model.ChainConfig{MaxRetries, Backoff}, clients...)` — цепочка
  провайдеров: пробует клиентов по очереди и ретраит через `model.Retryable` /
  `model.RetryAfter` (тонкие обёртки над `httpjson`).
- `context.Canceled`/`context.DeadlineExceeded` **не** ретраятся.

Тайм-ауты приходят как `context.DeadlineExceeded` или `ErrProvider`, а не как
отдельный sentinel.

## Fail-closed против fail-open

`kit` закрывается при неопределённости — это осознанный выбор (`BR-06`):

| Недоступно                   | Поведение                                           |
| ---------------------------- | --------------------------------------------------- |
| guard (модель, ORT, очередь) | `ErrGuardUnavailable`, операция отклонена           |
| `Approver`                   | `ErrApprovalRequired`, вызов не исполняется         |
| `policy.yaml` невалиден      | `ErrInvalidConfig` на старте, сервис не поднимается |

Единственный fail-open — тестовый детектор `guardtest.AllowAll()`, который
отключает проверки. Он живёт в `guard/guardtest` именно потому, что в публичном
API выключателей нет (`I-15`, `BR-60`).

> [!WARNING]
> `guardtest.AllowAll` нельзя ставить в прод. Он существует для тестов, где нужен
> «пропустить всё». Проверьте свой бинарь: `guardtest.AllowAll` не должен
> встречаться вне `_test.go`.

## Бюджет и лимиты

| Лимит                          | Ошибка / исход                                              |
| ------------------------------ | ----------------------------------------------------------- |
| `Config.MaxTotalTokens`        | `ErrBudget` (фатально для хода)                             |
| `Config.MaxSteps`              | `ErrMaxSteps` (фатально)                                    |
| `MaxCallsPerTool`              | результат-ошибка модели                                     |
| `ToolTimeout`                  | сбой инструмента; фатально, если это `ErrProvider`/контекст |
| `ArgsMaxBytes`                 | результат-ошибка модели                                     |
| один ретрай на невалидные args | второй — фатально (`BR-23`)                                 |

`ErrBudget` и `ErrMaxSteps` — признак «ход слишком дорогой/длинный»; это не сбой
инфраструктуры.

## Рецепты

### HTTP: полный `writeError`

```go
func writeError(w http.ResponseWriter, err error) {
	var reject *kit.GuardRejectError
	if errors.As(err, &reject) {
		rule, reason := "", ""
		if reject.Tripwire != nil {
			rule, reason = reject.Tripwire.RuleID, reject.Tripwire.Reason
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "guard_reject",
			"rail":   string(reject.Rail),
			"rule":   rule,
			"reason": reason,
		})
		return
	}

	switch {
	case errors.Is(err, kit.ErrInvalidArgs):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_args"})
	case errors.Is(err, kit.ErrUnauthorized):
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	case errors.Is(err, kit.ErrApprovalRequired):
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "approval_required"})
	case errors.Is(err, kit.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	case errors.Is(err, kit.ErrBudget):
		writeJSON(w, http.StatusPaymentRequired, map[string]any{"error": "budget_exceeded"})
	case errors.Is(err, kit.ErrMaxSteps):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "max_steps"})
	case errors.Is(err, kit.ErrRateLimited):
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited"})
	case errors.Is(err, kit.ErrGuardUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "guard_unavailable"})
	case errors.Is(err, kit.ErrProvider):
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "provider_error"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal"})
	}
}
```

Тело с `rule`/`reason` годится для **внутреннего** API — эту же форму
использует [Guardrails](../concepts/guardrails.md). Если ответ уходит конечному
пользователю, отдавайте только класс (`{"error":"guard_reject","rail":…}`):
[Справочник: Ошибки](../reference/errors.md) требует не раскрывать текст
сработавшего правила. `writeJSON` — ваш хелпер (в коде выше он только для
краткости): выставьте `Content-Type: application/json`, статус и закодируйте тело.

### Отличить ошибку пользователя от сбоя провайдера

Для алертинга делите ошибки на «ожидаемые 4xx» и «инфраструктурные 5xx»:

```go
func isInfra(err error) bool {
	switch {
	case errors.Is(err, kit.ErrProvider),
		errors.Is(err, kit.ErrGuardUnavailable),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, context.Canceled):
		return true
	}
	return false
}
```

`ErrGuardUnavailable` и `ErrProvider` — страница дежурного; `ErrGuardReject`,
`ErrInvalidArgs`, `ErrBudget`, `ErrMaxSteps` — сигнал о качестве промпта/лимитах,
а не об инфраструктуре.

### Ретрай с бэкоффом вокруг `Chat`

`Chat` — **не** идемпотентная операция, если инструменты имеют эффекты
`Write`/`Egress`: повтор хода повторит и их. Свой цикл повторения вокруг `Chat`
уместен только для хода без побочных эффектов (только `EffectRead`-инструменты):

```go
func chatReadOnly(ctx context.Context, app *kit.App, req kit.ChatRequest) (kit.Response, error) {
	backoff := 200 * time.Millisecond
	for attempt := 0; attempt < 3; attempt++ {
		res, err := app.Chat(ctx, req)
		if err == nil {
			return res, nil
		}
		if !model.Retryable(err) { // см. model/http.go
			return res, err // res всё равно несёт TraceID и Decision
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return kit.Response{}, fmt.Errorf("chat: retries exhausted")
}
```

Транспортные ретраи и `Retry-After` уже встроены в клиент провайдера — этот цикл
добавляется поверх, и только когда повтор безопасен.
Для effectful-инструментов дедуп делается на стороне эффекта: инструмент читает
стабильный ключ вызова через `kit.IdempotencyKey(ctx)` (`BR-105`) и передаёт его
downstream.

### Показать ошибку инструмента, не раскрывая внутренности

Не выводите сырой текст ошибки пользователю. Штатный путь: ошибка становится
`{"error": "…"}` для модели, и модель пересказывает её человеческим языком. Если
вы формируете ответ сами, маппьте классы, а не текст:

```go
func userFacingToolError(err error) string {
	switch {
	case errors.Is(err, kit.ErrUnauthorized):
		return "Недостаточно прав для этого действия."
	case errors.Is(err, context.DeadlineExceeded):
		return "Операция заняла слишком много времени."
	default:
		return "Не удалось выполнить действие. Попробуйте позже."
	}
}
```

## Частые ошибки

- **Оборачивание, ломающее `errors.Is`.** Каждый пакет оборачивает причину через
  `%w`. Оборачивайте так же: `fmt.Errorf("…: %w", err)` (или `errors.Join`).
  `%v`/`%s` разрывают цепочку, и `errors.Is`/`errors.As` перестают видеть
  sentinel.
- **Трактовать `ErrGuardUnavailable` как «повторить вечно».** Это fail-closed:
  если модель не загружена, повтор не поможет. Ретрайте осознанно и с потолком;
  «бесконечный ретрай» только превратит недоступность в таймаут.
- **Считать, что непустая ошибка значит «модель ошиблась».** `ErrBudget`,
  `ErrMaxSteps` и `ErrGuardReject` — про ваши лимиты и вход, а `ErrUnauthorized` —
  про права; это не «модель». Сортируйте по `errors.Is`, а не по тексту.
- **Путать `ErrRateLimited` с 429 провайдера.** `ErrRateLimited` возвращает
  лимит на тенанта (`Config.RateLimit`); 429 провайдера ретраит HTTP-слой
  (`Retry-After`), а после исчерпания ретраев он приходит как `ErrProvider`.
- **Пробрасывать `err.Error()` в UI.** Тексты содержат внутренние детали (имена
  правил, пути, статусы); наружу — только классы.

## Ограничения

- `ErrToolNotFound`, `ErrTool` и `ErrNotFound` имеют реальные сайты возврата
  (`App.CallTool`, обёртка сбоя инструмента, `eval`), но операции фасада их не
  возвращают: опирайтесь на `errors.Is` только на границах, где эти вызовы есть.
- Повтор (ретрай) есть только на уровне HTTP-транспорта провайдера; идемпотентности
  операций фасада `kit` не гарантирует.
- Нет отдельных sentinel’ов для «частичного успеха» `_bulk`/`_delete_by_query`:
  несоответствие `deleted`/`failures` приходит как `ErrProvider`.
- Причина `ErrGuardUnavailable` не различает «модель не загружена» и «очередь ORT
  переполнена» на уровне sentinel — обе дают один класс (детали в тексте).

## Смотрите также

- [Справочник: Ошибки](../reference/errors.md) — таблица sentinel и HTTP-маппинг
- [Guardrails](../concepts/guardrails.md) — `GuardRejectError` и tripwire
- [Наблюдаемость](../concepts/observability.md) — `error_kind` в observations
- [Безопасность](./security.md) — fail-closed, approval, эгресс
- [Справочник kit](../reference/kit.md) — где возвращается каждая ошибка
- [Бизнес-правила](../internals/business-rules.md) — `BR-23`, `BR-50`, `BR-60`
