# Провайдеры

kit не привязан к вендору. Клиент чата — интерфейс `model.Client` (`Chat`, `Model`, без стриминга), эмбеддер — `embed.Embedder`. Реализации живут в пакетах `model` и `embed`.

## Конструкторы LLM

| Конструктор                          | Wire-формат                                          | Особенности                                                                                                                                             |
| ------------------------------------ | ---------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `model.OpenAI(apiKey, modelName)`    | `POST https://api.openai.com/v1/chat/completions`    | Ключ в `Authorization: Bearer`. Structured output — `response_format: json_schema` с именем схемы `output`.                                             |
| `model.VLLM(baseURL, modelName)`     | OpenAI-совместимый `POST <baseURL>/chat/completions` | Обёртка над `model.NewOpenAI` без API-ключа. Хост вида `http://vllm:8000/v1`.                                                                           |
| `model.Ollama(baseURL, modelName)`   | native `POST <baseURL>/api/chat`                     | `tools` и `format` (JSON Schema), аргументы инструмента — объект. `Temperature`/`MaxTokens`/`Seed`/`Stop` уходят в `options.*`.                         |
| `model.GigaChat(cfg GigaChatConfig)` | OAuth + `POST <BaseURL>/v1/chat/completions`         | `functions` и `function_call`; structured output эмулируется функцией `structured_output`. Роль `tool` → `function`, берётся только первый `tool_call`. |

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
`kit.Generate` при `SchemaNative` без поддержки провайдера автоматически инлайнит
схему (D29).

## ToolChoice, Seed, Stop, ProviderOptions

Поле `Request.ToolChoice` — `""`, `"auto"`, `"none"`, `"required"` или имя
инструмента. Как оно доходит до провайдера:

| Провайдер   | `ToolChoice`                                                                                            |
| ----------- | ------------------------------------------------------------------------------------------------------- |
| OpenAI/vLLM | `tool_choice`: строка `auto`/`none`/`required`, а имя инструмента — объектом `{"type":"function",...}`. |
| Ollama      | Нет `tool_choice`: `none` эмулируется пропуском `tools`, прочие значения игнорируются.                  |
| GigaChat    | Нет `tool_choice`: `none` эмулируется пропуском `functions`, прочие значения → `function_call: auto`.   |

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
клиента.

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

Нижний HTTP-слой — `internal/httpjson`: `Config`, `NewClient` (ретраи), `StatusError`,
`IsRetryable`, `RetryAfter`.

## GigaChatConfig

| Поле          | Тип            | По умолчанию                                                                   | Назначение                                        |
| ------------- | -------------- | ------------------------------------------------------------------------------ | ------------------------------------------------- |
| `BaseURL`     | `string`       | `https://api.giga.chat` (`GigaChatDefaultBaseURL`)                             | База для `/v1/chat/completions`.                  |
| `AuthURL`     | `string`       | `https://ngw.devices.sberbank.ru:9443/api/v2/oauth` (`GigaChatDefaultAuthURL`) | OAuth-эндпоинт токена.                            |
| `Credentials` | `string`       | — (обязательно)                                                                | base64 от `clientID:clientSecret`.                |
| `Scope`       | `string`       | `GIGACHAT_API_PERS` (`GigaChatDefaultScope`)                                   | Скоуп OAuth-запроса.                              |
| `Model`       | `string`       | `GigaChat-2` (`GigaChatDefaultModel`)                                          | Идентификатор модели.                             |
| `HTTPClient`  | `*http.Client` | `nil`                                                                          | Свой клиент: корневой CA Сбера, таймауты, прокси. |

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

| Конструктор                               | Запрос                                 | Особенности                                                                                                                  |
| ----------------------------------------- | -------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| `embed.NewOpenAI(embed.OpenAIConfig)`     | `POST <BaseURL>/embeddings`            | `BaseURL` и `Model` обязательны. `Dim` можно задать заранее; иначе берётся из первого ответа. `APIKey` необязателен.         |
| `embed.NewGigaChat(embed.GigaChatConfig)` | OAuth + `POST <BaseURL>/v1/embeddings` | Тот же OAuth, что у чата (`Credentials` обязателен). `Model` по умолчанию `Embeddings`.                                      |
| `embed.NewLocal(embed.LocalConfig)`       | локальный ONNX-энкодер                 | Доступен только в сборке с тегом `ort`; в обычной сборке возвращает `ErrInvalidConfig`. `LocalConfig{Dir, Model, Sessions}`. |

Локальный энкодер (`embed.NewLocal` под `ort`) принимает графы с выходом
`sentence_embedding`, `last_hidden_state` или `token_embeddings`; размерность
берётся из графа, вход обрезается по `max_len`, батч ≤ 32 сортируется по длине,
пул сессий — `LocalConfig.Sessions` (по умолчанию 2). Декодерные / серверные
эмбеддеры подключают через удалённый OpenAI-совместимый `/v1/embeddings`
(`embed.NewOpenAI`).

## Пресеты

`embed.PresetFor(model)` сопоставляет модель по подстроке имени:

| Семейство          | `pooling` | `query_prefix` | `passage_prefix` | `normalize` | `max_len` |
| ------------------ | --------- | -------------- | ---------------- | ----------- | --------- |
| `multilingual-e5*` | `mean`    | `query: `      | `passage: `      | `true`      | `512`     |
| `bge-m3`           | `cls`     | —              | —                | `true`      | `8192`    |

Поля типа `embed.Preset`: `Name`, `Pooling` (`mean` или `cls`), `QueryPrefix`, `PassagePrefix`, `Normalize`, `MaxLen`. Пресет можно задать явно через поле `Preset *Preset` в `OpenAIConfig` / `GigaChatConfig`; иначе он выводится из имени модели. Неизвестная модель получает пустой пресет: без префиксов и без L2-нормы.

Неизвестную локальную модель описывают файлом `kit_embedder.json` рядом с весами: `embed.LoadFileConfig(path)` читает JSON в строгом режиме (`DisallowUnknownFields`) с полями `pooling`, `query_prefix`, `passage_prefix`, `normalize`, `max_len`. Значения по умолчанию: `pooling` = `mean`, `max_len` = `512`. Неизвестный `pooling`, неизвестное поле или ошибка разбора дают `ErrInvalidConfig`. Если `NewLocal` не находит ни пресет, ни `kit_embedder.json`, он также возвращает `ErrInvalidConfig`.

Каталог весов — `<models_dir>/<org>__<name>/` (`embed.ModelDir`; `/` в имени модели заменяется на `__`); `models_dir` берётся из опции `embed.Dir`, переменной `KIT_MODELS_DIR` или `./models`.

> [!NOTE]
> `Embedder` обязателен только операциям `Ingest` и `Search`. Чат-приложению он не нужен: `kit.New` принимает `Embedder: nil`, а `App.Setup` в этом случае пропускает индекс чанков.

## Смотрите также

- [Справочник kit](./kit.md) — поля `Config`, операции `Ingest` и `Search`
- [Конфигурация](./configuration.md) — `KIT_MODELS_DIR`, `KIT_ORT_LIB` и лимиты эмбеддера
- [Поиск](../concepts/retrieval.md) — как эмбеддер используется при ingest и search
- [Установка](../getting-started/installation.md) — сборка с `-tags ort`
