# Обзор

Провайдеры моделей (providers)

Провайдеры моделей реализуют интерфейс `model.Client` и используются в
`gobellim.Config.LLM`. gobellim не поставляет собственных клиентов — вы приносите свой.

## Доступные провайдеры

| Провайдер          | Пакет               | Особенности                                                            |
| ------------------ | ------------------- | ---------------------------------------------------------------------- |
| OpenAI-совместимый | `model.NewOpenAI`   | vLLM, LM Studio, шлюзы; base URL, ключ, `response_format`              |
| Ollama             | `model.NewOllama`   | локальные модели, native `/api/chat`, `options.*`, `format`, `num_ctx` |
| vLLM               | `model.NewVLLM`     | свой OpenAI-совместимый сервер, tool calling, guided decoding          |
| GigaChat           | `plugins/gigachat`  | OAuth, корневой CA, `functions`, эмуляция structured output            |
| Эмбеддеры          | `model.NewEmbedder` | выбор эмбеддера, `Query`/`Passages`, пресеты, `Dim()`                  |

## Пример: OpenAI-совместимый

```go
llm, err := model.NewOpenAI(model.OpenAIConfig{
	BaseURL: "http://localhost:8000/v1",
	Model:   "qwen2.5-7b-instruct",
})

app, err := gobellim.New(ctx, gobellim.Config{
	LLM: llm,
	// ...
})
```

## Устройство

Как это устроено → [implementation.md](./implementation.md)

## Смотрите также

- [Быстрый старт](../overview.md)
- [Справочник API](../reference/gobellim.md) — `gobellim.Config.LLM`, `model.Client`
- [Рабочие процессы](../workflows/README.md) — провайдер в шагах workflow
