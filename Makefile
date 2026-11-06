GO       ?= go
GOFUMPT  ?= gofumpt
GOLANGCI ?= golangci-lint

.PHONY: check check-full build vet test lint fmt fmt-check tidy tidy-check vuln

# Core gate: one module, no native libraries, no external services.
check: fmt-check build vet test

check-full: check lint vuln

build:
	@$(GO) build ./...

vet:
	@$(GO) vet ./...

test:
	@$(GO) test ./... -race -shuffle=on -count=1

lint:
	@$(GOLANGCI) run

vuln:
	@$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

fmt:
	@$(GOFUMPT) -w .

fmt-check:
	@out="$$($(GOFUMPT) -l .)"; \
	if [ -n "$$out" ]; then \
		echo "gofmt: the following files need formatting:"; \
		echo "$$out"; \
		echo "run 'make fmt' to fix"; \
		exit 1; \
	fi

tidy:
	@$(GO) mod tidy

tidy-check:
	@$(GO) mod tidy -diff
