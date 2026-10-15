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
  kit.go principal.go tool.go errors.go     фасад: New, Setup, Chat, Ingest, Search, Delete, Score, Generate[T], Tool[In,Out]
  internal/
    core/                                    общий лист: Principal + sentinel-ошибки (без зависимостей)
    call/                                    единый примитив вызова (authz → guard → span → fn)
    ort/                                     пул сессий инференса (реализацию сессии даёт вызывающий)
    httpjson/ oauth/                         общие листы для адаптеров провайдеров
    id/                                      генерация идентификаторов трейсов и observation
  authz/                                     policy.yaml, Load/Parse, Authorize(principal, permission)
  guard/                                     Rail/Detector/Pipeline; L1 — rules/, L2 — gliner/ (ONNX под тегом ort, заглушка без него)
  model/                                     Message, Client, openai.go vllm.go ollama.go gigachat.go
  embed/                                     Embedder, presets.go, remote.go, openai.go, gigachat.go, local.go
  chunk/                                     токенный чанкер + Tokenizer
  store/                                     ES interface, index.go (маппинги), chunks.go, search.go, ensure.go
  trace/                                     модель Langfuse, async writer (writer.go)
  cmd/kitctl/                                model pull, policy check
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
2. Пакеты `authz`, `guard`, `model`, `embed`, `chunk`, `store` не импортируют друг
   друга.
3. Исключения: `trace → store` (интерфейс ES), `guard → internal/ort`,
   `embed → internal/ort`.
4. `internal/core` — общий лист (`Principal`, sentinel-ошибки) без зависимостей; его
   импортируют все пакеты. `internal/httpjson`, `internal/oauth`, `internal/id` —
   тоже листья.
5. Глобального состояния нет; `init()` не используется; всё приходит через
   конструкторы и `context.Context`.

Правила проверяются линтером импортов в CI: нарушение ломает сборку.

### Куда что класть

| Что добавляете         | Куда                            | Через что                  |
| ---------------------- | ------------------------------- | -------------------------- |
| Новый провайдер LLM    | `model/`                        | интерфейс `model.Client`   |
| Новый эмбеддер         | `embed/`                        | интерфейс `embed.Embedder` |
| Новую guard-модель     | `guard/`                        | интерфейс `guard.Detector` |
| Новый вид запроса к ES | `store/`                        | интерфейс `store.ES`       |
| Бизнес-логику сервиса  | вне фреймворка, в вашем сервисе | —                          |

`model/` и `embed/` не знают друг о друге и о `store/`. Единственная точка, где эти
пакеты собираются вместе, — фасад `kit`.

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
