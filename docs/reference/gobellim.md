# Справочник gobellim

Полный справочник фасада `github.com/KirillYuzh/Gobellim`: конфигурация, жизненный цикл,
операции, типы, инструменты и ошибки.

Все операции идут через единый примитив `internal/call` (`authz → guard → span →
fn`). Добавлять middleware или обходить политику не нужно и нельзя.

## Жизненный цикл

### gobellim.Config

Поля конфигурации. Значения по умолчанию применяются в `gobellim.New`, если поле
оставлено нулевым.

| Поле               | Тип                      | По умолчанию                           | Назначение                                                                                                  |
| ------------------ | ------------------------ | -------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `ES`               | `store.ES`               | `nil`                                  | Опционален: без него работают `Chat`/`CallTool`/`Generate`; нужен для ingest/search/memory (BR-102).        |
| `LLM`              | `model.Client`           | — (обязательно)                        | Клиент чата.                                                                                                |
| `Pricing`          | `model.Pricing`          | нулевое (стоимость `0`)                | Цена модели за 1M токенов; библиотека считает `usage.cost` и `cost`.                                        |
| `Embedder`         | `embed.Embedder`         | `nil`                                  | Нужен только для `Ingest` и `Search`; чат-приложение может не задавать.                                     |
| `Policy`           | `*authz.Policy`          | — (обязательно)                        | Разобранный `policy.yaml`.                                                                                  |
| `GuardL2`          | `guard.Detector`         | — (обязательно)                        | ML-слой guard; обязателен. L1-only — явно через `guard.L1Only()`; `nil` — `ErrInvalidConfig` (D80, BR-109). |
| `GuardFailOpen`    | `[]guard.Rail`           | `nil` (fail-closed везде)              | Рельсы, где упавший ML-слой не роняет запрос (BR-06, D73).                                                  |
| `GuardMaxChars`    | `int`                    | `8000` (`guard.DefaultMaxChars`)       | Лимит текста для guard.                                                                                     |
| `GuardCacheSize`   | `int`                    | `4096` (`guard.DefaultCacheSize`)      | Размер LRU-кэша вердиктов guard.                                                                            |
| `GuardWindowChars` | `int`                    | `1024` (`guard.DefaultWindowChars`)    | Окно детекции: текст длиннее идёт перекрывающимися окнами, чтобы L2 видел хвост.                            |
| `GuardOnViolation` | `func(guard.Verdict)`    | `nil`                                  | Колбэк на каждый вердикт, кроме allow; без сырых значений (`AllowPII`).                                     |
| `Tokenizer`        | `chunk.Tokenizer`        | `nil`                                  | Токенайзер чанкера; без него `Ingest` — `ErrInvalidConfig` (BR-104).                                        |
| `ChunkSize`        | `int`                    | `400` (`chunk.DefaultSize`)            | Единиц токенайзера в чанке; у `WhitespaceTokenizer` — слов, а не токенов.                                   |
| `ChunkOverlap`     | `int`                    | `80` (`chunk.DefaultOverlap`)          | Перекрытие в тех же единицах, `< ChunkSize`.                                                                |
| `MaxSteps`         | `int`                    | `8` (`DefaultMaxSteps`)                | Лимит цикла «модель → инструменты → модель».                                                                |
| `MaxCallsPerTool`  | `int`                    | `3` (`DefaultMaxCallsPerTool`)         | Лимит вызовов одного инструмента.                                                                           |
| `ToolTimeout`      | `time.Duration`          | `30s` (`DefaultToolTimeout`)           | Таймаут одного вызова инструмента.                                                                          |
| `ArgsMaxBytes`     | `int`                    | `32768` (`DefaultArgsMaxBytes`)        | Лимит размера аргументов инструмента.                                                                       |
| `TopK`             | `int`                    | `10` (`DefaultTopK`)                   | Хитов поиска по умолчанию.                                                                                  |
| `MemoryLimit`      | `int`                    | `20` (`DefaultMemoryLimit`)            | Последних сообщений треда в контексте (при `ChatRequest.ThreadID`).                                         |
| `MemoryRecall`     | `int`                    | `0` (выключено)                        | Сколько сообщений подмешивать семантическим recall'ом; нужен `Embedder`.                                    |
| `Spotlight`        | `gobellim.SpotlightMode` | `gobellim.SpotlightOn`                 | Помечать недоверенный контент (результаты инструментов) как данные (I-18).                                  |
| `RateLimit`        | `gobellim.RateLimit`     | нулевое (лимит выключен)               | Лимит операций на тенанта (`PerSecond`, `Burst`); исчерпание — `ErrRateLimited`.                            |
| `ResponseCache`    | `gobellim.ResponseCache` | нулевое (кэш выключен)                 | LRU+TTL кэш ответов модели **без tools** (`Size`, `TTL`).                                                   |
| `Reranker`         | `rerank.Reranker`        | `nil`                                  | Реранкер для `SearchRequest.Rerank`; без него `Rerank` — `ErrInvalidConfig` (D49).                          |
| `RerankCandidates` | `int`                    | `0` → `TopK × DefaultRerankMultiplier` | Сколько кандидатов брать до реранкинга.                                                                     |
| `ToolSearch`       | `gobellim.ToolSearch`    | нулевое (выключено)                    | Лексический отбор инструментов при большом каталоге (D50).                                                  |
| `Logger`           | `*slog.Logger`           | `slog.Default()`                       | Логи; тексты сообщений в них не попадают.                                                                   |
| `TraceContent`     | `trace.ContentMode`      | `trace.ContentRedacted`                | Что попадает в трейс: маскированный текст (по умолчанию), только метаданные или исходный текст (opt-in).    |
| `TracerProvider`   | `trace.Provider`         | `nil` (операции не трассируются)       | Провайдер, создающий спаны; адаптер OpenTelemetry — `plugins/otel`.                                         |
| `MaxTotalTokens`   | `int`                    | `0` (без лимита)                       | Бюджет токенов хода; при превышении — `ErrBudget`.                                                          |
| `Egress`           | `EgressPolicy`           | нулевое (Rule of Two **включён**)      | Правила эгресса инструментов (`AllowEgressAfterUntrusted`).                                                 |

<!-- derived: BR-51 -->

| `Approver` | `Approver` | `nil` | Кто решает по инструментам с `RequireApproval`; без него — `ErrApprovalRequired`. |
| `Middleware` | `[]Middleware` | `nil` | App-level middleware: оборачивает каждый вызов инструмента перед авторизацией и политикой. |

`DefaultMaxSteps`, `DefaultMaxCallsPerTool`, `DefaultToolTimeout`, `DefaultArgsMaxBytes` и `DefaultTopK` — константы пакета `gobellim`. `GuardMaxChars`, `GuardCacheSize`, `ChunkSize` и `ChunkOverlap` добираются конструкторами `guard.New` и `chunk.New` из констант `guard.*` и `chunk.*`.

`TraceContent` в режиме `ContentFull` печатает предупреждение при `New`: в трейс попадёт необработанный текст. Политика выбирает, _что_ записывать, а не маскирует сама — маскирование делает guard. Сэмплирование задаёт OpenTelemetry SDK, а не библиотека.

`GuardFailOpen` управляет реакцией на недоступность ML-слоя (I-16, D73). По умолчанию список пуст — fail-closed: ошибка любого слоя отклоняет операцию (`ErrGuardUnavailable`). На перечисленных рельсах детерминированный L1 остаётся, упавший `GuardL2` пропускается, а вердикт и observation помечаются `guard_degraded`; при старте печатается `slog.Warn`.

`Pricing` — цена модели за миллион токенов (`model.Pricing{InputPerMTok, OutputPerMTok}`). Нулевое значение означает «цена неизвестна» и даёт `usage.cost`/`cost` = 0. Ненулевое — библиотека считает стоимость каждого вызова и суммирует её в trace (`Chat`, `Generate`); в observation генерации пишется цена одного вызова.

Слой L2 (`GuardL2`) конструируется отдельно — `gliner.New(gliner.Config{...})` в ORT-сборке. Поля `Dir`, `Model`, `Sessions`, `Wait`, `MaxTokens`: `Dir` по умолчанию берётся из `KIT_MODELS_DIR`, затем `./models`; `Model` — `hivetrace__gliner-guard-omni`; `Sessions` — 2; `Wait` — 2 с; `MaxTokens` — `DefaultMaxTokens` = 512 (кламп до `max_sequence_length` модели). Метки, пороги, `prefix_ids`, позиции маркеров и `category_map` детектор читает из `<dir>/<model>/config.json`, а не из Go-конфига.

Полная таблица значений с правилами — [Конфигурация](./configuration.md).

### gobellim.New

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

Клиенты ES, LLM и эмбеддера `App` не закрывает — их жизненным циклом
управляет приложение. Вызывайте при остановке сервиса.

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
func (a *App) Chat(ctx context.Context, req ChatRequest) (Response, error)
```

Гоняет guard, модель и инструменты в цикле и возвращает `Response` — финальное
сообщение плюс аудит хода: `Usage`, `TraceID` и `Decision` (версия guard, хэш
политики и признак недоверенности хода, BR-103).
Требует права `chat` (как и остальные операции, `Chat` проверяет право в
примитиве вызова; права на инструменты проверяются отдельно). При заданном
`ThreadID` подгружает историю треда и дописывает ход — нужно ещё право `memory`
(см. [Память](../memory/README.md)).
Ответ возвращается целиком; поток — у `ChatStream`. `Response` заполняется и при
ошибке (`TraceID`, `Decision`), поэтому отказ тоже аудируем.
На последнем шаге цикла `tool_choice` становится `none`, чтобы
модель отвечала, а не открывала вызов, который уже не исполнится.

```go
res, err := app.Chat(ctx, gobellim.ChatRequest{
    System:   "Ты ассистент.",
    Messages: []model.Message{{Role: model.RoleUser, Content: "Привет"}},
})
fmt.Println(res.Message.Content, res.TraceID)
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
Вызывающий обязан вычитать канал до закрытия. Каждый `StreamChunk` несёт
`TraceID` хода. Остаточный риск: совпадение длиннее
хвоста без границы может пересечь окно (I-20).

```go
ch, err := app.ChatStream(ctx, gobellim.ChatRequest{Messages: msgs})
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

Снимок рантайма: режим, состав слоёв и версия guard и каждый компонент,
реализующий `HealthChecker` (`guard_l2`, `embedder`, `es`, `llm`). `Healthy` ложен,
если хоть один компонент вернул `OK=false`; `Degraded` отражает режим guard.
Права не требует — это локальный снимок, а не чтение данных (BR-91).

```go
h := app.Health()
if !h.Healthy { log.Printf("unhealthy: %+v", h.Components) }
```

### Metrics

```go
func (a *App) Metrics() Metrics
```

Счётчики для дашборда: вердикты guard (`Guard.Scans`, `Guard.Blocked`,
`Guard.Truncated`, `Guard.Degradations` и гистограмма латентности `Guard.Latency`).
Ядро не тянет библиотеку метрик — снимите снимок и отправьте его туда, где уже
собираются метрики. Права не требует.

### CallTool

```go
func (a *App) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error)
```

Вызывает один зарегистрированный инструмент по имени тем же путём, что `Chat`:
право инструмента, валидация аргументов и лимит размера, approval, egress-политика,
хуки, guard `tool_result` и `ModelView`. Принципал берётся из `context.Context`.
Возвращает то, что увидела бы модель: ошибка инструмента приходит как
результат-ошибка (I-11). Неизвестный инструмент — `ErrToolNotFound`, невалидные или
слишком большие аргументы — `ErrInvalidArgs`, отказ политики — `ErrUnauthorized`.
Операция нужна не-модельным вызывающим — MCP-серверу
([`mcp.FromTool`](../mcp/README.md)), — чтобы вызов нельзя было провести в обход
примитива (BR-96).

```go
ctx = gobellim.WithPrincipal(ctx, gobellim.Principal{UserID: "u-1", Roles: []string{"user"}, Tenant: "acme"})
out, err := app.CallTool(ctx, "get_weather", json.RawMessage(`{"city":"Москва"}`))
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
[Память](../memory/README.md).

### Generate

```go
func Generate[T any](ctx context.Context, app *App, req GenerateRequest[T]) (Generated[T], error)
```

Полноценная операция примитива: принципал, право `generate`, input/output guard,
трейс. Просит модель вернуть значение типа `T` по JSON Schema, выведенной из `T`,
проверяет ответ по этой же схеме и декодирует его в `T`. `System` уходит ролью
`system`. При `SchemaNative` и отсутствии поддержки у провайдера схема
автоматически инлайнится.

```go
type Review struct {
    Approved bool   `json:"approved"`
    Notes    string `json:"notes"`
}

g, err := gobellim.Generate[Review](ctx, app, gobellim.GenerateRequest[Review]{Prompt: "Проверь заявку"})
// g.Value   — декодированное значение
// g.Raw     — сырой (уже прошедший guard) ответ
// g.Decoded — true, если ответ совпал со схемой и заполнил g.Value
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
    Untrusted bool
    CreatedAt time.Time
}
```

| Поле        | Тип          | Назначение                       |
| ----------- | ------------ | -------------------------------- |
| `Role`      | `model.Role` | Роль сообщения.                  |
| `Content`   | `string`     | Текст (уже прошедший guard).     |
| `Score`     | `float64`    | Оценка сходства при recall.      |
| `Untrusted` | `bool`       | Сообщение из недоверенного хода. |
| `CreatedAt` | `time.Time`  | Время записи сообщения.          |

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
| `ErrorStrategy`    | `ErrorStrategy`    | Что делать, если ответ не декодируется или не совпадает со схемой `T`.  |
| `Fallback`         | `*T`               | Значение по умолчанию для стратегии `Fallback`; обязательно для неё.    |
| `Tags`             | `[]string`         | Метки на trace.                                                         |
| `StructuringModel` | `model.Client`     | Второй вызов при неудачном прямом декодировании (см. ниже).             |

Если задан `StructuringModel`, то при неполадке прямого декодирования или несоответствии схеме (и до применения `ErrorStrategy`) gobellim делает одну попытку через `model.Structurer`: просит модель переписать прошлый ответ по той же схеме и декодирует результат. Вызов идёт через `internal/call`, пишется **отдельной** observation (с `metadata.structuring = true`) и **не** добавляет usage к итогу хода.

### Generated

```go
type Generated[T any] struct {
    Value        T
    Raw          string
    Decoded      bool
    UsedFallback bool
    FinishReason model.FinishReason
}
```

| Поле           | Тип                  | Назначение                                                                               |
| -------------- | -------------------- | ---------------------------------------------------------------------------------------- |
| `Value`        | `T`                  | Декодированное значение.                                                                 |
| `Raw`          | `string`             | Сырой ответ (уже прошедший output guard).                                                |
| `Decoded`      | `bool`               | `true`, если ответ совпал со схемой и заполнил `Value`; `false` при `Warn` и `Fallback`. |
| `UsedFallback` | `bool`               | `true`, если вернулось значение `Fallback`.                                              |
| `FinishReason` | `model.FinishReason` | Причина остановки модели.                                                                |

`Generate` валидирует сырой ответ по JSON Schema до декодирования в `T`: ответ сначала
разбирается в `any` и проверяется скомпилированной схемой, и только прошедший проверку
документ декодируется в `T`, а `Generated[T].Decoded` сообщает, какой из двух исходов
случился.

### ErrorStrategy

```go
type ErrorStrategy int

const (
    Strict   ErrorStrategy = iota // ошибка (по умолчанию)
    Warn                          // предупреждение в лог, `Raw` возвращается, `Decoded` = false
    Fallback                      // вернуть `Fallback` и пометить `UsedFallback`
)
```

### StreamChunk

```go
type StreamChunk struct {
    Text    string        // подтверждённый, уже проверенный текст
    TraceID string        // id хода; заполнен во всех чанках
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
    GuardFailOpen []string
    GuardLayers   []string
    GuardL1Only   bool
    GuardVersion  string
    GuardStats    guard.Stats // Scans, Blocked, Truncated, Degradations, Latency
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
    EffectRead   Effect = iota // читает состояние (по умолчанию)
    EffectWrite                // меняет состояние
    EffectEgress               // отправляет данные за границу доверия
)

func (e Effect) String() string
func (e Effect) IsEgress() bool
```

Эффект — свойство инструмента, а не модели: рантайм применяет правила эгресса, не
спрашивая модель (I-10).

### Объявление без `App`

Инструмент объявляется через `gobellim.Tool` (или `gobellim.RawTool`, или
`gobellim.RetrievalTool`), регистрируется в `gobellim.NewRegistry()` и
компилируется через `Build()`. ToolSet передаётся в `gobellim.New` как
`Config.Tools`. Объявление не касается `App`, поэтому ошибки конфигурации
всплывают при `Build()`, а не при первом вызове модели.

```go
type weatherIn struct {
    City string `json:"city"`
}

type weatherOut struct {
    Summary string `json:"summary"`
}

func getWeather(ctx context.Context, in weatherIn) (weatherOut, error) {
    return weatherOut{Summary: "В городе " + in.City + " солнечно"}, nil
}

tools, err := gobellim.NewRegistry().Register(
    gobellim.Tool("get_weather", getWeather,
        gobellim.Desc("Текущая погода в городе.")),
).Build()
if err != nil {
    return err
}

app, err := gobellim.New(ctx, gobellim.Config{
    LLM:     llm,
    Policy:  policy,
    GuardL2: guard.L1Only(),
    Tools:   tools,
})
```

Доступные опции:

| Опция                       | Назначение                                 |
| --------------------------- | ------------------------------------------ |
| `gobellim.Desc(s)`          | Описание для модели                        |
| `gobellim.Perm(p)`          | Право; по умолчанию `tool:<name>`          |
| `gobellim.Read()`           | Read-only (по умолчанию, effect `Read`)    |
| `gobellim.Write()`          | Меняет состояние (effect `Write`)          |
| `gobellim.Egress(hosts...)` | Эгресс с allowlist хостов                  |
| `gobellim.View[Out](f)`     | Типизированная проекция для модели         |
| `gobellimtool.AnyHost()`    | Эгресс на любой хост (явный opt-out)       |
| `gobellimtool.Approve()`    | Требует подтверждения перед каждым вызовом |

`Read()`, `Write()` и `Egress(...)` — это политики, а не отдельные опции:
политика сама является опцией, поэтому её можно передать в `Tool`/`RawTool`
напрямую, а несколько политик складываются (эффект и allowlist берутся из
последней непустой, подтверждение — если хотя бы одна его требовала).
`Write()` объявляет effect `Write`, `Egress(hosts...)` — effect `Egress` с
allowlist. `AnyHost()` и `Approve()` живут в пакете `tool` и требуют явного
импорта: у фасада нет сокращённых имён для них.

### gobellim.Call

```go
type Call struct {
    UserID string
    Tenant string
    Tool   string
    Args   json.RawMessage
    Key    string
    Index  int
    Policy Policy
}

func CallKey(ctx context.Context) string
```

`Call` — неизменяемый runtime-вид вызова, который получает middleware.
`Call.Key` — стабильный идентификатор: `SHA-256(имя + канонические аргументы)`.
`gobellim.CallKey(ctx)` отдаёт то же значение внутри инструмента, так что его
можно передать downstream как `Idempotency-Key`.

### gobellim.Decision

```go
type Decision int

const (
    Approve Decision = iota
    Deny
    Defer
)
```

Три исхода approver’а: разрешить, отказать, отложить (не rozwiązano — отказ).

### gobellim.Middleware

```go
type Middleware func(ctx context.Context, call Call, next Next) (json.RawMessage, error)
type Next func(ctx context.Context) (json.RawMessage, error)
```

Единственная точка расширения на уровне App. Заменяет старые
`BeforeToolCall`/`AfterToolCall`. Фактический порядок: авторизация →
валидация аргументов → подтверждение/egress → middleware → tool → guard
результата → проверка схемы выхода → View.

```go
gobellim.New(ctx, gobellim.Config{
    Middleware: []gobellim.Middleware{
        func(ctx context.Context, call gobellim.Call, next gobellim.Next) (json.RawMessage, error) {
            slog.Info("tool", "name", call.Tool, "key", call.Key)
            return next(ctx)
        },
    },
})
```

### gobellim.RawTool

```go
func RawTool(name string, schema json.RawMessage, fn func(ctx context.Context, args json.RawMessage) (string, error), opts ...Option) ToolDef
```

Регистрирует инструмент, чья схема приходит извне (так работает MCP).
`RawTool` — адаптер границы, не равноправный второй способ создавать tools:
схема компилируется при `Build()`; по умолчанию `Read`; egress требует
явной политики; результат проверяется guard'ом и схемой выхода.

### gobellim.RetrievalTool

```go
const DefaultRetrievalToolName = "search_knowledge"

func RetrievalTool(name string, opts ...Option) ToolDef

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

### Ошибки инструмента

Три класса, не двенадцать:

| Класс         | Примеры                                        | Куда идёт                   |
| ------------- | ---------------------------------------------- | --------------------------- |
| `ToolError`   | функция вернула ошибку, invalid args, limit    | модели как `{"error": ...}` |
| `PolicyError` | unauthorized, approval required, egress denied | caller/agent: fatal         |
| `SystemError` | provider failure, cancellation, deadline       | caller/agent: fatal         |

```go
var (
    ErrInvalidArgs   = errors.New("invalid arguments")
    ErrToolFailed    = errors.New("tool failed")
    ErrRateLimited   = errors.New("rate limited")

    ErrUnauthorized  = errors.New("unauthorized")
    ErrApprovalNeeded = errors.New("approval required")
    ErrEgressDenied  = errors.New("egress denied")

    ErrProvider      = errors.New("provider failed")
)
```

Решение fatal/non-fatal принимает agent loop по `errors.Is`.

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
| `embed.DefaultRemoteBatchSize` | `64`     | `embed`        |
| `gliner.DefaultSessions`       | `2`      | `guard/gliner` |
| `gliner.DefaultMaxTokens`      | `512`    | `guard/gliner` |

## Ошибки

Sentinels объявлены в `internal/app/errors.go` (реэкспорт из `internal/core`); матчинг —
через `errors.Is`. Отказы guard дополнительно доступны через `errors.As` как
`*gobellim.GuardRejectError` (алиас типа из `internal/call`; поля `Rail`,
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
| `ErrTool`             | Сбой, помеченный как падение внутри инструмента (`gobellim.go:989` — хуки, `mcp/register.go:70` — MCP-инструмент); фасад отдаёт такие сбои модели как результат, а не как ошибку.                   |
| `ErrRateLimited`      | Лимит на тенанта исчерпан (`Config.RateLimit`, BR-86) до обращения к провайдеру; 429 провайдера приходит как `ErrProvider`.                                                                         |
| `ErrToolNotFound`     | Возвращается `App.CallTool` для неизвестного инструмента (`calltool.go:29`); в `Chat` неизвестный инструмент — результат-ошибка модели.                                                             |
| `ErrNotFound`         | Объект не найден: в `eval` — трейс, который рекордер не получил (`eval/eval.go:103`); операции фасада его не возвращают.                                                                            |

Разбор по слоям и что видит клиент — [Ошибки](./errors.md).

## Смотрите также

- [Ошибки](./errors.md) — sentinels и коды ответов
- [Structured output](../overview.md) — рецепты `Generate[T]`
- [Вызов инструментов](../tools/README.md) — настройка `Tool` и разбор сбоев
- [Конфигурация](./configuration.md) — значения по умолчанию и лимиты
- [Провайдеры](./providers.md) — реализации `model.Client` и `embed.Embedder`
- [policy.yaml](./policy.md) — формат политики RBAC
- [Индексы Elasticsearch](./elasticsearch.md) — поля документов
- [Chat](../overview.md) — операция в контексте
- [Доступ и права](./policy.md) — принципал и права
- [Наблюдаемость](../tracing/README.md) — трейсы и оценки
