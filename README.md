# Gobellim

**[English](README.md) · [Русский](README.ru.md)**

![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

_Secure LLM primitives for Go._ Retrieval, tool calling and guardrails behind one
authorization-checked call path.

![Gobellim architecture](docs/architecture.en.svg)

## Why Gobellim?

**A runtime layer, not an orchestrator.** [Mastra](https://mastra.ai/), [LangChain](https://www.langchain.com/) and [CrewAI](https://www.crewai.com/) own your
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

**Untrusted content stays untrusted.** Tool results, including retrieved chunks, are
wrapped in `<untrusted-data>` markers, and the system prompt tells the model to treat
them as data, not instructions. A per-tenant rate limit and a tool-free response
cache run inside the same call path.

**Tools from anywhere.** Write a tool as a Go function, pull one from an MCP server
(`mcp.Register`), or reorder retrieval with a cross-encoder reranker (`rerank`): all
of them run through the same authorize → guard → trace path. `App.Health()` reports
the writer queue, the guard and any component that implements a health check.

**Memory lives in your store.** Thread history, semantic recall and working memory
sit in the same Elasticsearch, scoped by tenant and user, behind the same
authorization gate as everything else. A caller-supplied thread ID grants nothing
on its own.

**Safety fails closed.** Two layers run before the model, over inputs, tool
results and outputs: deterministic rules (patterns and checksums of Russian
personal IDs) and an ONNX GLiNER guard that masks PII and rejects unsafe
messages. A guard error rejects the request instead of passing it through.

**Bounded by construction.** Max steps, per-tool limits, tool timeouts and
argument size are enforced by the runtime, not asked for in a prompt. `Chat`
returns the whole answer because the output guard needs the full text; streaming
is a separate `ChatStream` operation that also goes through the guard.

### Operations at a glance

| Operation     | Permission    | Scope                                                       |
| ------------- | ------------- | ----------------------------------------------------------- |
| `Chat`        | `chat`        | permissions are checked again on every tool call            |
| `ChatStream`  | `chat`        | same turn as `Chat`, guarded sliding-window stream          |
| `CallTool`    | `tool:<name>` | one registered tool through the same call path (MCP server) |
| `Memory*`     | `memory`      | thread history, semantic recall and working memory          |
| `Generate[T]` | `generate`    | input and output guard, `ErrorStrategy`                     |
| `Ingest`      | `ingest`      | needs `Tenant` and an `Embedder`                            |
| `Search`      | `search`      | needs `Tenant`, an `Embedder`; `SearchRequest.Filter`       |
| `Delete`      | `delete`      | needs `Tenant`                                              |
| `Prune`       | `prune`       | deletes traces/observations older than a duration           |
| `Score`       | `trace`       | records a score on a trace                                  |

`Ingest`, `Search` and `Delete` require a non-empty `Tenant` in the principal, so an
operation can never widen across tenants. `Prune` is a global, time-based retention
action: it needs the principal and the `prune` permission, not a tenant.

## In code

Your Elasticsearch client is the only place that knows about transport, headers
and `refresh`: kit calls `store.ES` with an explicit `store.Request` and never
infers the content type from the path.

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
			ct = "application/json" // the default; kit never infers it from the path
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

// Generate is a full call-path operation: it needs a principal, the generate
// permission and the input/output guard.
fallback := Ticket{Severity: 3, Summary: "needs triage"}
ticket, err := kit.Generate[Ticket](ctx, app, kit.GenerateRequest[Ticket]{
	Prompt:        "Classify: the export job failed for three tenants.",
	ErrorStrategy: kit.Fallback, // Strict (default) | Warn | Fallback
	Fallback:      &fallback,
})
if ticket.UsedFallback {
	// the model's answer did not decode into Ticket; the fallback was returned
}
_ = ticket.FinishReason // model.FinishStop, FinishLength, FinishToolCalls, ...
```

```go
// RAG: chunk, embed and index, then search BM25 + kNN under the caller's ACL.
err = app.Ingest(ctx, kit.Doc{ID: "policy-1", Text: text, ACL: []string{"user"}})
hits, err := app.Search(ctx, kit.SearchRequest{
	Query: "refund policy",
	TopK:  5,
	// Filter narrows the metadata; it is ANDed with the principal ACL filter.
	Filter: store.Filter{"doc_id": "policy-1", "position": map[string]any{"$gte": 2}},
})

// Delete, prune and score go through the same gate.
err = app.Delete(ctx, "policy-1")
deleted, err := app.Prune(ctx, 30*24*time.Hour) // retention: traces and observations
err = app.Score(ctx, trace.Score{TraceID: traceID, Name: "helpful", Value: 1})
```

## Try it

The [demo](./demo) is an authorized support bot with a seeded knowledge base. It
answers these right after `docker compose up -d`:

| Ask                                            | Answer                   |
| ---------------------------------------------- | ------------------------ |
| "How many days do I have to return an item?"   | 14 calendar days         |
| "How long does delivery to another city take?" | 2–5 business days, ₽400  |
| "What is the warranty on electronics?"         | 12 months                |
| "Can I buy in installments?"                   | yes, "0-0-6" from ₽3,000 |
| "How can I contact support?"                   | 8 999 888-77-66, 24/7    |
| "How do I get 5% cashback?"                    | from ₽50,000             |

The seeded sources are Russian, so the bot may answer in Russian. `admin` can
additionally ask for the weather; `user` never sees that tool: it is removed from
the schema before the model runs. Ask from the [TUI or curl](./demo), or run the
[Postman collection](./demo/postman).

## Learn more

- [Runnable demo →](./demo)
- [Documentation →](./docs)
- [How-to guides →](./docs/guides) — task recipes, including provider setup

## License

[`MIT`](LICENSE).
