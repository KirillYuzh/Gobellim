# AGENTS.md

This repository is **kit**. The documentation is the specification: read it before changing code.

## Read order

1. `docs/README.md` — index and "where to look".
2. `docs/internals/architecture.md` — package layout and dependency rules; do not violate them.
3. `docs/internals/business-rules.md` — the BR-xx rules your change must keep true.
4. `docs/internals/plan.md` — current stage and Definition of Done.

## Rules

- If code and docs disagree, fix the docs in the same change.
- Every operation goes through `internal/call` (`authz → guard → span → fn`). Never add user-facing middleware or bypass the policy.
- `authz`, `guard`, `model`, `embed`, `chunk`, `store` must not import each other (`docs/internals/architecture.md`).
- The default build (`go build ./...`) must not require native libraries; ONNX/ORT code goes behind `//go:build ort`.
- Tests use the standard library only. Validate with `go build ./... && go vet ./... && go test ./...`.

## Layout

| Path                                                           | What                                                                                       |
| -------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| `docs/`                                                        | the specification: `getting-started/`, `concepts/`, `reference/`, `internals/`             |
| `*.go` (root)                                                  | the `kit` facade: `New`, `Chat`, `Ingest`, `Search`, `Delete`, `Score`, `Generate`, `Tool` |
| `internal/`                                                    | call primitive, core types, ORT pool, shared helpers                                       |
| `authz/` `guard/` `model/` `embed/` `chunk/` `store/` `trace/` | the subsystems                                                                             |
| `cmd/kitctl/`, `demo/`, `deploy/`                              | CLI, integration example, local stack                                                      |
| `tools/gliner/`                                                | L2 model export (`export.py`) + pipeline/graph spec; generates `models/` (gitignored)      |
