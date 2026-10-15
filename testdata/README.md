# testdata

Fixtures for tests, kept out of the packages on purpose (docs/internals/architecture.md).

| What                        | Used by                            | Stage |
| --------------------------- | ---------------------------------- | ----- |
| `policy.valid.yaml`         | `authz` strict parsing             | 1     |
| `policy.unknown-field.yaml` | `authz` strict parsing (must fail) | 1     |

Golden files for the GLiNER2 parity spike (Этап 0) and the ES mapping/`_msearch`
golden JSON (Этап 3) also belong here when those stages are implemented.
