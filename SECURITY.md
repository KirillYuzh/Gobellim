# Security policy

**[English](SECURITY.md) · [Русский](SECURITY.ru.md)**

Gobellim is a security-sensitive library: it sits between your handler and an
LLM, a retrieval store and tools.

## Supported versions

Gobellim is pre-1.0. Only the latest commit on `main` is supported. Fixes land on
`develop` first and are promoted to `main` by a release pull request. There are
no maintained release branches or backports yet.

## Reporting a vulnerability

Do not open a public issue. Use GitHub Security Advisories:
<https://github.com/KirillYuzh/Gobellim/security/advisories/new>

Include the affected commit, what you observed, a minimal reproduction and the
impact you believe it has. We aim to acknowledge within 3 business days and to
ship a fix or a mitigation plan within 30 days for high-severity reports. We will
credit you unless you ask otherwise.

## Threat model

The library assumes:

- your service authenticates users and passes a `kit.Principal` with a user ID,
  roles and tenant;
- `policy.yaml` is trusted input, reviewed like code;
- Elasticsearch, the model provider and the network are outside the trust
  boundary;
- model output and tool results are untrusted data, never instructions.

The full model is in [docs/concepts/security-model.md](docs/concepts/security-model.md).
The deployment checklist and the limits of each control are in
[docs/guides/security.md](docs/guides/security.md).

## Covered by construction

- **Prompt injection.** Untrusted-input taint (`D68`), the Rule of Two for
  irreversible actions, and guard rails on input, tool results and output.
- **PII leakage.** L1 masks Russian personal identifiers. The model, the embedder,
  the BM25 query and the trace receive masked text (`BR-04`, `BR-112`).
- **Tool abuse.** One permission-checked call path. Tools the caller may not use
  are removed from the schema before the model call and re-checked when they
  execute.
- **Cross-tenant access.** Tenant and ACL are part of the kNN filter, and search
  is deny-by-default (`BR-13`).
- **Egress.** Host allowlists match decoded JSON values, and egress is blocked
  after untrusted input in the same turn (`BR-51`, `BR-111`).
- **SSRF.** `webfetch` rejects loopback, private and link-local targets.

Each claim has code and tests behind it; the [Why Gobellim](README.md#why-gobellim)
section points at both.

## Not covered

- **End-user authentication.** You supply the principal; the library does not
  verify tokens or signatures.
- **A remote guard service.** `guard-remote` is not implemented (`D76`). L1-only
  is an explicit, visible choice through `guard.L1Only()`.
- **Audit of role and policy changes.** The operation audit log
  (`Config.AuditLog`) records calls, not who edited `policy.yaml`.
- **Encryption at rest and index isolation.** Both belong to Elasticsearch and
  your infrastructure.
- **Compliance certification.** The library provides controls, not a certificate.

## Release and dependency hygiene

A release publishes a CycloneDX SBOM for every Go module as a release asset and
a CI artifact. Pull-request CI runs build, vet, tests, the race detector, lint,
gofumpt, documentation links, the import rules, guard quality and the performance
budget. The ORT parity gate runs nightly. The core module has no native
dependencies; the ONNX path is opt-in behind `-tags ort`.
