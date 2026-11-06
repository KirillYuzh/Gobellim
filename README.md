# Gobellim

**[English](README.md) · [Русский](README.ru.md)**

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

![Gobellim](./site/public/gobellim-compact-white.svg)

Execution and policy layer for one LLM use case inside an existing Go service.
Gobellim is a library, not a platform: it starts no server, database or broker,
and the core module has no required dependencies.

![Gobellim architecture](docs/architecture.en.svg)

The main invariant:

> The model may propose text, arguments or an action. It cannot grant itself
> permissions, change policy, register a tool or execute a side effect outside
> the Gate.

## The delta: one handler, made safe

An ordinary HTTP handler calls your LLM client directly:

```go
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	answer, err := h.llm.Complete(r.Context(), req.Prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"answer": answer})
}
```

The same handler with Gobellim:

```diff
 func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
     var req chatRequest
     if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
         http.Error(w, err.Error(), http.StatusBadRequest)
         return
     }
-    answer, err := h.llm.Complete(r.Context(), req.Prompt)
+    res, err := h.task.Run(r.Context(), h.principal(r), TicketInput{
+        TenantID: principalFromRequest(r).Tenant,
+        TicketID: req.TicketID,
+        Body:     req.Prompt,
+    })
     if err != nil {
         http.Error(w, err.Error(), http.StatusBadGateway)
         return
     }
-    writeJSON(w, map[string]string{"answer": answer})
+    if res.Status == gobellim.TaskApprovalRequired {
+        // res.Pending is the escalated action. Nothing was executed.
+        writeJSON(w, map[string]any{"status": "approval_required", "action": res.Pending.Card})
+        return
+    }
+    writeJSON(w, map[string]string{"answer": res.Output.Reply})
 }
```

The handler keeps its `ctx`, its tooling and its business functions. Gobellim
adds one place where every model-initiated call is decided, and one place where
an approval resumes it.

## What Gobellim adds

```mermaid
flowchart LR
    H["your handler"] --> P["Principal"]
    P --> T["Task: typed input<br/>labels, handles, limits"]
    T --> M["model proposes<br/>output or tool call"]
    M --> G{"Gate"}
    G -->|"tenant · RBAC/ACL · grounding · Validate · access"| A["Allow"]
    G --> D["Deny"]
    G --> E["Escalate"]
    A --> X["your tool handler"]
    X --> R["redacted result<br/>back to the model"]
    E --> C["PendingAction<br/>digest · nonce · expiry"]
    C -->|"trusted approval"| EA["ExecuteApproved<br/>Gate re-runs"]
    EA --> X
    G --> AU["audit: labels and digests"]
```

- **Principal** — who calls: user, tenant, roles. It comes from your auth layer,
  never from model text.
- **Gate** — one function that turns a proposed action into `Allow`, `Deny` or
  `Escalate`. Unknown tool, invalid arguments, an authorizer error or a tenant
  mismatch are never `Allow`.
- **PendingAction** — a serializable escalation: resolved arguments, digest,
  nonce, expiry, and an HMAC that ties it to the engine that issued it. It is the
  only way to execute what an approver accepted, it is single-use, and an
  approval endpoint must never accept its fields from a client.
- **Foreign approval** — the decision about an escalated action comes from
  outside the model: a person, or your own business logic acting as an
  authenticated `Principal`. The Gate checks it the same way in both cases.
- **Audit** — labels, digests, decisions and counters. No prompt text, no raw
  arguments, no secrets.

Each is a gate on the call path, not a hook you can forget.

## Why Gobellim

Gobellim is a library, not a service: you keep your handlers, your concurrency
and your infrastructure. Each claim below is structural, and each one has code
and tests behind it.

- **One call path, no bypass.** Every model-initiated tool call is decided in
  [`gate.go`](./gate.go); the model loop in [`task.go`](./task.go) cannot reach a
  handler without it. [`internal/importrules`](./internal/importrules) is the
  build-time guard on the package graph.
- **The model gets no say in access.** Permissions and access are declared on
  the tool ([`tool.go`](./tool.go)), checked by your `Authorizer`, and re-checked
  when an approved action resumes.
- **Raw secrets stay out of the prompt.** Input fields and tool results are
  masked or replaced with handles ([`handles.go`](./handles.go),
  [`redact.go`](./redact.go)); the model can only reference a handle.
- **An approval is a bound, single-use action.** The digest covers the tool and
  its arguments; the nonce is claimed before the handler runs
  ([`claims.go`](./claims.go), [`flow/`](./flow)); and the engine's HMAC rejects
  an action it did not issue ([`approval.go`](./approval.go)). The approver may
  be a person or business logic, but never the model.
- **Audit is not a log of everything.** Events carry labels and digests, not
  values ([`audit.go`](./audit.go)); an unfinished run is audited as a failure,
  never as success.

General-purpose Go LLM frameworks give you adapters. You can wrap one with a
policy and a guard yourself — and then you own that wrapper and its tests.
Gobellim is that wrapper: the Gate, the approvals and the security suites ship
with it.

## Install

```sh
go get github.com/KirillYuzh/Gobellim
```

## Quickstart

The application keeps its handler, its `context.Context` and its business
functions. Gobellim fixes the model loop around them.

```go
type TicketInput struct {
	TenantID string `gobellim:"source=principal,required"`
	TicketID string `gobellim:"source=application,required"`
	Body     string `gobellim:"required,sensitivity=pii"`
	OrderID  string `gobellim:"sensitivity=secret"`
}

type TriageOutput struct {
	Category string `gobellim:"required,oneof=billing|technical|other"`
	Reply    string
}

type RefundArgs struct {
	OrderID string `gobellim:"required,ground"`
	Cents   int    `gobellim:"min=1,max=5000"`
}

type RefundResult struct {
	RefundID string
	Status   string
}

refund := gobellim.Tool[RefundArgs, RefundResult]{
	Name:       "orders.refund",
	Access:     gobellim.Write, // required: the zero value is rejected
	Permission: "orders:refund",
	Target: func(p gobellim.Principal, a RefundArgs) gobellim.Resource {
		return gobellim.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
	},
	// Validate is a deny check. A nil result does not raise trust.
	Validate: validateRefundAgainstBusinessRules,
	Run: func(ctx context.Context, p gobellim.Principal, a RefundArgs) (RefundResult, error) {
		return orders.Refund(ctx, p.Tenant, a.OrderID, a.Cents)
	},
}

engine, err := gobellim.New(
	gobellim.WithModel(model),               // your adapter; see plugins/gigachat
	gobellim.WithAuthorizer(existing),       // your RBAC/ACL
	gobellim.WithAudit(gobellim.SlogSink{}), // required; or plugins/langfuse
	gobellim.WithLimits(gobellim.Limits{
		MaxInputBytes:  32 << 10,
		MaxOutputBytes: 16 << 10,
		MaxToolCalls:   3,
	}),
)
if err != nil {
	return err
}

task, err := gobellim.NewTask[TicketInput, TriageOutput](engine, gobellim.TaskConfig{
	Name:        "ticket.triage",
	Instruction: "Classify the ticket and propose a reply.",
	Tools:       []gobellim.ToolDef{refund},
})
if err != nil {
	return err
}

res, err := task.Run(ctx, principalFromRequest(r), in)
if err != nil {
	return err
}
if res.Status == gobellim.TaskApprovalRequired {
	// res.Pending is the escalated action. After a trusted approver — a person
	// or your business logic — accepts it:
	//   out, rec, err := task.ExecuteApproved(ctx, requester, approver, res.Pending)
}
```

`gobellim.New` uses functional options. A nil model, authorizer or audit sink is
`ErrInvalidConfig`; there is no permissive default and no silent run without an
audit trail. In a test that ignores audit, pass
`gobellim.SlogSink{Logger: slog.New(slog.DiscardHandler)}`. Budgets can be tightened per
task with `TaskConfig.Limits`; a zero field inherits the engine value.

## Grounding is a handle

A `ground` field accepts only a handle minted for this run, and the handle must
come from the declared source. A bare `ground` uses the argument path, so
`OrderID` accepts a handle from an input field named `OrderID`. If the input
field has another name, write it explicitly: `ground=RequestedOrder`.

A bare `ground` binds to the argument's own JSON path, so the input field and the
argument must share a name. When they do not, name the source: `ground=TicketID`.
`ground=any` accepts a handle from any input field and is the explicit opt-out of
the binding.

```go
type TicketInput struct {
	OrderID string `gobellim:"sensitivity=secret,required"`
}
type RefundArgs struct {
	OrderID string `gobellim:"required,ground"`
}
// The model sees {"OrderID":"<h1>"} and may pass "<h1>" to orders.refund.
```

A handle that reaches a non-`ground` field is rejected before the tool runs,
even if the token exists. `ground=any` exists for tools that really do accept
any handle; it is a deliberate weakening, not the default.

## Foreign approval

An approval does not have to come from a person. It is any decision about an
escalated action made outside the model: a manager in your UI, or your own
business logic acting as an authenticated `Principal`. The Gate checks the
decision the same way in both cases; the model never approves itself.

`Task` turns an escalated tool call into a `PendingAction`; the application
stores it, the approver decides, and `ExecuteApproved` runs the action after
re-checking tenant, RBAC/ACL, `Validate`, access and the exact digest the
approver saw. `flow` escalates a workflow step the same way and returns a
server-side `Suspension` that `Resume` checks. Both are single-use, both claim
the nonce before the effect, and both reject an object the engine did not issue.

`FourEyes` is an optional separation-of-duties check: the approver must be a
different `Principal` from the requester. Keep it when the approver is a person;
business logic running as its own service principal can enforce whatever
difference your policy requires.

## Observability

```go
sink, err := langfuse.New(langfuse.Config{
	Host:      "https://cloud.langfuse.com",
	PublicKey: os.Getenv("LANGFUSE_PUBLIC_KEY"),
	SecretKey: os.Getenv("LANGFUSE_SECRET_KEY"),
	OnDrop:    func(e gobellim.Event) { metrics.Inc("audit_dropped", string(e.Kind)) },
})
if err != nil {
	return err
}
defer sink.Close(ctx)

engine, err := gobellim.New(
	gobellim.WithModel(model),
	gobellim.WithAuthorizer(authz),
	gobellim.WithAudit(sink),
)
```

The sink batches `trace-create`, `generation-create` and `span-create` events. It
sends only what an audit event already contains: labels, digests, decisions,
latency and token counts. Prompt text, raw arguments and secrets are never sent.
Principal id and tenant are off by default; `SendPrincipalID` / `SendTenant`
opt in when a deployment needs that correlation. For a local walkthrough, use
`langfuse.Config{DryRun: true, Writer: os.Stdout}`.

Model adapters live in optional packages. `plugins/gigachat` implements the
freemium OAuth flow and maps GigaChat's `functions`/`function_call` shape. See
the runnable notebooks.

## Guarantees and non-goals

Guaranteed when the application integrates correctly:

- a model tool call never reaches a handler without the Gate;
- unknown tool, invalid arguments, authorizer error and a tenant mismatch do not
  become `Allow`;
- an empty argument set is not treated as trusted;
- a `ground` argument cannot be satisfied by a raw model string;
- a handle is rejected outside a `ground` field, and a `ground` field accepts
  only a handle from its declared source;
- free model text does not become trusted through a JSON schema, a `Validate`
  call or a paraphrase;
- an approval is bound to the tool and arguments it was shown,
  `ExecuteApproved` re-runs the Gate, the approval is single-use, and an action
  the engine did not issue is rejected;
- `sensitivity` on a string, `[]string`, `[]byte` or `map[string]string` result
  keeps that value out of the model context; a tag on another type is a
  configuration error;
- audit events carry labels and digests, not prompt text or secrets;
- an unfinished run is audited as `task.failed`, never as `task.completed`;
- a tool handler that panics is not converted to `Deny`: the panic unwinds
  through the runtime, the run is audited as failed, and your server's
  `recover` decides what happens next.

Not guaranteed:

- handles are resolved or stripped in `Out`: a `<hN>` the model echoed reaches
  the caller as text;
- exactly-once for an external side effect: a shared `Store` gives a single-use
  claim, idempotency belongs to the provider;
- redaction inside a `time.Time` or a self-marshalling type in a tool result;
- that the model produces no harmful or false text;
- exact data-flow tracking inside the model;
- cross-replica single-use resume without a shared `Store`;
- durable recovery of an arbitrary Go function;
- retrieval/RAG. The library does not fetch or filter documents; the
  application applies its data ACL before it calls `Task`;
- `ReaderSet` / per-reader data ACL. It is not implemented; the application owns
  its data ACL.

## Documentation

`docs/` is plain Markdown and reads directly on GitHub. `site/` renders the same
files with Next.js and Fumadocs:

```sh
cd site && npm install && npm run dev
```

- [`DESIGN.md`](./DESIGN.md) — the specification.
- [`docs/README.md`](./docs/README.md) — the how-to guide.
- [`examples/notebooks`](./examples/notebooks/README.md) — runnable scenarios,
  mocks and real GigaChat free-tier calls.
- [`plugins/langfuse`](./plugins/langfuse) — audit sink.
- [`plugins/gigachat`](./plugins/gigachat) — model adapter.

## Contributing

Gobellim accepts issues, forks and pull requests. Start with
[CONTRIBUTING.md](.github/CONTRIBUTING.md). Security reports go through
[SECURITY.md](SECURITY.md), not the public tracker.

## License

[`MIT`](LICENSE)
