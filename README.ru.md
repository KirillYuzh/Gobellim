# Gobellim

**[Русский](README.ru.md) · [English](README.md)**

![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

_Безопасные LLM-примитивы для Go._ RAG, вызов инструментов и guardrails за одним
проверяющим права путём вызова.

![Архитектура Gobellim](docs/architecture.svg)

## Почему Gobellim?

**Слой рантайма, а не оркестратор.** [Mastra](https://mastra.ai/), [LangChain](https://www.langchain.com/) и [CrewAI](https://www.crewai.com/) владеют вашим
потоком управления — агенты, графы, workflows. Gobellim — нет: вы сохраняете
свои хендлеры и конкурентность и вызываете его как библиотеку.

**Один шлагбаум, без обхода.** Chat, ingest, search, delete, score и generate
идут одними и теми же четырьмя шагами — авторизация, guard, трейс, выполнение.
Второй точки входа нет, поэтому проверку нельзя забыть, а middleware писать не
нужно.

**Модель не решает про доступ.** Инструменты, недоступные вызывающему,
вырезаются из схемы до того, как запрос дойдёт до модели, а при исполнении
права проверяются снова.

**Поиск не может утечь.** ACL документа считается при записи и хранится в каждом
чанке, поэтому BM25 и kNN всегда фильтруют по вызывающему — а не после выдачи.

**Недоверенный контент не становится доверенным.** Результаты инструментов,
включая найденные фрагменты, оборачиваются в маркеры `<untrusted-data>`, а системный
промпт велит модели считать их данными, а не инструкциями. Rate limit на тенанта и
кэш ответов без tools работают на том же пути вызова.

**Инструменты — откуда угодно.** Напишите инструмент Go-функцией, возьмите его у
MCP-сервера (`mcp.Register`) или переупорядочьте выдачу кросс-энкодером (`rerank`):
всё идёт по одному пути authorize → guard → trace. `App.Health()` показывает очередь
писателя, guard и состояние компонентов, реализующих health-проверку.

**Память живёт в вашем хранилище.** История тредов, семантический recall и рабочая
память — в той же Elasticsearch, со скоупом по тенанту и пользователю и за тем же
шлагбаумом прав, что и всё остальное. `thread_id` от вызывающего сам по себе
доступа не даёт.

**Безопасность fail-closed.** До модели, по входу, результатам инструментов и
выходу, идут два слоя: детерминированные правила (шаблоны и контрольные суммы
российских персональных данных) и ONNX-guard GLiNER, который маскирует ПДн и
отклоняет небезопасные сообщения. Ошибка guard отклоняет запрос, а не пропускает
его.

**Ограничено по построению.** Число шагов, лимит вызовов на инструмент, таймауты
и размер аргументов задаёт рантайм, а не промпт. `Chat` возвращает ответ целиком,
потому что проверка выхода требует полного текста; стриминг есть отдельной
операцией `ChatStream` и тоже идёт через guard.

### Операции сразу

| Операция      | Право         | Область                                                |
| ------------- | ------------- | ------------------------------------------------------ |
| `Chat`        | `chat`        | права проверяются заново на каждый вызов инструмента   |
| `ChatStream`  | `chat`        | тот же ход, что `Chat`, но с guard'ом скользящего окна |
| `CallTool`    | `tool:<name>` | один инструмент тем же путём (MCP-сервер)              |
| Память        | `memory`      | история, recall и working; нужны `Tenant` и `UserID`   |
| `Generate[T]` | `generate`    | input/output guard, `ErrorStrategy`                    |
| `Ingest`      | `ingest`      | нужен непустой `Tenant` и `Embedder`                   |
| `Search`      | `search`      | нужен `Tenant`, `Embedder`; `SearchRequest.Filter`     |
| `Delete`      | `delete`      | нужен `Tenant`                                         |
| `Prune`       | `prune`       | удаляет трейсы/observations старше заданного срока     |
| `Score`       | `trace`       | записывает оценку трейса                               |

`Ingest`, `Search` и `Delete` требуют непустого `Tenant` в принципале:
операция не может расшириться на другие тенанты. `Prune` — глобальная ретенция по
времени: ему нужны принципал и право `prune`, а не тенант. Операции памяти требуют
непустых `Tenant` и `UserID`.

## В коде

Ваш клиент Elasticsearch — единственное место, которое знает про транспорт,
заголовки и `refresh`: kit вызывает `store.ES` с явным `store.Request` и никогда
не выводит тип контента из пути.

```go
type httpES struct {
	base string
	hc   *http.Client
}

func (c *httpES) Do(ctx context.Context, req store.Request) (int, []byte, error) {
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, c.base+req.EffectivePath(), body)
	if err != nil {
		return 0, nil, err
	}
	if len(req.Body) > 0 {
		ct := req.ContentType
		if ct == "" {
			ct = "application/json" // значение по умолчанию; kit не выводит его из пути
		}
		httpReq.Header.Set("Content-Type", ct)
	}
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}
```

```go
app, err := kit.New(ctx, kit.Config{
	ES:     es,     // ваш клиент Elasticsearch
	LLM:    llm,    // vLLM, Ollama, OpenAI-совместимый API или GigaChat
	Policy: policy, // authz.Load("policy.yaml")
})
if err != nil {
	return err
}
if err := app.Setup(ctx); err != nil { // один раз создаёт индексы
	return err
}

// Инструмент — типизированная Go-функция: схема и валидация берутся из weatherIn.
type weatherIn struct {
	City string `json:"city"`
}

func getWeather(ctx context.Context, in weatherIn) (domain.Weather, error) {
	return weather.Current(ctx, in.City)
}

if _, err := kit.Tool(app, "get_weather", getWeather, kit.Desc("Текущая погода в городе.")); err != nil {
	return err
}
```

```go
// У каждого вызова есть личность вызывающего.
ctx = kit.WithPrincipal(ctx, kit.Principal{UserID: "u-1", Roles: []string{"user"}, Tenant: "acme"})

// Chat отвечает сразу или сперва вызывает инструмент — в ограниченном цикле.
answer, err := app.Chat(ctx, kit.ChatRequest{
	System:   "Ты ассистент поддержки. Отвечай кратко.",
	Messages: []model.Message{{Role: model.RoleUser, Content: "Какая погода в Москве?"}},
})
```

```go
// Структурированный вывод: JSON Schema выводится из типа Go.
type Ticket struct {
	Severity int    `json:"severity"`
	Summary  string `json:"summary"`
}

// Generate — полноценная операция примитива: нужны принципал, право generate
// и input/output guard.
fallback := Ticket{Severity: 3, Summary: "нужна ручная разборка"}
ticket, err := kit.Generate[Ticket](ctx, app, kit.GenerateRequest[Ticket]{
	Prompt:        "Классифицируй: выгрузка упала у трёх тенантов.",
	ErrorStrategy: kit.Fallback, // Strict (по умолчанию) | Warn | Fallback
	Fallback:      &fallback,
})
if ticket.UsedFallback {
	// ответ модели не декодировался в Ticket или не совпал со схемой; вернулось значение Fallback
}
_ = ticket.Decoded      // false, если ErrorStrategy был Warn или Fallback
_ = ticket.FinishReason // model.FinishStop, FinishLength, FinishToolCalls, ...
```

```go
// RAG: чанкинг, эмбеддинги и индексация, затем поиск BM25 + kNN под ACL вызывающего.
err = app.Ingest(ctx, kit.Doc{ID: "policy-1", Text: text, ACL: []string{"user"}})
hits, err := app.Search(ctx, kit.SearchRequest{
	Query: "правила возврата",
	TopK:  5,
	// Filter сужает выборку по метаданным; AND-ится с ACL-фильтром принципала.
	Filter: store.Filter{"doc_id": "policy-1", "position": map[string]any{"$gte": 2}},
})

// Delete, prune и score идут через тот же шлагбаум.
err = app.Delete(ctx, "policy-1")
deleted, err := app.Prune(ctx, 30*24*time.Hour) // ретенция: трейсы и observations
err = app.Score(ctx, trace.Score{TraceID: traceID, Name: "helpful", Value: 1})
```

## Попробовать

[demo](./demo) — чат-бот поддержки с авторизацией и предзаполненной базой знаний.
Сразу после `docker compose up -d` он отвечает:

| Вопрос                                   | Ответ                                |
| ---------------------------------------- | ------------------------------------ |
| «Сколько дней даётся на возврат товара?» | 14 календарных дней                  |
| «Сколько идёт доставка в другой город?»  | 2–5 рабочих дней, 400 ₽              |
| «Какова гарантия на электронику?»        | 12 месяцев                           |
| «Можно ли купить в рассрочку?»           | да, «0-0-6» от 3000 ₽                |
| «Как связаться с поддержкой?»            | 8 999 888-77-66, круглосуточно       |
| «Как получить 5% кэшбэка?»               | от 50 000 ₽                          |
| «Какая погода в Москве?»                 | реальная погода — только для `admin` |

Роль `user` не видит инструмент погоды: он вырезается из схемы до того, как
запрос дойдёт до модели. Спрашивайте из [TUI или curl](./demo) либо запустите
[коллекцию Postman](./demo/postman).

## Что дальше

- [Запускаемый пример →](./demo)
- [Документация →](./docs)
- [Руководства (how-to) →](./docs/guides) — рецепты задач, в том числе подключение провайдеров

## Лицензия

[`MIT`](LICENSE).
