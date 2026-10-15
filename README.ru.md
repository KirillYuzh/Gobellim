# Gobellim

**[Русский](README.ru.md) · [English](README.md)**

![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

_Secure LLM primitives for Go._ RAG, вызов инструментов и guardrails за одним
проверяющим права путём вызова.

![Архитектура Gobellim](docs/architecture.svg)

## Почему Gobellim?

**Слой рантайма, а не оркестратор.** Mastra, LangChain и CrewAI владеют вашим
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

**Безопасность fail-closed.** Детерминированные правила — шаблоны и контрольные
суммы российских персональных данных — идут раньше модели, по входу, результатам
инструментов и выходу. Ошибка guard отклоняет запрос, а не пропускает его.

**Ограничено по построению.** Число шагов, лимит вызовов на инструмент, таймауты
и размер аргументов задаёт рантайм, а не промпт. Стриминга нет: проверка выхода
требует ответ целиком.

## В коде

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
// Structured output: JSON Schema выводится из типа Go.
type Ticket struct {
	Severity int    `json:"severity"`
	Summary  string `json:"summary"`
}

ticket, err := kit.Generate[Ticket](ctx, app, kit.GenerateRequest{
	Prompt: "Классифицируй: выгрузка упала у трёх тенантов.",
})
```

```go
// RAG: чанкинг, эмбеддинги и индексация, затем поиск BM25 + kNN под ACL вызывающего.
err = app.Ingest(ctx, kit.Doc{ID: "policy-1", Text: text, ACL: []string{"user"}})
hits, err := app.Search(ctx, kit.SearchRequest{Query: "правила возврата", TopK: 5})

// Delete и score идут через тот же шлагбаум.
err = app.Delete(ctx, "policy-1")
err = app.Score(ctx, trace.Score{TraceID: traceID, Name: "helpful", Value: 1})
```

## Что дальше

- [Запускаемый пример →](./demo)
- [Документация →](./docs)

## Лицензия

[`MIT`](LICENSE).
