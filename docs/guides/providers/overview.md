# Провайдеры: обзор

kit не владеет моделью. Вы приносите клиент чата (`model.Client`) и эмбеддер
(`embed.Embedder`), а kit вызывает их через единый примитив вызова. Провайдер
нельзя «выбрать строкой конфига»: каждый адаптер конструируется явно и
передаётся в `kit.Config{LLM: …}`.

Это осознанное решение (D13, [Решения](../../internals/decisions.md)): отличия
провайдеров изолированы в адаптерах, а не размазаны по ядру «универсальным
клиентом» с флагами. Точные сигнатуры и таблицы полей — в
[справочнике провайдеров](../../reference/providers.md); здесь — как выбрать и
подключить.

## Когда использовать

- Нужен вызов LLM с tool calling и structured output — берите любой
  из трёх adapter-семейств ниже.
- Нужно несколько моделей (основная + фолбэк, или разные модели под разные шаги) —
  соедините их `model.Chain`.
- Нужен эмбеддер для `Ingest`/`Search` — см. [Эмбеддеры](./embeddings.md).

## Как выбрать

| Провайдер                                                | Wire-формат                               | Когда подходит                                                         |
| -------------------------------------------------------- | ----------------------------------------- | ---------------------------------------------------------------------- |
| OpenAI-совместимые ([подробнее](./openai-compatible.md)) | `POST <base>/chat/completions`            | OpenAI, vLLM, LM Studio, TEI-совместимые шлюзы, Azure-стиль заголовков |
| [Ollama](./ollama.md)                                    | native `POST <base>/api/chat`             | локальный запуск, минимум настроек, `format` = JSON Schema             |
| [GigaChat](./gigachat.md)                                | OAuth + `POST <base>/v1/chat/completions` | Сбер-стек: `functions`/`function_call`, свой корневой CA               |

## Матрица возможностей

Колонка «OpenAI-совместимые» покрывает и `model.OpenAI`, и `model.VLLM`: это один
и тот же клиент `*model.OpenAIClient`. Значения выведены из кода адаптеров;
поддержку на **стороне сервера/модели** нужно проверять отдельно.

| Возможность                 | OpenAI-совместимые                                                               | Ollama                                                                 | GigaChat                                                                       |
| --------------------------- | -------------------------------------------------------------------------------- | ---------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| Инструменты                 | `tools` (массив `{type:function}`)                                               | `tools` в той же форме                                                 | `functions` (без `type`)                                                       |
| `tool_choice`               | `auto`/`none`/`required` строкой; имя инструмента — объектом `{type:function,…}` | поля нет: `none` = пропустить `tools`, остальные значения игнорируются | поля нет: `none` = пропустить `functions`, остальное → `function_call: "auto"` |
| Нативная JSON Schema        | `response_format: {type:json_schema, json_schema:{name:"output", schema}}`       | `format` = сырая JSON Schema                                           | нет: эмуляция forced-функцией `structured_output`                              |
| `SupportsNativeSchema()`    | `true`                                                                           | `true`                                                                 | `false`                                                                        |
| `Seed`                      | `seed`                                                                           | `options.seed`                                                         | игнорируется                                                                   |
| `Stop`                      | `stop`                                                                           | `options.stop`                                                         | игнорируется                                                                   |
| `Temperature`               | `temperature`                                                                    | `options.temperature`                                                  | `temperature`                                                                  |
| Лимит ответа (`MaxTokens`)  | `max_tokens`                                                                     | `options.num_predict`                                                  | `max_tokens`                                                                   |
| Usage                       | `usage.prompt_tokens` / `completion_tokens` / `total_tokens` из ответа           | `prompt_eval_count` / `eval_count`; `total` = их сумма                 | `usage.*` из ответа                                                            |
| Несколько tool calls за ход | все, из `choices[0].message.tool_calls`                                          | все; `id` синтезируется как `«имя»-«индекс»`                           | **только первый** `function_call`                                              |
| Стриминг (`ChatStream`)     | да (SSE)                                                                         | нет                                                                    | нет                                                                            |
| Авторизация                 | `Authorization: Bearer <APIKey>` (если ключ задан) и/или произвольные `Headers`  | заголовков нет                                                         | OAuth: Basic `Credentials` → Bearer-токен                                      |

> [!NOTE]
> Стриминг — операция `kit.ChatStream` (BR-89), а не поле провайдера: SSE умеет
> только `*model.OpenAIClient` (`openai`/`vllm`), потому что у `ollama` и
> `gigachat` другой wire-формат (I-19). `Chat` всегда запрашивает `stream: false`,
> а ключ `stream` защищён от подмены через `ProviderOptions`.

> [!WARNING]
> Матрица описывает то, что клиент **отправляет**. Нативная схема и `tool_choice`
> работают, только если их поддерживает конкретная модель на конкретном сервере.
> vLLM без нужных флагов, старые сборки и «урезанные» модели молча проигнорируют
> часть полей — см. troubleshooting на страницах провайдеров.

## Подключение клиента

`kit.Config.LLM` принимает интерфейс, поэтому конструктор провайдера встраивается
без обёрток:

```go
import (
	"github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/model"
)

llm, err := model.OpenAI(os.Getenv("OPENAI_API_KEY"), "gpt-4o-mini")
if err != nil {
	return err
}
app, err := kit.New(ctx, kit.Config{
	ES:     es,     // ваш store.ES
	LLM:    llm,    // любой model.Client
	Policy: policy, // *authz.Policy
})
```

Логика выбора провайдера — ваш код. Разумный приём: собрать конструктор из
переменных окружения один раз при старте.

```go
func newLLM(cfg Config) (model.Client, error) {
	switch cfg.Provider {
	case "openai":
		return model.OpenAI(cfg.APIKey, cfg.Model)
	case "vllm":
		return model.VLLM(cfg.BaseURL, cfg.Model) // baseURL включает /v1
	case "ollama":
		return model.Ollama(cfg.BaseURL, cfg.Model) // baseURL по умолчанию localhost:11434
	case "gigachat":
		return model.GigaChat(model.GigaChatConfig{
			Credentials: cfg.Credentials,
			Model:       cfg.Model,
			HTTPClient:  cfg.HTTPClient,
		})
	default:
		return nil, fmt.Errorf("unknown provider %q", cfg.Provider)
	}
}
```

`model.Client` требует только `Chat(ctx, Request)` и `Model()`. Необязательные
расширения — `model.Streamer` (стриминг) и `model.Regional` (`Region()`,
резидентность: вне `model.RegionRU` маскирование PII обязательно, BR-106). Свой провайдер —
это ваша реализация этих двух методов; остальное (guard, трейс, лимиты) берёт на
себя ядро.

## Смешивание провайдеров через `model.Chain`

`model.Chain` соединяет клиентов в порядке приоритета: звено ретраится при
retryable-ошибке (транспорт, `408`, `425`, `429`, `5xx`) до `MaxRetries`, уважая
`Retry-After`; иначе цепочка переходит к следующему звену.

```go
primary, err := model.VLLM("http://vllm:8000/v1", "Qwen/Qwen3-8B")
if err != nil {
	return err
}
fallback, err := model.OpenAI(os.Getenv("OPENAI_API_KEY"), "gpt-4o-mini")
if err != nil {
	return err
}
llm, err := model.Chain(model.ChainConfig{MaxRetries: 2, Backoff: 500 * time.Millisecond}, primary, fallback)
if err != nil {
	return err
}
app, err := kit.New(ctx, kit.Config{ES: es, LLM: llm, Policy: policy})
```

> [!WARNING]
> `model.Chain` **не пробрасывает** `SupportsNativeSchema()`. У результата `Chain`
> этого метода нет, поэтому `model.SupportsNativeSchema(chain)` возвращает `false`.
> Практическое следствие: `kit.Generate` и `model.Structurer` при `SchemaNative`
> решат, что нативная схема недоступна, и **заинлайнят** схему в сообщение — даже
> если все звенья её поддерживают. Если нативная схема важнее фолбэка, не оборачивайте
> клиент в `Chain`, либо оберните сами и реализуйте метод:

```go
type chainNative struct {
	model.Client
}

func (chainNative) SupportsNativeSchema() bool { return true }
```

`model.Chain` также требует хотя бы одного непустого клиента, иначе — `ErrInvalidConfig`.

## Чего библиотека не делает

- **Стриминг — только у OpenAI-совместимых** (I-19). `model.Client.Chat` возвращает
  весь ответ; `ChatStream` есть у `openai`/`vllm`, у `ollama`/`gigachat` — откат на
  буферизованный ответ.
- **Нет мультимодальности.** В `model.Message` есть только `Content string`; картинок,
  аудио и вложений в API нет.
- **Нет реестра провайдеров.** Нет `model.Get("openai")` и автоподбора провайдера по
  имени модели: конструктор выбираете вы.
- **Нет универсального клиента с флагами** (D13). Каждый адаптер — отдельный тип.
- **Нет автоматической проверки окна контекста.** Встроенные клиенты не реализуют
  `model.ContextWindow`, поэтому проверка I-06 в `kit.New` для них не срабатывает.
  Если это важно, оберните клиент и реализуйте `ContextTokens() int`.
- **Нет валидации имени модели.** Строка `Model` уходит на сервер как есть; опечатка
  проявится ошибкой провайдера, а не `ErrInvalidConfig`.

## Смотрите также

- [OpenAI-совместимые](./openai-compatible.md) — base URL, `Headers`, `response_format`
- [Ollama](./ollama.md) — `options.*`, `format`, `num_ctx`
- [vLLM](./vllm.md) — серверные флаги и guided decoding
- [GigaChat](./gigachat.md) — OAuth и свой CA
- [Эмбеддеры](./embeddings.md) — `embed.Embedder`
- [Провайдеры (справочник)](../../reference/providers.md) — сигнатуры и таблицы
- [Решения](../../internals/decisions.md) — D13 (адаптеры), D46 (стриминг)
