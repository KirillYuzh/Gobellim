# Gobellim

**[English](README.md) · [Русский](README.ru.md)**

![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Native Go primitives for using LLMs safely: retrieval, tool calling and
guardrails behind one permission-checked call path.

![Gobellim architecture](docs/architecture.en.svg)

## Why Gobellim

- **A point solution, not a platform.** Drop it into one handler in a service you
  already have — no runtime, no graph engine, no lifecycle to adopt. You keep your
  handlers and your concurrency.
- **It runs on your infrastructure.** Your Elasticsearch, Postgres or Kafka is
  reached through a port you implement. We never ship, own or copy your data.
- **A small, immutable core.** The default build is pure Go: no `cgo`, no native
  libraries, no network, and a dependency budget you can read at a glance.
- **Everything else is a plugin.** Storage, queues, caches and the heavy ML
  detector are opt-in; if you don't import a plugin, its dependencies and its
  start-up cost are not in your binary.
- **Safety you cannot forget.** Every operation runs the same four steps —
  authorize, guard, trace, execute — so no check can be skipped.
- **The model gets no say in access.** Tools the caller may not use are removed
  from the schema before the model runs, and re-checked when they execute.

## Install

```sh
go get github.com/KirillYuzh/Gobellim
```

## Quickstart

```go
app, err := kit.New(ctx, kit.Config{
	ES:     es,     // optional: your store.ES client (retrieval, memory, traces)
	LLM:    llm,    // vLLM, Ollama, OpenAI-compatible or GigaChat
	Policy: policy, // authz.Load("policy.yaml")
})
if err != nil {
	return err
}
if err := app.Setup(ctx); err != nil { // creates the indexes once
	return err
}

// A tool is a typed Go function: its schema and validation come from the type.
if _, err := kit.Tool(app, "get_weather", getWeather, kit.Desc("Current weather for a city.")); err != nil {
	return err
}

// Every call carries the caller's identity.
ctx = kit.WithPrincipal(ctx, kit.Principal{UserID: "u-1", Roles: []string{"user"}, Tenant: "acme"})

answer, err := app.Chat(ctx, kit.ChatRequest{
	System:   "You are a support assistant. Answer briefly.",
	Messages: []model.Message{{Role: model.RoleUser, Content: "Weather in Moscow?"}},
})
```

## Operations

| Operation     | Permission    | Notes                                              |
| ------------- | ------------- | -------------------------------------------------- |
| `Chat`        | `chat`        | bounded tool loop; permissions re-checked per call |
| `ChatStream`  | `chat`        | guarded sliding-window stream                      |
| `CallTool`    | `tool:<name>` | one tool through the same call path                |
| `Memory*`     | `memory`      | thread history, recall and working memory          |
| `Generate[T]` | `generate`    | typed output, validated against its schema         |
| `Ingest`      | `ingest`      | chunk, embed and index                             |
| `Search`      | `search`      | BM25 + kNN under the caller's ACL                  |
| `Delete`      | `delete`      | remove a document                                  |
| `Prune`       | `prune`       | retention by age                                   |
| `Score`       | `trace`       | attach a score to a trace                          |

`Ingest`, `Search` and `Delete` require a non-empty `Tenant`, so an operation can
never widen across tenants.

## Guardrails

<!-- derived: BR-26 -->

Input, tool results and output all pass the guard. L1 — deterministic rules
(Russian personal IDs and prompt injection) — is in the core and works out of the
box. L2 is the optional ML detector: `guard-onnx` (local, built with `-tags ort`)
or `guard-remote` (a shared service). Without L2, kit masks and rejects on L1 only.

## Demo

The [demo](./demo) is an authorized support bot on a Clean Architecture skeleton
with a seeded knowledge base — retrieval, per-role tool removal, typed output and
guardrails, right after `docker compose up -d`.

## Learn more

- [Documentation →](./docs)
- [How-to guides →](./docs/guides)
- [Runnable demo →](./demo)

## License

[`MIT`](LICENSE)
