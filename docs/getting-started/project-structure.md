# Структура проекта

kit — библиотека из пакетов по слоям. Межпакетные зависимости заданы явно и
проверяются линтером импортов в CI, поэтому место для нового кода определяется
правилом, а не вкусом.

## Когда использовать

- Нужно понять, в какой пакет добавить код.
- Нужно проверить, не нарушает ли изменение правила зависимостей.
- Нужно найти точку расширения: провайдер LLM, эмбеддер, guard-модель, запрос к ES.

## Быстрый старт

`kit/`

```text
kit/
  kit.go principal.go tool.go errors.go     фасад: New, Setup, Chat, Ingest, Search, Delete, Score, App.Prune, Generate[T], Tool[In,Out]
  retrieval.go                              встроенный инструмент поиска kit.RetrievalTool
  internal/
    core/                                    общий лист: Principal + sentinel-ошибки (без зависимостей)
    call/                                    единый примитив вызова (authz → guard → span → fn)
    ort/                                     пул сессий инференса (реализацию сессии даёт вызывающий)
    httpjson/ oauth/                         общие листы для адаптеров провайдеров
    id/                                      генерация идентификаторов трейсов и observation
    importrules/                             stdlib-тест правил импортов (I-13); дублирует depguard из .golangci.yml
  authz/                                     policy.yaml, Load/Parse, Authorize(principal, permission)
  guard/                                     Rail/Detector/Pipeline; L1 — rules/, L2 — gliner/ (ONNX под тегом ort, заглушка без него)
  guard/guardtest/                           AllowAll — тестовый детектор (I-15)
  model/                                     Message, Client, openai.go vllm.go ollama.go gigachat.go, structurer.go
  embed/                                     Embedder, presets.go, remote.go, openai.go, gigachat.go, local.go/local_ort.go, verify.go
  chunk/                                     токенный чанкер + Tokenizer
  store/                                     ES interface, index.go (маппинги), chunks.go, search.go, filter.go, maintenance.go
  trace/                                     модель Langfuse, sink (Recorder), async writer (writer.go)
  eval/                                      детерминированные проверки поверх трейсов (зависит от trace, не от kit)
  flow/                                      один типизированный шаг `Step[In,Out]`: schema-контракт и suspend/resume (без Pipeline/builder-DSL)
  rerank/                                    реранкер `Reranker` + адаптер TEI/Cohere, детерминированный `Reorder`
  mcp/                                       клиент MCP (stdio и streamable HTTP); `Register` → `mcp:<server>:<tool>`
  webfetch/                                  SSRF-защищённый инструмент исходящего HTTP (эффект egress)
  cmd/kitctl/                                model pull, policy check, verify window/models/parity/latency
  .github/workflows/ci.yml                   CI: build, test, race, ort-vet, lint, fumpt
  .golangci.yml Makefile                     линтер и локальный `make check`
  demo/                                      пример на Clean Architecture
    cmd/bot/                                 точка входа HTTP-сервиса
    internal/domain/                         доменные типы
    internal/usecase/                        сценарии (auth, bot) и порты
    internal/config/                         конфигурация из окружения
    internal/adapter/httpapi/                роутер, хендлеры, middleware
    internal/adapter/kitengine/              порт движка чата + два инструмента
    internal/adapter/weather/                провайдер инструмента погоды
    internal/adapter/memory/                 mock Directory и bearer-токены
    internal/adapter/elastic/                клиент Elasticsearch
  deploy/                                    Dockerfile, docker-compose.yml, policy.yaml, grafana/
  docs/                                      документация
  testdata/                                  золотые файлы, parity-фикстуры
```

## Как использовать

### Правила зависимостей

1. `kit` импортирует всё. `internal/call` зависит от контрактов `authz`/`guard`/`trace`
   как от интерфейсов (`Authorizer`, `Guarder`, `Tracer`), а напрямую импортирует
   `guard` (типы rail/verdict) и `trace` (тип observation).
2. Пакеты `authz`, `guard` (включая `guard/**`), `model`, `embed`, `chunk`, `store`,
   `trace` не импортируют друг друга.
3. Исключения: `trace → store` (интерфейс ES), `guard → internal/ort`,
   `embed → internal/ort`, `eval → trace` (пакет `eval` зависит только от `trace`
   и `internal/**` и **не** импортирует `kit`).
4. `internal/core` — общий лист (`Principal`, sentinel-ошибки) без зависимостей; его
   импортируют все пакеты. `internal/httpjson`, `internal/oauth`, `internal/id`,
   `internal/ort`, `internal/importrules` — тоже листья и фасад не импортируют.
5. Глобального состояния нет; `init()` не используется; всё приходит через
   конструкторы и `context.Context`.

Правила проверяются двумя способами: stdlib-тест `internal/importrules` (в
`go test ./...`) и правила `depguard` в `.golangci.yml` (в `make check` и в
CI-джобе `lint`); нарушение ломает сборку. Ограничения навешены только на
перечисленные пакеты: фасад `kit`, `flow`, `webfetch`, `cmd/**` и `demo/**`
импортируют что угодно — `flow → internal/core` и `webfetch → kit` разрешены.

### Куда что класть

| Что добавляете         | Куда                            | Через что                          |
| ---------------------- | ------------------------------- | ---------------------------------- |
| Новый провайдер LLM    | `model/`                        | интерфейс `model.Client`           |
| Новый эмбеддер         | `embed/`                        | интерфейс `embed.Embedder`         |
| Новую guard-модель     | `guard/`                        | интерфейс `guard.Detector`         |
| Новый вид запроса к ES | `store/`                        | интерфейс `store.ES`               |
| Eval-проверки          | `eval/`                         | поверх `trace.Sink`/`Recorder`     |
| Шаги пайплайна         | `flow/`                         | типизированные `flow.Step[In,Out]` |
| Инструмент наружу      | `webfetch/`                     | `kit.EffectEgress` + SSRF-проверки |
| Бизнес-логику сервиса  | вне библиотеки, в вашем сервисе | —                                  |

`model/` и `embed/` не знают друг о друге и о `store/`. Единственная точка, где эти
пакеты собираются вместе, — фасад `kit`.

### CLI (`cmd/kitctl`)

`kitctl` не только качает модели и проверяет политику, но и запускает гейты.
Подкоманды `verify` одинаково принимают флаги до и после позиционных аргументов.

| Команда                                                                                 | Что делает                                             |
| --------------------------------------------------------------------------------------- | ------------------------------------------------------ |
| `kitctl model pull <org/name> [--dir DIR]`                                              | скачивает веса в `<dir>/<org>__<name>/`                |
| `kitctl policy check <file>`                                                            | разбирает `policy.yaml` в строгом режиме               |
| `kitctl verify window [--model-context N] [--guard-max-chars M] [--policy FILE]`        | гейт I-06: окно guard не шире контекста модели         |
| `kitctl verify models [--dir DIR] [--guard-model NAME] [--embed-model NAME]`            | структурная проверка каталогов guard и эмбеддера       |
| `kitctl verify parity [--dir DIR] [--fixtures DIR] [--min-agreement 1.0]`               | гейт паритета guard по фикстурам (требует `-tags ort`) |
| `kitctl verify latency [--dir DIR] [--iterations 200] [--warmup 20] [--max-chars 8000]` | замер латентности guard (L1 и L1+L2) и RSS             |

## Дальше

| Цель                          | Куда идти                                               |
| ----------------------------- | ------------------------------------------------------- |
| Понять единый путь вызова     | [Chat](../concepts/chat.md)                             |
| Разобрать архитектуру целиком | [Устройство: архитектура](../internals/architecture.md) |
| Прочитать бизнес-правила      | [Бизнес-правила](../internals/business-rules.md)        |
| Подключить свою модель        | [Провайдеры](../reference/providers.md)                 |

## Смотрите также

- [Устройство: архитектура](../internals/architecture.md) — контейнеры, рантайм, безопасность
- [Устройство: решения](../internals/decisions.md) — журнал решений
- [demo](../../demo/README.ru.md) — раскладка Clean Architecture
- [Документация kit](../README.md) — индекс разделов
