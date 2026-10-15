# Провайдеры

kit не привязан к вендору. Клиент чата — интерфейс `model.Client` (`Chat`, `Model`, без стриминга), эмбеддер — `embed.Embedder`. Реализации живут в пакетах `model` и `embed`.

## Конструкторы LLM

| Конструктор                          | Wire-формат                                          | Особенности                                                                                                                                             |
| ------------------------------------ | ---------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `model.OpenAI(apiKey, modelName)`    | `POST https://api.openai.com/v1/chat/completions`    | Ключ в `Authorization: Bearer`. Structured output — `response_format: json_schema` с именем схемы `output`.                                             |
| `model.VLLM(baseURL, modelName)`     | OpenAI-совместимый `POST <baseURL>/chat/completions` | Обёртка над `model.NewOpenAI` без API-ключа. Хост вида `http://vllm:8000/v1`.                                                                           |
| `model.Ollama(baseURL, modelName)`   | native `POST <baseURL>/api/chat`                     | `tools` и `format` (JSON Schema), аргументы инструмента — объект. `Temperature` и `MaxTokens` уходят в `options.temperature` и `options.num_predict`.   |
| `model.GigaChat(cfg GigaChatConfig)` | OAuth + `POST <BaseURL>/v1/chat/completions`         | `functions` и `function_call`; structured output эмулируется функцией `structured_output`. Роль `tool` → `function`, берётся только первый `tool_call`. |

`model.NewOpenAI(cfg OpenAIConfig)` — общий конструктор: `BaseURL` и `Model` обязательны, дополнительно `APIKey`, `Headers map[string]string`, `HTTPClient`. `model.NewOllama(cfg OllamaConfig)`: `BaseURL` по умолчанию `http://localhost:11434`, `Model` обязателен, `HTTPClient`.

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

Общего метода `Embed()` нет: `Query()` добавляет префикс запроса, `Passages()` — префикс пассажа.

| Конструктор                               | Запрос                                 | Особенности                                                                                                                         |
| ----------------------------------------- | -------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `embed.NewOpenAI(embed.OpenAIConfig)`     | `POST <BaseURL>/embeddings`            | `BaseURL` и `Model` обязательны. `Dim` задаётся, если сервер не сообщает размерность: она нужна `App.Setup`. `APIKey` необязателен. |
| `embed.NewGigaChat(embed.GigaChatConfig)` | OAuth + `POST <BaseURL>/v1/embeddings` | Тот же OAuth, что у чата (`Credentials` обязателен). `Model` по умолчанию `Embeddings`.                                             |
| `embed.NewLocal(embed.LocalConfig)`       | локальный ONNX-энкодер                 | Доступен только в сборке с тегом `ort`; в обычной сборке возвращает `ErrInvalidConfig`. `LocalConfig{Dir, Model}`.                  |

## Пресеты

`embed.PresetFor(model)` сопоставляет модель по подстроке имени:

| Семейство          | `pooling` | `query_prefix` | `passage_prefix` | `normalize` | `max_len` |
| ------------------ | --------- | -------------- | ---------------- | ----------- | --------- |
| `multilingual-e5*` | `mean`    | `query: `      | `passage: `      | `true`      | `512`     |
| `bge-m3`           | `cls`     | —              | —                | `true`      | `8192`    |

Поля типа `embed.Preset`: `Name`, `Pooling` (`mean` или `cls`), `QueryPrefix`, `PassagePrefix`, `Normalize`, `MaxLen`. Пресет можно задать явно через поле `Preset *Preset` в `OpenAIConfig` / `GigaChatConfig`; иначе он выводится из имени модели. Неизвестная модель получает пустой пресет: без префиксов и без L2-нормы.

Неизвестную локальную модель описывают файлом `kit_embedder.json` рядом с весами: `embed.LoadFileConfig(path)` читает JSON в строгом режиме (`DisallowUnknownFields`) с полями `pooling`, `query_prefix`, `passage_prefix`, `normalize`, `max_len`. Значения по умолчанию: `pooling` = `mean`, `max_len` = `512`. Неизвестный `pooling`, неизвестное поле или ошибка разбора дают `ErrInvalidConfig`. Если `NewLocal` не находит ни пресет, ни `kit_embedder.json`, он также возвращает `ErrInvalidConfig`.

Каталог весов — `<models_dir>/<org>__<name>/` (`embed.ModelDir`); `models_dir` берётся из опции `embed.Dir`, переменной `KIT_MODELS_DIR` или `./models`.

> [!NOTE]
> `Embedder` обязателен только операциям `Ingest` и `Search`. Чат-приложению он не нужен: `kit.New` принимает `Embedder: nil`, а `App.Setup` в этом случае пропускает индекс чанков.

## Смотрите также

- [Справочник kit](./kit.md) — поля `Config`, операции `Ingest` и `Search`
- [Конфигурация](./configuration.md) — `KIT_MODELS_DIR`, `KIT_ORT_LIB` и лимиты эмбеддера
- [Поиск](../concepts/retrieval.md) — как эмбеддер используется при ingest и search
- [Установка](../getting-started/installation.md) — сборка с `-tags ort`
