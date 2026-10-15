# 01. Архитектура

## 1. Контейнеры

Framework — библиотека. Ей нужен один процесс на Go; всё остальное внешнее.

| Контейнер                   | Образ / владелец                                                                 | Порты                                  | Протокол         | Роль                                                           | Состояние              |
| --------------------------- | -------------------------------------------------------------------------------- | -------------------------------------- | ---------------- | -------------------------------------------------------------- | ---------------------- |
| `demo`                      | собирает команда; builder `golang:1.24-bookworm`, runtime `debian:bookworm-slim` | 8080/tcp                               | HTTP/JSON        | чат-бот: mock-авторизация + ручка чата с tools (guard, трейсы) | нет (stateless)        |
| `elasticsearch`             | `elasticsearch:8.x`, basic-лицензия                                              | 9200/tcp (9300 только внутри кластера) | HTTP(S)/JSON     | чанки, векторы, трейсы, оценки                                 | да                     |
| `grafana`                   | `grafana/grafana`                                                                | 3000/tcp                               | HTTP             | дашборды поверх ES                                             | provisioning из файлов |
| `vllm` (внешний)            | `vllm/vllm-openai`                                                               | 8000/tcp                               | HTTP, OpenAI API | локальная LLM                                                  | веса                   |
| `ollama` (внешний)          | `ollama/ollama`                                                                  | 11434/tcp                              | HTTP             | локальная LLM                                                  | веса                   |
| OpenAI-compatible (внешний) | сторонний                                                                        | 443/tcp                                | HTTPS            | LLM и embeddings по API                                        | —                      |
| GigaChat (внешний)          | Sber                                                                             | 9443/tcp (OAuth), 443/tcp (API)        | HTTPS            | LLM и embeddings                                               | —                      |

Ресурсы `demo` (оценка): 4 vCPU, 4 GB RAM. Оценка памяти в полной сборке: guard 1.2 GB в fp32 (≈0.4 GB в int8), эмбеддер ≈0.5 GB, остальное рантайм. GPU не используется.

Сетевые правила: `demo` → ES, GigaChat или локальные LLM, внешний API погоды (исходящие). Grafana → ES. Входящий трафик только на 8080. Локальные модели загружаются заранее (`kitctl model pull`) для ORT-сборки.

## 2. Пакеты

```
kit/
  kit.go principal.go tool.go errors.go     фасад: New, Setup, Chat, Ingest, Search, Delete, Score, Generate[T], Tool[In,Out]
  internal/core/                            общий лист: Principal + sentinel-ошибки (без зависимостей)
  internal/call/                            единый примитив вызова (authz → guard → span → fn)
  internal/ort/                             пул сессий инференса (реализацию сессии даёт вызывающий)
  internal/httpjson/ internal/oauth/        общие листы для адаптеров провайдеров
  internal/id/                              генерация идентификаторов трейсов и observation
  authz/                                    policy.yaml, Load/Parse, Authorize(principal, permission)
  guard/                                    Rail/Detector/Pipeline; L1 — rules/, L2 — gliner/ (заглушка без ORT)
  model/                                    Message, Client, openai.go vllm.go ollama.go gigachat.go
  embed/                                    Embedder, presets.go, remote.go, openai.go, gigachat.go, local.go
  chunk/                                    токенный чанкер + Tokenizer
  store/                                    ES interface, index.go (маппинги), chunks.go, search.go, ensure.go
  trace/                                    модель Langfuse, async writer (writer.go)
  cmd/kitctl/                               model pull, policy check
  demo/                         пример на Clean Architecture: cmd/bot + internal/{domain,usecase,adapter}
  deploy/                                   Dockerfile, docker-compose.yml, policy.yaml, grafana/
  docs/                                     документация (00–05, README-индекс)
  testdata/                                 золотые файлы, parity-фикстуры
```

**Правила зависимостей** (проверяются линтером импортов в CI):

1. `kit` импортирует всё. `internal/call` зависит от контрактов `authz`/`guard`/`trace` как от интерфейсов (`Authorizer`, `Guarder`, `Tracer`), а напрямую импортирует `guard` (типы rail/verdict) и `trace` (тип observation).
2. Пакеты `authz`, `guard`, `model`, `embed`, `chunk`, `store` не импортируют друг друга.
3. Исключения: `trace → store` (интерфейс ES), `guard → internal/ort`, `embed → internal/ort`.
4. `internal/core` — общий лист (Principal, sentinel-ошибки) без зависимостей; его импортируют все пакеты. `internal/httpjson`, `internal/oauth`, `internal/id` — тоже листья.
5. Глобального состояния нет; `init()` не используется; всё приходит через конструкторы и `context.Context`.

## 3. Библиотеки

Версии фиксируются в `go.mod` при старте (последние стабильные, возрасту не менее 2 недель). Прямых зависимостей в сборке по умолчанию три: `gopkg.in/yaml.v3`, `github.com/invopop/jsonschema`, `github.com/santhosh-tekuri/jsonschema/v6`. Зависимости ONNX Runtime подключаются только в сборке с тегом `ort`.

| Библиотека                                 | Для чего                                                                | Примечание                                                    |
| ------------------------------------------ | ----------------------------------------------------------------------- | ------------------------------------------------------------- |
| `github.com/yalue/onnxruntime_go`          | вызов ONNX Runtime                                                      | только `-tags ort`; динамическая загрузка `libonnxruntime.so` |
| `github.com/daulet/tokenizers`             | токенайзеры HF (`tokenizer.json`), offsets                              | только `-tags ort`; требует `libtokenizers.a` при сборке      |
| `gopkg.in/yaml.v3`                         | `policy.yaml`                                                           | строгий режим `KnownFields(true)`                             |
| `github.com/invopop/jsonschema`            | JSON Schema из Go-структур tools                                        | только при регистрации                                        |
| `github.com/santhosh-tekuri/jsonschema/v6` | валидация args и structured output                                      | схемы компилируются один раз при `New`                        |
| stdlib                                     | `net/http`, `encoding/json`, `log/slog`, `context`, `unicode`, `regexp` | HTTP-клиенты провайдеров, логи                                |

ES-клиент в зависимости не входит (D10). Тестовые зависимости — только stdlib.

## 4. Рантайм и сборка

**cgo.** `CGO_ENABLED=1`. Dockerfile: стадия builder ставит `libtokenizers.a`, собирает бинарь; стадия runtime копирует бинарь и `libonnxruntime.so` (в `/usr/lib`), переменная `KIT_ORT_LIB` указывает путь. Запуск от non-root, rootfs read-only, `/models` монтируется read-only.

**ORT-сессии.** Для каждой модели (guard, embed) создаётся пул из N сессий (по умолчанию 2), по одному batch на сессию одновременно. `intra_op_threads` = `NumCPU / (число сессий всех пулов)`, минимум 1. Запрос берёт сессию из канала, при ожидании дольше 2 с возвращает `ErrGuardUnavailable` (для embed — `ErrProvider`). Это ограничивает память и защищает от DoS.

**Эмбеддер.** Поддерживаются энкодерные модели в ONNX с выходом `last_hidden_state` (mean или CLS pooling) или готовым `sentence_embedding`. Пресеты (`embed/presets.go`): семейство `intfloat/multilingual-e5-*` (mean, префиксы `query: ` / `passage: `, L2-норма), `BAAI/bge-m3` (CLS, без префиксов, L2-норма). Неизвестную модель можно описать файлом `kit_embedder.json` рядом с весами (`pooling`, `query_prefix`, `passage_prefix`, `normalize`, `max_len`); иначе `NewLocal` возвращает ошибку. Вход обрезается по `max_len` (512), батч сортируется по длине, размер батча 32.

**Каталог моделей.** `<models_dir>/<org>__<name>/` с файлами `model.onnx`, `tokenizer.json`, `config.json`. `models_dir` берётся из опции `embed.Dir`, переменной `KIT_MODELS_DIR` или `./models`.

**Quantization.** По умолчанию fp32. Int8 допускается, если parity-тесты (см. план) проходят.

## 5. Безопасность

**Границы доверия.** AuthN выполняет gateway; `demo` доверяет заголовкам `X-User-ID`, `X-Roles`, `X-Tenant` только из сети gateway (сетевая политика). Всё остальное недоверенное: тело запроса, история сообщений, документы, результаты tools, ответ модели.

| Угроза                                                     | Контроль                                                                                                                          | Слой              |
| ---------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- | ----------------- |
| Прямая инъекция в промпте                                  | guard rail `input` (L1 фразы, L2 gliner)                                                                                          | ML + правила      |
| Косвенная инъекция через документ                          | карантин чанков при ingest; guard `tool_result`                                                                                   | ML                |
| Подделка истории клиентом (`system`, `tool`, `tool_calls`) | BR-30: клиентские роли только `user` и `assistant`, `tool_calls` отбрасываются; все `user` и `assistant` сообщения проходят guard | детерминированный |
| Вызов запрещённого tool                                    | вычитание из схемы + повторная проверка при исполнении                                                                            | детерминированный |
| Некорректные или вредоносные args tool                     | JSON Schema со `additionalProperties:false`, лимит размера args 32 KB                                                             | детерминированный |
| Утечка чужих данных через поиск                            | ACL-фильтр tenant + roles внутри kNN и BM25; deny-by-default                                                                      | детерминированный |
| Утечка PII и секретов в ответе                             | guard rail `output` (mask)                                                                                                        | правила + ML      |
| PII в трейсах                                              | в ES пишется замаскированный текст; флаг `TraceStoreRaw` выключен по умолчанию                                                    | детерминированный |
| DoS через guard                                            | лимит `max_chars`, пул сессий, таймаут очереди, fail-closed                                                                       | детерминированный |
| Бесконечный цикл tools                                     | `max_steps`, `max_calls` на tool, таймаут tool                                                                                    | детерминированный |
| Утечка секретов в логах                                    | `slog` не пишет тексты сообщений; только id, размеры, категории                                                                   | дисциплина        |

Ограничение v1: маскирование PII на входе необратимо, модель видит заглушки вида `[PII:SNILS]` и не может использовать реальные значения в args tools. Это сознательный выбор простоты.

## 6. Observability

Каждый вызов оборачивается в `internal/call` и создаёт observation; trace создаётся на входе `Chat`, `Ingest`, `Search`. Модель данных совместима по смыслу с Langfuse: trace → observations (SPAN, GENERATION, EVENT) → scores, session как группировка по `session_id`. Подробности полей — `03-api-and-config.md`, раздел 5, и схема ES (страница 6).

Запись: observations накапливаются в памяти на время запроса; при завершении trace и observations уходят в очередь асинхронного писателя (очередь 4096 документов, batch 500 или 2 с или 5 MB, три повтора с backoff 200 мс). При переполнении очереди новые документы отбрасываются и увеличивается счётчик `dropped` (логируется `slog.Warn`); запрос никогда не блокируется трейсингом. `App.Close(ctx)` сбрасывает очередь.

Grafana: в `deploy/grafana/` лежат provisioning ES-datasource и дашборды: латентность и ошибки по `name`/`kind`, токены и стоимость по модели и tenant, срабатывания guard по категориям и rail, топ tools и отказы авторизации, таблица последних trace.
