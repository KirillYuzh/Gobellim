# Releasing

## Before the tag

1. `develop` is green and the release pull request `develop → main` is merged.
2. Every submodule pins the core to the version being released:
   `require github.com/KirillYuzh/Gobellim vX.Y.Z` in `mcp`, `webfetch`, `flow`,
   `eval`, `kittest`, `cmd`, `demo` and `scripts`. The local
   `replace github.com/KirillYuzh/Gobellim => ..` stays for development;
   consumers ignore it, so the `require` must name a published version.
3. `go -C scripts run ./docslint` reports 0 failures.
4. `make check` passes.

## Tag

The core tag is the release. The `release` workflow runs on `v*`, generates a
CycloneDX SBOM for every module, uploads them as an artifact and attaches them to
the GitHub release.

```sh
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

Optional modules are separate Go modules and need their own tags on the same
commit:

```sh
for m in mcp webfetch flow eval kittest cmd demo scripts; do
  git tag -a "$m/vX.Y.Z" -m "$m/vX.Y.Z"
  git push origin "$m/vX.Y.Z"
done
```

The `v*` tag filter does not match `mcp/vX.Y.Z`, so these tags do not create a
second release.

## After the tag

In a consuming project:

```sh
go get github.com/KirillYuzh/Gobellim@vX.Y.Z
go get github.com/KirillYuzh/Gobellim/mcp@vX.Y.Z   # optional module
```

Verify:

- `go list -m github.com/KirillYuzh/Gobellim@vX.Y.Z` resolves through the module
  proxy.
- The GitHub release lists the SBOMs.
- `pkg.go.dev/github.com/KirillYuzh/Gobellim@vX.Y.Z` renders the godoc.

## Versioning

Pre-1.0, minor versions may break the API and patch versions do not. From v1 the
module path stays the same; breaking changes need a `/v2` module path. The docs
describe the tagged commit, so update `docs/` before tagging.
