# Справочник kit

Полный справочник фасада `github.com/gobellim/kit`: конфигурация, жизненный цикл,
операции, типы, инструменты и ошибки.

Все операции идут через единый примитив `internal/call` (`authz → guard → span →
fn`). Добавлять middleware или обходить политику не нужно и нельзя.

## Жизненный цикл

### kit.Config

Поля конфигурации. Значения по умолчанию применяются в `kit.New`, если поле
оставлено нулевым.

| Поле              | Тип               | По умолчанию                  | Назначение                                                              |
| ----------------- | ----------------- | ----------------------------- | ----------------------------------------------------------------------- |
| `ES`              | `store.ES`        | — (обязательно)               | Клиент Elasticsearch пишет пользователь.                                |
| `LLM`             | `model.Client`    | — (обязательно)               | Клиент чата.                                                            |
| `Embedder`        | `embed.Embedder`  | `nil`                         | Нужен только для `Ingest` и `Search`; чат-приложение может не задавать. |
| `Policy`          | `*authz.Policy`   | — (обязательно)               | Разобранный `policy.yaml`.                                              |
| `GuardL2`         | `guard.Detector`  | `nil`                         | ML-слой guard; при `nil` остаётся только детерминированный L1.          |
| `GuardMaxChars`   | `int`             | `8000`                        | Лимит текста для guard.                                                 |
| `GuardCacheSize`  | `int`             | `4096`                        | Размер LRU-кэша вердиктов guard.                                        |
| `Tokenizer`       | `chunk.Tokenizer` | `chunk.WhitespaceTokenizer{}` | Токенайзер чанкера (в ORT-сборке — из `internal/ort`).                  |
| `ChunkSize`       | `int`             | `400`                         | Токенов в чанке.                                                        |
| `ChunkOverlap`    | `int`             | `80`                          | Перекрытие чанков.                                                      |
| `MaxSteps`        | `int`             | `8`                           | Лимит цикла «модель → инструменты → модель».                            |
| `MaxCallsPerTool` | `int`             | `3`                           | Лимит вызовов одного инструмента.                                       |
| `ToolTimeout`     | `time.Duration`   | `30s`                         | Таймаут одного вызова инструмента.                                      |
| `ArgsMaxBytes`    | `int`             | `32768`                       | Лимит размера аргументов инструмента.                                   |
| `TopK`            | `int`             | `10`                          | Хитов поиска по умолчанию.                                              |
| `TraceStoreRaw`   | `bool`            | `false`                       | Писать в трейсы необработанный текст вместо маскированного.             |
| `Logger`          | `*slog.Logger`    | `slog.Default()`              | Логи; тексты сообщений в них не попадают.                               |

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

Закрывает очередь trace-писателя и дописывает остаток. Вызывайте при остановке
сервиса.

## Операции

### Chat

```go
func (a *App) Chat(ctx context.Context, req ChatRequest) (model.Message, error)
```

Гоняет guard, модель и инструменты в цикле и возвращает финальное сообщение.
Стриминга нет.

```go
msg, err := app.Chat(ctx, kit.ChatRequest{
    System:   "Ты ассистент.",
    Messages: []model.Message{{Role: model.RoleUser, Content: "Привет"}},
})
```

### Ingest

```go
func (a *App) Ingest(ctx context.Context, doc Doc) error
```

Чанкует документ, считает ACL и эмбеддинги, пишет чанки в Elasticsearch.
Отклонённый guard чанк уходит в карантин и не индексируется.

### Search

```go
func (a *App) Search(ctx context.Context, req SearchRequest) ([]SearchHit, error)
```

Гибридный поиск: kNN и BM25 через `_msearch`, слияние RRF в Go, top-k. Всегда
добавляет ACL-фильтр принципала.

### Delete

```go
func (a *App) Delete(ctx context.Context, docID string) error
```

Удаляет чанки документа по `doc_id` с фильтром `tenant` принципала.

### Score

```go
func (a *App) Score(ctx context.Context, s trace.Score) error
```

Присваивает trace оценку. Требует права `trace`. Пишет документ в `kit-traces`.

### Generate

```go
func Generate[T any](ctx context.Context, app *App, req GenerateRequest) (T, error)
```

Просит модель вернуть значение типа `T` по JSON Schema, выведенной из `T`, и
декодирует ответ в `T`.

```go
type Verdict struct {
    OK     bool   `json:"ok"`
    Reason string `json:"reason"`
}

v, err := kit.Generate[Verdict](ctx, app, kit.GenerateRequest{Prompt: "Проверь заявку"})
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
| `Tenant` | `string`   | Тенант; ограничивает запись и поиск.             |

### ChatRequest

```go
type ChatRequest struct {
    System      string
    Messages    []model.Message
    SessionID   string
    Temperature *float64
    MaxTokens   int
}
```

| Поле          | Тип               | Назначение                                              |
| ------------- | ----------------- | ------------------------------------------------------- |
| `System`      | `string`          | Серверный системный промпт; клиент задать его не может. |
| `Messages`    | `[]model.Message` | История; сохраняются только роли `user` и `assistant`.  |
| `SessionID`   | `string`          | Идентификатор сессии для трейсов.                       |
| `Temperature` | `*float64`        | Температура генерации; `nil` — значение провайдера.     |
| `MaxTokens`   | `int`             | Лимит токенов ответа; `0` — значение провайдера.        |

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
    Query string
    TopK  int
}
```

| Поле    | Тип      | Назначение                                         |
| ------- | -------- | -------------------------------------------------- |
| `Query` | `string` | Текст запроса; пустой возвращает `ErrInvalidArgs`. |
| `TopK`  | `int`    | Число хитов; `0` — берётся `Config.TopK`.          |

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

### GenerateRequest

```go
type GenerateRequest struct {
    Prompt      string
    System      string
    MaxTokens   int
    Temperature *float64
    SessionID   string
}
```

| Поле          | Тип        | Назначение                        |
| ------------- | ---------- | --------------------------------- |
| `Prompt`      | `string`   | Запрос к модели.                  |
| `System`      | `string`   | Системный промпт.                 |
| `MaxTokens`   | `int`      | Лимит токенов ответа.             |
| `Temperature` | `*float64` | Температура генерации.            |
| `SessionID`   | `string`   | Идентификатор сессии для трейсов. |

## Принципал в контексте

```go
func WithPrincipal(ctx context.Context, p Principal) context.Context
func PrincipalFrom(ctx context.Context) (Principal, bool)
```

`WithPrincipal` кладёт принципала в контекст. `PrincipalFrom` читает его и
сообщает, найден ли он. Без принципала операции возвращают `ErrUnauthorized`.

## Инструменты

### kit.Tool

```go
func Tool[In, Out any](app *App, name string, fn func(ctx context.Context, in In) (Out, error), opts ...ToolOption) (*ToolDef, error)
```

Регистрирует типизированный инструмент. JSON Schema выводится из `In`, результат
`Out` сериализуется для модели.

```go
kit.Tool(app, "get_weather", getWeather, kit.Desc("Return the weather by city."))
```

### kit.Desc и kit.Perm

```go
type ToolOption func(*toolOptions)

func Desc(s string) ToolOption
func Perm(p string) ToolOption
```

| Функция    | Назначение                                       |
| ---------- | ------------------------------------------------ |
| `kit.Desc` | Описание инструмента для модели.                 |
| `kit.Perm` | Переопределяет право; по умолчанию `tool:<имя>`. |

### ToolDef

```go
func (t *ToolDef) Name() string
func (t *ToolDef) Description() string
func (t *ToolDef) Permission() string
func (t *ToolDef) Schema() json.RawMessage
```

Дескриптор зарегистрированного инструмента. Возвращается из `kit.Tool`; аргументы
валидируются по схеме со `"additionalProperties": false`.

## Константы

| Константа                | Значение | Назначение                         |
| ------------------------ | -------- | ---------------------------------- |
| `DefaultMaxSteps`        | `8`      | Лимит шагов цикла инструментов.    |
| `DefaultMaxCallsPerTool` | `3`      | Лимит вызовов одного инструмента.  |
| `DefaultToolTimeout`     | `30s`    | Таймаут одного вызова инструмента. |
| `DefaultArgsMaxBytes`    | `32768`  | Лимит размера аргументов.          |
| `DefaultTopK`            | `10`     | Хитов поиска по умолчанию.         |

Значения по умолчанию живут и в других пакетах:

| Константа                    | Значение | Пакет   |
| ---------------------------- | -------- | ------- |
| `chunk.DefaultSize`          | `400`    | `chunk` |
| `chunk.DefaultOverlap`       | `80`     | `chunk` |
| `guard.DefaultMaxChars`      | `8000`   | `guard` |
| `guard.DefaultCacheSize`     | `4096`   | `guard` |
| `store.DefaultTopK`          | `10`     | `store` |
| `store.DefaultRRFRankConst`  | `60`     | `store` |
| `store.DefaultILMDays`       | `30`     | `store` |
| `trace.DefaultQueueSize`     | `4096`   | `trace` |
| `trace.DefaultBatchSize`     | `500`    | `trace` |
| `trace.DefaultFlushInterval` | `2s`     | `trace` |
| `trace.DefaultMaxBatchBytes` | `5 MB`   | `trace` |
| `trace.DefaultMaxRetries`    | `3`      | `trace` |
| `trace.DefaultRetryBackoff`  | `200ms`  | `trace` |

## Ошибки

Sentinels объявлены в `kit/errors.go`; матчинг — через `errors.Is`.

| Sentinel              | Когда возвращается                                                                                                                                              |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ErrInvalidConfig`    | `New`: нет `ES`, `LLM` или `Policy`; невалидный `policy.yaml`; `Ingest` без `Embedder` или с пустым `ID`; инструмент без имени или обработчика.                 |
| `ErrUnauthorized`     | Принципала нет в контексте или он пуст; роль не имеет права.                                                                                                    |
| `ErrGuardReject`      | Rail `input` или `output` вернул reject.                                                                                                                        |
| `ErrGuardUnavailable` | Guard недоступен (fail-closed).                                                                                                                                 |
| `ErrProvider`         | Ошибка провайдера LLM, эмбеддера или Elasticsearch; не удалось декодировать ответ в `Generate`.                                                                 |
| `ErrToolNotFound`     | Модель запросила инструмент, которого нет или который запрещён.                                                                                                 |
| `ErrInvalidArgs`      | Аргументы не разобрались как JSON, не прошли схему или превысили `ArgsMaxBytes`; пустой `SearchRequest.Query`; нет сообщений ролей `user`/`assistant` в `Chat`. |
| `ErrMaxSteps`         | Исчерпаны `MaxSteps` или `MaxCallsPerTool`.                                                                                                                     |
| `ErrNotFound`         | Документ или индекс не найдены.                                                                                                                                 |

Разбор по слоям и что видит клиент — [Ошибки](./errors.md).

## Смотрите также

- [Ошибки](./errors.md) — sentinels и коды ответов
- [Конфигурация](./configuration.md) — значения по умолчанию и лимиты
- [Провайдеры](./providers.md) — реализации `model.Client` и `embed.Embedder`
- [policy.yaml](./policy.md) — формат политики RBAC
- [Индексы Elasticsearch](./elasticsearch.md) — поля документов
- [Chat](../concepts/chat.md) — операция в контексте
- [Доступ](../concepts/access-control.md) — принципал и права
- [Наблюдаемость](../concepts/observability.md) — трейсы и оценки
