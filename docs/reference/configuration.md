# Конфигурация

Конфигурация приложения — один `kit.Config`, который передаётся в `kit.New`. Значения по умолчанию применяются при сборке `App`, если поле оставлено нулевым.

## kit.Config

| Поле              | Тип               | По умолчанию                      | Назначение                                                     |
| ----------------- | ----------------- | --------------------------------- | -------------------------------------------------------------- |
| `ES`              | `store.ES`        | — (обязательно)                   | Клиент Elasticsearch пишет пользователь.                       |
| `LLM`             | `model.Client`    | — (обязательно)                   | Клиент чата.                                                   |
| `Embedder`        | `embed.Embedder`  | `nil`                             | Нужен только для `Ingest` и `Search`.                          |
| `Policy`          | `*authz.Policy`   | — (обязательно)                   | Разобранный `policy.yaml`.                                     |
| `GuardL2`         | `guard.Detector`  | `nil`                             | ML-слой guard; при `nil` остаётся только детерминированный L1. |
| `GuardMaxChars`   | `int`             | `8000` (`guard.DefaultMaxChars`)  | Лимит текста для guard.                                        |
| `GuardCacheSize`  | `int`             | `4096` (`guard.DefaultCacheSize`) | Размер LRU-кэша вердиктов guard.                               |
| `Tokenizer`       | `chunk.Tokenizer` | `chunk.WhitespaceTokenizer{}`     | Токенайзер чанкера (в ORT-сборке — из `internal/ort`).         |
| `ChunkSize`       | `int`             | `400` (`chunk.DefaultSize`)       | Токенов в чанке.                                               |
| `ChunkOverlap`    | `int`             | `80` (`chunk.DefaultOverlap`)     | Перекрытие чанков, `< ChunkSize`.                              |
| `MaxSteps`        | `int`             | `8` (`DefaultMaxSteps`)           | Лимит цикла «модель → инструменты → модель».                   |
| `MaxCallsPerTool` | `int`             | `3` (`DefaultMaxCallsPerTool`)    | Лимит вызовов одного инструмента.                              |
| `ToolTimeout`     | `time.Duration`   | `30s` (`DefaultToolTimeout`)      | Таймаут одного вызова инструмента.                             |
| `ArgsMaxBytes`    | `int`             | `32768` (`DefaultArgsMaxBytes`)   | Лимит размера аргументов инструмента.                          |
| `TopK`            | `int`             | `10` (`DefaultTopK`)              | Хитов поиска по умолчанию.                                     |
| `TraceStoreRaw`   | `bool`            | `false`                           | Писать в трейсы необработанный текст вместо маскированного.    |
| `Logger`          | `*slog.Logger`    | `slog.Default()`                  | Логи; тексты сообщений в них не попадают.                      |

`DefaultMaxSteps`, `DefaultMaxCallsPerTool`, `DefaultToolTimeout`, `DefaultArgsMaxBytes` и `DefaultTopK` — константы пакета `kit`. `GuardMaxChars`, `GuardCacheSize`, `ChunkSize` и `ChunkOverlap` добираются конструкторами `guard.New` и `chunk.New` из констант `guard.*` и `chunk.*`.

Слой L2 (`GuardL2`) конструируется отдельно — `gliner.New(gliner.Config{...})` в ORT-сборке. `Dir` по умолчанию берётся из `KIT_MODELS_DIR`, затем `./models`; `Model` — `hivetrace__gliner-guard-omni`. Метки, пороги, `prefix_ids` и позиции маркеров детектор читает из `<dir>/<model>/config.json`, а не из Go-конфига.

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

| Ключ                                 | Значение                                                                                                 | Где применяется        |
| ------------------------------------ | -------------------------------------------------------------------------------------------------------- | ---------------------- |
| `ort.sessions_per_model`             | `2`                                                                                                      | пулы ORT guard/embed   |
| `ort.intra_op_threads`               | `NumCPU / (число сессий всех пулов)`, минимум `1`                                                        | ORT-сессии             |
| `ort.queue_wait`                     | `2s`                                                                                                     | ожидание сессии ORT    |
| `ort.quantization`                   | `fp32`                                                                                                   | модели guard/embed     |
| `guard.max_chars`                    | `8000`                                                                                                   | `guard.Pipeline`       |
| `guard.fail_closed`                  | `true`                                                                                                   | `guard.Pipeline`       |
| `guard.verdict_cache`                | LRU на `4096`                                                                                            | `guard.Pipeline`       |
| `guard.l2.dir`                       | `KIT_MODELS_DIR` или `./models`                                                                          | `gliner.Config.Dir`    |
| `guard.l2.model`                     | `hivetrace__gliner-guard-omni`                                                                           | `gliner.Config.Model`  |
| `guard.l2.sessions`                  | `2` (`gliner.DefaultSessions`)                                                                           | пул ORT L2             |
| `guard.l2.wait`                      | `2s` (`ort.DefaultWait`)                                                                                 | ожидание сессии L2     |
| `guard.l2.thresholds`                | из `<model>/config.json` (`0.4` на метку, общий `0.5`)                                                   | декодирование спанов   |
| `chat.max_steps`                     | `8`                                                                                                      | `App.Chat`             |
| `chat.max_calls_per_tool`            | `3`                                                                                                      | исполнение инструмента |
| `tools.args_max_bytes`               | `32768`                                                                                                  | исполнение инструмента |
| `tools.schema.additional_properties` | `false`                                                                                                  | схема инструмента      |
| `tools.timeout`                      | `30s`                                                                                                    | вызов инструмента      |
| `tools.retries_on_invalid_args`      | `1`                                                                                                      | исполнение инструмента |
| `embed.max_len`                      | `512`                                                                                                    | локальный энкодер      |
| `embed.batch_size`                   | `32`                                                                                                     | локальный энкодер      |
| `embed.dir`                          | `./models`                                                                                               | каталог весов          |
| `chunk.size` / `chunk.overlap`       | `400` / `80`                                                                                             | чанкер                 |
| `search.rrf`                         | считается в Go по `_msearch`                                                                             | `App.Search`           |
| `search.top_k`                       | `10`                                                                                                     | `App.Search`           |
| `authz.policy`                       | `policy.yaml`, режим `KnownFields(true)`                                                                 | `authz`                |
| `es.version`                         | `≥ 8.14`, basic-лицензия                                                                                 | Elasticsearch          |
| `trace.store_raw` (`TraceStoreRaw`)  | `false`                                                                                                  | trace-писатель         |
| `trace.queue`                        | `4096` (`trace.DefaultQueueSize`)                                                                        | trace-писатель         |
| `trace.batch`                        | `500` (`trace.DefaultBatchSize`), или `2s` (`DefaultFlushInterval`), или `5 MB` (`DefaultMaxBatchBytes`) | trace-писатель         |
| `trace.retries`                      | `3` (`trace.DefaultMaxRetries`), backoff `200ms` (`DefaultRetryBackoff`)                                 | trace-писатель         |
| `trace.ilm`                          | `30` дней (`store.DefaultILMDays`)                                                                       | ILM-политика           |

## Смотрите также

- [Справочник kit](./kit.md) — `kit.New` и жизненный цикл
- [Провайдеры](./providers.md) — конфиги `model` и `embed`
- [Устройство](../internals/architecture.md) — рантайм, ORT и сборка
- [Бизнес-правила](../internals/business-rules.md) — источники лимитов `BR-xx`
