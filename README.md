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

## Status

P0 and P1 from [`DESIGN.md`](./DESIGN.md) are implemented: typed tasks, typed
tools, schema by tags, labels and handles, the Gate, tenant binding, audit
events, limits, a sequential workflow with approval cards, resume checks and a
`database/sql` claims store. There is no sealed continuation token: `flow` gives
the application the `Suspension` and the state to store (`DESIGN.md` §8.5).

P2 is not implemented: replay/diff, detectors, HTTP approval handlers,
OpenTelemetry, quorum, compensation, and editing an approved action.

The runnable, notebook-style examples live in
[`examples/notebooks`](./examples/notebooks/README.md).

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
    G -->|"tenant · RBAC/ACL · grounding · Validate · operation"| A["Allow"]
    G --> D["Deny"]
    G --> E["Escalate"]
    A --> X["your tool handler"]
    X --> R["redacted result<br/>back to the model"]
    E --> C["PendingAction<br/>digest · nonce · expiry"]
    C -->|"human approves"| EA["ExecuteApproved<br/>Gate re-runs"]
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
  only way to execute what a human approved, it is single-use, and an approval
  endpoint must never accept its fields from a client.
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
- **The model gets no say in access.** Permissions and operation classes are
  declared on the tool ([`tool.go`](./tool.go)), checked by your `Authorizer`,
  and re-checked when an approved action resumes.
- **Raw secrets stay out of the prompt.** Input fields and tool results are
  masked or replaced with handles ([`handles.go`](./handles.go),
  [`redact.go`](./redact.go)); the model can only reference a handle.
- **An approval is a bound, single-use action.** The digest covers the tool and
  its arguments; the nonce is claimed before the handler runs
  ([`claims.go`](./claims.go), [`flow/`](./flow)); and the engine's HMAC rejects
  an action it did not issue ([`approval.go`](./approval.go)).
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
	Operation:     gobellim.Write, // required: the zero value is rejected
	Permission: "orders:refund",
	Target: func(p gobellim.Principal, a RefundArgs) gobellim.Resource {
		return gobellim.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
	},
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
	// res.Pending is the escalated action. After a human approves it:
	//   out, rec, err := task.ExecuteApproved(ctx, requester, approver, res.Pending)
}
```

`gobellim.New` uses functional options. A nil model, authorizer or audit sink is
`ErrInvalidConfig`; there is no permissive default and no silent run without an
audit trail. In a test that ignores audit, pass
`gobellim.SlogSink{Logger: slog.New(slog.DiscardHandler)}`. Budgets can be tightened per
task with `TaskConfig.Limits`; a zero field inherits the engine value.

The snippet above is the body of `Example` in [`example_test.go`](./example_test.go),
which `go test` compiles and runs, so a rename that breaks the quickstart breaks
the build instead of the reader.

## The Gate

Every model-initiated call goes through the Gate: tenant binding, grounding,
RBAC/ACL, business validation, then the operation ceiling. Grounding comes before
the ACL so a resource id the model invented never reaches your `Authorizer`, and
authorization comes before `Validate` so a business rule never runs for a caller
who could not perform the action anyway.

| Worst argument integrity | Read, Compute | Write    | Send  | Delete   |
| ------------------------ | ------------- | -------- | ----- | -------- |
| `Trusted`                | allow         | allow    | allow | escalate |
| `Derived`                | allow         | escalate | deny  | deny     |
| `Untrusted` / no labels  | allow         | escalate | deny  | deny     |

On top of the table:

- `Operation` is explicit. Its zero value is `OperationUnknown` and `NewTask` rejects
  it, so a mutating tool cannot silently become `Read`.
- `Target` is required for `Write`, `Send` and `Delete` and must return a
  `Resource` with a non-empty `Type`, `ID` and `TenantID`. A target without a
  tenant cannot be checked against the caller's tenant, so it is `Deny`, not a
  reason to fill the tenant in from the principal.
- an empty label set is `Untrusted`, not `Trusted`;
- `Resource.TenantID` must match `Principal.Tenant`;
- an `Authorizer` error is `Deny`;
- an unknown tool or invalid arguments is `Deny`;
- an argument marked `ground` must be an existing handle; a raw value is `Deny`;
- a business `Validate` error is `Deny`;
- the model's own claims about permissions never matter.

**How an argument becomes `Trusted`.** Trust is not earned inside the Gate. An
argument is `Trusted` only when its value arrived through a handle minted for a
task input field marked `source=principal` or `source=application`. Free model
text is always `Derived`, and `Tool.Validate` does not raise it: a validator that
passes confirms the business meaning, not where the value came from. That is why
an unattended `Write` needs every argument to come from such a handle, and still
needs RBAC/ACL.

## Schema by tags

The supported subset is deliberately small and fails closed.

| Tag                    | Meaning                                                   |
| ---------------------- | --------------------------------------------------------- |
| `required`             | reject the zero value (not allowed on `bool`)             |
| `source=principal`     | the value must equal the matching field of `Principal`    |
| `source=application`   | the value is application state and becomes trusted        |
| `sensitivity=pii`      | replace email/phone/card-like matches with handles        |
| `sensitivity=secret`   | replace the whole value with one handle                   |
| `sensitivity=internal` | label only; no masking                                    |
| `oneof=a\|b`           | string enum                                               |
| `min`, `max`           | numeric range, or item count for a slice                  |
| `ground`               | the argument must be a handle for the argument's own path |
| `ground=TicketID`      | the argument must be a handle for that input field        |
| `ground=any`           | the argument must be a handle for any input field         |

`source` is meaningful only on a task **input** field and `ground` only on a
tool **argument**. A tag on the wrong side is `ErrInvalidConfig` at `NewTask`,
not a silent no-op: the runtime would never honour it. An untagged field is
already `Untrusted`; there is no tag to spell that out.

`source` and `ground` apply only to strings. `sensitivity` applies to strings
and `[]string`. Supported field types: `string`, `bool`, integers, floats,
slices of scalars, and nested structs up to two levels. `time.Time`, `[]byte`,
maps, pointers, interfaces, embedded structs without an explicit `json` name
and deeper nesting are rejected by `NewTask`. This is intentional: a schema that
does not match `encoding/json` would lie to the model.

The model-facing JSON Schema is generated from these tags, but the runtime
decodes and validates the arguments again. The schema is not a security
boundary.

The same `sensitivity` tags apply to a tool **result** before it re-enters the
model context: a `secret` field becomes a handle and a `pii` field is masked.
A result struct is ordinary Go, so `time.Time` and `json.Marshaler` values are
passed through unchanged. An unknown tag on a result is a configuration error,
not a silent no-op.

## Grounding is a handle

A `ground` field accepts only a handle minted for this run. The model cannot
invent an ID and cannot copy an unrelated trusted string. To let the model
reference a value, mark the input field `sensitivity=secret` (or
`sensitivity=pii`). `source=application` labels the value `Trusted`, but does not
create a handle by itself.

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

## Human approval

A write with model-derived arguments is not executed. `Task.Run` returns
`Status == TaskApprovalRequired` and a `PendingAction` holding the resolved
arguments, their digest, a nonce, an expiry and an HMAC from the issuing engine.

```go
res, err := task.Run(ctx, requester, in)
if err != nil {
	return err
}
if res.Status == gobellim.TaskApprovalRequired {
	// Store res.Pending server-side, keyed by res.RunID. Never accept these
	// fields from a client: the engine rejects an action it did not sign.
	save(ctx, res.RunID, res.Pending)
}
```

After an authenticated approver decides, load the action from your own store and
execute it:

```go
out, rec, err := task.ExecuteApproved(ctx, requester, approver, pending)
```

`ExecuteApproved` re-runs the Gate: tenant binding, RBAC/ACL, `Validate` and the
digest of the exact arguments the approver saw. The operation ceiling is the only
check an approval outside the model replaces, and the approver is recorded as
`approval.granted`. The nonce is claimed before your handler runs, so a second
call is `ErrApprovalReplay` and a crash in that window loses the approval instead
of double-executing it.

For a workflow with several steps and approval boundaries, use `flow`. It
suspends at a step boundary, hands you the `Suspension` and the state to store,
and resumes from the next step:

```go
wf, err := flow.FromEngine[RefundCase](engine, "refund",
	flow.WithTTL(24*time.Hour), // optional; the engine default is 1h
)
if err != nil {
	return err
}

wf.Step("propose", proposeRefund).
	Approve("manager", flow.ApproveConfig[RefundCase]{
		Permission:       "refund:approve", // the approver's own permission
		ActionPermission: "orders:refund",  // re-checked for the requester on resume
		FourEyes:         true,
		Card:             refundCard,     // builds the card from the state
		Target:           refundResource, // tenant binding for the approver's ACL
	}).
	Step("execute", func(ctx context.Context, p gobellim.Principal, s *RefundCase) error {
		// p is the requester: Resume re-runs the remaining steps as them.
		approver, ok := flow.ApproverFrom(ctx)
		if !ok {
			return errors.New("no approver in the context")
		}
		_, _, err := task.ExecuteApproved(ctx, p, approver, &s.Pending)
		return err
	})

res, err := wf.Run(ctx, requester, RefundCase{})
// res.Status == flow.Suspended. Store res.Suspension and res.State; show
// res.Suspension.Card to the approver.

out, err := wf.Resume(ctx, approver, *res.Suspension, res.State, flow.Approve)
```

`flow.FromEngine` takes the authorizer, audit sink, claim store, clock and
signing key from the `Engine`. A flow that authorizes an approver with a
different adapter than the Task it resumes is a security gap, so the default
wiring shares them; `flow.New` builds a flow with no Task around it.

`Resume` checks tenant binding, expiry, workflow version, the step id, the
approver's permission, four-eyes, the requester's action permission, the action
digest recomputed from the stored state, and an atomic claim. A rejected
approval, an expired suspension, a revoked permission or a changed action never
executes the step. A rejection claims the nonce as well, so a "no" cannot be
replayed later as a "yes".

There is no sealed continuation token. `flow` hands you the `Suspension` and the
state and you store both yourself; `DESIGN.md` §8.5 explains what that implies.
The suspension carries an HMAC from the flow that issued it, so an object this
process did not sign is `ErrFlowDrift`: store it server-side and never build one
from a request body.
Without `WithStore` the flow uses a process-local `MemStore`, so a suspension
cannot be resumed twice inside one replica; a shared store is needed between
replicas.

A `PendingAction` carries an HMAC over its fields, keyed by the engine. Keep
it on the server: an approval endpoint that decodes a `PendingAction` from a
request body hands the client the arguments, the digest and the nonce, and the
engine rejects it as `ErrApprovalDrift`. A random per-process key is the default;
`gobellim.WithApprovalKey` sets a shared 32-byte key when one replica escalates
and another executes.

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
For a local walkthrough, use `langfuse.Config{DryRun: true, Writer: os.Stdout}`.

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
- free model text does not become trusted through a JSON schema or a
  paraphrase;
- an approval is bound to the tool and arguments it was shown,
  `ExecuteApproved` re-runs the Gate, the approval is single-use, and an action
  the engine did not issue is rejected;
- `sensitivity` on a tool result keeps a secret out of the model context;
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
- redaction inside a map or a self-marshalling type in a tool result;
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
