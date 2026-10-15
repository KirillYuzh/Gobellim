# 05. Референсы: Go-проекты

Ориентиры для архитектуры, эргономики API, оформления кода и документации. Это **не зависимости** `go.mod` (там ровно три прямых: `invopop/jsonschema`, `santhosh-tekuri/jsonschema/v6`, `gopkg.in/yaml.v3`), а проекты, с которых снимаются идеи и подходы. Список только по Go; таксономии guard (NeMo Guardrails) и модель трейсов (Langfuse) уже зафиксированы в `00-README.md` и `02-business-logic.md` и здесь не дублируются.

## Читать первыми

| Проект | Что берём | Куда в `kit` |
|---|---|---|
| **gocloud.dev** (`google/go-cloud`) | «Portable API»: узкий интерфейс-контракт, провайдера инъектирует пользователь, ничего не протекает в ядро. Отдельный `design.md` до кода. | `store.ES` (D10), `guard.Detector` (D6), адаптеры `model`/`embed` (D13) |
| **stdlib: `database/sql`, `net/http`, `io`** | Ядро определяет интерфейс, реализацию даёт вызывающий; опции через функциональные опции; никакого глобального состояния и `init()`. | `internal/call`, `kit.New`/`Config`, правила зависимостей (`01-architecture.md` §2) |
| **`open-telemetry/opentelemetry-go`** | Модель span и контекста, батч-экспортёр, асинхронная очередь, «трейсинг не ломает запрос». | `trace.Writer` (BR-42, BR-44), `internal/call` observations |
| **`uber-go/fx`** | Управление жизненным циклом через конструкторы и явный `Close`. | `kit.New` / `App.Setup` / `App.Close` |
| **`aws/aws-sdk-go-v2`** | Отдельный клиент на сервис, стеки middleware, функциональные опции запроса. | Форма адаптеров `model`, различие провайдеров изолировано |

## Остальное по категориям

| Проект | Что берём | Куда в `kit` |
|---|---|---|
| `go-kit/kit` | Слои `transport → endpoint → service`, один примитив вокруг бизнес-операции. | Идея `internal/call` (D1) |
| `spf13/cobra` | Структура CLI: команды, флаги, справка. | `cmd/kitctl` (`model pull`, `policy check`) |
| `caddyserver/caddy` | Модульность, конфиг как данные, документация и архитектурные заметки. | Формат `01-architecture.md` / `03-api-and-config.md` |
| `yelp/detour` / `stretchr/testify` | Эргономика тестов. | `model`/`embed` тесты на `httptest` (в `kit` — только stdlib) |
| `tmc/langchaingo` | Каталог провайдеров; **антипример** лишних абстракций. | Что не копировать в API `kit` |
| `cloudwego/eino` | Граф-оркестрация и work с tools; брать форму, не объём. | Границы v1: без workflows и мультиагентности |
| `YasserCR/galdor` | Tools, типизированные `In`/`Out`, трейсинг. | `kit.Tool[In, Out]` (BR-15) |
| `knights-analytics/hugot` | ONNX-пайплайны и embedding-API в Go; батчинг. | `embed` (D8), `internal/ort` (D7) |
| `yalue/onnxruntime_go` | Пул сессий ORT, управление временем жизни. | `internal/ort.Pool` (BR-29, BR-46) |
| `daulet/tokenizers` | Токенайзеры HF с offsets. | `chunk.Tokenizer` в ORT-сборке (A4) |
| `philippgille/chromem-go` | Интерфейсы векторного поиска и chunk/RAG API в чистом Go. | Форма `store` и `chunk` |
| `elastic/go-elasticsearch` | Формат `_msearch`, `knn`, `_bulk`. | `store/search.go`, `store/ensure.go` (D10) |
| `go-chi/chi` · stdlib `net/http` | Минимальные роутеры без фреймворка. | `demo` |
| `golangci/golangci-lint` (+ `depguard`) · `mvdan/gofumpt` · `securego/gosec` | Линт, форматирование, безопасность в CI. | Проверка правил зависимостей (`01-architecture.md` §2) |

## Эталоны документации

| Источник | Что берём |
|---|---|
| Google Go Style Guide, Uber Go Style Guide, Effective Go, Go Code Review Comments | оформление кода |
| `golang-standards/project-layout` | раскладка `cmd/`, `internal/`, `examples/` |
| Kubernetes `api-conventions.md`, design docs | однозначные спецификации API |
| `gocloud.dev` `design.md` | doc-before-code |
| ADR (Nygard) | формат журнала решений (`00-README.md` §4) |
| Diátaxis | структура документации: обзор / how-to / reference / explanation |

## Отвергнуто сознательно

| Подход | Почему |
|---|---|
| Пользовательские цепочки middleware (LangChain/Mastra) | политику можно обойти (D1) |
| Единый «универсальный» клиент к провайдерам | различия провайдеров утекают в ядро (D13) |
| Встроенный ES-клиент | зависимость от версии клиента (D10) |
| Мультиагентность, workflows, approval-флоу | вне границ v1 (`00-README.md` §3) |
