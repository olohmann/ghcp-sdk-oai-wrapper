BINARY_NAME := ghcp-sdk-oai-wrapper
BUILD_DIR := bin
INSTALL_DIR := $(HOME)/.local/bin
MAIN_PKG := ./cmd/server

LDFLAGS := -s -w

.PHONY: all build install uninstall test test-e2e vet lint clean run docker-build help

all: build ## Build the binary (default)

build: ## Build the binary to bin/
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PKG)

install: build ## Install to ~/.local/bin
	@mkdir -p $(INSTALL_DIR)
	cp $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "Installed $(INSTALL_DIR)/$(BINARY_NAME)"

uninstall: ## Remove from ~/.local/bin
	rm -f $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "Removed $(INSTALL_DIR)/$(BINARY_NAME)"

test: ## Run unit tests
	go test ./...

test-e2e: build ## Run end-to-end integration tests (requires Copilot CLI)
	go test -tags e2e -v -timeout 10m ./test/e2e/

vet: ## Run go vet
	go vet ./...

lint: vet ## Run linters (go vet)

clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR)

run: build ## Build and run the server
	$(BUILD_DIR)/$(BINARY_NAME)

docker-build: ## Build Docker image
	docker build -t $(BINARY_NAME) .

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
