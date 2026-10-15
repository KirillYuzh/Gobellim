# 03. API и конфигурация

Источники: `00-README.md` (§3 границы, §4 журнал решений), `01-architecture.md` (§2 пакеты и зависимости, §3 библиотеки, §4 рантайм, §5 безопасность, §6 observability), `02-business-logic.md` (BR-xx). Этот документ — единственная точка истины для сигнатур Go, формата `policy.yaml`, HTTP-контракта и маппингов Elasticsearch.

---

## 1. Go API (фасад `kit`)

Пакет модуля: `github.com/gobellim/kit` (финальное имя выбирает команда, см. `00-README.md` §0). Всё, что делает фасад, идёт через единый примитив `internal/call` (`authz → guard → span → fn`), поэтому пользователь не пишет middleware и не может вызвать операцию мимо политики (D1, BR-01).

### 1.1 Сборка и жизненный цикл

| Символ          | Сигнатура                                                         | Назначение                                                                                   |
| --------------- | ----------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `kit.New`       | `func New(ctx context.Context, cfg kit.Config) (*kit.App, error)` | Проверяет конфигурацию, собирает guard-конвейер и trace-писатель. Сетевых вызовов не делает. |
| `kit.App.Setup` | `func (a *App) Setup(ctx context.Context) error`                  | Создаёт ILM-политику, шаблоны data stream и индекс чанков (BR-38, BR-43). Идемпотентно.      |
| `kit.App.Close` | `func (a *App) Close(ctx context.Context) error`                  | Сбрасывает очередь trace-писателя (BR-44).                                                   |

### 1.2 `kit.Config`

| Поле              | Тип               | По умолчанию                  | Назначение                                                                |
| ----------------- | ----------------- | ----------------------------- | ------------------------------------------------------------------------- |
| `ES`              | `store.ES`        | — (обязательно)               | Клиент Elasticsearch пишет пользователь (D10).                            |
| `LLM`             | `model.Client`    | — (обязательно)               | Клиент чата.                                                              |
| `Embedder`        | `embed.Embedder`  | `nil`                         | Нужен только для `Ingest` и `Search`; чат-приложение может не задавать.   |
| `Policy`          | `*authz.Policy`   | — (обязательно)               | Разобранный `policy.yaml`.                                                |
| `GuardL2`         | `guard.Detector`  | `nil`                         | ML-слой guard; при `nil` остаётся только детерминированный L1.            |
| `GuardMaxChars`   | `int`             | `8000`                        | Лимит текста для guard (BR-28).                                           |
| `GuardCacheSize`  | `int`             | `4096`                        | Размер LRU-кэша вердиктов (BR-33).                                        |
| `Tokenizer`       | `chunk.Tokenizer` | `chunk.WhitespaceTokenizer{}` | Токенайзер чанкера (в ORT-сборке — из `internal/ort`).                    |
| `ChunkSize`       | `int`             | `400`                         | Токенов в чанке (BR-36).                                                  |
| `ChunkOverlap`    | `int`             | `80`                          | Перекрытие чанков, `< ChunkSize` (BR-36).                                 |
| `MaxSteps`        | `int`             | `8`                           | Лимит цикла «модель → tools → модель» (BR-20).                            |
| `MaxCallsPerTool` | `int`             | `3`                           | Лимит вызовов одного tool (BR-21).                                        |
| `ToolTimeout`     | `time.Duration`   | `30s`                         | Таймаут одного вызова tool (BR-22).                                       |
| `ArgsMaxBytes`    | `int`             | `32768`                       | Лимит размера аргументов (BR-19).                                         |
| `TopK`            | `int`             | `10`                          | Хитов поиска по умолчанию (BR-39).                                        |
| `TraceStoreRaw`   | `bool`            | `false`                       | Писать в трейсы необработанный текст; по умолчанию маскированный (BR-41). |
| `Logger`          | `*slog.Logger`    | `slog.Default()`              | Логи; тексты сообщений не пишутся (BR-47).                                |

### 1.3 Принципал

```go
type Principal struct {
    UserID string
    Roles  []string
    Tenant string
}

func WithPrincipal(ctx context.Context, p Principal) context.Context
func PrincipalFrom(ctx context.Context) (Principal, bool)
```

AuthN выполняет gateway (доверенные заголовки) либо сервис аутентифицирует запрос сам и кладёт принципала в контекст — как `demo` с bearer-токеном (BR-07). Без принципала операция возвращает `ErrUnauthorized`. Принципал нужен в контексте для `internal/call`, поэтому адаптеры, которые не используют kit напрямую, вызывают `kit.WithPrincipal` сами.

### 1.4 Операции

| Символ       | Сигнатура                                                               | Право    | Rejected при                 |
| ------------ | ----------------------------------------------------------------------- | -------- | ---------------------------- |
| `App.Chat`   | `func (a *App) Chat(ctx, kit.ChatRequest) (model.Message, error)`       | —        | пустой/только системный ввод |
| `App.Ingest` | `func (a *App) Ingest(ctx, kit.Doc) error`                              | `ingest` | пустой `ID`                  |
| `App.Search` | `func (a *App) Search(ctx, kit.SearchRequest) ([]kit.SearchHit, error)` | `search` | пустой `Query`               |
| `App.Delete` | `func (a *App) Delete(ctx context.Context, docID string) error`         | `delete` | —                            |
| `App.Score`  | `func (a *App) Score(ctx context.Context, s trace.Score) error`         | `trace`  | —                            |

Пользовательские типы:

```go
type ChatRequest struct {
    System      string            // серверный системный промпт
    Messages    []model.Message   // сохраняются только user/assistant (BR-30)
    SessionID   string
    Temperature *float64
    MaxTokens   int
}

type Doc struct {
    ID     string
    Text   string
    ACL    []string   // роли, которым разрешён документ (BR-10)
    Source string
}

type SearchRequest struct { Query string; TopK int }

type SearchHit struct {
    ID       string
    DocID    string   `json:"doc_id"`
    Text     string   `json:"text"`
    Position int      `json:"position"`
    Score    float64
}
```

### 1.5 Tools и structured output

```go
// Регистрация: JSON Schema выводится из In, результат Out сериализуется в JSON.
func Tool[In, Out any](app *App, name string, fn func(ctx context.Context, in In) (Out, error), opts ...ToolOption) (*ToolDef, error)

// Опции:
func Desc(s string) ToolOption   // описание для модели
func Perm(p string) ToolOption   // переопределить право (по умолчанию "tool:<name>")

// Structured output: схема выводится из T.
func Generate[T any](ctx context.Context, app *App, req GenerateRequest) (T, error)

type GenerateRequest struct {
    Prompt      string
    System      string
    MaxTokens   int
    Temperature *float64
    SessionID   string
}
```

Схема tool: `invopop/jsonschema` (`DoNotReference`, `ExpandedStruct`), затем удаляются `$schema` и `$id` и принудительно ставится `"additionalProperties": false` (BR-18). Компиляция и валидация аргументов — `santhosh-tekuri/jsonschema/v6`. Запрещённые по роли tools вычитаются из списка, который видит модель, и перепроверяются при исполнении (BR-16, BR-17).

### 1.6 Ошибки

```go
var (
    ErrInvalidConfig    // New: невалидный policy.yaml, схема tool, конфиг провайдера
    ErrUnauthorized     // нет принципала в ctx; отказ RBAC/ACL
    ErrGuardReject      // rail input/tool_result вернул reject (fast-fail)
    ErrGuardUnavailable // guard недоступен (fail-closed, BR-06)
    ErrProvider         // ошибка LLM/embed/ES-провайдера
    ErrToolNotFound     // tool неизвестен или запрещён (BR-17)
    ErrInvalidArgs      // args не прошли схему или превысили лимит (BR-18, BR-19)
    ErrMaxSteps         // исчерпан max_steps/max_calls (BR-20, BR-21)
    ErrNotFound         // документ/индекс не найдены
)
```

Матчинг — `errors.Is`.

### 1.7 Эталонный сценарий

```go
policy, _ := authz.Load("policy.yaml")
llm, _    := model.VLLM("http://vllm:8000/v1", "qwen3-32b")
ember, _  := embed.NewOpenAI(embed.OpenAIConfig{
    BaseURL: "http://vllm:8000/v1", Model: "intfloat/multilingual-e5-small", Dim: 384,
})

app, err := kit.New(ctx, kit.Config{ES: esClient, LLM: llm, Embedder: ember, Policy: policy})
if err != nil { return err }

_, err = kit.Tool(app, "get_ticket", getTicket, kit.Desc("Return a ticket by id."))

if err := app.Setup(ctx); err != nil { return err }

ctx = kit.WithPrincipal(ctx, kit.Principal{UserID: "u1", Roles: []string{"support"}, Tenant: "t1"})

_ = app.Ingest(ctx, kit.Doc{ID: "1", Text: "…", ACL: []string{"support"}})
hits, _ := app.Search(ctx, kit.SearchRequest{Query: "вопрос"})
ans, _  := app.Chat(ctx, kit.ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "…"}}})
```

---

## 2. `policy.yaml`

Строгий режим: неизвестные поля отклоняются (`KnownFields(true)`, BR-08). Поддерживается только `version: 1` и `default: deny`.

```yaml
version: 1
default: deny
roles:
  user:
    permissions:
      - tool:get_weather
  admin:
    permissions:
      - "*"
```

| Ключ                       | Значение     | Правило                                                          |
| -------------------------- | ------------ | ---------------------------------------------------------------- |
| `version`                  | `1`          | иначе `ErrInvalidConfig`                                         |
| `default`                  | `deny`       | только deny (deny-by-default, BR-02, BR-14)                      |
| `roles.<name>.permissions` | список строк | `search`, `ingest`, `delete`, `trace`, `tool:<name>`, либо `"*"` |

Проверка в CLI: `kitctl policy check ./policy.yaml`.

---

## 3. Провайдеры и эмбеддеры

### 3.1 LLM (`model`)

| Конструктор                    | Wire-формат                    | Особенности                                                                                  |
| ------------------------------ | ------------------------------ | -------------------------------------------------------------------------------------------- |
| `model.OpenAI(apiKey, model)`  | `/v1/chat/completions`         | `response_format: json_schema` для structured output                                         |
| `model.VLLM(baseURL, model)`   | тот же OpenAI-совместимый      | тонкая обёртка, без ключа (D13)                                                              |
| `model.Ollama(baseURL, model)` | native `/api/chat`             | `tools` + `format` (JSON Schema), аргументы — объект                                         |
| `model.GigaChat(cfg)`          | OAuth + `/v1/chat/completions` | `functions`/`function_call`, structured output эмулируется функцией `structured_output` (A5) |

`GigaChatConfig`: `BaseURL` (по умолчанию `https://api.giga.chat`), `AuthURL` (`https://ngw.devices.sberbank.ru:9443/api/v2/oauth`), `Credentials` (base64 `clientID:clientSecret`), `Scope` (`GIGACHAT_API_PERS`), `Model` (`GigaChat-2`), `HTTPClient` (для своего CA). OAuth-запрос обязательно несёт заголовок `RqUID` (UUIDv4), иначе шлюз отвечает `400`.

### 3.2 Эмбеддеры (`embed`)

API: `Query(ctx, texts)`, `Passages(ctx, texts)`, `Dim()`, `Model()` — без общего `Embed()` (D9, BR-34).

| Конструктор                               | Назначение                                                                                                |
| ----------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `embed.NewOpenAI(embed.OpenAIConfig)`     | любой OpenAI-совместимый `/embeddings`; `Dim` обязателен для `Setup`, если сервер не сообщает размерность |
| `embed.NewGigaChat(embed.GigaChatConfig)` | `/v1/embeddings`, тот же OAuth                                                                            |
| `embed.NewLocal(embed.LocalConfig)`       | локальный ONNX-энкодер; доступен только в сборке с тегом `ort`                                            |

Пресеты (`embed.PresetFor`): `multilingual-e5*` (mean, `query: `/`passage: `, L2-норма) и `bge-m3` (CLS, без префиксов, L2-норма). Неизвестная модель описывается `kit_embedder.json` через `embed.LoadFileConfig` (`pooling`, `query_prefix`, `passage_prefix`, `normalize`, `max_len`), иначе `NewLocal` возвращает `ErrInvalidConfig` (BR-35).

### 3.3 Переменные окружения

| Переменная       | По умолчанию | Назначение                                      |
| ---------------- | ------------ | ----------------------------------------------- |
| `KIT_MODELS_DIR` | `./models`   | Каталог моделей: `<dir>/<org>__<name>/` (BR-35) |
| `KIT_ORT_LIB`    | —            | Путь к `libonnxruntime.so` (ORT-сборка)         |

---

## 4. HTTP API демо-сервиса (`demo`)

Порт `8080/tcp`, HTTP/JSON. `demo/` — пример на Clean Architecture: mock-авторизация выдаёт bearer-токен, защищённая ручка запускает чат с tools (погода через keyless `wttr.in`, роль вызывающего). AuthN в продакшене может выполнять и gateway доверенными заголовками (BR-07).

| Метод  | Путь          | Auth   | Запрос              | Ответ                                             |
| ------ | ------------- | ------ | ------------------- | ------------------------------------------------- |
| `GET`  | `/healthz`    | —      | —                   | `{"status":"ok"}`                                 |
| `POST` | `/auth/login` | —      | `{"login":"alice"}` | `{"token","user_id","login","role","expires_at"}` |
| `GET`  | `/auth/me`    | bearer | —                   | `{"user_id","login","role","tenant"}`             |
| `POST` | `/bot/ask`    | bearer | `{"prompt":"…"}`    | `{"answer":"…"}`                                  |

Маппинг ошибок в HTTP: адаптер переводит ошибки `kit` в доменные, транспорт — в статусы.

| Доменная ошибка   | Причина                                      | HTTP |
| ----------------- | -------------------------------------------- | ---- |
| `ErrInvalidInput` | пустой prompt, невалидный JSON, reject guard | 400  |
| `ErrUnauthorized` | нет или просрочен bearer-токен, отказ RBAC   | 401  |
| `ErrNotFound`     | город не найден                              | 404  |
| `ErrUnavailable`  | модель, погода или guard недоступны          | 502  |
| прочее            | —                                            | 500  |

Тело ошибки: `{"error":"…"}` без внутренних деталей (BR-47).

---

## 5. Модель данных Elasticsearch

Клиент пишет пользователь (D10); kit готовит данные.

Индексы и потоки:

| Имя                | Тип         | Содержимое                                     |
| ------------------ | ----------- | ---------------------------------------------- |
| `kit-chunks`       | index       | чанки документов: текст, вектор, ACL           |
| `kit-traces`       | data stream | один документ на trace (D12)                   |
| `kit-observations` | data stream | spans/generations/events                       |
| `kit-traces-30d`   | ILM policy  | rollover (10gb) + delete через 30 дней (BR-43) |

### 5.1 `kit-chunks` (создаётся `store.ChunksIndexBody`)

| Поле          | Тип                                            | Назначение                                               |
| ------------- | ---------------------------------------------- | -------------------------------------------------------- |
| `doc_id`      | keyword                                        | исходный документ                                        |
| `tenant`      | keyword                                        | тенант из принципала (BR-14)                             |
| `acl`         | keyword                                        | роли, которым разрешён чанк (BR-11)                      |
| `text`        | text                                           | текст чанка (BM25)                                       |
| `vector`      | dense_vector `dims=<Dim>`, `similarity=cosine` | эмбеддинг (BR-38)                                        |
| `position`    | integer                                        | порядковый номер чанка                                   |
| `source`      | keyword                                        | источник документа                                       |
| `created_at`  | date                                           | время записи                                             |
| `quarantined` | boolean                                        | чанк, отклонённый guard; никогда не возвращается (BR-31) |

### 5.2 `kit-traces` и `kit-observations`

Trace: `@timestamp`, `trace_id`, `name` (`chat`/`ingest`/`search`/`generate`), `user_id`, `session_id`, `tenant`, `input`, `output`, `tags`, `metadata`, `usage.{prompt_tokens, completion_tokens, total_tokens, cost}`.

Observation: `@timestamp`, `trace_id`, `observation_id`, `parent_id`, `name`, `kind` (`SPAN`/`GENERATION`/`EVENT`), `level` (`DEFAULT`/`ERROR`), `error_kind` (`guard_reject`, `unauthorized`, `provider`, `invalid_args`, `tool_not_found`, `max_steps`, `not_found`), `model`, `input`, `output`, `status_message`, `start_time`, `end_time`, `latency_ms`, токены и `cost`, `metadata` (не индексируется).

Каждый вызов пишет ровно одну observation (`internal/call.Do`). Проверки guard идут через `call.Guard` и пишутся как `kind: EVENT` с именем `guard:<rail>`; отказ содержит `guard_rail`, `guard_action` и `guard_categories`. Пустые поля (`error_kind`, `guard_*`) в документ не попадают. Это то, на чём построены панели «Errors by kind», «Guard triggers by rail» и «Top tools».

Score: документ в `kit-traces` с `kind: SCORE`, `trace_id`, `name`, `score`, `comment`.

Панели дашборда (`deploy/grafana/dashboards/kit-overview.json`) читают данные streams напрямую: латентность и токены — из `kit-observations` (`kind:GENERATION`), ошибки — по `error_kind`, срабатывания guard — по `guard_action:reject` и `guard_rail`, топ tools — по `kind:SPAN` и `name`, стоимость — из `kit-traces` по `usage.cost`. Интервалы `date_histogram` заданы явно (`1h`), потому что `interval: auto` при вложенных `terms` возвращает пустые бакеты.

### 5.3 Запись

| Что                        | Запрос                                                                                 |
| -------------------------- | -------------------------------------------------------------------------------------- |
| чанки                      | `POST /_bulk` с действием `index` (`store.ChunksBulkBody`)                             |
| трейсы/observations/scores | `POST /_bulk` с действием `create` — data streams append-only (`store.BulkCreateBody`) |
| удаление документа         | `POST /kit-chunks/_delete_by_query` с фильтром `doc_id` + `tenant` принципала          |

---

## 6. Запросы поиска

`store.MSearchNDJSON` строит тело `_msearch`: два подзапроса — kNN и BM25, каждый с ACL-фильтром принципала (BR-12, BR-37). RRF считается в Go (`store.RRF`), платный retriever не нужен (D11).

```
{"index":"kit-chunks"}
{"size":10,"query":{"knn":{"field":"vector","query_vector":[…],"k":10,"num_candidates":100,
  "filter":{"bool":{"filter":[{"term":{"tenant":"t1"}},{"terms":{"acl":["support"]}},{"term":{"quarantined":false}}]}}}}}
{"index":"kit-chunks"}
{"size":10,"query":{"bool":{"must":[{"match":{"text":"вопрос"}}],
  "filter":[{"bool":{"filter":[{"term":{"tenant":"t1"}},{"terms":{"acl":["support"]}},{"term":{"quarantined":false}}]}}]}}}
```

Слияние (RRF, `rankConstant = 60`; `store.DefaultRRFRankConst`):

```
score(id) = Σ_lists 1 / (60 + rank + 1)
```

Пустой список ролей даёт `"terms":{"acl":[]}` — не совпадает ни с чем (deny-by-default, BR-13).

---

## 7. Значения по умолчанию

Сводная таблица — `02-business-logic.md` §6; имена полей `Config` — §1.2 выше.
