# Конфигурация

Конфигурация приложения — один `gobellim.Config`, который передаётся в `gobellim.New`. Значения по умолчанию применяются при сборке `App`, если поле оставлено нулевым.

## gobellim.Config

| Поле       | Тип              | По умолчанию            | Назначение                                                                                                              |
| ---------- | ---------------- | ----------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `ES`       | `store.ES`       | — (обязательно)         | Клиент Elasticsearch пишет пользователь.                                                                                |
| `LLM`      | `model.Client`   | — (обязательно)         | Клиент чата.                                                                                                            |
| `Pricing`  | `model.Pricing`  | нулевое (стоимость `0`) | Цена за 1M токенов; библиотека считает `usage.cost` и `cost`.                                                           |
| `Embedder` | `embed.Embedder` | `nil`                   | Нужен только для `Ingest` и `Search`.                                                                                   |
| `Policy`   | `*authz.Policy`  | — (обязательно)         | Разобранный `policy.yaml`.                                                                                              |
| `GuardL2`  | `guard.Detector` | — (обязательно)         | ML-слой guard; обязателен. L1-only запрашивается явно через `guard.L1Only()`; `nil` — `ErrInvalidConfig` (D80, BR-109). |

<!-- derived: BR-26 -->

| `GuardFailOpen` | `[]guard.Rail` | `nil` (fail-closed везде) | Рельсы, где упавший ML-слой не роняет запрос (BR-06, D73). |
| `GuardMaxChars` | `int` | `8000` (`guard.DefaultMaxChars`) | Лимит текста для guard. |
| `GuardCacheSize` | `int` | `4096` (`guard.DefaultCacheSize`) | Размер LRU-кэша вердиктов guard. |
| `GuardWindowChars` | `int` | `1024` (`guard.DefaultWindowChars`) | Окно детекции: текст длиннее идёт перекрывающимися окнами, чтобы L2 видел хвост. |
| `GuardOnViolation` | `func(guard.Verdict)` | `nil` | Колбэк на каждый вердикт, кроме allow; вердикт приходит без сырых значений (`AllowPII`). |
| `Tokenizer` | `chunk.Tokenizer` | `nil` | Токенайзер чанкера; без него `Ingest` — `ErrInvalidConfig` (BR-104). |
| `ChunkSize` | `int` | `400` (`chunk.DefaultSize`) | Единиц токенайзера в чанке; у `WhitespaceTokenizer` — слов, а не токенов. |
| `ChunkOverlap` | `int` | `80` (`chunk.DefaultOverlap`) | Перекрытие в тех же единицах, `< ChunkSize`. |
| `MaxSteps` | `int` | `8` (`DefaultMaxSteps`) | Лимит цикла «модель → инструменты → модель». |
| `MaxCallsPerTool` | `int` | `3` (`DefaultMaxCallsPerTool`) | Лимит вызовов одного инструмента. |
| `ToolTimeout` | `time.Duration` | `30s` (`DefaultToolTimeout`) | Таймаут одного вызова инструмента. |
| `ArgsMaxBytes` | `int` | `32768` (`DefaultArgsMaxBytes`) | Лимит размера аргументов инструмента. |
| `TopK` | `int` | `10` (`DefaultTopK`) | Хитов поиска по умолчанию. |
| `Logger` | `*slog.Logger` | `slog.Default()` | Логи; тексты сообщений в них не попадают. |
| `AuditLog` | `*slog.Logger` | `nil` (выключен) | Структурный аудит операций: principal, tenant, действие, результат. Отдельно от логов. |
| `TraceContent` | `trace.ContentMode` | `trace.ContentRedacted` | Что попадает в трейс: маскированный текст (по умолчанию), только метаданные или исходный текст (opt-in). |
| `TracerProvider` | `trace.Provider` | `nil` (операции не трассируются) | Провайдер, создающий спаны; адаптер OpenTelemetry — `plugins/otel`. |
| `MaxTotalTokens` | `int` | `0` (без лимита) | Бюджет токенов хода; при превышении — `ErrBudget`. |
| `Egress` | `EgressPolicy` | нулевое (Rule of Two **включён**) | Правила эгресса инструментов (`AllowEgressAfterUntrusted`). |
<!-- derived: BR-51 -->

| `Approver` | `Approver` | `nil` | Кто решает по инструментам с `Approve`; без него — `ErrApprovalRequired`. |
| `Middleware` | `[]Middleware` | `nil` | App-level middleware: оборачивает каждый вызов инструмента перед авторизацией и политикой. |
| `Spotlight` | `gobellim.SpotlightMode` | `gobellim.SpotlightOn` | Помечать результаты инструментов как недоверенные данные (I-18). |
| `RateLimit` | `gobellim.RateLimit` | нулевое (выключено) | Лимит операций на тенанта; исчерпание — `ErrRateLimited`. |
| `ResponseCache` | `gobellim.ResponseCache` | нулевое (выключено) | LRU+TTL кэш ответов модели без tools. |
| `Reranker` | `rerank.Reranker` | `nil` | Реранкер для `SearchRequest.Rerank`. |
| `RerankCandidates` | `int` | `0` → `TopK × DefaultRerankMultiplier` | Кандидатов брать до реранкинга. |
| `ToolSearch` | `gobellim.ToolSearch` | нулевое (выключено) | Лексический отбор инструментов при большом каталоге. |

`DefaultMaxSteps`, `DefaultMaxCallsPerTool`, `DefaultToolTimeout`, `DefaultArgsMaxBytes` и `DefaultTopK` — константы пакета `gobellim`. `GuardMaxChars`, `GuardCacheSize`, `ChunkSize` и `ChunkOverlap` добираются конструкторами `guard.New` и `chunk.New` из констант `guard.*` и `chunk.*`.

### Лимит операций (`RateLimit`)

```go
type RateLimit struct {
    PerSecond float64
    Burst     int
}
```

| Поле        | Тип       | Назначение                                                                       |
| ----------- | --------- | -------------------------------------------------------------------------------- |
| `PerSecond` | `float64` | Норма пополнения token bucket; `0` (нулевое значение) — лимитирование выключено. |
| `Burst`     | `int`     | Ёмкость бакета; при `PerSecond > 0` задаёт размер начального запаса.             |

При исчерпании бакета операция возвращает `ErrRateLimited` **до любого I/O** — до обращения
к провайдеру, хранилищу и guard, поэтому отказ не стоит ни одного внешнего запроса. Ключ
бакета — `Principal.Tenant`.

### Слои guard: L1 в сборке по умолчанию, L2 — плагин

Детерминированный **L1** (фразы, regex, `Canonicalize`, `DecodedVariants`) работает
всегда.

<!-- derived: BR-26 --> **L2** — ML-слой (`Config.GuardL2`) — обязателен: `gobellim.New` возвращает

`ErrInvalidConfig` при `nil` (D80, BR-109). Поставляемый ML-детектор — `guard/gliner`
(сборка с `-tags ort`); для намеренно L1-only развёртывания передают `guard.L1Only()`.
Плагин `guard-remote` не реализован — существует только контракт
`kittest.DetectorContract`. Состав слоёв отдают `Health.GuardLayers` и
`Health.GuardL1Only`.

### Политика содержимого трейса

`TraceContent` — явная политика (`trace.ContentMode`):

| Значение                | Что попадает в трейс                                                          |
| ----------------------- | ----------------------------------------------------------------------------- |
| `trace.ContentRedacted` | Только текст, прошедший guard (по умолчанию, I-07).                           |
| `trace.ContentNone`     | Только метаданные: ни входа, ни выхода, ни содержимого инструментов.          |
| `trace.ContentFull`     | Необработанный текст; требует явного opt-in и печатает `slog.Warn` при `New`. |

Политика выбирает, _что_ записывать, а не маскирует сама: маскирование делает guard,
и в трейс попадает уже замаскированный текст. В режиме `ContentFull` записывается
исходный текст. Значение вне перечисленных — `ErrInvalidConfig`.

Сэмплирование — не задача библиотеки. Библиотека создаёт спаны, а экспортёр,
батчинг и сэмплер — забота OpenTelemetry SDK, который настраивает приложение.

Слой L2 (`GuardL2`) конструируется отдельно — `gliner.New(gliner.Config{...})` в ORT-сборке. `Dir` по умолчанию берётся из `KIT_MODELS_DIR`, затем `./models`; `Model` — `hivetrace__gliner-guard-omni`; `Sessions` — 2, `Wait` — 2 с, `MaxTokens` — `DefaultMaxTokens` = 512 (кламп до `max_sequence_length` модели). Метки, пороги, `prefix_ids`, позиции маркеров и `category_map` детектор читает из `<dir>/<model>/config.json`, а не из Go-конфига.

## Переменные окружения

Переменные, которые читает сама библиотека:

| Переменная       | По умолчанию                                | Назначение                                                         |
| ---------------- | ------------------------------------------- | ------------------------------------------------------------------ |
| `KIT_MODELS_DIR` | `./models`                                  | Каталог моделей: `<dir>/<org>__<name>/` (guard L2 и эмбеддер).     |
| `KIT_ORT_LIB`    | `.ort/lib/libonnxruntime.dylib` (или `.so`) | Путь к разделяемой библиотеке ONNX Runtime; читается в ORT-сборке. |

Переменные примера `lite-demo` (не библиотеки):

| Переменная             | По умолчанию                                                                | Назначение                         |
| ---------------------- | --------------------------------------------------------------------------- | ---------------------------------- |
| `KIT_ADDR`             | `:8080`                                                                     | Адрес HTTP-сервера.                |
| `KIT_ES_URL`           | `http://localhost:9200`                                                     | URL Elasticsearch.                 |
| `KIT_POLICY`           | `lite-demo/policy.yaml`                                                     | Путь к `policy.yaml`.              |
| `WEATHER_BASE_URL`     | `https://wttr.in`                                                           | Keyless-провайдер погоды.          |
| `GIGACHAT_CREDENTIALS` | — (обязательно)                                                             | base64 от `clientID:clientSecret`. |
| `GIGACHAT_MODEL`       | `GigaChat-2`                                                                | Имя модели.                        |
| `GIGACHAT_BASE_URL`    | — (провайдер подставит `https://api.giga.chat`)                             | База API.                          |
| `GIGACHAT_AUTH_URL`    | — (провайдер подставит `https://ngw.devices.sberbank.ru:9443/api/v2/oauth`) | OAuth-эндпоинт.                    |
| `GIGACHAT_CA_FILE`     | —                                                                           | PEM с корневым CA Сбера.           |
| `KIT_HTTP_TIMEOUT`     | `60s`                                                                       | Таймаут HTTP-клиента.              |
| `KIT_TOKEN_TTL`        | `12h`                                                                       | Время жизни bearer-токена примера. |

## Лимиты и значения по умолчанию

| Ключ                                                             | Значение                                                                                             | Где применяется           |
| ---------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- | ------------------------- |
| `ort.sessions_per_model`                                         | `2`                                                                                                  | пулы ORT guard/embed      |
| `ort.intra_op_threads`                                           | `NumCPU / (число сессий всех пулов)`, минимум `1`                                                    | ORT-сессии                |
| `ort.queue_wait`                                                 | `2s`                                                                                                 | ожидание сессии ORT       |
| `ort.quantization`                                               | `fp32`                                                                                               | модели guard/embed        |
| `guard.max_chars`                                                | `8000`                                                                                               | `guard.Pipeline`          |
| `guard.mode`                                                     | `strict` (по умолчанию) или `degraded`                                                               | `guard.Pipeline`          |
| `guard.fail_closed`                                              | `true` в `strict`; в `degraded` падение ML-слоя не фатально                                          | `guard.Pipeline`          |
| `guard.verdict_cache`                                            | LRU на `4096`                                                                                        | `guard.Pipeline`          |
| `guard.l2.dir`                                                   | `KIT_MODELS_DIR` или `./models`                                                                      | `gliner.Config.Dir`       |
| `guard.l2.model`                                                 | `hivetrace__gliner-guard-omni`                                                                       | `gliner.Config.Model`     |
| `guard.l2.sessions`                                              | `2` (`gliner.DefaultSessions`)                                                                       | пул ORT L2                |
| `guard.l2.wait`                                                  | `2s` (`ort.DefaultWait`)                                                                             | ожидание сессии L2        |
| `guard.l2.max_tokens` (`gliner.Config.MaxTokens`)                | `512` (`gliner.DefaultMaxTokens`), кламп до `max_sequence_length` модели (= 2048)                    | окно токенов L2           |
| `guard.l2.thresholds`                                            | из `<model>/config.json` (`0.4` на метку, общий `0.5`)                                               | декодирование спанов      |
| `chat.max_steps`                                                 | `8`                                                                                                  | `App.Chat`                |
| `chat.max_calls_per_tool`                                        | `3`                                                                                                  | исполнение инструмента    |
| `chat.max_total_tokens` (`MaxTotalTokens`)                       | `0` (без лимита) → `ErrBudget`                                                                       | бюджет хода               |
| `pricing.input_per_mtok` / `pricing.output_per_mtok` (`Pricing`) | `0` (стоимость не считается)                                                                         | `gobellim.Config.Pricing` |
| `memory.limit` (`MemoryLimit`)                                   | `20` (`DefaultMemoryLimit`)                                                                          | `App.Chat`                |
| `memory.recall` (`MemoryRecall`)                                 | `0` (выключено; нужен `Embedder`)                                                                    | `App.Chat`                |
| `chat.spotlight` (`Spotlight`)                                   | `on` (`SpotlightOn`, по умолчанию) / `off` (`SpotlightOff`)                                          | `App.Chat`                |
| `rate_limit.per_second` / `rate_limit.burst` (`RateLimit`)       | `0` / `0` — лимит выключен; `PerSecond > 0` — token bucket на тенанта; отказ `ErrRateLimited` до I/O | все операции фасада       |

<!-- derived: BR-86 -->

| `response_cache.size` / `response_cache.ttl` (`ResponseCache`) | `0` / `0` (кэш выключен); кэшируются только ответы без tools | `App.Chat` |
| `stream.window` / `stream.holdback` | `400` / `96` рун (константы пакета `gobellim`) | `App.ChatStream` |
| `search.rerank` (`Reranker`, `SearchRequest.Rerank`) | выключено; при `Rerank=true` без `Reranker` — `ErrInvalidConfig` | `App.Search` |
| `search.rerank_candidates` (`RerankCandidates`) | `TopK × 4` (`DefaultRerankMultiplier`) | `App.Search` |
| `tools.search` (`ToolSearch`) | выключено; `Threshold` — 20, `TopK` — 8 | выбор инструментов |
| `tools.args_max_bytes` | `32768` | исполнение инструмента |
| `tools.schema.additional_properties` | `false` | схема инструмента |
| `tools.timeout` | `30s` | вызов инструмента |
| `tools.retries_on_invalid_args` | `1` | исполнение инструмента |
| `tools.effects` / `tools.egress_hosts` / `tools.require_approval` | опции инструмента | исполнение инструмента |
| `egress.allow_after_untrusted` (`Egress.AllowEgressAfterUntrusted`) | `false` — то есть Rule of Two включён по умолчанию | рантайм tools |
<!-- derived: BR-51 -->

| `embed.max_len` | `512` | локальный энкодер |
| `embed.batch_size` | `32` | локальный энкодер |
| `embed.sessions` (`LocalConfig.Sessions`) | `2` | пул локального энкодера |
| `embed.dir` | `./models` | каталог весов |
| `chunk.size` / `chunk.overlap` | `400` / `80` | чанкер |
| `chunk.tokenizer` | `WhitespaceTokenizer` (ORT-токенайзера в сборке нет) | чанкер |
| `search.rrf` | считается в Go по `_msearch` | `App.Search` |
| `search.top_k` | `10` | `App.Search` |
| `search.embed_model_scope` | всегда: только чанки текущей модели эмбеддера (`embed_model`) | `App.Search` |
| `search.max_top_k` / `search.max_num_candidates` | `1000` / `10000` | клампы kNN |
| `authz.policy` | `policy.yaml`, режим `KnownFields(true)` | `authz` |
| `es.version` | `≥ 8.14`, basic-лицензия | Elasticsearch |
| `trace.content` (`TraceContent`) | `redacted` (по умолчанию) / `none` / `full` (opt-in) | трейс операции |

## Смотрите также

- [Справочник gobellim](./gobellim.md) — `gobellim.New` и жизненный цикл
- [Провайдеры](./providers.md) — конфиги `model` и `embed`
- [Установка и сборка](../installation.md) — требования и переменные окружения
- [Защита](../guard/README.md) — слои L1/L2 и рельсы
