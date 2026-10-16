# Справочник kit

Полный справочник фасада `github.com/gobellim/kit`: конфигурация, жизненный цикл,
операции, типы, инструменты и ошибки.

Все операции идут через единый примитив `internal/call` (`authz → guard → span →
fn`). Добавлять middleware или обходить политику не нужно и нельзя.

## Жизненный цикл

### kit.Config

Поля конфигурации. Значения по умолчанию применяются в `kit.New`, если поле
оставлено нулевым.

| Поле               | Тип                 | По умолчанию                           | Назначение                                                                         |
| ------------------ | ------------------- | -------------------------------------- | ---------------------------------------------------------------------------------- |
| `ES`               | `store.ES`          | — (обязательно)                        | Клиент Elasticsearch пишет пользователь.                                           |
| `LLM`              | `model.Client`      | — (обязательно)                        | Клиент чата.                                                                       |
| `Pricing`          | `model.Pricing`     | нулевое (стоимость `0`)                | Цена модели за 1M токенов; framework считает `usage.cost` и `cost`.                |
| `Embedder`         | `embed.Embedder`    | `nil`                                  | Нужен только для `Ingest` и `Search`; чат-приложение может не задавать.            |
| `Policy`           | `*authz.Policy`     | — (обязательно)                        | Разобранный `policy.yaml`.                                                         |
| `GuardL2`          | `guard.Detector`    | `nil`                                  | ML-слой guard; при `nil` остаётся только детерминированный L1.                     |
| `GuardMode`        | `kit.GuardMode`     | `kit.GuardStrict`                      | `GuardStrict` — fail-closed; `GuardDegraded` — упавший ML-слой не роняет запрос.   |
| `GuardMaxChars`    | `int`               | `8000` (`guard.DefaultMaxChars`)       | Лимит текста для guard.                                                            |
| `GuardCacheSize`   | `int`               | `4096` (`guard.DefaultCacheSize`)      | Размер LRU-кэша вердиктов guard.                                                   |
| `Tokenizer`        | `chunk.Tokenizer`   | `chunk.WhitespaceTokenizer{}`          | Токенайзер чанкера; ORT-токенайзера в сборке нет.                                  |
| `ChunkSize`        | `int`               | `400` (`chunk.DefaultSize`)            | Единиц токенайзера в чанке; у дефолтного `WhitespaceTokenizer` — слов.             |
| `ChunkOverlap`     | `int`               | `80` (`chunk.DefaultOverlap`)          | Перекрытие в тех же единицах, `< ChunkSize`.                                       |
| `MaxSteps`         | `int`               | `8` (`DefaultMaxSteps`)                | Лимит цикла «модель → инструменты → модель».                                       |
| `MaxCallsPerTool`  | `int`               | `3` (`DefaultMaxCallsPerTool`)         | Лимит вызовов одного инструмента.                                                  |
| `ToolTimeout`      | `time.Duration`     | `30s` (`DefaultToolTimeout`)           | Таймаут одного вызова инструмента.                                                 |
| `ArgsMaxBytes`     | `int`               | `32768` (`DefaultArgsMaxBytes`)        | Лимит размера аргументов инструмента.                                              |
| `TopK`             | `int`               | `10` (`DefaultTopK`)                   | Хитов поиска по умолчанию.                                                         |
| `MemoryLimit`      | `int`               | `20` (`DefaultMemoryLimit`)            | Последних сообщений треда в контексте (при `ChatRequest.ThreadID`).                |
| `MemoryRecall`     | `int`               | `0` (выключено)                        | Сколько сообщений подмешивать семантическим recall'ом; нужен `Embedder`.           |
| `Spotlight`        | `kit.SpotlightMode` | `kit.SpotlightOn`                      | Помечать недоверенный контент (результаты инструментов) как данные (I-18).         |
| `RateLimit`        | `kit.RateLimit`     | нулевое (лимит выключен)               | Лимит операций на тенанта (`PerSecond`, `Burst`); исчерпание — `ErrRateLimited`.   |
| `ResponseCache`    | `kit.ResponseCache` | нулевое (кэш выключен)                 | LRU+TTL кэш ответов модели **без tools** (`Size`, `TTL`).                          |
| `Reranker`         | `rerank.Reranker`   | `nil`                                  | Реранкер для `SearchRequest.Rerank`; без него `Rerank` — `ErrInvalidConfig` (D49). |
| `RerankCandidates` | `int`               | `0` → `TopK × DefaultRerankMultiplier` | Сколько кандидатов брать до реранкинга.                                            |
| `ToolSearch`       | `kit.ToolSearch`    | нулевое (выключено)                    | Лексический отбор инструментов при большом каталоге (D50).                         |
| `Logger`           | `*slog.Logger`      | `slog.Default()`                       | Логи; тексты сообщений в них не попадают.                                          |
| `TraceText`        | `trace.TextPolicy`  | `trace.TextMasked`                     | Политика текста трейсов: `masked` (по умолчанию) или `full` (opt-in).              |
| `Sampler`          | `trace.Sampler`     | `nil` (значит «всегда»)                | Решение о записи трейса по `trace_id`.                                             |
| `HideInput`        | `bool`              | `false`                                | Обнулять поле `input` во всех документах.                                          |
| `HideOutput`       | `bool`              | `false`                                | Обнулять поле `output` во всех документах.                                         |
| `Sinks`            | `[]trace.Sink`      | `nil`                                  | Дополнительные приёмники документов (например, `trace.NewRecorder`).               |
| `MaxTotalTokens`   | `int`               | `0` (без лимита)                       | Бюджет токенов хода; при превышении — `ErrBudget`.                                 |
| `Egress`           | `EgressPolicy`      | нулевое (правило выключено)            | Правила эгресса инструментов (`RuleOfTwo`).                                        |
| `Approver`         | `Approver`          | `nil`                                  | Кто решает по инструментам с `RequireApproval`; без него — `ErrApprovalRequired`.  |
| `BeforeToolCall`   | `BeforeToolHook`    | `nil`                                  | App-level before-хук; хук инструмента его заменяет.                                |
| `AfterToolCall`    | `AfterToolHook`     | `nil`                                  | App-level after-хук; хук инструмента его заменяет.                                 |

`DefaultMaxSteps`, `DefaultMaxCallsPerTool`, `DefaultToolTimeout`, `DefaultArgsMaxBytes` и `DefaultTopK` — константы пакета `kit`. `GuardMaxChars`, `GuardCacheSize`, `ChunkSize` и `ChunkOverlap` добираются конструкторами `guard.New` и `chunk.New` из констант `guard.*` и `chunk.*`.

`TraceText` в режиме `full` печатает предупреждение при `New`: в ES пойдёт необработанный текст. Значения `Sampler` разбираются из строки `always|never|ratio:<float>` функцией `trace.SamplerFromString`.

`GuardMode` управляет реакцией на недоступность ML-слоя (I-16). `GuardStrict` (по умолчанию) — fail-closed: ошибка любого слоя отклоняет операцию (`ErrGuardUnavailable`). `GuardDegraded` оставляет детерминированный L1, пропускает упавший `GuardL2` и помечает вердикт и observation флагом `guard_degraded`; при старте печатается `slog.Warn`.

`Pricing` — цена модели за миллион токенов (`model.Pricing{InputPerMTok, OutputPerMTok}`). Нулевое значение означает «цена неизвестна» и даёт `usage.cost`/`cost` = 0. Ненулевое — framework считает стоимость каждого вызова и суммирует её в trace (`Chat`, `Generate`); в observation генерации пишется цена одного вызова.

Слой L2 (`GuardL2`) конструируется отдельно — `gliner.New(gliner.Config{...})` в ORT-сборке. Поля `Dir`, `Model`, `Sessions`, `Wait`, `MaxTokens`: `Dir` по умолчанию берётся из `KIT_MODELS_DIR`, затем `./models`; `Model` — `hivetrace__gliner-guard-omni`; `Sessions` — 2; `Wait` — 2 с; `MaxTokens` — `DefaultMaxTokens` = 512 (кламп до `max_sequence_length` модели). Метки, пороги, `prefix_ids`, позиции маркеров и `category_map` детектор читает из `<dir>/<model>/config.json`, а не из Go-конфига.

Полная таблица значений с правилами — [Конфигурация](./configuration.md).

### kit.New

```go
func New(ctx context.Context, cfg Config) (*App, error)
```

Проверяет конфигурацию и собирает `App`: guard-конвейер, чанкер и trace-писатель.
Сетевых вызовов не делает. Отсутствие `ES`, `LLM` или `Policy`, невалидный
`policy.yaml` и ошибки сборки возвращаются как `ErrInvalidConfig`.

### App.Setup

```go
func (a *App) Setup(ctx context.Context) error
```

Создаёт ILM-политику, шаблоны data stream и — при известной размерности
эмбеддера — индекс чанков. Идемпотентно.

### App.Close

```go
func (a *App) Close(ctx context.Context) error
```

Закрывает очередь trace-писателя и дописывает остаток. Клиенты ES, LLM и
эмбеддера `App` не закрывает — их жизненным циклом управляет приложение.
Вызывайте при остановке сервиса.

### App.Guard, GuardVersion, PolicyHash

```go
func (a *App) Guard() *guard.Pipeline
func (a *App) GuardVersion() string
func (a *App) PolicyHash() string
```

`Guard` отдаёт сам конвейер (например, для проверки версии вне `App`).
`GuardVersion` и `PolicyHash` — те же значения, что штампуются в трейсы и
observation (I-08).

## Операции

### Chat

```go
func (a *App) Chat(ctx context.Context, req ChatRequest) (model.Message, error)
```

Гоняет guard, модель и инструменты в цикле и возвращает финальное сообщение.
Требует права `chat` (как и остальные операции, `Chat` проверяет право в
примитиве вызова; права на инструменты проверяются отдельно). При заданном
`ThreadID` подгружает историю треда и дописывает ход — нужно ещё право `memory`
(см. [Память](../concepts/memory.md)).
Ответ возвращается целиком; поток — у `ChatStream`.
На последнем шаге цикла `tool_choice` становится `none`, чтобы
модель отвечала, а не открывала вызов, который уже не исполнится.

```go
msg, err := app.Chat(ctx, kit.ChatRequest{
    System:   "Ты ассистент.",
    Messages: []model.Message{{Role: model.RoleUser, Content: "Привет"}},
})
```

### ChatStream

```go
func (a *App) ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
```

Стриминговая версия `Chat`: тот же ход (tools, RBAC, память, бюджет, трейс), но
подтверждённый текст отдаётся через канал по мере готовности. Каждое окно ответа
проходит рельс `output` (скользящее окно 400 рун, хвост 96 рун, резка по границе
предложения) **до** эмиссии; отказ рельса даёт `StreamChunk.Retract` и аборт.
Требует права `chat`. Если клиент LLM не реализует `model.Streamer`, `ChatStream`
откатывается на буферизованный `Chat` и выдаёт один чанк `Text` + `Done`.
Вызывающий обязан вычитать канал до закрытия. Остаточный риск: совпадение длиннее
хвоста без границы может пересечь окно (I-20).

```go
ch, err := app.ChatStream(ctx, kit.ChatRequest{Messages: msgs})
for c := range ch {
    if c.Err != nil { return c.Err }
    if c.Text != "" { fmt.Print(c.Text) }
    if c.Done { fmt.Println(c.Final.Content) }
}
```

### Health

```go
func (a *App) Health() Health
```

Снимок рантайма: очередь trace-писателя, режим и версия guard и каждый компонент,
реализующий `HealthChecker` (`guard_l2`, `embedder`, `es`, `llm`). `Healthy` ложен,
если хоть один компонент вернул `OK=false`; `Degraded` отражает режим guard.
Права не требует — это локальный снимок, а не чтение данных (BR-91).

```go
h := app.Health()
if !h.Healthy { log.Printf("unhealthy: %+v", h.Components) }
```

### Ingest

```go
func (a *App) Ingest(ctx context.Context, doc Doc) error
```

Чанкует документ, считает ACL и эмбеддинги, пишет чанки в Elasticsearch.
Отклонённый guard чанк пишется маркером с `quarantined: true` без текста и
вектора и не возвращается поиском. Перед записью прежние чанки документа
(`doc_id` + `tenant`) удаляются, чтобы повторная индексация не оставляла хвосты.
Требует непустой `Doc.ID`, `Embedder` и `Tenant` принципала.

### Search

```go
func (a *App) Search(ctx context.Context, req SearchRequest) ([]SearchHit, error)
```

Гибридный поиск: kNN и BM25 через `_msearch`, слияние RRF в Go, top-k. Всегда
добавляет ACL-фильтр принципала (tenant + roles) и `quarantined: false`, плюс
необязательный `req.Filter`. Поиск всегда ограничен текущей моделью эмбеддера
(`embed_model`): чанки, посчитанные другой моделью, не возвращаются (BR-79).

При `SearchRequest.Rerank` набор кандидатов расширяется до `Config.RerankCandidates`
(по умолчанию `TopK × DefaultRerankMultiplier`), проходит через `Config.Reranker` и
усекается `rerank.Reorder` до `TopK`; без `Config.Reranker` — `ErrInvalidConfig`
(BR-92).

### Delete

```go
func (a *App) Delete(ctx context.Context, docID string) error
```

Удаляет чанки документа по `doc_id` с фильтром `tenant` принципала. Проверяет
`deleted`/`failures` ответа `_delete_by_query` (частичное удаление — ошибка).

### Prune

```go
func (a *App) Prune(ctx context.Context, olderThan time.Duration) (int, error)
```

Удаляет trace и observation старше `olderThan` по `@timestamp` (ретенция) и
возвращает число удалённых документов. Требует права `prune`.

### Score

```go
func (a *App) Score(ctx context.Context, s trace.Score) error
```

Присваивает trace оценку. Требует права `trace`. Пишет документ в `kit-traces`.

### Память

```go
func (a *App) MemoryLoad(ctx context.Context, threadID string, limit int) ([]model.Message, error)
func (a *App) MemoryAppend(ctx context.Context, threadID string, msgs ...model.Message) error
func (a *App) MemoryRecall(ctx context.Context, query string, topK int) ([]MemoryHit, error)
func (a *App) MemoryClear(ctx context.Context, threadID string) (int, error)
func (a *App) MemoryWorking(ctx context.Context, threadID string) (map[string]any, error)
func (a *App) MemorySetWorking(ctx context.Context, threadID string, data map[string]any) error
```

История треда, семантический recall и working-память в индексах
`kit-memory-messages` / `kit-memory-working`. Все операции требуют права `memory`
и непустых `Tenant` и `UserID` принципала: память скоупится парой `tenant` +
`user_id`, а `threadID` даёт вызывающий и сам по себе доступа не даёт (I-17).
`MemoryLoad` возвращает последние `limit` сообщений (`0` — `Config.MemoryLimit`) в
хронологическом порядке; `MemoryAppend` сохраняет только роли `user`/`assistant` с
непустым текстом; `MemoryRecall` ищет по всем тредам resource и требует
`Embedder`; `MemoryClear` удаляет сообщения треда. Подробнее —
[Память](../concepts/memory.md).

### Generate

```go
func Generate[T any](ctx context.Context, app *App, req GenerateRequest[T]) (Generated[T], error)
```

Полноценная операция примитива: принципал, право `generate`, input/output guard,
трейс. Просит модель вернуть значение типа `T` по JSON Schema, выведенной из `T`,
и декодирует ответ в `T`. `System` уходит ролью `system`. При `SchemaNative` и
отсутствии поддержки у провайдера схема автоматически инлайнится.

```go
type Review struct {
    Approved bool   `json:"approved"`
    Notes    string `json:"notes"`
}

g, err := kit.Generate[Review](ctx, app, kit.GenerateRequest[Review]{Prompt: "Проверь заявку"})
// g.Value   — декодированное значение
// g.Raw     — сырой (уже прошедший guard) ответ
// g.UsedFallback — true, если сработала стратегия Fallback
```

## Типы

### Principal

```go
type Principal struct {
    UserID string
    Roles  []string
    Tenant string
}
```

| Поле     | Тип        | Назначение                                       |
| -------- | ---------- | ------------------------------------------------ |
| `UserID` | `string`   | Идентификатор для трейсов и сообщений об отказе. |
| `Roles`  | `[]string` | Роли для проверки RBAC.                          |
| `Tenant` | `string`   | Тенант; обязателен для ingest/search/delete.     |

### ChatRequest

```go
type ChatRequest struct {
    System      string
    Messages    []model.Message
    SessionID   string
    ThreadID    string
    Temperature *float64
    MaxTokens   int
    ToolChoice  string
    Tags        []string
}
```

| Поле          | Тип               | Назначение                                                    |
| ------------- | ----------------- | ------------------------------------------------------------- |
| `System`      | `string`          | Серверный системный промпт; клиент задать его не может.       |
| `Messages`    | `[]model.Message` | Сообщения хода; сохраняются только роли `user` и `assistant`. |
| `SessionID`   | `string`          | Идентификатор сессии для трейсов.                             |
| `ThreadID`    | `string`          | Задан — `Chat` читает и пишет память треда (право `memory`).  |
| `Temperature` | `*float64`        | Температура генерации; `nil` — значение провайдера.           |
| `MaxTokens`   | `int`             | Лимит токенов ответа; `0` — значение провайдера.              |
| `ToolChoice`  | `string`          | `""`, `auto`, `none`, `required` или имя инструмента.         |
| `Tags`        | `[]string`        | Метки, записываемые на trace.                                 |

### Doc

```go
type Doc struct {
    ID     string
    Text   string
    ACL    []string
    Source string
}
```

| Поле     | Тип        | Назначение                                         |
| -------- | ---------- | -------------------------------------------------- |
| `ID`     | `string`   | Идентификатор документа; обязателен.               |
| `Text`   | `string`   | Текст для чанкования.                              |
| `ACL`    | `[]string` | Роли, которым документ разрешён.                   |
| `Source` | `string`   | Источник документа; пишется в чанк для фильтрации. |

### SearchRequest

```go
type SearchRequest struct {
    Query  string
    TopK   int
    Filter store.Filter
    Rerank bool
}
```

| Поле     | Тип            | Назначение                                                                   |
| -------- | -------------- | ---------------------------------------------------------------------------- |
| `Query`  | `string`       | Текст запроса; пустой возвращает `ErrInvalidArgs`.                           |
| `TopK`   | `int`          | Число хитов; `0` — берётся `Config.TopK`.                                    |
| `Filter` | `store.Filter` | Фильтр метаданных (см. [Elasticsearch](./elasticsearch.md)); AND-ится с ACL. |
| `Rerank` | `bool`         | Переранжировать кандидатов через `Config.Reranker` (BR-92).                  |

### SearchHit

```go
type SearchHit struct {
    ID       string  `json:"-"`
    DocID    string  `json:"doc_id"`
    Text     string  `json:"text"`
    Position int     `json:"position"`
    Score    float64 `json:"-"`
}
```

| Поле       | Тип       | Назначение                          |
| ---------- | --------- | ----------------------------------- |
| `ID`       | `string`  | Идентификатор чанка.                |
| `DocID`    | `string`  | Исходный документ.                  |
| `Text`     | `string`  | Текст чанка.                        |
| `Position` | `int`     | Порядковый номер чанка в документе. |
| `Score`    | `float64` | Оценка после слияния RRF.           |

### MemoryHit

```go
type MemoryHit struct {
    Role      model.Role
    Content   string
    Score     float64
    CreatedAt time.Time
}
```

| Поле        | Тип          | Назначение                   |
| ----------- | ------------ | ---------------------------- |
| `Role`      | `model.Role` | Роль сообщения.              |
| `Content`   | `string`     | Текст (уже прошедший guard). |
| `Score`     | `float64`    | Оценка сходства при recall.  |
| `CreatedAt` | `time.Time`  | Время записи сообщения.      |

### GenerateRequest

```go
type GenerateRequest[T any] struct {
    Prompt           string
    System           string
    MaxTokens        int
    Temperature      *float64
    SessionID        string
    SchemaMode       model.SchemaMode
    ErrorStrategy    ErrorStrategy
    Fallback         *T
    Tags             []string
    StructuringModel model.Client // опциональный второй вызов
}
```

| Поле               | Тип                | Назначение                                                              |
| ------------------ | ------------------ | ----------------------------------------------------------------------- |
| `Prompt`           | `string`           | Запрос к модели.                                                        |
| `System`           | `string`           | Системный промпт.                                                       |
| `MaxTokens`        | `int`              | Лимит токенов ответа.                                                   |
| `Temperature`      | `*float64`         | Температура генерации.                                                  |
| `SessionID`        | `string`           | Идентификатор сессии для трейсов.                                       |
| `SchemaMode`       | `model.SchemaMode` | Как передать схему; `SchemaNative` без поддержки провайдера инлайнится. |
| `ErrorStrategy`    | `ErrorStrategy`    | Что делать, если ответ не декодируется в `T`.                           |
| `Fallback`         | `*T`               | Значение по умолчанию для стратегии `Fallback`; обязательно для неё.    |
| `Tags`             | `[]string`         | Метки на trace.                                                         |
| `StructuringModel` | `model.Client`     | Второй вызов при неудачном прямом декодировании (см. ниже).             |

Если задан `StructuringModel`, то при неполадке прямого декодирования в `T` (и до применения `ErrorStrategy`) kit делает одну попытку через `model.Structurer`: просит модель переписать прошлый ответ по той же схеме и декодирует результат. Вызов идёт через `internal/call`, пишется **отдельной** observation (с `metadata.structuring = true`) и **не** добавляет usage к итогу хода.

### Generated

```go
type Generated[T any] struct {
    Value        T
    Raw          string
    UsedFallback bool
    FinishReason model.FinishReason
}
```

| Поле           | Тип                  | Назначение                                  |
| -------------- | -------------------- | ------------------------------------------- |
| `Value`        | `T`                  | Декодированное значение.                    |
| `Raw`          | `string`             | Сырой ответ (уже прошедший output guard).   |
| `UsedFallback` | `bool`               | `true`, если вернулось значение `Fallback`. |
| `FinishReason` | `model.FinishReason` | Причина остановки модели.                   |

### ErrorStrategy

```go
type ErrorStrategy int

const (
    Strict   ErrorStrategy = iota // ошибка (по умолчанию)
    Warn                          // предупреждение в лог, `Raw` возвращается
    Fallback                      // вернуть `Fallback` и пометить `UsedFallback`
)
```

### StreamChunk

```go
type StreamChunk struct {
    Text    string        // подтверждённый, уже проверенный текст
    Retract bool          // отбросить показанное; поток завершится ошибкой
    Done    bool          // последний чанк, с собранным Final
    Final   model.Message
    Err     error
}
```

На чанк имеет смысл ровно одно из полей. `Done.Final.Content` равен конкатенации
всех выданных `Text` (I-19).

### SpotlightMode, RateLimit, ResponseCache

```go
type SpotlightMode int
const (
    SpotlightOn  SpotlightMode = iota // по умолчанию
    SpotlightOff
)

type RateLimit struct {
    PerSecond float64
    Burst     int
}

type ResponseCache struct {
    Size int
    TTL  time.Duration
}

type ToolSearch struct {
    Enabled   bool
    Threshold int // 0 → DefaultToolSearchThreshold
    TopK      int // 0 → DefaultToolSearchTopK
}
```

### Health, ComponentHealth

```go
type HealthStatus = core.HealthStatus   // {OK, Detail, InUse, Capacity}
type HealthChecker = core.HealthChecker // Health() HealthStatus

type Health struct {
    Healthy       bool
    Degraded      bool
    GuardMode     GuardMode
    GuardVersion  string
    TraceQueue    int
    TraceQueueCap int
    TraceDropped  int64
    Components    []ComponentHealth
}

type ComponentHealth struct {
    Name     string
    OK       bool
    Error    string
    InUse    int
    Capacity int
}
```

## Принципал в контексте

```go
func WithPrincipal(ctx context.Context, p Principal) context.Context
func PrincipalFrom(ctx context.Context) (Principal, bool)

func WithTraceMetadata(ctx context.Context, md map[string]any) context.Context
func TraceMetadataFrom(ctx context.Context) map[string]any
```

`WithPrincipal` кладёт принципала в контекст. `PrincipalFrom` читает его и
сообщает, найден ли он. Без принципала операции возвращают `ErrUnauthorized`.

`WithTraceMetadata` прикладывает request-scoped метаданные: они попадают в
`metadata` **каждой** observation, порождённой этим контекстом (обёртка над
`core.WithSpanMetadata`). Значение, заданное на конкретном вызове, перекрывает
одноимённый ключ из контекста. `TraceMetadataFrom` читает их обратно (или `nil`).
Отдельного dot-селектора, как `requestContextKeys` в [Mastra](https://mastra.ai/), нет — передаётся ровно
та карта, которую надо записать.

## Инструменты

### Effect

```go
type Effect int

const (
    EffectRead   Effect = iota // читает состояние
    EffectWrite                // меняет состояние
    EffectEgress               // отправляет данные за границу доверия
)

func (e Effect) String() string
func (e Effect) IsEgress() bool
```

Эффект — свойство инструмента, а не модели: рантайм применяет правила эгресса, не
спрашивая модель (I-10).

### kit.Tool

```go
func Tool[In, Out any](app *App, name string, fn func(ctx context.Context, in In) (Out, error), opts ...ToolOption) (*ToolDef, error)
```

Регистрирует типизированный инструмент. JSON Schema выводится из `In`, результат
`Out` сериализуется для модели.

```go
kit.Tool(app, "get_weather", getWeather,
    kit.Desc("Return the weather by city."),
    kit.Effects(kit.EffectEgress),
    kit.EgressHosts("wttr.in"),
)
```

### kit.RawTool

```go
func RawTool(app *App, name string, schema json.RawMessage, fn func(ctx context.Context, args json.RawMessage) (string, error), opts ...ToolOption) (*ToolDef, error)
```

Регистрирует инструмент, чья схема приходит извне, а не выводится из Go-типа
(так работает MCP). Схема принимается дословно; невалидная — `ErrInvalidConfig`
(BR-94). Пустая схема превращается в `{"type":"object"}`.

### Опции инструмента

```go
type ToolOption func(*toolOptions)

func Desc(s string) ToolOption
func Perm(p string) ToolOption
func Effects(e Effect) ToolOption
func EgressHosts(hosts ...string) ToolOption
func EgressAny() ToolOption
func RequireApproval(b bool) ToolOption
func ValidateOutput(b bool) ToolOption
func ModelView(f func(string) string) ToolOption
func BeforeToolCall(h BeforeToolHook) ToolOption
func AfterToolCall(h AfterToolHook) ToolOption
```

| Функция               | Назначение                                                                    |
| --------------------- | ----------------------------------------------------------------------------- |
| `kit.Desc`            | Описание инструмента для модели.                                              |
| `kit.Perm`            | Переопределяет право; по умолчанию `tool:<имя>`.                              |
| `kit.Effects`         | Эффект инструмента (`Read`/`Write`/`Egress`).                                 |
| `kit.EgressHosts`     | Host-allowlist для `Egress`: проверяются хосты URL в аргументах.              |
| `kit.EgressAny`       | Явный opt-in «любой хост»; без него пустой allowlist — отказ.                 |
| `kit.RequireApproval` | Перед вызовом спрашивать `Config.Approver`.                                   |
| `kit.ValidateOutput`  | Проверять результат по JSON Schema выходного типа (несовпадение не фатально). |
| `kit.ModelView`       | Что видит модель вместо результата; на приложение не влияет.                  |
| `kit.BeforeToolCall`  | Before-хук этого инструмента; заменяет `Config.BeforeToolCall`.               |
| `kit.AfterToolCall`   | After-хук этого инструмента; заменяет `Config.AfterToolCall`.                 |

### Хуки инструмента

```go
type ToolCallInfo struct {
    UserID      string
    Tenant      string
    Tool        string
    Args        string
    Fingerprint string // хэш tool + канонических аргументов
    Effect      Effect
    CallIndex   int // 1-based счётчик вызовов этого инструмента в ходе
}

type BeforeToolHook func(ctx context.Context, info ToolCallInfo) (proceed bool, output string, err error)
type AfterToolHook  func(ctx context.Context, info ToolCallInfo, result string, err error) (string, error)
```

| Поле          | Назначение                                           |
| ------------- | ---------------------------------------------------- |
| `Fingerprint` | Хэш инструмента и канонических аргументов.           |
| `Effect`      | Эффект инструмента (`Read`/`Write`/`Egress`).        |
| `CallIndex`   | Номер вызова этого инструмента в текущем ходе (с 1). |

Правило разрешения: хук на инструменте **заменяет** одноимённый хук уровня приложения
(`Config.BeforeToolCall`/`AfterToolCall`), а не складывается с ним. `BeforeToolHook`
идёт после валидации аргументов и до исполнения (`proceed=false` подставляет `output`
и пропускает инструмент); `AfterToolHook` — всегда, и может переписать результат или
ошибку. Guard результата (`tool_result`) выполняется **последним**, после обоих хуков.
Подробнее — [Инструменты: хуки](../concepts/tools.md).

### ToolDef

```go
func (t *ToolDef) Name() string
func (t *ToolDef) Description() string
func (t *ToolDef) Permission() string
func (t *ToolDef) Effect() Effect
func (t *ToolDef) EgressHosts() []string
func (t *ToolDef) EgressAny() bool
func (t *ToolDef) RequireApproval() bool
func (t *ToolDef) ValidateOutput() bool
func (t *ToolDef) Schema() json.RawMessage
func (t *ToolDef) ModelView(result string) string
func (t *ToolDef) Fingerprint(args string) string
```

Дескриптор зарегистрированного инструмента. Аргументы валидируются по схеме со
`"additionalProperties": false`. `Fingerprint` — стабильный хэш имени инструмента
и канонических аргументов; approval привязан именно к нему.

### Approval

```go
type EgressPolicy struct {
    RuleOfTwo bool
}

type ApprovalRequest struct {
    UserID      string
    Tenant      string
    Tool        string
    Args        string
    Fingerprint string
    Effect      Effect
}

type Approver interface {
    Approve(ctx context.Context, req ApprovalRequest) (bool, error)
}

type ApproverFunc func(ctx context.Context, req ApprovalRequest) (bool, error)
```

| Поле          | Назначение                                                              |
| ------------- | ----------------------------------------------------------------------- |
| `RuleOfTwo`   | Запрет эгресса в ходе, где уже был недоверенный ввод (untrusted input). |
| `Fingerprint` | Хэш `tool` + канонических аргументов; реализация обязана его потребить. |

`Approver` вызывается только для инструментов с `RequireApproval`. Одобрение **расходуется
один раз за ход**: рантайм помнит уже использованные fingerprint'ы этого хода, и повтор
того же fingerprint даёт `ErrApprovalRequired` (approve-once/replay-N невозможно). Другой
набор аргументов — другой fingerprint и отдельное одобрение; в новом ходе счётчик
начинается заново.

### RetrievalTool

```go
const DefaultRetrievalToolName = "search_knowledge"

func RetrievalTool(app *App, name string, opts ...ToolOption) (*ToolDef, error)

type RetrievalIn struct {
    Query string `json:"query"`
    TopK  int    `json:"top_k,omitempty"`
}

type RetrievalHit struct {
    DocID    string `json:"doc_id"`
    Text     string `json:"text"`
    Position int    `json:"position"`
}

type RetrievalOut struct {
    Hits []RetrievalHit `json:"hits"`
}
```

Регистрирует типизированный инструмент поверх собственного гибридного поиска
приложения. Идёт тем же путём вызова: проверяется право инструмента, а `Search`
ещё раз проверяет право поиска и ACL-фильтр, поэтому ACL применяется дважды.
Требует настроенный `Embedder`.

## Константы

| Константа                | Значение | Назначение                         |
| ------------------------ | -------- | ---------------------------------- |
| `DefaultMaxSteps`        | `8`      | Лимит шагов цикла инструментов.    |
| `DefaultMaxCallsPerTool` | `3`      | Лимит вызовов одного инструмента.  |
| `DefaultToolTimeout`     | `30s`    | Таймаут одного вызова инструмента. |
| `DefaultArgsMaxBytes`    | `32768`  | Лимит размера аргументов.          |
| `DefaultTopK`            | `10`     | Хитов поиска по умолчанию.         |

Значения по умолчанию живут и в других пакетах:

| Константа                      | Значение | Пакет          |
| ------------------------------ | -------- | -------------- |
| `chunk.DefaultSize`            | `400`    | `chunk`        |
| `chunk.DefaultOverlap`         | `80`     | `chunk`        |
| `guard.DefaultMaxChars`        | `8000`   | `guard`        |
| `guard.DefaultCacheSize`       | `4096`   | `guard`        |
| `store.DefaultTopK`            | `10`     | `store`        |
| `store.MaxTopK`                | `1000`   | `store`        |
| `store.MaxNumCandidates`       | `10000`  | `store`        |
| `store.DefaultRRFRankConst`    | `60`     | `store`        |
| `store.DefaultILMDays`         | `30`     | `store`        |
| `trace.DefaultQueueSize`       | `4096`   | `trace`        |
| `trace.DefaultBatchSize`       | `500`    | `trace`        |
| `trace.DefaultFlushInterval`   | `2s`     | `trace`        |
| `trace.DefaultMaxBatchBytes`   | `5 MB`   | `trace`        |
| `trace.DefaultMaxRetries`      | `3`      | `trace`        |
| `trace.DefaultRetryBackoff`    | `200ms`  | `trace`        |
| `trace.DefaultMaxStringLength` | `131072` | `trace`        |
| `trace.DefaultMaxDepth`        | `8`      | `trace`        |
| `trace.DefaultMaxArrayLength`  | `50`     | `trace`        |
| `trace.DefaultMaxObjectKeys`   | `50`     | `trace`        |
| `embed.DefaultSessions`        | `2`      | `embed`        |
| `embed.DefaultBatchSize`       | `32`     | `embed`        |
| `gliner.DefaultSessions`       | `2`      | `guard/gliner` |
| `gliner.DefaultMaxTokens`      | `512`    | `guard/gliner` |

## Ошибки

Sentinels объявлены в `kit/errors.go` (реэкспорт из `internal/core`); матчинг —
через `errors.Is`. Отказы guard дополнительно доступны через `errors.As` как
`*kit.GuardRejectError` (алиас типа из `internal/call`; поля `Rail`,
`Tripwire`, `Version`).

| Sentinel              | Когда возвращается                                                                                                                                                                                  |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ErrInvalidConfig`    | `New`: нет `ES`, `LLM` или `Policy`; невалидный `policy.yaml`; `Ingest` без `Embedder` или с пустым `ID`; `Generate` со стратегией `Fallback` без `Fallback`; инструмент без имени или обработчика. |
| `ErrUnauthorized`     | Принципала нет в контексте или он пуст; роль не имеет права; `Ingest`/`Search`/`Delete` без `Tenant`; эгресс заблокирован «Rule of Two»/host-allowlist.                                             |
| `ErrGuardReject`      | Rail `input` вернул reject (fast-fail) в `Chat`/`Generate`.                                                                                                                                         |
| `ErrGuardUnavailable` | Guard недоступен (fail-closed).                                                                                                                                                                     |
| `ErrProvider`         | Ошибка провайдера LLM, эмбеддера или Elasticsearch; не удалось декодировать ответ в `Generate`; ошибка чтения/записи ES.                                                                            |
| `ErrInvalidArgs`      | Аргументы не разобрались как JSON или не прошли схему (как результат-ошибка инструмента); пустой `SearchRequest.Query`; нет сообщений ролей `user`/`assistant` в `Chat`.                            |
| `ErrMaxSteps`         | Исчерпан `MaxSteps` цикла.                                                                                                                                                                          |
| `ErrBudget`           | Превышен `Config.MaxTotalTokens`.                                                                                                                                                                   |
| `ErrApprovalRequired` | Инструмент с `RequireApproval`, а `Approver` не задан, отказал или одобрение этого fingerprint уже израсходовано.                                                                                   |
| `ErrTool`             | Зарезервирован под падение внутри инструмента; фасад возвращает такие сбои модели как результат, а не как ошибку.                                                                                   |
| `ErrRateLimited`      | Лимит на тенанта исчерпан (`Config.RateLimit`, BR-86) до обращения к провайдеру; 429 провайдера приходит как `ErrProvider`.                                                                         |
| `ErrToolNotFound`     | Зарезервирован: неизвестный инструмент сейчас возвращается модели результатом-ошибкой, а не этим sentinel’ом.                                                                                       |
| `ErrNotFound`         | Зарезервирован для приложения и адаптеров; операции фасада его не возвращают.                                                                                                                       |

Разбор по слоям и что видит клиент — [Ошибки](./errors.md).

## Смотрите также

- [Ошибки](./errors.md) — sentinels и коды ответов
- [Structured output](../guides/structured-output.md) — рецепты `Generate[T]`
- [Вызов инструментов](../guides/tool-calling.md) — настройка `Tool` и разбор сбоев
- [Конфигурация](./configuration.md) — значения по умолчанию и лимиты
- [Провайдеры](./providers.md) — реализации `model.Client` и `embed.Embedder`
- [policy.yaml](./policy.md) — формат политики RBAC
- [Индексы Elasticsearch](./elasticsearch.md) — поля документов
- [Chat](../concepts/chat.md) — операция в контексте
- [Доступ](../concepts/access-control.md) — принципал и права
- [Наблюдаемость](../concepts/observability.md) — трейсы и оценки
