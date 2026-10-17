# Ориентиры

Ориентиры для архитектуры, эргономики API, оформления кода и документации. Это
**не зависимости** `go.mod` (там четыре прямых зависимости сборки по умолчанию:
`invopop/jsonschema`, `santhosh-tekuri/jsonschema/v6`, `gopkg.in/yaml.v3`,
`golang.org/x/text`), а проекты, с которых снимаются идеи и подходы. Список
только по Go; таксономии guard ([NeMo Guardrails](https://github.com/NVIDIA-NeMo/Guardrails)) и модель трейсов ([Langfuse](https://langfuse.com/)) уже
зафиксированы в [Решениях](./decisions.md) и [Бизнес-правилах](./business-rules.md)
и здесь не дублируются.

## Читать первыми

| Проект                                                                                    | Что берём                                                                                                                                 | Куда в `kit`                                                                                 |
| ----------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| **gocloud.dev** ([google/go-cloud](https://github.com/google/go-cloud))                   | «Portable API»: узкий интерфейс-контракт, провайдера инъектирует пользователь, ничего не протекает в ядро. Отдельный `design.md` до кода. | `store.ES` (D10), `guard.Detector` (D6), адаптеры `model`/`embed` (D13)                      |
| **stdlib: `database/sql`, `net/http`, `io`**                                              | Ядро определяет интерфейс, реализацию даёт вызывающий; опции через функциональные опции; никакого глобального состояния и `init()`.       | `internal/call`, `kit.New`/`Config`, правила зависимостей ([архитектура](./architecture.md)) |
| **[open-telemetry/opentelemetry-go](https://github.com/open-telemetry/opentelemetry-go)** | Модель span и контекста, батч-экспортёр, асинхронная очередь, «трейсинг не ломает запрос».                                                | `trace.Writer` (BR-42, BR-44), `internal/call` observations                                  |
| **[uber-go/fx](https://github.com/uber-go/fx)**                                           | Управление жизненным циклом через конструкторы и явный `Close`.                                                                           | `kit.New` / `App.Setup` / `App.Close`                                                        |
| **[aws/aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2)**                             | Отдельный клиент на сервис, стеки middleware, функциональные опции запроса.                                                               | Форма адаптеров `model`, различие провайдеров изолировано                                    |

## Остальное по категориям

| Проект                                                                                                                                                                                       | Что берём                                                                       | Куда в `kit`                                                                                     |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| [go-kit/kit](https://github.com/go-kit/kit)                                                                                                                                                  | Слои `transport → endpoint → service`, один примитив вокруг бизнес-операции.    | Идея `internal/call` (D1)                                                                        |
| [spf13/cobra](https://github.com/spf13/cobra)                                                                                                                                                | Структура CLI: команды, флаги, справка.                                         | `cmd/kitctl` (`model pull`, `policy check`, `verify …`)                                          |
| [caddyserver/caddy](https://github.com/caddyserver/caddy)                                                                                                                                    | Модульность, конфиг как данные, документация и архитектурные заметки.           | Формат [архитектуры](./architecture.md) / [справочника](../reference/kit.md)                     |
| [yelp/detour](https://github.com/yelp/detour) / [stretchr/testify](https://github.com/stretchr/testify)                                                                                      | Эргономика тестов.                                                              | `model`/`embed` тесты на `httptest` (в `kit` — только stdlib)                                    |
| [langchaingo](https://github.com/tmc/langchaingo)                                                                                                                                            | Каталог провайдеров; **антипример** лишних абстракций.                          | Что не копировать в API `kit`                                                                    |
| [Eino](https://github.com/cloudwego/eino)                                                                                                                                                    | Граф-оркестрация и work с tools; брать форму, не объём.                         | Границы v1: без граф-оркестрации и мультиагентности; типизированный последовательный `flow` есть |
| [galdor](https://github.com/YasserCR/galdor)                                                                                                                                                 | Tools, типизированные `In`/`Out`, трейсинг.                                     | `kit.Tool[In, Out]` (BR-15)                                                                      |
| [hugot](https://github.com/knights-analytics/hugot)                                                                                                                                          | ONNX-пайплайны и embedding-API в Go; батчинг.                                   | `embed` (D8), `internal/ort` (D7)                                                                |
| [yalue/onnxruntime_go](https://github.com/yalue/onnxruntime_go)                                                                                                                              | Пул сессий ORT, управление временем жизни.                                      | `internal/ort.Pool` (BR-29, BR-46)                                                               |
| [daulet/tokenizers](https://github.com/daulet/tokenizers)                                                                                                                                    | Токенайзеры HF с offsets.                                                       | `guard/gliner` и `embed` в ORT-сборке (D7, A4)                                                   |
| [philippgille/chromem-go](https://github.com/philippgille/chromem-go)                                                                                                                        | Интерфейсы векторного поиска и chunk/RAG API в чистом Go.                       | Форма `store` и `chunk`                                                                          |
| [elastic/go-elasticsearch](https://github.com/elastic/go-elasticsearch)                                                                                                                      | Формат `_msearch`, `knn`, `_bulk`.                                              | `store/search.go`, `store/ensure.go` (D10)                                                       |
| [go-chi/chi](https://github.com/go-chi/chi) · stdlib `net/http`                                                                                                                              | Минимальные роутеры без фреймворка.                                             | `demo`                                                                                           |
| [golangci/golangci-lint](https://github.com/golangci/golangci-lint) (+ `depguard`) · [mvdan/gofumpt](https://github.com/mvdan/gofumpt) · [securego/gosec](https://github.com/securego/gosec) | Линт, форматирование, безопасность (подключено: CI-джоба `lint`, `make check`). | Проверка правил зависимостей ([архитектура](./architecture.md))                                  |

## Эталоны документации

| Источник                                                                              | Что берём                                                        |
| ------------------------------------------------------------------------------------- | ---------------------------------------------------------------- |
| Google Go Style Guide, Uber Go Style Guide, Effective Go, Go Code Review Comments     | оформление кода                                                  |
| [golang-standards/project-layout](https://github.com/golang-standards/project-layout) | раскладка `cmd/`, `internal/`, `examples/`                       |
| Kubernetes `api-conventions.md`, design docs                                          | однозначные спецификации API                                     |
| `gocloud.dev` `design.md`                                                             | doc-before-code                                                  |
| ADR (Nygard)                                                                          | формат журнала решений ([Решения](./decisions.md))               |
| Diátaxis                                                                              | структура документации: обзор / how-to / reference / explanation |

## Отвергнуто сознательно

| Подход                                                                                                     | Почему                                                                                                                                                         |
| ---------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Пользовательские цепочки middleware ([LangChain](https://www.langchain.com/)/[Mastra](https://mastra.ai/)) | политику можно обойти (D1)                                                                                                                                     |
| Единый «универсальный» клиент к провайдерам                                                                | различия провайдеров утекают в ядро (D13)                                                                                                                      |
| Встроенный ES-клиент                                                                                       | зависимость от версии клиента (D10)                                                                                                                            |
| Мультиагентность, workflows                                                                                | вне границ v1; типизированный последовательный `flow` с suspend/resume есть, а граф/builder-DSL — нет (см. [`D31`](./decisions.md), [Решения](./decisions.md)) |

## Смотрите также

- [Архитектура](./architecture.md) — пакеты, рантайм, библиотеки
- [Решения и допущения](./decisions.md) — `D1`…`D84`, `A1`…`A9` и границы v1
- [План](./plan.md) — этапы и риски
- [Бизнес-правила](./business-rules.md) — `BR-01`…`BR-113`
