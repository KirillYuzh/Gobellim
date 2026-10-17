GO        ?= go
GOFUMPT   ?= gofumpt
GOLANGCI  ?= golangci-lint

# Optional packages extracted into their own modules (D85). rerank stays in the
# core module because the façade imports it (D84).
MODULES := mcp webfetch flow eval kittest cmd demo scripts

.PHONY: check require-tools build test lint fmt fmt-check tidy docs-lint perf

# Full local gate. Fails fast: require-tools runs first,
# then fmt-check, then docs-lint, then the build/vet/test/lint chain.
check: require-tools fmt-check docs-lint
	@echo "==> go build ./..."
	@$(GO) build ./...
	@echo "==> go vet ./..."
	@$(GO) vet ./...
	@echo "==> go test ./... -count=1"
	@$(GO) test ./... -count=1
	@for d in $(MODULES); do \
		echo "==> $$d: build, vet, test"; \
		if [ "$$d" = "cmd" ]; then \
			$(GO) -C $$d build -o /dev/null ./... || exit 1; \
		else \
			$(GO) -C $$d build ./... || exit 1; \
		fi; \
		$(GO) -C $$d vet ./... || exit 1; \
		$(GO) -C $$d test ./... -count=1 || exit 1; \
	done
	@echo "==> go vet -tags ort ./... (type-check only, all modules)"
	@$(GO) vet -tags ort ./...
	@for d in $(MODULES); do $(GO) -C $$d vet -tags ort ./... || exit 1; done
	@echo "==> golangci-lint run"
	@$(GOLANGCI) run
	@for d in $(MODULES); do (cd $$d && $(GOLANGCI) run) || exit 1; done
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
	@$(GO) test ./... -count=1
	@for d in $(MODULES); do $(GO) -C $$d test ./... -count=1 || exit 1; done

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
	@for d in $(MODULES); do $(GO) -C $$d mod tidy || exit 1; done

# Dependency budgets (scripts/perfbudget) and the benchmark set. The hard gate is
# static (direct requires, go.sum size, default-build module closure); ns/op is
# machine-dependent, so the numbers are published for `benchstat`, not gated. A
# second hard gate — the fast-path allocation budget — lives in perf_gate_test.go
# and runs with the ordinary tests.
perf:
	@echo "==> dependency budgets"
	@$(GO) -C scripts run ./perfbudget/
	@echo "==> benchmarks (core overhead, no L2)"
	@$(GO) test -run '^$$' -bench . -benchmem ./...
