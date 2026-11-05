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
- Unknown tool, invalid arguments, authorizer error, tenant mismatch and
  ungrounded arguments are `Deny`, never `Allow`.
- `Effect` has no permissive default. The zero value is `EffectUnknown` and
  `NewTask` rejects it; declare `EffectRead`, `EffectWrite`, `EffectEgress` or
  `EffectDestructive`.
- An empty label set is `Untrusted`. Do not initialize an accumulator to
  `Trusted` without a non-empty-label check.
- `ground` means "the value must be an existing handle minted for this run". A
  raw string from the model never grounds an argument. To let the model
  reference a value, mark the input field `sensitivity=secret` or
  `sensitivity=pii`.
- Labels only worsen. Do not add a path that promotes `Untrusted`/`Derived` to
  `Trusted`/`Validated` without an explicit validator or a human approval. The
  one promotion in the core is `Tool.Validate` returning nil on a handle-backed
  argument; a tool without `Validate` promotes nothing. Free model text is
  `Derived`; `oneof` and numeric ranges constrain shape, not truth.
- A tool result re-enters the model context, so `sensitivity` on the result
  struct is honoured before the result is marshalled (`redact.go`). Do not add a
  path that puts a raw `secret` field into a tool message. An unknown tag on a
  result is a configuration error.
- `Resource` is required for `Write`, `Egress` and `Destructive`: without a
  target the ACL check silently degrades to RBAC only.
- `PendingAction` is single-use. `Task.ExecuteApproved` claims its nonce in the
  `Engine` ClaimStore before the handler runs; a second call is
  `ErrApprovalReplay`. Claim before the effect is deliberate: a crash loses the
  approval instead of double-executing it. A `PendingAction` also carries an HMAC
  from the issuing `Engine`; an object the engine did not issue is
  `ErrApprovalDrift`, so an approval endpoint must never decode one from a
  client. `flow.Reject` claims the nonce too, so a rejection is final.
- `Readers` / per-reader ACL is not implemented. Do not claim it exists. The
  application owns its data ACL.
- `Resource.TenantID` must match `Principal.Tenant`; the Gate enforces this
  before the `Authorizer`.
- The core `go.mod` has no required dependencies. Do not add one without an ADR
  and a benchmark; put optional code in its own package or module.
- `flow`, `store/sql`, `testkit`, `plugins/*` and `examples/*` are optional
  packages of the same module. The root package must not import them.
  `internal/importrules` enforces this.
- `flow.Store` has one method: `Claim`. Do not reintroduce `Commit`; `Claim`
  already consumes the nonce.
- Tests use the standard library only. Validate with
  `go build ./... && go vet ./... && go test ./... -race -shuffle=on`.
- Never log or persist prompt text, raw arguments, secrets or continuation
  tokens. Audit events carry digests and labels, not values.
- The `gobellim:"..."` tag subset is documented in `DESIGN.md` §5.3 and
  `README.md`. Rejecting an unknown tag is intentional.
- `source` and `ground` apply to strings only. `sensitivity` applies to strings
  and `[]string`. `time.Time`, `[]byte`, maps, pointers and embedded structs
  without an explicit `json` name are rejected at `NewTask`.
- `DESIGN.md` lists P2 work (replay/diff, detectors, HTTP approval handlers,
  OpenTelemetry, quorum, compensation, editing approved arguments) that is not
  implemented. Do not claim it exists.

## Layout

| Path                    | What                                                                                                                 |
| ----------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `*.go`                  | the root `gobellim` package: `Engine`, `Task`, `Tool`, `Gate`, labels, handles, schema, audit                        |
| `flow/`                 | sequential workflow, approval card, sealed continuation, `Store` interface, `MemStore`                               |
| `store/sql/`            | `database/sql` claims adapter with `Prune`                                                                           |
| `plugins/langfuse/`     | optional audit sink: batched trace/generation/span ingestion, dry-run mode                                           |
| `plugins/gigachat/`     | optional GigaChat model adapter: freemium OAuth, `functions`/`function_call` mapping                                 |
| `examples/notebooks/`   | runnable notebook-style scenarios: deterministic mocks and real GigaChat free-tier calls                             |
| `testkit/`              | fake model, authorizers, audit recorder                                                                              |
| `internal/importrules/` | stdlib-only dependency-direction test                                                                                |
| `DESIGN.md`             | the specification                                                                                                    |
| `docs/`                 | how-to documentation                                                                                                 |
| `site/`                 | docs site                                                                                                            |
| `embedding-demo/`       | embeddings example: e5-small via ONNX Runtime in-process, hybrid BM25 + kNN in Elasticsearch, own module and compose |
