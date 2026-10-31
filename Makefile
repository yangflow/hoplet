.DEFAULT_GOAL := help

# Go targets are skipped until the module exists, so `make check` stays green
# while the repository holds only docs.
HAS_GO := $(wildcard go.mod)

.PHONY: help check fmt fmt-check lint test gen gen-check

help: ## Show available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

check: fmt-check lint test gen-check ## Run every check CI runs

fmt: ## Format Go code
ifneq ($(HAS_GO),)
	golangci-lint fmt ./...
endif

fmt-check: ## Fail if Go code is not formatted
ifneq ($(HAS_GO),)
	@test -z "$$(golangci-lint fmt --diff ./... 2>&1)" || { golangci-lint fmt --diff ./...; exit 1; }
endif

lint: ## Run golangci-lint
ifneq ($(HAS_GO),)
	golangci-lint run ./...
endif

test: ## Run tests with the race detector
ifneq ($(HAS_GO),)
	go test -race ./...
endif

gen: ## Regenerate generated code
ifneq ($(HAS_GO),)
	go generate ./...
endif

gen-check: ## Fail if generated code is out of date
ifneq ($(HAS_GO),)
	@before="$$(git status --porcelain; git diff)"; go generate ./...; \
	after="$$(git status --porcelain; git diff)"; \
	[ "$$before" = "$$after" ] || { echo "Generated code is out of date; run make gen and commit the result."; git status --short; exit 1; }
endif
