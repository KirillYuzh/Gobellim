# Ошибки

Sentinel-ошибки kit объявлены в `internal/core` и переэкспортированы из пакета `kit` (`errors.go`). Все они оборачиваются с сохранением причины через `%w` и `errors.Join`, поэтому проверяются только через `errors.Is`.

## Sentinel-ошибки

| Sentinel              | Когда возвращается                                                                                                                                                                                                                                                                                                                                                                                                  | Что видит клиент                         | `error_kind` в трейсе |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------- | --------------------- |
| `ErrInvalidConfig`    | `kit.New`: нет `ES`, `LLM` или `Policy`; невалидный `policy.yaml`; ошибка сборки guard, чанкера или trace-писателя; инструмент без имени, обработчика или с невалидной схемой. Также неполный конфиг провайдера, `Ingest` без `Embedder` или с пустым `ID`, `Search` без `Embedder`, `Generate` со стратегией `Fallback` без `Fallback`, `NewLocal` без тега `ort` или без пресета, невалидный `kit_embedder.json`. | падение на старте, сервис не поднимается | —                     |
| `ErrUnauthorized`     | Нет или пустого принципала в контексте; `Ingest`/`Search`/`Delete` без `Tenant`; RBAC отказал (`authz.Authorize`); эгресс заблокирован «Rule of Two» или host-allowlist.                                                                                                                                                                                                                                            | 401 без деталей политики                 | `unauthorized`        |
| `ErrInvalidArgs`      | Аргументы не разобрались как JSON или не прошли JSON Schema; пустой `SearchRequest.Query`; нет сообщений ролей `user`/`assistant` в `Chat`; пустой `doc_id` в `Delete`. Для инструмента невалидные или слишком большие аргументы возвращаются модели результатом-ошибкой, а не этим sentinel’ом.                                                                                                                    | 400 с перечнем нарушенных полей          | `invalid_args`        |
| `ErrMaxSteps`         | Исчерпан `MaxSteps` цикла (модель не дала финального ответа).                                                                                                                                                                                                                                                                                                                                                       | 422, признак «слишком длинный tool-цикл» | `max_steps`           |
| `ErrBudget`           | Превышен `Config.MaxTotalTokens` за ход.                                                                                                                                                                                                                                                                                                                                                                            | 402/429, признак «бюджет исчерпан»       | `budget`              |
| `ErrApprovalRequired` | Инструмент с `RequireApproval` вызван без настроенного `Approver`, `Approver` отказал или одобрение этого fingerprint уже израсходовано в текущем ходе.                                                                                                                                                                                                                                                             | 403/409, запрос на подтверждение         | `approval_required`   |
| `ErrGuardReject`      | Rail `input` вернул reject (fast-fail) в `Chat`/`Generate`.                                                                                                                                                                                                                                                                                                                                                         | 400 без текста сработавшего правила      | `guard_reject`        |
| `ErrGuardUnavailable` | Guard недоступен (fail-closed) или ожидание сессии ORT заняло больше 2 секунд.                                                                                                                                                                                                                                                                                                                                      | 503                                      | `guard_unavailable`   |
| `ErrProvider`         | Ошибка провайдера LLM, эмбеддера или Elasticsearch (ответ не-2xx); таймаут очереди ORT-эмбеддера; ответ `Generate` не декодировался в `T`; ошибка итема `_bulk`.                                                                                                                                                                                                                                                    | 502                                      | `provider`            |

`ErrRateLimited` — действующий sentinel: его возвращает лимит на тенанта
(`Config.RateLimit`, BR-86) до обращения к провайдеру, хранилищу или guard;
429 провайдера обрабатывает HTTP-слой через `Retry-After`.

Зарезервированные sentinels (объявлены и размечаются в `internal/call`, но операции
фасада их сейчас не возвращают — они нужны приложению и адаптерам):

| Sentinel          | Назначение                                                                                                 | `error_kind` в трейсе |
| ----------------- | ---------------------------------------------------------------------------------------------------------- | --------------------- |
| `ErrToolNotFound` | Задуман под неизвестный инструмент. Сейчас неизвестный инструмент возвращается модели результатом-ошибкой. | `tool_not_found`      |
| `ErrTool`         | Задуман под падение внутри инструмента. Сейчас такие сбои возвращаются модели как результат.               | `tool`                |
| `ErrNotFound`     | Документ или индекс не найдены; нужен приложению и адаптерам.                                              | `not_found`           |

> [!NOTE]
> Ошибка инструмента — не падение хода (I-11): `App.Chat` возвращает её модели
> как `{"error": "..."}` и продолжает цикл. Фатальны отказ авторизации (RBAC и
> эгресс-политика), ошибки провайдера, отмена/дедлайн контекста, отсутствие
> требуемого подтверждения (`ErrApprovalRequired`) и исчерпание одного ретрая на
> невалидные аргументы (BR-23). По той же причине reject на
> `tool_result` превращается в результат-ошибку, а reject при `Ingest` — в
> карантинный чанк: `ErrGuardReject` наружу отдаёт только rail `input`.

## GuardRejectError и tripwire

Отказ guard несут значения `*kit.GuardRejectError` — это алиас типа из
`internal/call`, так что `errors.As` доступен и внешним сервисам:

```go
var reject *kit.GuardRejectError
// reject.Rail     guard.Rail
// reject.Tripwire *guard.Tripwire  // Reason, RuleID, Metadata
// reject.Version  string           // версия guard (I-08)
// reject.Kind, reject.Name — внутренние типы, нужны только для диагностики.
```

`Unwrap` возвращает `core.ErrGuardReject`, поэтому `errors.Is(err, kit.ErrGuardReject)`
остаётся верным. `Tripwire` объясняет решение (`Reason`, `RuleID`, `Metadata`), а
`Version` — версия guard.

> [!WARNING]
> На **reject** ключи `tripwire_rule`/`tripwire_reason` в метаданных observation
> могут отсутствовать: `internal/call` возвращает `GuardRejectError` до блока,
> который их пишет. Надёжные носители — сам `Tripwire` в ошибке (`errors.As`) и
> поля observation `guard_rail`, `guard_action`, `guard_categories`.

## Матчинг

```go
if errors.Is(err, kit.ErrGuardReject) {
    // отклонил guard
}

var reject *kit.GuardRejectError
if errors.As(err, &reject) {
    // reject.Rail, reject.Tripwire, reject.Version
}
```

Каждая операция оборачивает причину, не теряя её. `errors.Is`/`errors.As` проходят
по всей цепочке, поэтому проверка работает и для ошибок, обёрнутых адаптером
приложения.

## Маппинг в HTTP

Пример перевода sentinel-ошибок в статусы (адаптер приложения):

| Ошибка kit                         | HTTP              |
| ---------------------------------- | ----------------- |
| `ErrInvalidArgs`, `ErrGuardReject` | 400               |
| `ErrUnauthorized`                  | 401               |
| `ErrApprovalRequired`              | 403               |
| `ErrNotFound`                      | 404               |
| `ErrBudget`                        | 402               |
| `ErrMaxSteps`                      | 422               |
| `ErrRateLimited`                   | 429               |
| `ErrProvider`                      | 502               |
| `ErrGuardUnavailable`              | 503               |
| `ErrInvalidConfig`                 | падение на старте |

Ответы не содержат внутренних деталей: тела ошибок ограничены статусом и общим сообщением, без текста сработавшего правила и деталей политики.

## Смотрите также

- [Справочник kit](./kit.md) — где возвращается каждая ошибка
- [Обработка ошибок (how-to)](../guides/errors.md) — таксономия, ретраи и маппинг в HTTP
- [policy.yaml](./policy.md) — `ErrUnauthorized` и `ErrInvalidConfig`
- [Наблюдаемость](../concepts/observability.md) — `error_kind` в observations
- [Chat](../concepts/chat.md) — guard входа и выхода в цикле
