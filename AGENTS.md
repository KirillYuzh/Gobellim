# AGENTS.md

This repository is **kit**. The specification lives in `docs/` only: read it before changing code.
Code comments are not a contract — do not infer behaviour from them, and never let a comment
override the specification.

## Read order

1. `docs/README.md` — index and "where to look".
2. `docs/internals/design.md` — the frozen contracts: invariants `I-01`…`I-15`, package contracts, decisions `D21`…`D30`, and §5, the API changelist.
3. `docs/internals/architecture.md` — package layout and dependency rules; do not violate them.
4. `docs/internals/business-rules.md` — the BR-xx rules your change must keep true.
5. `docs/internals/plan.md` — current stage and Definition of Done.

`docs/guides/` is the how-to layer — task recipes (providers, structured output, tool calling, guardrails, retrieval, observability, eval, pipelines, errors, deployment, performance, security). `docs/concepts/` is the explanation layer, and `docs/reference/` is the lookup layer; see `docs/guides/README.md` for the Diátaxis split.

## Rules

- If code and docs disagree, fix the docs in the same change.
- Every operation goes through `internal/call` (`authz → guard → span → fn`). Never add user-facing middleware or bypass the policy.
- `authz`, `guard`, `model`, `embed`, `chunk`, `store`, `trace` must not import each other (`docs/internals/architecture.md`); the only permitted core-to-core edge is `trace → store`. `eval` imports `trace` and `internal/**` only and never `kit`. The rules are enforced two ways: the stdlib-only test `internal/importrules` (`go test ./...`) and the `depguard` rules in `.golangci.yml`.
- The default build (`go build ./...`) must not require native libraries; ONNX/ORT code goes behind `//go:build ort`.
- Detection does not depend on the rail; the rail only affects the decision table (`docs/internals/design.md`, `I-02`).
- Traces carry masked text by default (`Config.TraceText`, `trace.TextMasked`); never log or persist raw message text.
- Tests use the standard library only. Validate with `go build ./... && go vet ./... && go test ./...` (`make check` adds `gofumpt` and `golangci-lint`).

## Layout

| Path                                                           | What                                                                                                                 |
| -------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `docs/`                                                        | the specification: `getting-started/`, `concepts/` (explanation), `reference/`, `guides/` (how-to), `internals/`     |
| `*.go` (root)                                                  | the `kit` facade: `New`, `Chat`, `Ingest`, `Search`, `Delete`, `Score`, `Prune`, `Generate`, `Tool`, `RetrievalTool` |
| `internal/`                                                    | call primitive, core types, ORT pool, shared helpers                                                                 |
| `internal/importrules/`                                        | stdlib-only test enforcing the package dependency rules (I-13)                                                       |
| `authz/` `guard/` `model/` `embed/` `chunk/` `store/` `trace/` | the subsystems                                                                                                       |
| `guard/guardtest/`                                             | `AllowAll` test detector (I-15)                                                                                      |
| `eval/`                                                        | deterministic checks over recorded traces (stdlib only; imports `trace`)                                             |
| `flow/`                                                        | typed, sequential step pipelines with schema validation and suspend/resume (no branch/parallel/foreach/loop DSL)     |
| `webfetch/`                                                    | SSRF-hardened outbound HTTP fetch tool; the built-in `EffectEgress` tool                                             |
| `cmd/kitctl/`, `demo/`, `deploy/`                              | CLI (`model pull`, `policy check`, `verify …`), integration example, local stack                                     |
| `tools/gliner/`                                                | L2 model export (`export.py`) + pipeline/graph spec; generates `models/` (gitignored)                                |
| `.github/workflows/ci.yml`, `.golangci.yml`, `Makefile`        | the CI harness (build, test, race, ort-vet, lint, fumpt) and the local `make check` gate                             |
