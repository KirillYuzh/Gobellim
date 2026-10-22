GO        ?= go
GOFUMPT   ?= gofumpt
GOLANGCI  ?= golangci-lint

# Optional packages extracted into their own modules (D85). rerank stays in the
# core module because the façade imports it (D84).
MODULES := mcp webfetch flow eval kittest cmd scripts plugins/elasticsearch plugins/gigachat plugins/otel

# Every module with its own go.mod: the library family plus lite-demo, the
# sample project (not part of the family, but dependency-gated like the rest).
TIDY_MODULES := $(MODULES) lite-demo

# govulncheck is pinned so the gate is reproducible, and it is invoked by
# absolute path because $(go env GOPATH)/bin is not necessarily on PATH.
GOVULNCHECK_VERSION ?= v1.9.0
GOVULNCHECK = $$($(GO) env GOPATH)/bin/govulncheck

.PHONY: check require-tools build test lint fmt fmt-check tidy tidy-check docs-lint perf

# Full local gate. Fails fast: require-tools runs first, then fmt-check,
# tidy-check, then docs-lint, then the build/vet/test/lint chain. Mirrors CI:
# the same -race -shuffle=on test flags, the same tidy check, and the same
# pinned govulncheck.
check: require-tools fmt-check tidy-check docs-lint
	@echo "==> go build ./..."
	@$(GO) build ./...
	@echo "==> go vet ./..."
	@$(GO) vet ./...
	@echo "==> go test ./... -race -shuffle=on -count=1"
	@$(GO) test ./... -race -shuffle=on -count=1
	@for d in $(MODULES); do \
		echo "==> $$d: build, vet, test"; \
		if [ "$$d" = "cmd" ]; then \
			$(GO) -C $$d build -o /dev/null ./... || exit 1; \
		else \
			$(GO) -C $$d build ./... || exit 1; \
		fi; \
		$(GO) -C $$d vet ./... || exit 1; \
		$(GO) -C $$d test ./... -race -shuffle=on -count=1 || exit 1; \
	done
	@echo "==> go vet -tags ort ./... (type-check only, all modules)"
	@$(GO) vet -tags ort ./...
	@for d in $(MODULES); do $(GO) -C $$d vet -tags ort ./... || exit 1; done
	@echo "==> golangci-lint run"
	@$(GOLANGCI) run
	@for d in $(MODULES); do (cd $$d && $(GOLANGCI) run) || exit 1; done
	@echo "==> govulncheck ./... ($(GOVULNCHECK_VERSION))"
	@$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@$(GOVULNCHECK) ./...
	@for d in $(MODULES); do (cd $$d && $(GOVULNCHECK) ./...) || exit 1; done
	@echo "all checks passed"

# Documentation gate: broken relative links, references to undefined I-/BR-/D-
# ids, closed ranges that freeze the numbering, and duplicate definitions.
docs-lint:
	@echo "==> docs-lint"
	@$(GO) -C scripts run ./docslint/

# Fail fast with a helpful hint when a required tool is missing.
require-tools:
	@command -v $(GO) >/dev/null 2>&1 || { echo "missing: $(GO) — install Go from https://go.dev/dl/"; exit 1; }
	@command -v $(GOFUMPT) >/dev/null 2>&1 || { echo "missing: $(GOFUMPT) — install with 'go install mvdan.cc/gofumpt@latest'"; exit 1; }
	@command -v $(GOLANGCI) >/dev/null 2>&1 || { echo "missing: $(GOLANGCI) — install from https://golangci-lint.run/welcome/install/"; exit 1; }

build:
	@$(GO) build ./...
	@for d in $(MODULES); do if [ "$$d" = "cmd" ]; then $(GO) -C $$d build -o /dev/null ./...; else $(GO) -C $$d build ./...; fi || exit 1; done

test:
	@$(GO) test ./... -race -shuffle=on -count=1
	@for d in $(MODULES); do $(GO) -C $$d test ./... -race -shuffle=on -count=1 || exit 1; done

lint:
	@$(GOLANGCI) run
	@for d in $(MODULES); do (cd $$d && $(GOLANGCI) run) || exit 1; done

fmt:
	@$(GOFUMPT) -w .

fmt-check: require-tools
	@out="$$($(GOFUMPT) -l .)"; \
	if [ -n "$$out" ]; then \
		echo "gofumpt: the following files need formatting:"; \
		echo "$$out"; \
		echo "run 'make fmt' to fix"; \
		exit 1; \
	fi

tidy:
	@$(GO) mod tidy
	@for d in $(TIDY_MODULES); do $(GO) -C $$d mod tidy || exit 1; done

# Fail if `go mod tidy` would change go.mod/go.sum: the same check the CI
# `deps` job runs. -diff reports the change without touching the working
# tree, so a failed gate never leaves a half-tidied module behind.
tidy-check:
	@echo "==> go mod tidy -diff (must change nothing)"
	@$(GO) mod tidy -diff || { echo "core go.mod/go.sum are not tidy - run 'make tidy'"; exit 1; }
	@for d in $(TIDY_MODULES); do \
		echo "==> $$d"; \
		$(GO) -C $$d mod tidy -diff || { echo "$$d/go.mod or $$d/go.sum are not tidy - run 'make tidy'"; exit 1; }; \
	done

# Dependency budgets (scripts/perfbudget) and the benchmark set. The hard gate is
# static (direct requires, go.sum size, default-build module closure); ns/op is
# machine-dependent, so the numbers are published for `benchstat`, not gated —
# the CI `perf` job publishes the same numbers in its job summary and gates
# nothing on them. A second hard gate — the fast-path allocation budget —
# lives in perf_gate_test.go and runs with the ordinary tests.
perf:
	@echo "==> dependency budgets"
	@$(GO) -C scripts run ./perfbudget/
	@echo "==> benchmarks (core overhead, no L2)"
	@$(GO) test -run '^$$' -bench . -benchmem ./...
