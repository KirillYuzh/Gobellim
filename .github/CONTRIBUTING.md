# Contributing to Gobellim

**[English](CONTRIBUTING.md) · [Русский](CONTRIBUTING.ru.md)**

Gobellim is a Go library for LLM calls behind a permission-checked call path.
Bug reports, documentation fixes, examples and code are welcome. Contributors
are listed in [CONTRIBUTORS.md](CONTRIBUTORS.md).

## Ways to contribute

- Bug report: open an issue with the bug template. Include the Go version, the
  Gobellim version or commit, and a minimal reproduction.
- Security issue: do not open a public issue. Follow [SECURITY.md](../SECURITY.md)
  and use GitHub Security Advisories.
- Documentation and examples: a confusing paragraph or a missing example is a
  useful report. Open an issue or send the fix directly.
- Code: start with issues labelled `good first issue`. If one of them is
  unclear, comment on it. That is a labelling bug, not your fault.
- Integration feedback: "I tried Gobellim in my stack and X broke" is a
  contribution. Open an issue.

## Before you write code

Open an issue for anything larger than a typo, so we can agree on the shape
before you spend time on a patch.

The integration branch is `develop`; `main` holds releases. Branch from
`develop` and open the pull request against `develop`.

Requirements:

- Go 1.24 or newer.
- `gofumpt` and `golangci-lint` for the local gate. `make check` prints the
  install command when either is missing.

## Local checks

Run these before opening a pull request:

```sh
make fmt      # gofumpt
make check    # build, vet, test, ort vet, lint, docs links
```

`make check` covers the core module and the optional modules (`mcp`,
`webfetch`, `flow`, `eval`, `kittest`, `cmd`, `demo`, `scripts`). `make test`
and `make lint` run only the test and lint parts.

CI also runs the race detector across all modules and the `-tags ort` type
check. Neither is part of `make check`.

## Tests and documentation

- Tests use the standard library only.
- `docs/` is the specification. If code and docs disagree, fix both in the same
  change.
- Exported API in published packages needs a godoc comment; `internal/apidoc`
  enforces it.

## Pull request lifecycle

1. Fork the repository and branch from `develop`.
2. Keep one logical change per pull request. Do not bundle unrelated refactors.
3. Use a Conventional Commit subject (`feat:`, `fix:`, `docs:`, `test:`,
   `ci:`, `chore:`, `refactor:`).
4. Fill the pull request template: what changed, why, and how you tested it.
5. Wait for CI. A maintainer aims to review within five working days.
6. We merge with a merge commit and keep the pull request title as the commit
   subject.

## What we will not merge

- Changes that weaken the permission-checked call path, skip the guard, or make
  L2 silently optional. These are security invariants, not preferences.
- A hard dependency on one vector store or LLM provider without a port behind
  it.
- Raw user text in traces or logs by default.

If you need one of these for a specific deployment, open an issue first. A
configuration option may be the right answer; a changed default usually is not.

## Sign-off

We ask contributors to sign off their commits under the Developer Certificate
of Origin (DCO). There is no CLA. Add a sign-off to each commit:

```sh
git commit -s
```

Sign-off certifies that you wrote the change or have the right to submit it.

## Questions

Open an issue with the `question` label. "How do I ..." is a valid issue.
