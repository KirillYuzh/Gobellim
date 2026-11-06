# AGENTS.md

This repository is **gobellim**: a small in-process execution and policy layer
around an LLM. The specification is `DESIGN.md`. The `docs/` tree is the how-to
layer and must match the current code; if code and `DESIGN.md` disagree, fix both.

## Read order

1. `DESIGN.md` — threat model, invariants, API, workflow, roadmap.
2. `README.md` — install, quickstart, the one call path.
3. `docs/README.md` — task recipes.
4. `examples/notebooks/README.md` — runnable, notebook-style scenarios.

## Rules

- The model proposes; the Gate decides. Every model-initiated tool call goes
  through the Gate in `gate.go`. Never call a tool handler directly from the
  model loop.
- `gobellim.New` requires a model, an authorizer and an audit sink; `flow.New`
  requires an authorizer and an audit sink. Each missing one is
  `ErrInvalidConfig`. Do not reintroduce a silent no-op default: a run without an
  audit trail must be an explicit
  `SlogSink{Logger: slog.New(slog.DiscardHandler)}`.
- Unknown tool, invalid arguments, authorizer error, tenant mismatch, a target
  mismatch and ungrounded arguments are `Deny`, never `Allow`.
- Gate order is tenant binding → grounding → `Authorizer` → `Tool.Validate` →
  access ceiling. Do not move `Authorizer` before grounding: an id the model
  invented must not reach the application's ACL, and `Validate` must not run for
  a caller who is already unauthorized.
- `Operation` has no permissive default. The zero value is `OperationUnknown` and
  `NewTask` rejects it; declare `OperationRead`, `OperationWrite`, `OperationSend`,
  `OperationDelete` or `OperationCompute`. `Compute` is target-free; every other class
  requires `Tool.Target` to return a `Resource` with a non-empty `Type`, `ID` and
  `TenantID`.
- An empty label set is `Untrusted`. Do not initialize an accumulator to
  `Trusted` without a non-empty-label check.
- `ground` means "the value must be an existing handle minted for this run, from
  the declared input field". A raw string from the model never grounds an
  argument, and a handle in a non-`ground` field is `ErrUngrounded`. A bare
  `ground` binds to the argument path; `ground=SourceField` binds to another
  input field; `ground=any` is the explicit opt-out. To let the model reference a
  value, mark the input field `ref` (public but addressable) or
  `sensitivity=secret` (confidential). `NewTask` rejects a `ground` argument that
  binds to an input field the task does not declare: a renamed input field must
  fail at startup, not as a runtime `Deny`.
- Labels only worsen. Do not add a path that promotes `Untrusted`/`Derived` to
  `Trusted`. There is no promotion inside the Gate: `Tool.Validate` confirms the
  business meaning, not the origin, so its success raises nothing. `Trusted`
  arrives only from a task input field marked `source=principal` or
  `source=application`, and from the handle minted for such a field. Free model
  text is `Derived`; `oneof` and numeric ranges constrain shape, not truth.
- A tool result re-enters the model context, so `sensitivity` on the result
  struct is honoured before the result is marshalled (`redact.go`). Do not add a
  path that puts a raw `secret` field into a tool message. An unknown tag on a
  result is a configuration error.
- `ApprovalCard.Args` is the masked view, never the executable arguments; the
  real values stay in `PendingAction.Args` on the server. Do not put raw values
  back into the card: an approver endpoint stores, renders and logs it.
- `Out` is restored only in a field tagged `resolve`: there a handle this run
  minted becomes its value. Every other field loses all `<hN>` tokens, minted or
  not. Do not pass an unresolved handle to the caller, do not restore a handle
  the run did not mint, and do not make restoration the default: a model that
  types `<h3>` must not make the runtime paste a secret into a reply.
- `WithApprovalKey` requires `WithClaimStore` (and `flow.WithSealer` requires
  `flow.WithStore`): a shared signing key with a per-process claim store lets one
  approval execute once per replica. Both `New`s reject that combination.
- `Target` is required for every operation except `OperationCompute`: without a
  non-empty `Resource` the ACL check silently degrades to RBAC only. An empty
  target is `Deny` with rule `target-unbound`.
- `PendingAction` is single-use. `Task.ExecuteApproved` claims its nonce in the
  `Engine` ClaimStore before the handler runs; a second call is
  `ErrApprovalReplay`. Claim before the effect is deliberate: a crash loses the
  approval instead of double-executing it. A `PendingAction` also carries an HMAC
  from the issuing `Engine`; an object the engine did not issue is
  `ErrApprovalDrift`, so an approval endpoint must never decode one from a
  client. `flow.VerdictReject` claims the nonce too, so a rejection is final.
- `Readers` / per-reader ACL is not implemented. Do not claim it exists. The
  application owns its data ACL.
- `Resource.TenantID` must match `Principal.Tenant`; the Gate enforces this
  before the `Authorizer`. An empty `TenantID` on a non-`Compute` target is
  `Deny` with `target-unbound`; `flow.Resume` applies the same rule to an
  approval step that declares `Target`. Do not fill it from the principal: that
  makes the tenant check pass by construction.
- The core `go.mod` has no required dependencies. Do not add one without an ADR
  and a benchmark; put optional code in its own package or module.
- `flow`, `store/sql`, `testkit`, `plugins/*` and `examples/*` are optional
  packages of the same module. The root package must not import them.
  `internal/importrules` enforces this.
- `flow.Store` has one method: `Claim`. Do not reintroduce `Commit`; `Claim`
  already consumes the nonce.
- `flow.FromEngine` is the intended wiring when a flow resumes a `Task`: it shares
  the `Engine`'s authorizer, audit sink, claim store, clock and sealer. Do not
  build a second authorizer for the approval path; the Gate and the approval
  must check the same ACL.
- A `Suspension` is signed with the same `Sealer` as a `PendingAction`, and
  `Resume` rejects an object the flow did not issue with `ErrFlowDrift`. Do not
  drop the proof to make a round-trip through a client work.
- Tests use the standard library only. Validate with
  `go build ./... && go vet ./... && go test ./... -race -shuffle=on`.
  `testkit.DiscardAudit()` is the explicit sink for a test that ignores audit.
- Never log or persist prompt text, raw arguments, secrets or approval nonces.
  Audit events carry digests and labels, not values.
- The `gobellim:"..."` tag subset is documented in `DESIGN.md` §5.3 and
  `README.md`. Rejecting an unknown tag is intentional.
- `source`, `ref` and `ground` apply to strings only, on opposite sides of the
  boundary: `source` and `ref` on a task input field, `ground` on a tool
  argument. A tag on the wrong side is `ErrInvalidConfig` at `NewTask`, never a
  silent no-op. `ref` and `sensitivity` are mutually exclusive: one makes a value
  addressable, the other limits disclosure.
  `sensitivity` applies to strings and `[]string`. `time.Time`, `[]byte`, maps,
  pointers and embedded structs without an explicit `json` name are rejected at
  `NewTask`.
- `DESIGN.md` lists P2 work (replay/diff, detectors, HTTP approval handlers,
  OpenTelemetry, quorum, compensation, editing approved arguments) that is not
  implemented. Do not claim it exists.

## Layout

| Path                    | What                                                                                                   |
| ----------------------- | ------------------------------------------------------------------------------------------------------ |
| `*.go`                  | the root `gobellim` package: `Engine`, `Task`, `Tool`, `Gate`, labels, handles, schema, audit          |
| `flow/`                 | sequential workflow, approval card, `Suspension` the application stores, `Store` interface, `MemStore` |
| `store/sql/`            | `database/sql` claims adapter with `Prune`                                                             |
| `plugins/langfuse/`     | optional audit sink: batched trace/generation/span ingestion, dry-run mode                             |
| `plugins/gigachat/`     | optional GigaChat model adapter: freemium OAuth, `functions`/`function_call` mapping                   |
| `examples/notebooks/`   | runnable notebook-style scenarios: deterministic mocks and real GigaChat free-tier calls               |
| `testkit/`              | fake model, authorizers, audit recorder                                                                |
| `internal/importrules/` | stdlib-only dependency-direction test                                                                  |
| `DESIGN.md`             | the specification                                                                                      |
| `docs/`                 | how-to documentation                                                                                   |
| `site/`                 | docs site                                                                                              |
