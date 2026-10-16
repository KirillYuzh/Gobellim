# Конфигурация

Конфигурация приложения — один `kit.Config`, который передаётся в `kit.New`. Значения по умолчанию применяются при сборке `App`, если поле оставлено нулевым.

## kit.Config

| Поле               | Тип                 | По умолчанию                           | Назначение                                                                        |
| ------------------ | ------------------- | -------------------------------------- | --------------------------------------------------------------------------------- |
| `ES`               | `store.ES`          | — (обязательно)                        | Клиент Elasticsearch пишет пользователь.                                          |
| `LLM`              | `model.Client`      | — (обязательно)                        | Клиент чата.                                                                      |
| `Pricing`          | `model.Pricing`     | нулевое (стоимость `0`)                | Цена за 1M токенов; framework считает `usage.cost` и `cost`.                      |
| `Embedder`         | `embed.Embedder`    | `nil`                                  | Нужен только для `Ingest` и `Search`.                                             |
| `Policy`           | `*authz.Policy`     | — (обязательно)                        | Разобранный `policy.yaml`.                                                        |
| `GuardL2`          | `guard.Detector`    | `nil`                                  | ML-слой guard; при `nil` остаётся только детерминированный L1.                    |
| `GuardMode`        | `kit.GuardMode`     | `kit.GuardStrict`                      | `GuardStrict` — fail-closed; `GuardDegraded` — упавший ML-слой не роняет запрос.  |
| `GuardMaxChars`    | `int`               | `8000` (`guard.DefaultMaxChars`)       | Лимит текста для guard.                                                           |
| `GuardCacheSize`   | `int`               | `4096` (`guard.DefaultCacheSize`)      | Размер LRU-кэша вердиктов guard.                                                  |
| `Tokenizer`        | `chunk.Tokenizer`   | `chunk.WhitespaceTokenizer{}`          | Токенайзер чанкера; ORT-токенайзера в сборке нет.                                 |
| `ChunkSize`        | `int`               | `400` (`chunk.DefaultSize`)            | Токенов в чанке.                                                                  |
| `ChunkOverlap`     | `int`               | `80` (`chunk.DefaultOverlap`)          | Перекрытие чанков, `< ChunkSize`.                                                 |
| `MaxSteps`         | `int`               | `8` (`DefaultMaxSteps`)                | Лимит цикла «модель → инструменты → модель».                                      |
| `MaxCallsPerTool`  | `int`               | `3` (`DefaultMaxCallsPerTool`)         | Лимит вызовов одного инструмента.                                                 |
| `ToolTimeout`      | `time.Duration`     | `30s` (`DefaultToolTimeout`)           | Таймаут одного вызова инструмента.                                                |
| `ArgsMaxBytes`     | `int`               | `32768` (`DefaultArgsMaxBytes`)        | Лимит размера аргументов инструмента.                                             |
| `TopK`             | `int`               | `10` (`DefaultTopK`)                   | Хитов поиска по умолчанию.                                                        |
| `Logger`           | `*slog.Logger`      | `slog.Default()`                       | Логи; тексты сообщений в них не попадают.                                         |
| `TraceText`        | `trace.TextPolicy`  | `trace.TextMasked`                     | Политика текста трейсов: `masked` (по умолчанию) или `full` (opt-in).             |
| `Sampler`          | `trace.Sampler`     | `nil` (значит «всегда»)                | Решение о записи трейса по `trace_id`.                                            |
| `HideInput`        | `bool`              | `false`                                | Обнулять поле `input` во всех документах.                                         |
| `HideOutput`       | `bool`              | `false`                                | Обнулять поле `output` во всех документах.                                        |
| `Sinks`            | `[]trace.Sink`      | `nil`                                  | Дополнительные приёмники документов (например, `trace.NewRecorder`).              |
| `MaxTotalTokens`   | `int`               | `0` (без лимита)                       | Бюджет токенов хода; при превышении — `ErrBudget`.                                |
| `Egress`           | `EgressPolicy`      | нулевое (правило выключено)            | Правила эгресса инструментов (`RuleOfTwo`).                                       |
| `Approver`         | `Approver`          | `nil`                                  | Кто решает по инструментам с `RequireApproval`; без него — `ErrApprovalRequired`. |
| `BeforeToolCall`   | `BeforeToolHook`    | `nil`                                  | App-level before-хук; хук инструмента заменяет его.                               |
| `AfterToolCall`    | `AfterToolHook`     | `nil`                                  | App-level after-хук; хук инструмента заменяет его.                                |
| `Spotlight`        | `kit.SpotlightMode` | `kit.SpotlightOn`                      | Помечать результаты инструментов как недоверенные данные (I-18).                  |
| `RateLimit`        | `kit.RateLimit`     | нулевое (выключено)                    | Лимит операций на тенанта; исчерпание — `ErrRateLimited`.                         |
| `ResponseCache`    | `kit.ResponseCache` | нулевое (выключено)                    | LRU+TTL кэш ответов модели без tools.                                             |
| `Reranker`         | `rerank.Reranker`   | `nil`                                  | Реранкер для `SearchRequest.Rerank`.                                              |
| `RerankCandidates` | `int`               | `0` → `TopK × DefaultRerankMultiplier` | Кандидатов брать до реранкинга.                                                   |
| `ToolSearch`       | `kit.ToolSearch`    | нулевое (выключено)                    | Лексический отбор инструментов при большом каталоге.                              |

`DefaultMaxSteps`, `DefaultMaxCallsPerTool`, `DefaultToolTimeout`, `DefaultArgsMaxBytes` и `DefaultTopK` — константы пакета `kit`. `GuardMaxChars`, `GuardCacheSize`, `ChunkSize` и `ChunkOverlap` добираются конструкторами `guard.New` и `chunk.New` из констант `guard.*` и `chunk.*`.

### Политика текста трейса

`TraceText` — явная политика (`trace.TextPolicy`):

| Значение           | Что попадает в ES                                                             |
| ------------------ | ----------------------------------------------------------------------------- |
| `trace.TextMasked` | Только текст, прошедший guard (по умолчанию, I-07).                           |
| `trace.TextFull`   | Необработанный текст; требует явного opt-in и печатает `slog.Warn` при `New`. |

### Sampling

`Sampler` — функция `func(traceID string) bool`. Готовые значения и парсинг:

| Значение                                   | Смысл                                       |
| ------------------------------------------ | ------------------------------------------- |
| `trace.SamplerAlways`                      | Писать все трейсы (по умолчанию при `nil`). |
| `trace.SamplerNever`                       | Не писать ничего.                           |
| `trace.SamplerRatio(p)`                    | Детерминированная доля `p` по `trace_id`.   |
| `"always"` / `"never"` / `"ratio:<float>"` | Синтаксис `trace.SamplerFromString`.        |

Решение детерминировано по `trace_id`, поэтому одинаково во всех процессах и
перезапусках, а trace и его observations сэмплируются вместе (D24, BR-54).

Слой L2 (`GuardL2`) конструируется отдельно — `gliner.New(gliner.Config{...})` в ORT-сборке. `Dir` по умолчанию берётся из `KIT_MODELS_DIR`, затем `./models`; `Model` — `hivetrace__gliner-guard-omni`; `Sessions` — 2, `Wait` — 2 с, `MaxTokens` — `DefaultMaxTokens` = 512 (кламп до `max_sequence_length` модели). Метки, пороги, `prefix_ids`, позиции маркеров и `category_map` детектор читает из `<dir>/<model>/config.json`, а не из Go-конфига.

## Переменные окружения

Переменные, которые читает сам фреймворк:

| Переменная       | По умолчанию                                | Назначение                                                         |
| ---------------- | ------------------------------------------- | ------------------------------------------------------------------ |
| `KIT_MODELS_DIR` | `./models`                                  | Каталог моделей: `<dir>/<org>__<name>/` (guard L2 и эмбеддер).     |
| `KIT_ORT_LIB`    | `.ort/lib/libonnxruntime.dylib` (или `.so`) | Путь к разделяемой библиотеке ONNX Runtime; читается в ORT-сборке. |

Переменные примера `demo` (не фреймворка):

| Переменная             | По умолчанию                                                                | Назначение                         |
| ---------------------- | --------------------------------------------------------------------------- | ---------------------------------- |
| `KIT_ADDR`             | `:8080`                                                                     | Адрес HTTP-сервера.                |
| `KIT_ES_URL`           | `http://localhost:9200`                                                     | URL Elasticsearch.                 |
| `KIT_POLICY`           | `demo/policy.yaml`                                                          | Путь к `policy.yaml`.              |
| `WEATHER_BASE_URL`     | `https://wttr.in`                                                           | Keyless-провайдер погоды.          |
| `GIGACHAT_CREDENTIALS` | — (обязательно)                                                             | base64 от `clientID:clientSecret`. |
| `GIGACHAT_MODEL`       | `GigaChat-2`                                                                | Имя модели.                        |
| `GIGACHAT_BASE_URL`    | — (провайдер подставит `https://api.giga.chat`)                             | База API.                          |
| `GIGACHAT_AUTH_URL`    | — (провайдер подставит `https://ngw.devices.sberbank.ru:9443/api/v2/oauth`) | OAuth-эндпоинт.                    |
| `GIGACHAT_CA_FILE`     | —                                                                           | PEM с корневым CA Сбера.           |
| `KIT_HTTP_TIMEOUT`     | `60s`                                                                       | Таймаут HTTP-клиента.              |
| `KIT_TOKEN_TTL`        | `12h`                                                                       | Время жизни bearer-токена примера. |

## Лимиты и значения по умолчанию

| Ключ                                                              | Значение                                                                                                 | Где применяется         |
| ----------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | ----------------------- |
| `ort.sessions_per_model`                                          | `2`                                                                                                      | пулы ORT guard/embed    |
| `ort.intra_op_threads`                                            | `NumCPU / (число сессий всех пулов)`, минимум `1`                                                        | ORT-сессии              |
| `ort.queue_wait`                                                  | `2s`                                                                                                     | ожидание сессии ORT     |
| `ort.quantization`                                                | `fp32`                                                                                                   | модели guard/embed      |
| `guard.max_chars`                                                 | `8000`                                                                                                   | `guard.Pipeline`        |
| `guard.mode`                                                      | `strict` (по умолчанию) или `degraded`                                                                   | `guard.Pipeline`        |
| `guard.fail_closed`                                               | `true` в `strict`; в `degraded` падение ML-слоя не фатально                                              | `guard.Pipeline`        |
| `guard.verdict_cache`                                             | LRU на `4096`                                                                                            | `guard.Pipeline`        |
| `guard.l2.dir`                                                    | `KIT_MODELS_DIR` или `./models`                                                                          | `gliner.Config.Dir`     |
| `guard.l2.model`                                                  | `hivetrace__gliner-guard-omni`                                                                           | `gliner.Config.Model`   |
| `guard.l2.sessions`                                               | `2` (`gliner.DefaultSessions`)                                                                           | пул ORT L2              |
| `guard.l2.wait`                                                   | `2s` (`ort.DefaultWait`)                                                                                 | ожидание сессии L2      |
| `guard.l2.max_tokens` (`gliner.Config.MaxTokens`)                 | `512` (`gliner.DefaultMaxTokens`), кламп до `max_sequence_length` модели (= 2048)                        | окно токенов L2         |
| `guard.l2.thresholds`                                             | из `<model>/config.json` (`0.4` на метку, общий `0.5`)                                                   | декодирование спанов    |
| `chat.max_steps`                                                  | `8`                                                                                                      | `App.Chat`              |
| `chat.max_calls_per_tool`                                         | `3`                                                                                                      | исполнение инструмента  |
| `chat.max_total_tokens` (`MaxTotalTokens`)                        | `0` (без лимита) → `ErrBudget`                                                                           | бюджет хода             |
| `pricing.input_per_mtok` / `pricing.output_per_mtok` (`Pricing`)  | `0` (стоимость не считается)                                                                             | `kit.Config.Pricing`    |
| `memory.limit` (`MemoryLimit`)                                    | `20` (`DefaultMemoryLimit`)                                                                              | `App.Chat`              |
| `memory.recall` (`MemoryRecall`)                                  | `0` (выключено; нужен `Embedder`)                                                                        | `App.Chat`              |
| `chat.spotlight` (`Spotlight`)                                    | `on` (`SpotlightOn`, по умолчанию) / `off` (`SpotlightOff`)                                              | `App.Chat`              |
| `rate_limit.per_second` / `rate_limit.burst` (`RateLimit`)        | `0` / `0` (лимит выключен); `>0` — token bucket на тенанта                                               | все операции фасада     |
| `response_cache.size` / `response_cache.ttl` (`ResponseCache`)    | `0` / `0` (кэш выключен); кэшируются только ответы без tools                                             | `App.Chat`              |
| `stream.window` / `stream.holdback`                               | `400` / `96` рун (константы пакета `kit`)                                                                | `App.ChatStream`        |
| `search.rerank` (`Reranker`, `SearchRequest.Rerank`)              | выключено; при `Rerank=true` без `Reranker` — `ErrInvalidConfig`                                         | `App.Search`            |
| `search.rerank_candidates` (`RerankCandidates`)                   | `TopK × 4` (`DefaultRerankMultiplier`)                                                                   | `App.Search`            |
| `tools.search` (`ToolSearch`)                                     | выключено; `Threshold` — 20, `TopK` — 8                                                                  | выбор инструментов      |
| `tools.args_max_bytes`                                            | `32768`                                                                                                  | исполнение инструмента  |
| `tools.schema.additional_properties`                              | `false`                                                                                                  | схема инструмента       |
| `tools.timeout`                                                   | `30s`                                                                                                    | вызов инструмента       |
| `tools.retries_on_invalid_args`                                   | `1`                                                                                                      | исполнение инструмента  |
| `tools.effects` / `tools.egress_hosts` / `tools.require_approval` | опции инструмента                                                                                        | исполнение инструмента  |
| `egress.rule_of_two` (`Egress.RuleOfTwo`)                         | `false`                                                                                                  | рантайм tools           |
| `embed.max_len`                                                   | `512`                                                                                                    | локальный энкодер       |
| `embed.batch_size`                                                | `32`                                                                                                     | локальный энкодер       |
| `embed.sessions` (`LocalConfig.Sessions`)                         | `2`                                                                                                      | пул локального энкодера |
| `embed.dir`                                                       | `./models`                                                                                               | каталог весов           |
| `chunk.size` / `chunk.overlap`                                    | `400` / `80`                                                                                             | чанкер                  |
| `chunk.tokenizer`                                                 | `WhitespaceTokenizer` (ORT-токенайзера в сборке нет)                                                     | чанкер                  |
| `search.rrf`                                                      | считается в Go по `_msearch`                                                                             | `App.Search`            |
| `search.top_k`                                                    | `10`                                                                                                     | `App.Search`            |
| `search.embed_model_scope`                                        | всегда: только чанки текущей модели эмбеддера (`embed_model`)                                            | `App.Search`            |
| `search.max_top_k` / `search.max_num_candidates`                  | `1000` / `10000`                                                                                         | клампы kNN              |
| `authz.policy`                                                    | `policy.yaml`, режим `KnownFields(true)`                                                                 | `authz`                 |
| `es.version`                                                      | `≥ 8.14`, basic-лицензия                                                                                 | Elasticsearch           |
| `trace.text` (`TraceText`)                                        | `masked` (по умолчанию) / `full` (opt-in)                                                                | trace-писатель          |
| `trace.sampler` (`Sampler`)                                       | `always` (при `nil`)                                                                                     | trace-писатель          |
| `trace.max_string_length`                                         | `131072` (`trace.DefaultMaxStringLength`)                                                                | export-фильтр           |
| `trace.max_depth`                                                 | `8` (`trace.DefaultMaxDepth`)                                                                            | export-фильтр           |
| `trace.max_array_length`                                          | `50` (`trace.DefaultMaxArrayLength`)                                                                     | export-фильтр           |
| `trace.max_object_keys`                                           | `50` (`trace.DefaultMaxObjectKeys`)                                                                      | export-фильтр           |
| `trace.queue`                                                     | `4096` (`trace.DefaultQueueSize`)                                                                        | trace-писатель          |
| `trace.batch`                                                     | `500` (`trace.DefaultBatchSize`), или `2s` (`DefaultFlushInterval`), или `5 MB` (`DefaultMaxBatchBytes`) | trace-писатель          |
| `trace.retries`                                                   | `3` (`trace.DefaultMaxRetries`), backoff `200ms` (`DefaultRetryBackoff`)                                 | trace-писатель          |
| `trace.ilm`                                                       | `30` дней (`store.DefaultILMDays`)                                                                       | ILM-политика            |

## Смотрите также

- [Справочник kit](./kit.md) — `kit.New` и жизненный цикл
- [Провайдеры](./providers.md) — конфиги `model` и `embed`
- [Производительность](../guides/performance.md) — латентность, память и ручки
- [Деплой](../guides/deployment.md) — режимы сборки, гейты и откат
- [Устройство](../internals/architecture.md) — рантайм, ORT и сборка
- [Бизнес-правила](../internals/business-rules.md) — источники лимитов `BR-xx`
