.DEFAULT_GOAL := help

# Go targets are skipped until the module exists, so `make check` stays green
# while the repository holds only docs.
HAS_GO := $(wildcard go.mod)
# Swift checks need Xcode, so they run on Macs only.
HAS_XCODE := $(shell command -v xcodebuild 2>/dev/null)

.PHONY: help check fmt fmt-check lint test swift-test gen gen-check

help: ## Show available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

check: fmt-check lint test swift-test gen-check ## Run every check CI runs

fmt: ## Format Go code
ifneq ($(HAS_GO),)
	golangci-lint fmt ./...
endif

fmt-check: ## Fail if Go code is not formatted
ifneq ($(HAS_GO),)
	@test -z "$$(golangci-lint fmt --diff ./... 2>&1)" || { golangci-lint fmt --diff ./...; exit 1; }
endif

lint: ## Run golangci-lint and the desktop's UI checks
ifneq ($(HAS_GO),)
	golangci-lint run ./...
	go tool mygo vet desktop
endif

test: ## Run tests with the race detector
ifneq ($(HAS_GO),)
	go test -race ./...
endif

swift-test: ## Run the Swift package tests
ifneq ($(HAS_XCODE),)
	swift format lint --strict -r apple/HopletKit/Tests apple/HopletKit/Sources/HopletProto/JSONValue.swift apple/HopletKit/Sources/HopletProto/ProtoCoding.swift apple/HopletKit/Sources/HopletClient
	swift test --package-path apple/HopletKit -Xswiftc -warnings-as-errors
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
