# Провайдеры

gobellim не привязан к вендору. Клиент чата — интерфейс `model.Client` (`Chat`, `Model`); необязательные расширения — `model.Streamer` (стриминг; его реализует `*model.OpenAIClient`, то есть `openai`/`vllm`) и `model.Regional` (`Region()`, резидентность, BR-106). Эмбеддер — `embed.Embedder`. Open-совместимые реализации и Ollama живут в `model`/`embed`; вендорские интеграции — отдельные модули в `plugins/` (`plugins/gigachat`, `plugins/elasticsearch`).

## Конструкторы LLM

| Конструктор                        | Wire-формат                                          | Особенности                                                                                                                                                                                                                           |
| ---------------------------------- | ---------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `model.OpenAI(apiKey, modelName)`  | `POST https://api.openai.com/v1/chat/completions`    | Ключ в `Authorization: Bearer`. Structured output — `response_format: json_schema` с именем схемы `output`.                                                                                                                           |
| `model.VLLM(baseURL, modelName)`   | OpenAI-совместимый `POST <baseURL>/chat/completions` | Обёртка над `model.NewOpenAI` без API-ключа. Хост вида `http://vllm:8000/v1`.                                                                                                                                                         |
| `model.Ollama(baseURL, modelName)` | native `POST <baseURL>/api/chat`                     | `tools` и `format` (JSON Schema), аргументы инструмента — объект. `Temperature`/`MaxTokens`/`Seed`/`Stop` уходят в `options.*`.                                                                                                       |
| `gigachat.New(gigachat.Config)`    | OAuth + `POST <BaseURL>/v1/chat/completions`         | Модуль `plugins/gigachat`. `functions` и `function_call`; structured output эмулируется функцией `structured_output`. Роль `tool` → `function`. Несколько параллельных `tool_call` клиент не поддерживает и возвращает `ErrProvider`. |

`model.NewOpenAI(cfg OpenAIConfig)` — общий конструктор: `BaseURL` и `Model` обязательны, дополнительно `APIKey`, `Headers map[string]string`, `HTTPClient`. `model.NewOllama(cfg OllamaConfig)`: `BaseURL` по умолчанию `http://localhost:11434`, `Model` обязателен, `HTTPClient`.

## FinishReason

Ответ несёт нормализованную причину остановки:

| Значение                    | Смысл                                     |
| --------------------------- | ----------------------------------------- |
| `model.FinishStop`          | `stop` — обычное завершение.              |
| `model.FinishToolCalls`     | `tool_calls` — модель просит инструменты. |
| `model.FinishLength`        | `length` — упёрлась в лимит токенов.      |
| `model.FinishContentFilter` | `content_filter` — фильтр провайдера.     |
| `model.FinishOther`         | `other` — прочее/неизвестное.             |

`model.ParseFinishReason(s)` нормализует провайдерское значение; неизвестное или
пустое становится `FinishOther`.

## Схемная инъекция

`Request.JSONSchema` — JSON Schema ответа, `Request.SchemaMode` (`model.SchemaMode`) — как её передать:

| Режим                | Что делает                                                         |
| -------------------- | ------------------------------------------------------------------ |
| `model.SchemaNative` | Отдаёт схему нативно (`response_format`/`format`/forced function). |
| `model.SchemaInline` | Дописывает инструкцию со схемой в последнее `user`-сообщение.      |
| `model.SchemaSystem` | Дописывает инструкцию со схемой в системное сообщение.             |

`model.SupportsNativeSchema(client)` сообщает, умеет ли провайдер нативную схему:
`true` для OpenAI/vLLM и Ollama, `false` для GigaChat (эмулирует forced function).
Клиент может переопределить таблицу, реализовав `SupportsNativeSchema() bool`.
`gobellim.Generate` при `SchemaNative` без поддержки провайдера автоматически инлайнит
схему (D29).

> [!NOTE]
> `model.Chain` не форвардит `SupportsNativeSchema`, поэтому цепочка считается не
> поддерживающей нативную схему: `gobellim.Generate`/`model.Structurer` инлайнят схему,
> даже если нативную схему поддерживают все звенья.

## ToolChoice, Seed, Stop, ProviderOptions

Поле `Request.ToolChoice` — `""`, `"auto"`, `"none"`, `"required"` или имя
инструмента. Как оно доходит до провайдера:

| Провайдер   | `ToolChoice`                                                                                                                            |
| ----------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| OpenAI/vLLM | `tool_choice`: строка `auto`/`none`/`required`, а имя инструмента — объектом `{"type":"function",...}`.                                 |
| Ollama      | Нет `tool_choice`: `none` эмулируется пропуском `tools`, прочие значения игнорируются.                                                  |
| GigaChat    | `none` пропускает `functions`; `auto` или пусто → `function_call: auto`; имя инструмента → `{"name": ...}`; `required` → `ErrProvider`. |

| Параметр | OpenAI/vLLM | Ollama         | GigaChat     |
| -------- | ----------- | -------------- | ------------ |
| `Seed`   | `seed`      | `options.seed` | игнорируется |
| `Stop`   | `stop`      | `options.stop` | игнорируется |

`Request.ProviderOptions` — escape hatch: объект вливается в исходящее JSON-тело
провайдера. Ключи `model`, `messages` и `stream` защищены и переустанавливаются
после слияния, поэтому переопределить их нельзя.

## Цепочка и история

```go
type ChainConfig struct {
    MaxRetries int           // дополнительных попыток на звено
    Backoff    time.Duration // база задержки между попытками
}
func Chain(cfg ChainConfig, clients ...Client) (Client, error)
```

`model.Chain` пробует клиентов по порядку. Звено ретраится при retryable-ошибке
(транспорт, `408`, `425`, `429`, `5xx`) до `MaxRetries`, уважая `Retry-After`;
иначе цепочка переходит к следующему звену. `Model()` возвращает модель первого
клиента. `Chain` не реализует `SupportsNativeSchema`, поэтому для цепочки схема
всегда инлайнится (см. «Схемная инъекция»).

```go
type Counter interface{ Count(text string) int }
type ByteCounter struct{} // Count = len(text)/4 — грубая эвристика
type History struct {
    Counter  Counter
    Budget   int // <= 0 отключает обрезку
    KeepLast int // минимум хвостовых сообщений (по умолчанию 2)
}
func (h History) Trim(msgs []Message) []Message
```

`History.Trim` режет старейшие сообщения до бюджета, сохраняя системное сообщение
(никогда не выбрасывается), пары `tool_call`/`tool` (не оставляет висячих вызовов)
и стабильный системный префикс для prompt-кэша.

Нижний HTTP-слой — непубличный пакет `internal/httpjson` (`Config`, `NewClient`,
`StatusError`, `IsRetryable`, `RetryAfter`); импортировать его приложение не может.
Ретраи включает транспорт, который строит только `httpjson.NewClient`. Адаптеры
`model` и `embed` используют `httpjson.Client(hc)`, возвращающий переданный
`*http.Client` как есть, поэтому **свой `HTTPClient` получает одну попытку**, а
`nil` — обычный клиент с таймаутом 60 с: сам `internal/httpjson` при пользовательском
клиенте ретраев не делает. Пользовательский механизм ретраев — `model.Chain`.

## gigachat.Config

GigaChat — отдельный модуль `github.com/KirillYuzh/Gobellim/plugins/gigachat`.

| Поле            | Тип            | По умолчанию                                                                    | Назначение                                        |
| --------------- | -------------- | ------------------------------------------------------------------------------- | ------------------------------------------------- |
| `BaseURL`       | `string`       | `https://api.giga.chat` (`gigachat.DefaultBaseURL`)                             | База для `/v1/chat/completions`.                  |
| `AuthURL`       | `string`       | `https://ngw.devices.sberbank.ru:9443/api/v2/oauth` (`gigachat.DefaultAuthURL`) | OAuth-эндпоинт токена.                            |
| `Credentials`   | `string`       | — (обязательно)                                                                 | base64 от `clientID:clientSecret`.                |
| `Scope`         | `string`       | `GIGACHAT_API_PERS` (`gigachat.DefaultScope`)                                   | Скоуп OAuth-запроса.                              |
| `Model`         | `string`       | `GigaChat-2` (`gigachat.DefaultModel`)                                          | Идентификатор модели.                             |
| `HTTPClient`    | `*http.Client` | `nil`                                                                           | Свой клиент: корневой CA Сбера, таймауты, прокси. |
| `ContextTokens` | `int`          | `0`                                                                             | Окно контекста; реализует `model.ContextWindow`.  |

> [!WARNING]
> OAuth-запрос GigaChat несёт заголовки `Authorization: Basic <Credentials>` и `RqUID` — UUIDv4. Без заголовка `RqUID` шлюз отвечает `400`. Тело запроса — `application/x-www-form-urlencoded` с `scope=<Scope>`. Токен кэшируется и обновляется за 30 секунд до `expires_at`; если поле не пришло, срок жизни берётся равным 25 минутам.

## Эмбеддеры

```go
type Embedder interface {
    Query(ctx context.Context, texts []string) ([][]float32, error)
    Passages(ctx context.Context, texts []string) ([][]float32, error)
    Dim() int
    Model() string
}
```

Общего метода `Embed()` нет: `Query()` добавляет префикс запроса, `Passages()` —
префикс пассажа. `Closer` (`Close() error`) опционален: локальный энкодер владеет
пулом ORT-сессий, но `Embedder` его не требует.

| Конструктор                                  | Запрос                                 | Особенности                                                                                                                                                                                               |
| -------------------------------------------- | -------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `embed.NewOpenAI(embed.OpenAIConfig)`        | `POST <BaseURL>/embeddings`            | `BaseURL` и `Model` обязательны. `Dim` можно задать заранее; иначе берётся из первого ответа. `APIKey` необязателен. `BatchSize` ограничивает размер батча (по умолчанию `embed.DefaultRemoteBatchSize`). |
| `gigachat.NewEmbedder(gigachat.EmbedConfig)` | OAuth + `POST <BaseURL>/v1/embeddings` | Модуль `plugins/gigachat`. Тот же OAuth, что у чата (`Credentials` обязателен). `Model` по умолчанию `Embeddings`. `BatchSize` ограничивает размер батча.                                                 |
| `embed.NewLocal(embed.LocalConfig)`          | локальный ONNX-энкодер                 | Доступен только в сборке с тегом `ort`; в обычной сборке возвращает `ErrInvalidConfig`. `LocalConfig{Dir, Model, Sessions}`.                                                                              |

Удалённые эмбеддеры режут тексты на батчи (по умолчанию 64) и склеивают ответы
в исходном порядке: один документ на тысячи чанков не уходит в провайдера одним
мегабайтным запросом. Размерность вектора запоминается при первом ответе, и
изменение под уже записанным индексом отвергается с `ErrProvider` — смена
размерности требует переиндексации.

Локальный энкодер (`embed.NewLocal` под `ort`) принимает графы с выходом
`sentence_embedding`, `last_hidden_state` или `token_embeddings`; размерность
берётся из графа, вход обрезается по `max_len`, батч ≤ 32 сортируется по длине,
пул сессий — `LocalConfig.Sessions` (по умолчанию 2). Декодерные / серверные
эмбеддеры подключают через удалённый OpenAI-совместимый `/v1/embeddings`
(`embed.NewOpenAI`).

## Пресеты

`embed.PresetFor(model)` сопоставляет модель по имени семейства: сегмент имени
(path-сегмент, либо префикс, после которого идёт `-`, `_` или `.`) должен совпасть
с семейством целиком.

| Семейство          | `pooling` | `query_prefix` | `passage_prefix` | `normalize` | `max_len` |
| ------------------ | --------- | -------------- | ---------------- | ----------- | --------- |
| `multilingual-e5*` | `mean`    | `query: `      | `passage: `      | `true`      | `512`     |
| `bge-m3`           | `cls`     | —              | —                | `true`      | `8192`    |

Имя вроде `my-bge-m3-tuned` не называет семейство `bge-m3` (совпадение не по
подстроке), поэтому пресет не выводится: молча взять pooling и нормализацию
другой модели значило бы считать векторы, которые никто не ожидал. Имя, которое
называет сразу два семейства, — ошибка конфигурации (`ErrInvalidConfig`), а не
выбор одного из них.

Поля типа `embed.Preset`: `Name`, `Pooling` (`mean` или `cls`), `QueryPrefix`, `PassagePrefix`, `Normalize`, `MaxLen`. Пресет можно задать явно через поле `Preset *Preset` в `OpenAIConfig` / `gigachat.EmbedConfig`; иначе он выводится из имени модели. Неизвестная модель получает пустой пресет: без префиксов и без L2-нормы.

Неизвестную локальную модель описывают файлом `kit_embedder.json` рядом с весами: `embed.LoadFileConfig(path)` читает JSON в строгом режиме (`DisallowUnknownFields`) с полями `pooling`, `query_prefix`, `passage_prefix`, `normalize`, `max_len`. Значения по умолчанию: `pooling` = `mean`, `max_len` = `512`. Неизвестный `pooling`, неизвестное поле или ошибка разбора дают `ErrInvalidConfig`. Если `NewLocal` не находит ни пресет, ни `kit_embedder.json`, он также возвращает `ErrInvalidConfig`.

Каталог весов — `<models_dir>/<org>__<name>/` (`embed.ModelDir`; `/` в имени модели заменяется на `__`); `models_dir` берётся из опции `embed.LocalConfig.Dir`, переменной `KIT_MODELS_DIR` или `./models`.

> [!NOTE]
> `Embedder` обязателен только операциям `Ingest` и `Search`. Чат-приложению он не нужен: `gobellim.New` принимает `Embedder: nil`, а `App.Setup` в этом случае пропускает индекс чанков.

> [!WARNING]
> **Размерность индекса чанков фиксируется при `App.Setup`.** `Setup` вызывает `store.EnsureSchema(ctx, es, Embedder.Dim())`, а индекс `kit-chunks` создаётся только когда `Dim() > 0`. У удалённых эмбеддеров (`embed.NewOpenAI`, `gigachat.NewEmbedder`) до первого вызова `Dim()` возвращает сконфигурированное `Dim`, а при `Dim == 0` — `0`; размерность из ответа подставляется уже в рантайме, при первом `embed`. Поэтому с удалённым эмбеддером и без явного `Dim` `App.Setup` **не создаёт** индекс `kit-chunks`, и первый `Ingest` падает на отсутствующем индексе — задайте `Dim` заранее. `EnsureSchema` идемпотентен и **не сверяет** размерности, поэтому смена размерности эмбеддера требует переиндексации: пересоздайте индекс и повторите `Ingest`.

## Смотрите также

- [Справочник gobellim](./gobellim.md) — поля `Config`, операции `Ingest` и `Search`
- [Конфигурация](./configuration.md) — `KIT_MODELS_DIR`, `KIT_ORT_LIB` и лимиты эмбеддера
- [Поиск](../retrieval/README.md) — как эмбеддер используется при ingest и search
- [Установка](../installation.md) — сборка с `-tags ort`

Руководства (how-to):

- [Как подключить провайдера](../providers/README.md) — выбор, матрица возможностей, `gobellim.Config{LLM: …}`, `model.Chain`
- [OpenAI-совместимые, Ollama, vLLM, GigaChat](../providers/README.md) — пошаговая настройка, рецепты и troubleshooting
- [Эмбеддеры](../providers/README.md) — `Query`/`Passages`, пресеты, `Dim()` и переиндексация
