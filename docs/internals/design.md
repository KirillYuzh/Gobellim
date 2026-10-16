# Дизайн v1 (замороженные контракты)

Этот документ — **контракты и инварианты v1**: сигнатуры пакетов (§3),
обязательные свойства (§2) и **журнал изменений API** (§5). Роли документов
разведены (`D52`): **поведение** описано в [Бизнес-правилах](./business-rules.md)
(`BR-xx`), **раскладка пакетов и рантайм** — в [Архитектуре](./architecture.md).
§5 — исторический список правок, а не второй контракт: нормативное утверждение
живёт в `BR` или в §3. Расхождения правятся в том же изменении (AGENTS.md).

Цель правок: привести код в соответствие с собственной спекой, закрыть найденные
дефекты корректности и добавить функциональность, перенесённую из [Mastra](https://mastra.ai/)
(см. §4 «Решения»).

## 1. Схемы

Путь вызова после шлифовки: каноникализация и guard идут до авторизации
операции, детерминированный слой раньше ML, решение принимает рантайм.

```mermaid
flowchart TD
    A["Oперация: Chat / Ingest / Search / Generate"] --> B["principal из context"]
    B --> C["authz: permission"]
    C --> D["canonicalize + decode-and-rescan"]
    D --> E["guard rules L1: фразы, regex, контрольные суммы"]
    E --> F["guard ML L2: ONNX detector"]
    F --> G["decide: категория x рельс"]
    G -->|reject| H["ErrGuardReject + tripwire"]
    G -->|mask| I["маскированный текст"]
    G -->|allow| I
    I --> J["fn: LLM / tool / embed / ES"]
    J --> K["trace: masked, versioned, sampled"]
```

Конвейер guard: детекторы не знают про рельс, решение — чистая таблица.

```mermaid
flowchart LR
    T["текст"] --> CAN["canonicalize"]
    CAN --> DEC["decode-and-rescan"]
    DEC --> L1["rules L1"]
    L1 --> L2["ML L2"]
    L2 --> DECIDE["decide(rail, findings)"]
    DECIDE --> V["Verdict: action + findings + text"]
```

Поток трейсинга: один документ на trace в конце, observations отдельным потоком;
в ES уходит только разрешённый политикой текст.

```mermaid
flowchart TD
    CALL["call.Do"] --> OBS["trace.Observation (masked)"]
    OBS --> SINK{"sampling по trace_id"}
    SINK -->|drop| NULL["ничего не пишем"]
    SINK -->|keep| PROC["export-фильтр: truncate, hide, processors"]
    PROC --> W["async writer: batch, retry, 409=ok"]
    W --> ES["kit-observations"]
    TR["операция закончилась"] --> TD["trace.Trace (masked)"] --> W
```

## 2. Инварианты

Обязательные свойства; каждое проверяемо тестом или конфигурацией.

| ID   | Инвариант                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| ---- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| I-01 | Любой I/O (LLM, tool, embed, ES, guard) идёт через `internal/call`. Обойти политику нельзя: `Generate` — такая же операция примитива.                                                                                                                                                                                                                                                                                                                                                                                                                     |
| I-02 | Детекторы guard не зависят от рельса. Рельс влияет только на решение (`decide`), а не на детекцию. Кэш вердиктов ключуется каноническим текстом и версией guard.                                                                                                                                                                                                                                                                                                                                                                                          |
| I-03 | Каноникализация и decode-and-rescan выполняются **до** L1 и L2; оба слоя видят один и тот же канонический текст.                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| I-04 | Mask перезаписывает текст: последующие слои и последующие рельсы видят маску, а не исходные значения.                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| I-05 | Fail-closed различим: недоступность модели — `ErrGuardUnavailable`; битая конфигурация/модель на старте — `ErrInvalidConfig`.                                                                                                                                                                                                                                                                                                                                                                                                                             |
| I-06 | Окно guard не уже окна модели: если клиент реализует `model.ContextWindow`, `New` сверяет `guard.max_chars` с `ContextTokens()` и возвращает `ErrInvalidConfig` при превышении. Окно модели в токенах, лимит guard в рунах — это верхняя оценка, а не точная гарантия.                                                                                                                                                                                                                                                                                    |
| I-07 | В трейсы не попадает необработанный текст по умолчанию. Политика текста трейса — явная: `masked` / `full`.                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| I-08 | Вердикт guard воспроизводим: в observation пишутся версия guard (`<policy_hash>/<layer_id>`, имена слоёв через `+`), хэш политики и имя слоя L2 (`guard_model` = `"L2"`).                                                                                                                                                                                                                                                                                                                                                                                 |
| I-09 | Лимиты и деньги — часть контракта: `ErrBudget` (бюджет хода) и `ErrRateLimited` (лимит на тенанта); `Retry-After` учитывается в `internal/httpjson` при 429 провайдера; таймауты приходят как `context.DeadlineExceeded`/`ErrProvider`.                                                                                                                                                                                                                                                                                                                   |
| I-10 | Эгресс — явное свойство инструмента (`Effects`); политика хостов обязательна (`EgressHosts` или `EgressAny`), пустая — отказ; «Rule of Two» проверяется рантаймом.                                                                                                                                                                                                                                                                                                                                                                                        |
| I-11 | Ошибка инструмента — данные для модели: результат приходит как `{"error": "..."}`, и ход продолжается. Прерывают ход: отказ авторизации (RBAC и эгресс-политика), ошибка провайдера, отмена/дедлайн контекста, отсутствие требуемого подтверждения (`ErrApprovalRequired`) и исчерпание одного ретрая на невалидные аргументы (BR-23).                                                                                                                                                                                                                    |
| I-12 | Контракт ES-запроса явный: метод, путь, тип контента, refresh, идемпотентность — поля запроса, а не вывод из строки пути.                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| I-13 | Тесты — только stdlib. Обвязка **подключена**: CI (`.github/workflows/ci.yml`) гоняет джобы build, test, race, ort-vet, lint, fumpt; `golangci-lint` (`gofumpt`, `govet`, `errcheck`, `staticcheck`, `gosec`, `depguard` и др.) и `make check` дают ту же планку локально. Правила импортов закодированы дважды — `depguard` в `.golangci.yml` и stdlib-тест `internal/importrules`. Обвязка **подключена и зелёная**: `golangci-lint` v2.13.0 локально даёт 0 замечаний, а прогоны workflow на раннере зелёные (push в `develop`/`main` и pull request). |
| I-14 | Единственный источник правды о вердиктах — этот документ и `guardrails.md`; `BR-27`/`architecture.md` не заявляют больше, чем умеет модель.                                                                                                                                                                                                                                                                                                                                                                                                               |
| I-15 | Публичный API не содержит «выключателей безопасности»: `AllowAll` живёт в `guard/guardtest`.                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| I-16 | Деградация guard явная: режим задаёт `Config.GuardMode` (`GuardStrict` — fail-closed по умолчанию; `GuardDegraded` — недоступный необязательный (ML) слой пропускается, вердикт помечается `Degraded`, observation — `guard_degraded`). Детерминированный слой (L1) обязателен всегда; деградировавшие вердикты не кэшируются.                                                                                                                                                                                                                            |
| I-17 | Память ограничена парой `tenant` + `user_id` принципала: история треда, очистка и recall фильтруют по обоим. `thread_id` даёт вызывающий, поэтому сам по себе доступа не даёт; пустой `UserID` — `ErrUnauthorized`. История треда на чтении считается доверенной (замаскирована при записи) и повторно через L2 не проходит.                                                                                                                                                                                                                              |
| I-18 | Недоверенный ввод помечается (spotlighting): результат инструмента (включая retrieval) оборачивается в `<untrusted-data>…</untrusted-data>` **после** `ModelView`, а в системный промпт добавляется напоминание трактовать этот блок как данные, а не инструкции. По умолчанию включено (`Config.Spotlight` = `SpotlightOn`); `SpotlightOff` — явный opt-in.                                                                                                                                                                                              |
| I-19 | `ChatStream` — расширение `Chat`, а не второй путь: цикл tools, RBAC, память, бюджет и трейс работают так же. Скользящее окно (`streamMinWindow` = 400 рун) отправляет каждый кусок ответа через рельс `output` до эмиссии, удерживая хвост `streamHoldback` = 96 рун; отказ рельса даёт `Retract` и аборт. Для клиента без `model.Streamer` — откат на буферизованный `Chat` одним чанком. Инвариант потока: `Done.Final.Content` равен конкатенации выданных `Text`.                                                                                    |
| I-20 | Остаточное ограничение стриминга документируется, а не скрывается: совпадение длиннее `streamHoldback` (96 рун) без границы предложения внутри всё ещё может пересечь окно и просочиться в уже выданный текст. Один проход guard на окно (не на весь ответ) — осознанная цена стриминга.                                                                                                                                                                                                                                                                  |

## 3. Контракты пакетов

### 3.1 `internal/core`

Добавляются sentinel-ошибки (и реэкспорт в `errors.go`):

```go
var (
	ErrTool             = errors.New("kit: tool error")        // падение внутри инструмента
	ErrBudget           = errors.New("kit: budget exceeded")   // лимит токенов хода
	ErrRateLimited      = errors.New("kit: rate limited")      // лимит на тенанта
	ErrApprovalRequired = errors.New("kit: approval required") // tool требует подтверждения
)
```

`Principal` получает метод `Validate() error`: `Tenant` обязателен (BR-49).

### 3.2 `store`

```go
// Request — явный контракт ES-запроса (I-12).
type Request struct {
	Method      string // GET, POST, PUT, DELETE
	Path        string
	Body        []byte
	ContentType string // "" -> application/json
	Refresh     string // "", "true", "wait_for" -> ?refresh=<v>
	Idempotent  bool   // можно ретраить без последствий
}

type ES interface {
	Do(ctx context.Context, req Request) (statusCode int, body []byte, err error)
}

func JSONRequest(method, path string, body any) (Request, error)
func NDJSONRequest(method, path string, body []byte) Request
func RawRequest(method, path string, body []byte, contentType string) Request
```

Поиск:

- убрать `"k"` из `knn`-query; `k` задаётся `size`;
- `msearchResponse` получает `Error json.RawMessage` и `Status int`; ошибка любой
  ветви — ошибка вызова; `_shards.failed > 0` — ошибка;
- `NumCandidates` клампится до `10000`; `TopK` валидируется (`1..1000`);
- RRF: детерминированный tie-break по `_id`;
- `SearchQuery` получает `Filter Filter`.

Фильтр метаданных (подмножество операторов, компилируется в ES-query):

```go
type Filter map[string]any // {"doc_id": "1", "position": map[string]any{"$gte": 2}}

// Поддерживаются: $eq, $ne, $gt, $gte, $lt, $lte, $in, $nin, $exists, $and, $or, $not.
// Разрешённые поля: doc_id, source, position, created_at, quarantined.
func CompileFilter(f Filter) (map[string]any, error)
```

Обслуживание (домены хранения, ретенция):

```go
func PruneBody(olderThan time.Duration) []byte        // delete_by_query по @timestamp
func ParseDeleteByQuery(body []byte) (deleted int, err error) // failures учитываются
```

Память (обычные индексы, не data stream):

```go
func MemoryMessagesIndexBody(dims int) []byte // vector/embed_model — только если dims > 0
func MemoryWorkingIndexBody() []byte

type MemoryMessage struct {
	ID, ThreadID, Tenant, UserID, Role, Content string
	Vector     []float32
	EmbedModel string
	Seq        int64 // монотонный ключ порядка; created_at (date) хранит только мс
	CreatedAt  time.Time
}
func MemoryBulkBody(msgs []MemoryMessage, dims int) ([]byte, error)

func MemoryHistoryBody(tenant, userID, threadID string, limit int) []byte
func MemoryRecallBody(tenant, userID string, vector []float32, topK, numCandidates int) ([]byte, error)
func MemoryClearBody(tenant, userID, threadID string) []byte

type WorkingMemory struct { ThreadID, Tenant, UserID string; Data map[string]any; UpdatedAt time.Time }
func MemoryWorkingID(tenant, threadID string) string // sha256-hex, path-safe

type MemoryHit struct { Role, Content string; CreatedAt time.Time; Score float64 }
func ParseMemorySearch(body []byte) ([]MemoryHit, error)
```

### 3.3 `trace`

```go
// Sink принимает готовые документы. Writer его реализует; Recorder — для тестов и eval.
type Sink interface {
	Enqueue(Trace)
	EnqueueObservation(Observation)
	EnqueueScore(Score)
}

func NewRecorder() *Recorder           // in-memory, потокобезопасный
func (r *Recorder) Snapshot() Snapshot // Traces, Observations, Scores

type TextPolicy int
const (
	TextMasked TextPolicy = iota // по умолчанию (I-07)
	TextFull                     // требует явного opt-in + slog.Warn при старте
)

type Sampler func(traceID string) bool
var SamplerAlways Sampler = func(string) bool { return true }
var SamplerNever  Sampler = func(string) bool { return false }
func SamplerRatio(p float64) Sampler   // решение детерминировано по trace_id
```

`Options` дополняется:

```go
type Options struct {
	ES store.ES
	Sinks []Sink       // writer всегда добавляется первым
	Text TextPolicy
	Sampler Sampler
	HideInput, HideOutput bool
	MaxStringLength int // по умолчанию 131072
	MaxDepth        int // 8
	MaxArrayLength  int // 50
	MaxObjectKeys   int // 50
	Processors []func(*Observation) // export-фильтр; может скрыть (nil -> drop)
	Logger *slog.Logger
	// QueueSize, BatchSize, FlushInterval, MaxBatchBytes, MaxRetries, RetryBackoff — как было.
}
```

`Trace`/`Observation` получают поля версии guard и политики текста:
`GuardVersion` (`<policy_hash>/<layer_id>`, имена слоёв через `+`, например
`L1+L2`), `GuardModel` (`Detector.Name()` слоя L2 — у поставляемого детектора
`"L2"`), `PolicyHash` (хэш `policy.yaml`) и `TextPolicy` (для аудита, I-08).

`Observation` получает `Tags []string` (отдельно от `Metadata`, item 9).

Правки писателя:

- нет гонки `Enqueue`/`Close` (закрытие через `chan struct{}` + mutex);
- ретрай идемпотентен: item-статус `409`/`version_conflict` считается успехом;
- `Close(ctx)` уважает контекст; остаток очереди дописывается;
- `lastErr` не липкий; `estimateSize` не маршалит дважды;
- детали ошибок берутся из `store.BulkErrors`.

### 3.4 `guard`

```go
// Action дополняется значением Detect (записать находку, не менять текст).
const (
	Allow Action = iota
	Detect
	Mask
	Reject
)

type Tripwire struct {
	Reason   string
	RuleID   string
	Metadata map[string]any
}

type Verdict struct {
	Action   Action
	Rail     Rail
	Findings []Finding
	Text     string
	Tripwire *Tripwire
	Version  string

	// Substitutions — значения, которые заменил mask (только при Action == Mask),
	// чтобы вызывающий мог вернуть их инструменту с AllowPII.
	Substitutions []Substitution
}

type Substitution struct {
	Placeholder string
	Category    Category
	Value       string `json:"-"`
}
```

- `Detector` больше не принимает rail: `Detect(ctx context.Context, text string) ([]Finding, error)`.
- `Options` дополняется `Version string` и `OnViolation func(Verdict)`;
  `Pipeline.Version()` возвращает версию (хэш имени слоёв + конфигурации).
- Решение — таблица `actionFor(rail, category)`; `Detect` возвращает true, если
  находка есть, текст не меняется.
- `canonicalize(text) string`: NFKC (`golang.org/x/text/unicode/norm`), case-fold,
  удаление zero-width/bidi, фолдинг кириллических гомоглифов, нормализация цифр.
- `decodedVariants(text) []string`: base64/hex/percent-encoded фрагменты,
  декодированные и переданные повторно в L1.
- L1 (`guard/rules`) дополняется: `Luhn` для карт, `IBAN mod-97`, `CVV`,
  `JWT`/`BEARER`/`GOOGLE_API_KEY`/`SLACK_TOKEN` как секреты, RU-паспорт, плюс
  regex-паттерны инъекций (не только дословные фразы).
- `maskRunes` остаётся, но перед маскированием спаны объединяются и
  сортируются; приоритет категорий задаётся таблицей.
- `AllowAll` переезжает в `guard/guardtest`.
- `Options.Optional []string` — имена необязательных слоёв; упавший optional-слой
  не роняет проверку, а помечает вердикт `Verdict.Degraded = true` и заполняет
  `Verdict.DegradedReason`. Деградировавшие вердикты не кладутся в кэш.
- `Verdict` получает `Degraded bool` и `DegradedReason string` (I-16).

### 3.5 `embed`

- `embed/local.go` — заглушка `//go:build !ort` (как сейчас).
- `embed/local_ort.go` — **реализация** `//go:build ort`:
  ONNX-энкодер из `internal/ort`, `daulet/tokenizers`, пресеты e5/bge-m3
  (mean/CLS pooling, префиксы, L2-норма), обрезка по `MaxLen`, батч ≤ 32,
  сортировка по длине для эффективности, `Dim()` определяется из выхода модели.
- `Preset.Validate()` проверяет пресет (в том числе загруженный из `kit_embedder.json`);
  `LocalConfig` получает поле `Sessions` (размер пула, по умолчанию `DefaultSessions` = 2).
- `NewLocal` под `!ort` возвращает `ErrInvalidConfig` (как сейчас).

### 3.6 `model`

```go
type FinishReason string
const (
	FinishStop          FinishReason = "stop"
	FinishToolCalls     FinishReason = "tool_calls"
	FinishLength        FinishReason = "length"
	FinishContentFilter FinishReason = "content_filter"
	FinishOther         FinishReason = "other"
)

type Response struct {
	Message      Message
	Usage        Usage
	Model        string
	FinishReason FinishReason
}

// Схемная инъекция (item 3): как передать JSON Schema провайдеру.
type SchemaMode int
const (
	SchemaNative SchemaMode = iota // response_format / format
	SchemaInline                   // добавить в последнее user-сообщение
	SchemaSystem                   // добавить в системное сообщение
)

type Request struct {
	Messages    []Message
	Tools       []ToolSpec
	Temperature *float64
	MaxTokens   int
	JSONSchema  json.RawMessage
	SchemaMode  SchemaMode
	ToolChoice  string // "", "auto", "none", "required"
	Seed        *int
	Stop        []string
	ProviderOptions map[string]any
}

// Capability сообщает, поддерживает ли провайдер/модель нативную схему.
func SupportsNativeSchema(client Client) bool

// Streamer — необязательное расширение Client (I-19). Клиент без него не
// реализует стриминг, и kit.ChatStream откатывается на буферизованный Chat.
type Streamer interface {
	Client
	ChatStream(ctx context.Context, req Request, onDelta func(string) error) (Response, error)
}
```

- Цепочка фолбэков и ретраев (item 8 §6 частично + §4.5):

```go
type ChainConfig struct {
	MaxRetries int           // на звено
	Backoff    time.Duration
}
func Chain(cfg ChainConfig, clients ...Client) (Client, error) // Client impl
```

- `model/history.go` (item 14):

```go
type Counter interface{ Count(text string) int }
type ByteCounter struct{}
func (ByteCounter) Count(text string) int // len(text)/4, документированное приближение

type History struct {
	Counter  Counter // обязателен; nil отключает обрезку
	Budget   int     // токены; <= 0 отключает обрезку
	KeepLast int     // минимум хвостовых сообщений, по умолчанию 2
}
// Trim сохраняет пары tool_call/tool, системный префикс и последнее сообщение.
func (h History) Trim(msgs []Message) []Message
```

- `model/structurer.go`: второй модельный вызов, который превращает прозу в JSON по схеме (Go-ответ на «structuring agent»).

```go
type Structurer struct {
	Client       Client
	Schema       json.RawMessage
	SchemaMode   SchemaMode // SchemaNative инлайнится, если клиент не умеет нативно
	MaxTokens    int
	Temperature  *float64
	Instructions string // заменяет встроенный префикс инструкции
}
// Structure просит клиента выразить prose как JSON по Schema и возвращает сырой текст
// (без валидации: декодирование — на вызывающем). Проза фенсится, чтобы читалась как данные.
func (s Structurer) Structure(ctx context.Context, prose string) (string, error)
```

- `Usage` получает `Cost float64`; добавлен тип `Pricing{InputPerMTok, OutputPerMTok}`
  с методом `Cost(Usage) float64`. `kit.Config.Pricing` задаёт цену модели; при
  ненулевом значении `Chat`/`Generate` заполняют `usage.cost` и `cost` (BR-77).

### 3.7 `eval` (новый пакет, item 7)

Только stdlib. Зависит от `trace`; **не** импортирует `kit`.

```go
// Trace — записанный трейс с его observation.
type Trace struct {
	Trace        trace.Trace
	Observations []trace.Observation
}

// Check возвращает nil, если трейс удовлетворяет проверке.
type Check func(t Trace) error

func Includes(s string) Check
func Excludes(s string) Check
func Equals(s string) Check
func Matches(re *regexp.Regexp) Check
func Similarity(want string, min float64) Check // Dice по биграммам слов
func CalledTool(name string) Check
func DidNotCall(name string) Check
func UsedNoTools() Check
func MaxToolCalls(n int) Check
func ToolOrder(names ...string) Check // подпоследовательность, не обязательно подряд
func NoToolErrors() Check
func All(fs ...Check) Check
func Any(fs ...Check) Check
func Not(f Check) Check

// DiceSimilarity — метрика, на которой построен Similarity.
func DiceSimilarity(a, b string) float64

// Recorded возвращает последний трейс рекордера.
func Recorded(rec *trace.Recorder) (Trace, error)

type Case struct {
	Name     string
	Input    string
	Checks   []Check
	Metadata map[string]any
}

type Result struct {
	Name    string
	Passed  bool
	Err     error
	Trace   Trace
	Elapsed time.Duration
}

type Report struct {
	Total, Passed, Failed int
	Results               []Result
}
func Summarize(results []Result) Report
func (r Report) OK() bool
func (r Report) String() string // одна строка на кейс

// Run сбрасывает рекордер перед каждым кейсом, прогоняет его через run и
// проверяет трейс. Ошибка run делает кейс провальным без проверок.
func Run(ctx context.Context, rec *trace.Recorder, cases []Case,
	run func(ctx context.Context, input string) error) []Result
```

Конвенции: tool-observation — это `Kind == trace.KindSpan`, имя — `Name`; текстовые
проверки читают `Trace.Output` (вход не инспектируется).

### 3.8 `kit` (фасад)

`Config` дополняется:

```go
type Config struct {
	// ... ES, LLM, Pricing, Embedder, Policy, GuardL2, GuardMode, GuardMaxChars,
	//     GuardCacheSize, Tokenizer, ChunkSize, ChunkOverlap, MaxSteps,
	//     MaxCallsPerTool, ToolTimeout, ArgsMaxBytes, TopK, Logger
	TraceText      trace.TextPolicy
	Sampler        trace.Sampler
	HideInput      bool
	HideOutput     bool
	Sinks          []trace.Sink
	MaxTotalTokens int          // бюджет хода (0 — без лимита)
	Egress         EgressPolicy // Rule of Two и allowlist инструментов
	Approver       Approver     // вызывается для tool с RequireApproval
	Spotlight      SpotlightMode  // I-18; ноль = SpotlightOn
	RateLimit      RateLimit      // лимит операций на тенанта; ноль = выкл.
	ResponseCache  ResponseCache  // LRU+TTL ответов модели без tools; ноль = выкл.
	MemoryLimit    int            // BR-82; DefaultMemoryLimit = 20
	MemoryRecall   int            // BR-82; 0 = выкл.
	BeforeToolCall BeforeToolHook // app-level hook; per-tool hook его заменяет
	AfterToolCall  AfterToolHook  // app-level hook; per-tool hook его заменяет
}

type EgressPolicy struct {
	RuleOfTwo bool // запрет эгресса, если на ходу был недоверенный ввод и приватные данные
}

// RateLimit — token bucket на тенанта (ключ — Principal.Tenant).
type RateLimit struct {
	PerSecond float64
	Burst     int
}

// ResponseCache кэширует ответы модели только для вызовов без tools.
type ResponseCache struct {
	Size int           // число записей; 0 — кэш выключен
	TTL  time.Duration // 0 — без истечения
}

// SpotlightMode управляет пометкой недоверенного контента (I-18).
type SpotlightMode int

const (
	SpotlightOn  SpotlightMode = iota // по умолчанию
	SpotlightOff
)

// ChatStream — стриминг расширением Chat (I-19). Вызывающий обязан вычитать
// канал до закрытия.
func (a *App) ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)

type StreamChunk struct {
	Text    string        // подтверждённый, уже проверенный текст
	Retract bool          // клиент отбрасывает показанное; поток абортится ошибкой
	Done    bool          // последний чанк, с собранным Final
	Final   model.Message
	Err     error
}
```

Новые/изменённые операции:

- `Generate[T]` — через примитив: `principal` → `PermGenerate` → input-guard →
  модель → output-guard; `System` идёт ролью `system`; ответ валидируется по схеме
  `T` до декодирования; поддержка `ErrorStrategy` (`strict`/`warn`/`fallback`),
  `SchemaMode`, `Decoded`, `UsedFallback`.
  `GenerateRequest[T].StructuringModel` — второй модельный вызов, который при
  неудачном прямом декодировании переписывает прозу в схему **до** применения
  `ErrorStrategy`; он пишется отдельной observation и не добавляет usage к итогу хода.
- `SearchRequest.Filter store.Filter`.
- `Ingest` — валидация размерности векторов, удаление прежних чанков документа
  перед записью новых (иначе остаются хвосты), карантин не индексирует текст.
- `Delete` — проверяет `deleted`/`failures`; корректная метка observation.
- `Prune(ctx, olderThan)` — удаление старых трейсов (ретенция).
- `RetrievalTool(app, name, opts...)` — инструмент поиска по KB как типизированный `Tool`
  (item 10).
- `CallTool(ctx, name, args)` (D54) — вызов одного инструмента тем же путём, что
  `Chat` (право, валидация, approval, egress, хуки, guard `tool_result`);
  неопределённый инструмент — `ErrToolNotFound`. Нужен не-модельным вызывающим
  (MCP-сервер, `mcp.FromTool`).

`Tool` дополняется опциями (items 4, 5, 12, I-10):

```go
func Effects(e Effects) ToolOption            // Read | Write | Egress
func EgressHosts(hosts ...string) ToolOption  // allowlist для Egress
func EgressAny() ToolOption                   // явный отказ от allowlist: любой хост
func AllowPII(b bool) ToolOption              // подставлять реальные значения (BR-04)
func RequireApproval(b bool) ToolOption
func ValidateOutput(b bool) ToolOption        // fail-closed проверка выхода (D61)
func ModelView(f func(string) string) ToolOption // item 4: что видит модель
func BeforeToolCall(h BeforeToolHook) ToolOption // per-tool hook заменяет Config.BeforeToolCall
func AfterToolCall(h AfterToolHook) ToolOption   // per-tool hook заменяет Config.AfterToolCall

// ToolCallInfo — что видит хук; Fingerprint привязан к tool + каноническим args.
type ToolCallInfo struct {
	UserID, Tenant, Tool, Args, Fingerprint string
	Effect                                 Effect
	CallIndex                             int // 1-based в этом ходе
}
type BeforeToolHook func(ctx context.Context, info ToolCallInfo) (proceed bool, output string, err error)
type AfterToolHook func(ctx context.Context, info ToolCallInfo, result string, err error) (string, error)
```

`ToolDef` получает `effects`, `egress`, `requireApproval`, `validateOutput`,
`modelView`, `outputSchema`.

Цикл `Chat`:

- ошибка инструмента возвращается модели как результат с `isError` (I-11);
- «ходовые» хуки: до вызова — RBAC, approval (по fingerprint, consume-once), egress-политика, затем `BeforeToolCall`; после исполнения — `AfterToolCall`, за ним guard результата (**последним**), `ValidateOutput`, `ModelView`. Ошибка хука становится результатом-ошибкой модели (I-11), если она не фатальна.
- `ArgsMaxBytes`, `max_steps`, `max_calls`, timeout — как было;
- бюджет хода (`MaxTotalTokens`) → `ErrBudget`.

Память (item 2.1):

```go
type ChatRequest struct {
	// ... System, Messages, SessionID, Temperature, MaxTokens, ToolChoice, Tags
	ThreadID string // задан — Chat читает и пишет память треда
}
// Config получает MemoryLimit (DefaultMemoryLimit = 20) и MemoryRecall (0 — выкл.).

func (a *App) MemoryLoad(ctx context.Context, threadID string, limit int) ([]model.Message, error)
func (a *App) MemoryAppend(ctx context.Context, threadID string, msgs ...model.Message) error
func (a *App) MemoryRecall(ctx context.Context, query string, topK int) ([]MemoryHit, error)
func (a *App) MemoryClear(ctx context.Context, threadID string) (int, error)
func (a *App) MemoryWorking(ctx context.Context, threadID string) (map[string]any, error)
func (a *App) MemorySetWorking(ctx context.Context, threadID string, data map[string]any) error

type MemoryHit struct {
	Role      model.Role
	Content   string
	Score     float64
	Untrusted bool
	CreatedAt time.Time
}
```

- `Chat` с `ThreadID`: `MemoryLoad` (последние `MemoryLimit` сообщений) перед новым ходом, `MemoryAppend` (сообщения хода + ответ) после успеха; при `MemoryRecall > 0` и наличии эмбеддера в системное сообщение добавляется блок recall.
- Перенос taint между ходами (D59): сообщение хранит признак `untrusted`; недоверенная строка истории или recall снова помечает ход для Rule of Two.
- Право `memory` (`authz.PermMemory`); `internal/call` получает `KindMemory`; каждый публичный memory-вызов создаёт trace (`name = "memory"`, `metadata.op`).
- `store.EnsureSchema` создаёт `kit-memory-messages` и `kit-memory-working`.

## 4. Решения (дополнение к decisions.md)

| #   | Решение                                                                                                | Обоснование                                                                                                                                                                                          | Отвергнуто                     |
| --- | ------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------ |
| D21 | `store.ES` принимает `store.Request` с типом контента, refresh и флагом идемпотентности                | тип контента нельзя выводить из подстроки пути (I-12)                                                                                                                                                | эвристика по пути              |
| D22 | Guard: детекция один раз, решение по таблице; каноникализация перед слоями                             | измеренный класс атак на уровне символов; −работы на рельсах                                                                                                                                         | rail-зависимые детекторы       |
| D23 | Политика текста трейса — `masked` по умолчанию; `full` требует opt-in                                  | соответствие BR-41 (I-07)                                                                                                                                                                            | сырой текст по умолчанию       |
| D24 | Sampling трейсов детерминирован по `trace_id`                                                          | воспроизводимость eval и снижение нагрузки на ES                                                                                                                                                     | случайная выборка              |
| D25 | Эгресс — свойство инструмента + Rule of Two в рантайме                                                 | защита от эксфильтрации без ML                                                                                                                                                                       | доверие к модели               |
| D26 | Ошибка инструмента — результат для модели                                                              | цикл не рушится на первой ошибке инструмента                                                                                                                                                         | падение хода                   |
| D27 | Guard версионируется строкой «хэш политики + имена слоёв»; версия и хэш политики пишутся в трейс       | апгрейд guard — смена политики, а не патч                                                                                                                                                            | безусловный кэш вердиктов      |
| D28 | L2 отвечает за семантику (safety и таксономия), L1 — за структурированные ID (карты, ИНН, SNILS, IBAN) | данные GLiNER Guard: model-only F1 на RU-ID ≈ 0; правила дешевле; Go-сторона поддерживает полную таксономию модели (N групп, `thresholds`, `category_map`), поставляемый артефакт — компактный (D35) | ML на структурированных ID     |
| D29 | Схемная инъекция выбирается по возможностям провайдера (`SchemaMode`)                                  | провайдеры различаются в поддержке `response_format`                                                                                                                                                 | обязательный `response_format` |
| D30 | Пакет `eval` с детерминированными проверками вместо LLM-судей                                          | воспроизводимо, без стоимости и без сети                                                                                                                                                             | LLM-as-judge для авторизации   |

## 5. Что изменилось в API (справочник для документации)

Этот раздел — единственный авторитетный список изменений v1; документация и
README описывают именно это поведение.

### 5.1 `store`

- `ES` теперь `Do(ctx, store.Request) (int, []byte, error)`;
  `Request{Method, Path, Body, ContentType, Refresh, Idempotent}` и
  `Request.EffectivePath()`; хелперы `JSONRequest`, `NDJSONRequest`, `RawRequest`.
  Тип контента и `refresh` — поля запроса, клиент пользователя их применяет (I-12).
- Поиск: в kNN-запросе нет `k` (`k` = `size`); ошибка любой ветви `_msearch` и
  `_shards.failed > 0` — ошибка вызова; `TopK` клампится до 1000, `num_candidates`
  — до 10000; RRF с детерминированным tie-break по `_id`.
- `store.Filter` (item 11): операторы `$eq $ne $gt $gte $lt $lte $in $nin $exists $and $or $not`,
  поля `doc_id source position created_at quarantined`; `store.CompileFilter`
  компилирует в ES-query и AND-ится с ACL-фильтром.
- `store.ValidateChunk`, `store.ChunksBulkBody(chunks, dims)`; карантинный чанк не
  несёт ни текста, ни вектора.
- `store.DeleteByDocBody`, `store.ParseDeleteByQuery`, `store.PruneBody`.
- `store.IsVersionConflict`, `store.VersionConflictError`, `store.BulkErrors`.

### 5.2 `trace`

- `Sink`, `Recorder`, `Snapshot`, `Fanout`; `Writer` реализует `Sink`.
- `TextPolicy` (`TextMasked` по умолчанию / `TextFull`), `Sampler`
  (`SamplerAlways`, `SamplerNever`, `SamplerRatio`, `SamplerFromString`).
  Решение сэмплера детерминировано по `trace_id`.
- `Options`: `Sinks`, `Text`, `Sampler`, `HideInput`, `HideOutput`,
  `MaxStringLength` (131072), `MaxDepth` (8), `MaxArrayLength` (50),
  `MaxObjectKeys` (50), `Processors` (очистка `Name` удаляет observation).
- Поля `Trace`/`Observation`: `Tags`, `GuardVersion`, `GuardModel`, `PolicyHash`,
  `TextPolicy`.
- Писатель: нет гонки `Enqueue`/`Close`, идемпотентные ретраи (409 = успех),
  `Close(ctx)` учитывает контекст, ошибка не липкая.

### 5.3 `guard`

- `Detector.Detect(ctx, text)` — без рельса (I-02); детекция один раз, решение по
  таблице `decide(rail, findings)`; кэш по каноническому тексту и версии.
- `Canonicalize`, `DecodedVariants` (I-03); `Action` дополнен `Detect`;
  `Verdict.Tripwire`, `Verdict.Version`; `Tripwire{Reason, RuleID, Metadata}`;
  `Options.Version`, `Options.OnViolation`, `Pipeline.Version()`.
- `Options.Version`/`Pipeline.Version()` — строка `<policy_hash>/<layer_id>`
  (`layer_id` — имена детекторов через `+`; `kit.New` строит её из хэша политики
  и `layerID`), а аудит-поле `guard_model` — `Detector.Name()` слоя L2 (`"L2"`),
  не имя каталога модели (I-08).
- L1 расширен: `BANK_CARD` (Luhn), `IBAN` (mod-97), `CVV`, `PASSPORT`,
  `JWT`, `BEARER`, `GOOGLE_API_KEY`, `SLACK_TOKEN`; regex-паттерны инъекций.
- `AllowAll` переехал в `guard/guardtest` (I-15).
- На рельсе `output` категории INJECTION/UNSAFE дают `Detect`, а не `Mask`.
- L2 GLiNER: метки сущностей → `PII`; метки классификации маппятся так: явный `category_map` переопределяет всё; значение-категория (`PII`/`SECRET`/`INJECTION`/`UNSAFE`) задаёт категорию; значение **`SAFE`** подавляет находку для этой метки (`category_map` принимает именно эти пять значений); без маппинга работает эвристика (`unsafe/harmful/...` → `UNSAFE`, `inject/jailbreak/attack/override/exfiltrat/abuse` → `INJECTION`), а встроенный список безопасных имён даёт «ничего не сообщать». Незнакомая, немаппленная метка fail-safe → `UNSAFE` (I-14). Go принимает N групп классификации: у каждой своё имя `output`, `multi_label` + `sigmoid`, пороги на метку/группу (`thresholds`). Поставляемый по умолчанию экспорт компактен (`person/email/phone` + `safe/unsafe`); богатый (32 сущности, 5 групп, 70 меток классификации; адверсариальные метки дают `INJECTION`) собирается `export.py --schema full` (D28, D35).
- L2 получает окно детекции целиком, но тензорный вход внутри окна ограничен `Config.MaxTokens` (`DefaultMaxTokens` = 512, кламп до `max_sequence_length` модели); текст дальше покрывают следующие окна (D34, D60).
- `Canonicalize` — фиксированная точка: невалидный UTF-8 санируется, затем NFKC →
  `fold` → снова NFKC (D56).
- `Options.WindowChars` — окно детекции: текст длиннее сканируется перекрывающимися
  окнами (D60); `DefaultWindowChars` = 1024.

### 5.4 `embed`

- `NewLocal` реализован под `-tags ort`: ONNX-энкодер, пресеты e5 (mean,
  префиксы, L2) и bge-m3 (CLS, L2), `kit_embedder.json`, батч 32, `max_len` 512,
  размерность берётся из графа; `LocalConfig.Sessions` (по умолчанию 2).
  Под `!ort` — прежняя заглушка `ErrInvalidConfig`.

### 5.5 `model`

- `Response.FinishReason` (`FinishStop/ToolCalls/Length/ContentFilter/Other`),
  `ParseFinishReason`; `Request.SchemaMode` (`SchemaNative/Inline/System`),
  `SupportsNativeSchema`; `Request.ToolChoice`, `Seed`, `Stop`, `ProviderOptions`.
- `Streamer` (`ChatStream(ctx, req, onDelta)`) — необязательное расширение `Client`;
  `model/openai_stream.go` реализует его для `openai`/`vllm` (SSE, сборка
  фрагментов tool_calls по индексу, `stream_options.include_usage`). `ollama` и
  `gigachat` стриминг не реализуют и идут через откат (I-19).
- `Chain(ChainConfig, ...Client)`: фолбэк-цепочка с ретраями и `Retry-After`.
- `Chain` не реализует `SupportsNativeSchema`, поэтому `SupportsNativeSchema(chain)`
  = `false`: `kit.Generate`/`model.Structurer` инлайнят схему даже при
  `SchemaNative`, если все звенья цепочки умеют нативную.
- `History`/`Counter`/`ByteCounter`: обрезка истории по бюджету с сохранением пар
  `tool_call`/`tool` и стабильного системного префикса.
- `internal/httpjson`: `Config`, `NewClient` (ретраи, `Retry-After`),
  `StatusError`, `IsRetryable`, `RetryAfter`.

### 5.6 `kit`

- `Generate[T](ctx, app, GenerateRequest[T]) (Generated[T], error)` — полноценная
  операция примитива: принципал, право `generate`, input/output guard, трейс;
  `System` уходит ролью `system`; ответ проверяется по схеме `T`; `ErrorStrategy`
  `Strict/Warn/Fallback` и флаги `Generated.Decoded`/`UsedFallback`; при
  `SchemaNative` и отсутствии поддержки провайдера схема автоматически инлайнится.
- Права: добавлены `generate` и `prune`; `Ingest`, `Search`, `Delete`
  требуют непустой `Tenant` (иначе `ErrUnauthorized`). `Prune` требует
  принципала и право `prune`, но не тенант: это глобальное действие
  эксплуатации по времени (как ILM), а не тенант-скоупная операция.
- `Tool`-опции: `Effects`, `EgressHosts`, `EgressAny`, `RequireApproval`,
  `ValidateOutput`, `ModelView`, `BeforeToolCall`, `AfterToolCall`; `Config.Approver`,
  `ApproverFunc`, `ApprovalRequest.Fingerprint` (хэш `tool` + канонических аргументов).
  Хук на инструменте **заменяет** хук приложения (`Config.BeforeToolCall`/
  `AfterToolCall`), а не складывается с ним; `ToolCallInfo` несёт `Fingerprint` и
  `CallIndex`. Одобрение расходуется один раз за ход (повтор того же fingerprint →
  `ErrApprovalRequired`).
- `GenerateRequest[T].StructuringModel` — второй модельный вызов при неудачном прямом
  декодировании; `model.Structurer` — сама операция структурирования.
- `kit.WithTraceMetadata(ctx, md)` / `kit.TraceMetadataFrom(ctx)` — request-scoped
  метаданные, которые примешиваются в `metadata` каждой observation (per-call значение
  перекрывает значение из контекста).
- `Config.MaxTotalTokens` → `ErrBudget`; `Config.Egress.RuleOfTwo`;
  `Config.TraceText`, `Config.Sampler`, `Config.HideInput`, `Config.HideOutput`,
  `Config.Sinks`.
- `Config.Spotlight` (`SpotlightOn`/`SpotlightOff`) и типы `SpotlightMode`,
  `RateLimit{PerSecond, Burst}`, `ResponseCache{Size, TTL}`; `App.ChatStream` и
  `StreamChunk` (см. §5.12).
- Ошибка инструмента возвращается модели как `{"error": "..."}` (I-11); отказ
  RBAC и ошибки провайдера по-прежнему прерывают ход; неизвестный инструмент —
  результат-ошибка; на последнем шаге цикла `tool_choice` = `none`.
- `Ingest` удаляет прежние чанки документа, валидирует размерность вектора;
  `Delete` проверяет `deleted`/`failures`; новый `App.Prune(ctx, olderThan)`.
- Новый `kit.RetrievalTool(app, name, opts...)` — поиск как типизированный tool.
- `kit.GuardRejectError` (алиас типа из `internal/call`, доступен через
  `errors.As`): `Rail`, `Tripwire`, `Version`; `errors.Is(err, kit.ErrGuardReject)`
  сохраняется.
- Новые sentinel-ошибки: `ErrTool`, `ErrBudget`, `ErrRateLimited`,
  `ErrApprovalRequired`.

### 5.7 `eval` (новый пакет)

- `Check`, `Trace`, `Case`, `Result`, `Report`, `Summarize`, `Run`, `Recorded`,
  `DiceSimilarity`; проверки `Includes`, `Excludes`, `Equals`, `Matches`,
  `Similarity`, `CalledTool`, `DidNotCall`, `UsedNoTools`, `MaxToolCalls`,
  `ToolOrder`, `NoToolErrors`, `All`, `Any`, `Not`.
- Применяется с `trace.NewRecorder()` и `kit.Config.Sinks`; только stdlib;
  не импортирует `kit`.

### 5.8 `flow`, `webfetch`, `cmd/kitctl verify` и новые хелперы

- `flow` (новый пакет): `State`, `Status` (`success`/`failed`/`suspended`),
  `Suspension`/`Suspend`, `Step[In,Out]`, `New`, `NewResumable`, `Result[Out]`;
  `Step.Run`/`Step.Resume` через `Result.Snapshot`.
  Схемы выводятся из Go-типов, входы/выходы/resume валидируются в рантайме.
  Branch/parallel/foreach/loop DSL и объект `Pipeline` намеренно отсутствуют
  (D31; `Pipeline`/`Then`/`Chain` удалены в D47).
- `webfetch` (новый пакет): `Config` (`AllowHosts`, `MaxBytes`, `Timeout`,
  `MaxRedirects`, `Headers`), `In` (`url`), `Out` (`status`, `content_type`,
  `text`, `truncated`), `New(app, name, cfg)`. Регистрируется через
  `Effects(EffectEgress)` + `EgressHosts(...)` (при пустом `AllowHosts` — `EgressAny()`),
  поэтому host-allowlist и Rule of Two
  рантайма применяются тоже; SSRF-проверки — в dialer'е (включая CGNAT `100.64.0.0/10`
  и ULA `fc00::/7`), с потолками редиректов и тела; не-2xx — данные (D33).
- `cmd/kitctl verify` (новые подкоманды): `window` (I-06), `models`
  (`gliner.VerifyDir`/`embed.VerifyDir`), `parity` (`gliner.CheckParity`, требует
  `-tags ort`), `latency`. Флаги принимаются до и после позиционных аргументов.
- Новые экспортированные хелперы: `gliner.VerifyDir`, `gliner.CheckParity`/
  `gliner.Report`, `embed.VerifyDir`, `model.Structurer`,
  `gliner.Config.MaxTokens` (`gliner.DefaultMaxTokens` = 512, кламп до
  `max_sequence_length` модели = 2048).
- CI: `.github/workflows/ci.yml` (build, test, race, ort-vet, lint, fumpt),
  `.golangci.yml` (`gofumpt`, `govet`, `errcheck`, `staticcheck`, `gosec`, `depguard`
  и др.), `Makefile` (`make check`) и `internal/importrules` (I-13).

### 5.9 Часть 1 шлифовки (P0)

- `kit.Config.GuardMode` (`GuardStrict`/`GuardDegraded`), `guard.Options.Optional`,
  `guard.Verdict.Degraded`/`DegradedReason`; observation несёт `guard_degraded` (I-16).
- `kit.Config.Pricing` (`model.Pricing`), `model.Usage.Cost`; `usage.cost`/`cost`
  заполняются framework'ом (BR-77).
- Право `chat` на операцию `Chat` (`authz.PermChat`, BR-76).
- `store.Chunk.EmbedModel` + поле `embed_model` в `kit-chunks`;
  `store.SearchQuery.EmbedModel` фильтрует поиск по текущей модели эмбеддера (BR-79).

### 5.10 Память (часть 2.1)

- `store`: индексы `kit-memory-messages` / `kit-memory-working`;
  `MemoryMessage`/`WorkingMemory`/`MemoryHit`; `MemoryMessagesIndexBody`,
  `MemoryWorkingIndexBody`, `MemoryBulkBody`, `MemoryHistoryBody`,
  `MemoryRecallBody`, `MemoryClearBody`, `MemoryWorkingID`, `ParseMemorySearch`.
- `kit`: `ChatRequest.ThreadID`, `Config.MemoryLimit`, `Config.MemoryRecall`;
  операции `MemoryLoad/Append/Recall/Clear/Working/SetWorking`;
  `MemoryHit`, `DefaultMemoryLimit` = 20, `DefaultMemoryTopK` = 5 (I-17).
- `authz.PermMemory` (`memory`); `call.KindMemory` (`memory`).

### 5.11 Часть 2.2–2.6 (spotlighting, лимиты, ingest, кэш, OTLP)

- **Spotlighting (2.2, I-18):** `Config.Spotlight` (`SpotlightOn`/`SpotlightOff`);
  `spotlight()` оборачивает результат инструмента (после `ModelView`) в
  `<untrusted-data>\n…\n</untrusted-data>`, в системный промпт через `joinSystem`
  добавляется напоминание (`spotlightReminder`) трактовать блок как данные.
- **Rate limit (2.4):** `internal/ratelimit` (token bucket на ключ с эвикцией
  простаивающих); `kit.RateLimit{PerSecond, Burst}` и `App.limiter`; применятся в
  `Chat`/`ChatStream`/`Ingest`/`Search`/`Delete`/`Generate`/`Score` и memory-операциях;
  исчерпание — `ErrRateLimited`.
- **Атомарный ingest (2.5):** `store.Chunk.DocVersion`, поле `doc_version` (`long`),
  id чанка `<docID>#<version>#<i>`; новый `store.DeleteByDocVersionBody(docID, tenant,
keepVersion)`. `Ingest` пишет новые чанки (`_bulk?refresh=wait_for`) **первым**
  действием и только затем удаляет прежние версии (`_delete_by_query` с
  `doc_id`+`tenant` и `must_not[term doc_version]`) — сбой между шагами даёт
  дубликаты (самоисцеляющиеся), но не потерю (BR-87).
- **Кэш ответов (2.6):** `internal/cache` (LRU+TTL); `kit.ResponseCache{Size, TTL}`;
  ключ — sha256 от `{model, messages, tools, temperature, maxTokens, toolChoice,
jsonSchema, schemaMode, seed, stop}`. Кэшируются только ответы **без tools**;
  попадание — `metadata.cache = "hit"`, стоимость `0`, токены из кэшированного
  ответа (BR-88).
- **OTLP-синк (2.3):** `trace.NewOTLP(trace.OTLPOptions{...})` реализует `trace.Sink`
  (OTLP/HTTP JSON, stdlib); детерминированные id (sha256), атрибуты `gen_ai.*` и
  `kit.*`, scores — событием `score`. Подключается через `Config.Sinks`.

### 5.12 Стриминг (часть 3, I-19/I-20)

- `model.Streamer` (`ChatStream`) и `model/openai_stream.go` (SSE для
  `openai`/`vllm`; `ollama` и `gigachat` идут через откат).
- `App.ChatStream(ctx, req) (<-chan StreamChunk, error)`, `StreamChunk{Text, Retract,
Done, Final, Err}`; скользящее окно `windowGuard` (`streamMinWindow` = 400,
  `streamHoldback` = 96, резка по границе `\n . ! ? ; :`); один проход guard на
  окно; `Retract` при отказе рельса `output`. Клиент без `model.Streamer`
  получает буферизованный `Chat` одним чанком `Text` + `Done`. Вызывающий обязан
  вычитать канал до закрытия.

### 5.13 Часть 3 (сужение `flow`, health, реранкинг, поиск инструментов, MCP)

- **`flow` сужен (D47):** удалены `Pipeline`, `Then`, `Chain`, `Chain3`,
  `Step.Pipeline`, `StepResult`; остались `Step[In,Out]` с `Run(ctx, st, in)` и
  `Resume(ctx, snapshot, data)` и `Result[Out]{Status, Output, Error, Suspension,
Snapshot, State}`.
- **Health (D48):** `internal/core` получает `HealthStatus{OK, Detail, InUse,
Capacity}` и `HealthChecker`; `kit` их алиасит (`kit.HealthStatus`,
  `kit.HealthChecker`). `trace.Writer.QueueDepth`/`QueueCapacity`, `ort.Pool.Stats`;
  `guard/gliner` и локальный `embed` реализуют `Health()`. `App.Health()`
  возвращает `Health{Healthy, Degraded, GuardMode, GuardVersion, TraceQueue,
TraceQueueCap, TraceDropped, Components}`.
- **Реранкинг (D49):** новый пакет `rerank` (`Reranker`, `Rank`, `Reorder`,
  `TEIConfig`/`NewTEI`); `Config.Reranker`, `Config.RerankCandidates`,
  `SearchRequest.Rerank`; `call.KindRerank`; кандидатов `TopK ×
DefaultRerankMultiplier`.
- **Поиск по инструментам (D50):** `Config.ToolSearch{Enabled, Threshold, TopK}`,
  `DefaultToolSearchThreshold` = 20, `DefaultToolSearchTopK` = 8; `allowedToolSpecs`
  учитывает последнее сообщение пользователя; лексическое ранжирование
  (`toolsearch.go`).
- **MCP и `RawTool` (D51):** `kit.RawTool(app, name, schema, fn, opts...)` —
  инструмент со схемой дословно. Новый пакет `mcp`: `New`, `Initialize`,
  `ListTools`, `CallTool`, `Close`, `ServerInfo`, `Tool`, `CallResult`, `Content`;
  транспорты stdio и streamable HTTP; `Register(ctx, app, client, opts...)`
  регистрирует `mcp:<server>:<tool>` с эффектом `Egress`.
- **MCP-сервер и `CallTool` (D54):** `mcp.NewServer(ServerConfig{Name, Version, Tools,
AllowedOrigins, Logger})`, `mcp.ServerTool`, `mcp.FromTool(app, def)`,
  `Server.ServeStdio(ctx, in, out)` и `Server.Handler()` (streamable HTTP, только
  `POST`). Методы `initialize`, `ping`, `tools/list`, `tools/call`; неизвестный
  инструмент — ошибка JSON-RPC, ошибка обработчика — `CallResult.IsError`; запрос с
  заголовком `Origin` отклоняется, пока источник не в `AllowedOrigins`.
  `kit.App.CallTool(ctx, name, args)` — одиночный вызов инструмента через примитив.
- **Возврат PII инструменту (D55):** `guard.Verdict.Substitutions`
  (`Substitution{Placeholder, Category, Value}`; `Value` помечен `json:"-"`)
  заполняется при `Mask`; `OnViolation` получает вердикт без сырых значений.
  `kit.AllowPII(true)` разрешает подстановку значений в args инструмента — только
  при однозначном отображении заглушки и только внутри вызова; результат
  маскируется обратно. Замена формата заглушки не потребовалась.

### 5.14 Фаззинг и фиксированная точка каноникализации (D56)

- `FuzzCanonicalize`, `FuzzDecodedVariants` (`guard`), `FuzzCompileFilter`,
  `FuzzRRF` (`store`) — stdlib-фаззинг; находки пишутся в `testdata/fuzz` как
  регрессия.
- `Canonicalize` — фиксированная точка: невалидный UTF-8 санируется
  (`strings.ToValidUTF8`) до первого NFKC, затем `fold` (регистр, гомоглифы,
  невидимые, полноширинные цифры), затем ещё один NFKC.

### 5.15 Хуки фасада (D57)

- `Config.GuardOnViolation func(guard.Verdict)` — колбэк на каждый вердикт, кроме
  allow; передаётся в `guard.Options.OnViolation` без сырых значений (`AllowPII`).
- `Config.TraceProcessors []func(*trace.Observation)` — правки наблюдения до
  записи и до sink'ов; очистка `Name` отбрасывает документ.

### 5.16 Валидация ответа `Generate` (D58)

- Ответ разбирается в `any` и проверяется скомпилированной схемой, затем декодируется
  в `T`: `json.Unmarshal` один принимал документ с отсутствующим или лишним полем.
- `Generated[T].Decoded` — `true`, если ответ совпал со схемой; при `Warn`/`Fallback`
  остаётся `false`.

### 5.17 Перенос taint между ходами (D59)

- `store.MemoryMessage`/`MemoryHit` несут `Untrusted bool` (`untrusted` в
  `kit-memory-messages`); `MemoryHistoryBody` и `MemoryRecallBody` запрашивают поле.
- `memoryLoad` возвращает признак недоверенности, `memoryAppend` его пишет; `Chat`
  и `ChatStream` помечают ход (`turn.untrusted`), если история или recall недоверенны.

### 5.18 Окно детекции, fail-closed `ValidateOutput`, русский анализатор (D60–D62)

- `guard.Options.WindowChars` (дефолт `DefaultWindowChars` = 1024,
  `Config.GuardWindowChars`): `Pipeline.windows` режет канонический текст на
  перекрывающиеся окна, находки смещаются на смещение окна и дедуплицируются.
- `ValidateOutput(true)` — fail-closed: несовпадение со схемой `Out` даёт модели
  error-result и помечает ход недоверенным (раньше — только `slog.Warn`).
- `store.ChunksIndexBody`/`MemoryMessagesIndexBody` создают текстовые поля с
  анализатором `russian` (`textField()`).
