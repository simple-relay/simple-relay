.PHONY: build test race run clean tidy help

BINARY_NAME=relay
BIN_DIR=bin
MAIN_SRC=./cmd/relay

help: ## Show list of available make targets
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

tidy: ## Tidy and verify go.mod and go.sum
	go mod tidy

build: ## Build static binary without CGO (CGO_ENABLED=0)
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY_NAME) $(MAIN_SRC)

test: ## Run all test suites
	go test -v ./...

race: ## Run test suites with data race detector
	go test -race ./...

run: ## Run relay service directly
	go run $(MAIN_SRC)

clean: ## Clean build artifacts, test coverages, and local databases
	rm -rf $(BIN_DIR) coverage.out coverage.html *.db *.db-shm *.db-wal
