.PHONY: build test lint docker-build clean help

# Variables
BINARY_NAME=cm-gc
DOCKER_IMAGE=cm-collector
DOCKER_TAG=latest
GO_FILES=$(shell find . -name '*.go' -type f)

help:
	@echo "Available Make targets:"
	@echo "  make build        - Build the binary"
	@echo "  make test         - Run unit tests with coverage"
	@echo "  make lint         - Run golangci-lint"
	@echo "  make docker-build - Build Docker image"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make deploy       - Deploy to Kubernetes (kubectl apply)"

## build: Build binary to bin/
build:
	@echo "=> Building $(BINARY_NAME)..."
	@mkdir -p bin
	@CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/$(BINARY_NAME) ./cmd/cm-gc
	@echo "✓ Build complete: bin/$(BINARY_NAME)"

## test: Run all unit tests with coverage report
test:
	@echo "=> Running unit tests with coverage..."
	@go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
	@go tool cover -func=coverage.out
	@echo "Coverage report written to coverage.out"

## lint: Run golangci-lint
lint:
	@echo "=> Running golangci-lint..."
	@if ! command -v golangci-lint &> /dev/null; then \
		echo "golangci-lint not found, install it with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi
	@golangci-lint run --timeout=5m

## docker-build: Build Docker image
docker-build:
	@echo "=> Building Docker image: $(DOCKER_IMAGE):$(DOCKER_TAG)..."
	@docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .
	@echo "✓ Docker image built successfully"

## deploy: Apply Kubernetes manifests
deploy:
	@echo "=> Deploying to Kubernetes..."
	@kubectl apply -f deploy/

## clean: Remove build artifacts
clean:
	@echo "=> Cleaning build artifacts..."
	@rm -rf bin/
	@rm -f coverage.out
	@echo "✓ Clean complete"

.DEFAULT_GOAL := help
