# Gobellim

**[English](README.md) · [Русский](README.ru.md)**

![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

_Secure LLM primitives for Go._ Retrieval, tool calling and guardrails behind one
authorization-checked call path.

![Gobellim architecture](docs/architecture.svg)

## Why Gobellim?

**A runtime layer, not an orchestrator.** Mastra, LangChain and CrewAI own your
control flow — agents, graphs, workflows. Gobellim does not: you keep your
handlers and your concurrency, and call it like a library.

**One gate, no bypass.** Chat, ingest, search, delete, score and generate all run
the same four steps — authorize, guard, trace, execute. There is no second entry
point, so no check can be forgotten and no middleware has to be written.

**The model gets no say in access.** Tools the caller may not use are removed
from the schema before the request reaches the model, and permission is checked
again when the tool runs.

**Retrieval cannot leak.** A document's ACL is computed at write time and stored
in every chunk, so BM25 and kNN search always filter by the caller — never after
the fact.

**Safety fails closed.** Deterministic rules — patterns and checksums of Russian
personal IDs — run before the model, over inputs, tool results and outputs. A
guard error rejects the request instead of passing it through.

**Bounded by construction.** Max steps, per-tool limits, tool timeouts and
argument size are enforced by the runtime, not asked for in a prompt. No
streaming: reviewing the output needs the whole answer.

## In code

```go
app, err := kit.New(ctx, kit.Config{
	ES:     es,     // your Elasticsearch client
	LLM:    llm,    // vLLM, Ollama, OpenAI-compatible or GigaChat
	Policy: policy, // authz.Load("policy.yaml")
})
if err != nil {
	return err
}
if err := app.Setup(ctx); err != nil { // creates the indexes once
	return err
}

// A tool is a typed Go function: its schema and validation come from weatherIn.
type weatherIn struct {
	City string `json:"city"`
}

func getWeather(ctx context.Context, in weatherIn) (domain.Weather, error) {
	return weather.Current(ctx, in.City)
}

if _, err := kit.Tool(app, "get_weather", getWeather, kit.Desc("Current weather for a city.")); err != nil {
	return err
}
```

```go
// Every call carries the caller's identity.
ctx = kit.WithPrincipal(ctx, kit.Principal{UserID: "u-1", Roles: []string{"user"}, Tenant: "acme"})

// Chat answers directly, or calls a tool first, in a bounded loop.
answer, err := app.Chat(ctx, kit.ChatRequest{
	System:   "You are a support assistant. Answer briefly.",
	Messages: []model.Message{{Role: model.RoleUser, Content: "Weather in Moscow?"}},
})
```

```go
// Structured output: the JSON Schema is derived from the Go type.
type Ticket struct {
	Severity int    `json:"severity"`
	Summary  string `json:"summary"`
}

ticket, err := kit.Generate[Ticket](ctx, app, kit.GenerateRequest{
	Prompt: "Classify: the export job failed for three tenants.",
})
```

```go
// RAG: chunk, embed and index, then search BM25 + kNN under the caller's ACL.
err = app.Ingest(ctx, kit.Doc{ID: "policy-1", Text: text, ACL: []string{"user"}})
hits, err := app.Search(ctx, kit.SearchRequest{Query: "refund policy", TopK: 5})

// Delete and score go through the same gate.
err = app.Delete(ctx, "policy-1")
err = app.Score(ctx, trace.Score{TraceID: traceID, Name: "helpful", Value: 1})
```

## Learn more

- [Runnable demo →](./demo)
- [Documentation →](./docs)

## License

[`MIT`](LICENSE).
