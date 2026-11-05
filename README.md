# Gobellim

**Execution and policy layer for one LLM use case inside an existing Go service.**

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Gobellim is a library, not a platform. It starts no server, database or broker,
and the core module has no required dependencies.

The main invariant:

> The model may propose text, arguments or an action. It cannot grant itself
> permissions, change policy, register a tool or execute a side effect outside
> the Gate.

## Status

P0 and P1 from [`DESIGN.md`](./DESIGN.md) are implemented: typed tasks, typed
tools, schema by tags, labels and handles, the Gate, tenant binding, audit
events, limits, a sequential workflow with approval cards, sealed continuations,
resume checks and a `database/sql` claims store.

P2 is not implemented: replay/diff, detectors, HTTP approval handlers,
OpenTelemetry, quorum, compensation, and editing an approved action.

The runnable, notebook-style examples live in
[`examples/notebooks`](./examples/notebooks/README.md).

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
	Effect:     gobellim.Write, // required: the zero value is rejected
	Permission: "orders:refund",
	Resource: func(p gobellim.Principal, a RefundArgs) gobellim.Resource {
		return gobellim.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
	},
	Validate: validateRefundAgainstBusinessRules,
	Run: func(ctx context.Context, p gobellim.Principal, a RefundArgs) (RefundResult, error) {
		return orders.Refund(ctx, p.Tenant, a.OrderID, a.Cents)
	},
}

engine, err := gobellim.New(
	gobellim.WithModel(model),         // your adapter; see plugins/gigachat
	gobellim.WithAuthorizer(existing), // your RBAC/ACL
	gobellim.WithAudit(gobellim.SlogSink{}), // or plugins/langfuse
	gobellim.WithLimits(gobellim.Limits{
		MaxInputBytes:  32 << 10,
		MaxOutputBytes: 16 << 10,
		MaxToolCalls:   3,
	}),
)
if err != nil {
	return err
}

task, err := gobellim.NewTask(engine, gobellim.TaskConfig[TicketInput, TriageOutput]{
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
	// res.Approval is the card. The action was not executed.
}
```

`gobellim.New` uses functional options. A nil model or authorizer is
`ErrInvalidConfig`; there is no permissive default.

## The Gate

Every model-initiated call goes through the Gate: tenant binding, RBAC/ACL,
grounding, business validation, then the effect ceiling.

| Worst argument integrity | Read  | Write    | Egress | Destructive |
| ------------------------ | ----- | -------- | ------ | ----------- |
| `Trusted` / `Validated`  | allow | allow    | allow  | escalate    |
| `Derived`                | allow | escalate | deny   | deny        |
| `Untrusted` / no labels  | allow | escalate | deny   | deny        |

On top of the table:

- `Effect` is explicit. Its zero value is `EffectUnknown` and `NewTask` rejects
  it, so a mutating tool cannot silently become `Read`.
- an empty label set is `Untrusted`, not `Trusted`;
- `Resource.TenantID` must match `Principal.Tenant` (an empty value is filled
  from the principal);
- an `Authorizer` error is `Deny`;
- an unknown tool or invalid arguments is `Deny`;
- an argument marked `ground` must be an existing handle; a raw value is `Deny`;
- a business `Validate` error is `Deny`;
- the model's own claims about permissions never matter.

A `Task` returns `TaskApprovalRequired` and a card for an escalated action. The
action is not executed. For resumable human approval, use `flow`.

## Schema by tags

The supported subset is deliberately small and fails closed.

| Tag                    | Meaning                                                      |
| ---------------------- | ------------------------------------------------------------ |
| `required`             | reject the zero value (not allowed on `bool`)                |
| `source=principal`     | the value must equal the matching field of `Principal`       |
| `source=application`   | the value is application state and becomes trusted           |
| `untrusted`            | explicit untrusted input (the default for an untagged field) |
| `sensitivity=pii`      | replace email/phone/card-like matches with handles           |
| `sensitivity=secret`   | replace the whole value with one handle                      |
| `sensitivity=internal` | label only; no masking                                       |
| `oneof=a\|b`           | string enum                                                  |
| `min`, `max`           | numeric range, or item count for a slice                     |
| `ground`               | the argument must be an existing handle                      |

`source` and `ground` apply only to strings. `sensitivity` applies to strings
and `[]string`. Supported field types: `string`, `bool`, integers, floats,
slices of scalars, and nested structs up to two levels. `time.Time`, `[]byte`,
maps, pointers, interfaces, embedded structs without an explicit `json` name
and deeper nesting are rejected by `NewTask`. This is intentional: a schema that
does not match `encoding/json` would lie to the model.

The model-facing JSON Schema is generated from these tags, but the runtime
decodes and validates the arguments again. The schema is not a security
boundary.

## Grounding is a handle

A `ground` field accepts only a handle minted for this run. The model cannot
invent an ID and cannot copy an unrelated trusted string. To let the model
reference a value, mark the input field `sensitivity=secret` (or
`sensitivity=pii`). `source=application` labels the value `Trusted`, but does not
create a handle by itself.

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

`flow` is the sequential, code-driven workflow. It never resumes a Go stack: it
suspends at a step boundary, seals the minimal state into a token and continues
from the next step after the checks in `DESIGN.md` §8.6.

```go
sealer, err := flow.NewSealer("v1", key) // AES-GCM; the key is an app secret
if err != nil {
	return err
}
store, err := sqlstore.New(db) // optional: cross-replica replay protection
if err != nil {
	return err
}
wf, err := flow.New[RefundCase]("refund",
	flow.WithAuthorizer(authz),
	flow.WithSealer(sealer), // required
	flow.WithStore(store),
	flow.WithTTL(24*time.Hour),
)
if err != nil {
	return err
}

wf.Step("prepare", prepareRefund).
	Approve("manager", flow.ApproveConfig[RefundCase]{
		Permission:       "refund:approve",
		ActionPermission: "orders:refund",
		FourEyes:         true,
		Card:             refundApprovalCard,
		Resource:         refundResource,
	}).
	Step("execute", executeRefund)

res, err := wf.Run(ctx, requester, state)
// res.Status == flow.Suspended
// send res.Suspension.Card and res.Suspension.Token to the approver UI

out, err := wf.Resume(ctx, approver, res.Suspension.Token, flow.Approve)
```

Resume checks the AEAD, tenant binding, expiry, workflow version, approver
permission, four-eyes, the requester's action permission, the action digest and
an optional atomic claim. A rejected approval, an expired token, a revoked
permission or a changed action never executes the step.

`flow.Store` has one method: `Claim`. The SQL adapter also exposes `Prune` for
expired nonces. The step after `Approve` is the action itself. Make it
idempotent: a crash after the external call cannot be rolled back, and the
library does not promise exactly-once delivery.

## Observability

```go
sink, err := langfuse.New(langfuse.Config{
	Host:      "https://cloud.langfuse.com",
	PublicKey: os.Getenv("LANGFUSE_PUBLIC_KEY"),
	SecretKey: os.Getenv("LANGFUSE_SECRET_KEY"),
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
- an approval is bound to the tool and arguments it was shown;
- audit events carry labels and digests, not prompt text or secrets.

Not guaranteed:

- that the model produces no harmful or false text;
- exact data-flow tracking inside the model;
- exactly-once for an external side effect;
- cross-replica single-use resume without a `Store`;
- durable recovery of an arbitrary Go function;
- retrieval/RAG. The library does not fetch or filter documents; the
  application applies its data ACL before it calls `Task`;
- `ReaderSet` / per-reader data ACL. It is not implemented; the application owns
  its data ACL.

## Install

```sh
go get github.com/KirillYuzh/Gobellim
```

## Documentation

- [`DESIGN.md`](./DESIGN.md) — the specification.
- [`docs/`](./docs) — the how-to guide.
- [`examples/notebooks`](./examples/notebooks/README.md) — runnable scenarios,
  mocks and real GigaChat free-tier calls.
- [`plugins/langfuse`](./plugins/langfuse) — audit sink.
- [`plugins/gigachat`](./plugins/gigachat) — model adapter.

## License

[`MIT`](LICENSE)
