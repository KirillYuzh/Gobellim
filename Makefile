GO        ?= go
GOFUMPT   ?= gofumpt
GOLANGCI  ?= golangci-lint

.PHONY: check require-tools build test lint fmt fmt-check tidy docs-lint

# Full local gate. Fails fast: require-tools runs first,
# then fmt-check, then docs-lint, then the build/vet/test/lint chain.
check: require-tools fmt-check docs-lint
	@echo "==> go build ./..."
	@$(GO) build ./...
	@echo "==> go vet ./..."
	@$(GO) vet ./...
	@echo "==> go test ./... -count=1"
	@$(GO) test ./... -count=1
	@echo "==> go vet -tags ort ./... (type-check only)"
	@$(GO) vet -tags ort ./...
	@echo "==> golangci-lint run"
	@$(GOLANGCI) run
	@echo "all checks passed"

# Documentation gate: broken relative links, references to undefined I-/BR-/D-
# ids, closed ranges that freeze the numbering, and duplicate definitions.
docs-lint:
	@echo "==> docs-lint"
	@$(GO) run ./scripts/docslint/

# Fail fast with a helpful hint when a required tool is missing.
require-tools:
	@command -v $(GO) >/dev/null 2>&1 || { echo "missing: $(GO) — install Go from https://go.dev/dl/"; exit 1; }
	@command -v $(GOFUMPT) >/dev/null 2>&1 || { echo "missing: $(GOFUMPT) — install with 'go install mvdan.cc/gofumpt@latest'"; exit 1; }
	@command -v $(GOLANGCI) >/dev/null 2>&1 || { echo "missing: $(GOLANGCI) — install from https://golangci-lint.run/welcome/install/"; exit 1; }

build:
	@$(GO) build ./...

test:
	@$(GO) test ./... -count=1

lint:
	@$(GOLANGCI) run

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
