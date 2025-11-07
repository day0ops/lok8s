OS ?= $(shell uname)
ARCH ?= $(shell uname -m)

# Read Go version from go.mod
GO_VERSION := $(shell grep '^go ' go.mod | awk '{print $$2}')

GOOS ?= $(shell echo "$(OS)" | tr '[:upper:]' '[:lower:]')
GOARCH_x86_64 = amd64
GOARCH_aarch64 = arm64
GOARCH_arm64 = arm64
GOARCH ?= $(shell echo "$(GOARCH_$(ARCH))")

VERSION := $(shell git describe --tags --exact-match 2>/dev/null || git rev-parse --short HEAD 2>/dev/null || echo "dev")
REVISION := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
PACKAGE := github.com/day0ops/lok8s/pkg/config
VERSION_VARIABLES := -X $(PACKAGE).AppVersion=$(VERSION)

OUTPUT_DIR := _output/binaries
OUTPUT_BIN := lok8s-$(GOOS)-$(ARCH)
BIN_NAME := lok8s

LDFLAGS := $(VERSION_VARIABLES)

# Define all target platforms
PLATFORMS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64

# Build tags
# libvirt_dlopen enables dynamic loading of libvirt at runtime
BUILD_TAGS ?= libvirt_dlopen

# GOARM setting for ARM architectures
# For ARM64 (arm64), GOARM is ignored but included for consistency
# For 32-bit ARM (arm), GOARM must be set (5, 6, or 7)
# Setting to 7 for ARMv7 compatibility (most common 32-bit ARM)
GOARM ?= 7

# Detect if we're cross-compiling
NATIVE_OS := $(shell uname | tr '[:upper:]' '[:lower:]')
NATIVE_ARCH := $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
IS_CROSS_COMPILE := $(if $(filter $(NATIVE_OS),$(GOOS)),$(if $(filter $(NATIVE_ARCH),$(GOARCH)),0,1),1)

.PHONY: all
all: build-all

.PHONY: clean
clean:
	rm -rf _output _build

.PHONY: fmt
fmt: install-go-tools
	go fmt ./...
	goimports -w .

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test:
	go test -v ./pkg/...

.PHONY: test-unit
test-unit:
	go test -v -race -coverprofile=coverage.out ./pkg/...

.PHONY: test-all
test-all: test-unit

.PHONY: coverage
coverage: test-unit
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

.PHONY: build
build: fmt vet
	mkdir -p $(OUTPUT_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(if $(filter linux,$(GOOS)),-tags=$(BUILD_TAGS)) -ldflags="$(LDFLAGS)" -o $(OUTPUT_DIR)/$(OUTPUT_BIN) ./cmd
ifeq ($(GOOS),darwin)
	codesign -s - $(OUTPUT_DIR)/$(OUTPUT_BIN) 2>/dev/null || true
endif
	cd $(OUTPUT_DIR) && openssl sha256 -r $(OUTPUT_BIN) | sed 's/ \*/ /' > $(OUTPUT_BIN).sha256sum || shasum -a 256 $(OUTPUT_BIN) > $(OUTPUT_BIN).sha256sum

.PHONY: build-all
build-all: fmt vet
	@echo "Building lok8s for all platforms..."
	@mkdir -p $(OUTPUT_DIR)
	@$(foreach platform,$(PLATFORMS), \
		echo "Building for $(platform)..."; \
		ARCH=$(word 2,$(subst /, ,$(platform))); \
		OS=$(word 1,$(subst /, ,$(platform))); \
		$(if $(filter linux,$$OS),CGO_ENABLED=1) GOOS=$$OS GOARCH=$$ARCH \
			go build $(if $(filter linux,$$OS),-tags=$(BUILD_TAGS),) \
				-ldflags="$(LDFLAGS)" \
				-o $(OUTPUT_DIR)/lok8s-$$OS-$$ARCH ./cmd; \
	)
	@echo "Generating checksums..."
	@cd $(OUTPUT_DIR) && for binary in lok8s-*; do \
		if [ -f "$$binary" ] && [ ! -f "$$binary.sha256sum" ]; then \
			echo "Processing $$binary"; \
			openssl sha256 -r "$$binary" | sed 's/ \*/ /' > "$$binary.sha256sum" 2>/dev/null || shasum -a 256 "$$binary" > "$$binary.sha256sum"; \
		fi \
	done
	@echo "Build complete! Binaries available in $(OUTPUT_DIR)/"
	@ls -la $(OUTPUT_DIR)/

.PHONY: build-darwin
build-darwin: fmt vet
	@echo "Building lok8s for macOS..."
	@mkdir -p $(OUTPUT_DIR)
	@echo "Building darwin amd64..."
	@GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(OUTPUT_DIR)/lok8s-darwin-amd64 ./cmd
	@echo "Building darwin arm64..."
	@GOOS=darwin GOARCH=arm64 GOARM=$(GOARM) go build -ldflags="$(LDFLAGS)" -o $(OUTPUT_DIR)/lok8s-darwin-arm64 ./cmd
	@cd $(OUTPUT_DIR) && for binary in lok8s-darwin-amd64 lok8s-darwin-arm64; do \
		if [ -f "$$binary" ] && [ ! -f "$$binary.sha256sum" ]; then \
			echo "Code signing $$binary..."; \
			codesign -s - "$$binary" 2>/dev/null || true; \
			codesign --verify --verbose "$$binary" 2>/dev/null || true; \
			echo "Generating checksum for $$binary..."; \
			openssl sha256 -r "$$binary" | sed 's/ \*/ /' > "$$binary.sha256sum"; \
		fi \
	done
	@echo "macOS builds complete!"

.PHONY: build-linux
build-linux: build-linux-amd64 build-linux-arm64
	@echo "Linux builds complete!"

.PHONY: build-linux-amd64
build-linux-amd64: fmt vet
	@echo "Building lok8s for Linux amd64..."
	@mkdir -p $(OUTPUT_DIR)
	@CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -tags=$(BUILD_TAGS) -ldflags="$(LDFLAGS)" -o $(OUTPUT_DIR)/lok8s-linux-amd64 ./cmd
	@cd $(OUTPUT_DIR) && if [ -f "lok8s-linux-amd64" ] && [ ! -f "lok8s-linux-amd64.sha256sum" ]; then \
		openssl sha256 -r "lok8s-linux-amd64" | sed 's/ \*/ /' > "lok8s-linux-amd64.sha256sum" 2>/dev/null || shasum -a 256 "lok8s-linux-amd64" > "lok8s-linux-amd64.sha256sum"; \
	fi
	@echo "Linux amd64 build complete!"

.PHONY: build-linux-arm64
build-linux-arm64: fmt vet
	@echo "Building lok8s for Linux arm64..."
	@mkdir -p $(OUTPUT_DIR)
	@CGO_ENABLED=1 GOOS=linux GOARCH=arm64 GOARM=$(GOARM) go build -buildvcs=false -tags=$(BUILD_TAGS) -ldflags="$(LDFLAGS)" -o $(OUTPUT_DIR)/lok8s-linux-arm64 ./cmd
	@cd $(OUTPUT_DIR) && if [ -f "lok8s-linux-arm64" ] && [ ! -f "lok8s-linux-arm64.sha256sum" ]; then \
		openssl sha256 -r "lok8s-linux-arm64" | sed 's/ \*/ /' > "lok8s-linux-arm64.sha256sum" 2>/dev/null || shasum -a 256 "lok8s-linux-arm64" > "lok8s-linux-arm64.sha256sum"; \
	fi
	@echo "Linux arm64 build complete!"

.PHONY: checksums
checksums:
	@echo "Generating checksums for all binaries..."
	@cd $(OUTPUT_DIR) && for binary in lok8s-*; do \
		if [ -f "$$binary" ] && [ ! -f "$$binary.sha256sum" ]; then \
			echo "Generating checksum for $$binary"; \
			openssl sha256 -r "$$binary" | sed 's/ \*/ /' > "$$binary.sha256sum" 2>/dev/null || shasum -a 256 "$$binary" > "$$binary.sha256sum"; \
		fi \
	done

.PHONY: build-container
build-container:
	@echo "Building lok8s using containers..."
	@if command -v podman >/dev/null 2>&1; then \
		echo "Using Podman for containerized builds..."; \
		$(MAKE) build-container-podman; \
	elif command -v docker >/dev/null 2>&1; then \
		echo "Using Docker for containerized builds..."; \
		$(MAKE) build-container-docker; \
	else \
		echo "Neither Podman nor Docker found. Using native Go cross-compilation..."; \
		$(MAKE) build-all; \
	fi

.PHONY: build-container-podman
build-container-podman:
	@mkdir -p $(OUTPUT_DIR)
	@podman run --rm -v "$$(pwd):/workspace" -w /workspace \
		golang:$(GO_VERSION) bash -c '\
		apt-get update && apt-get install -y git make gcc libc6-dev pkg-config && \
		make build-all'

.PHONY: build-container-docker
build-container-docker:
	@mkdir -p $(OUTPUT_DIR)
	@echo "Note: If using Docker Desktop, ensure the path $$(pwd) is shared in Docker settings."
	@docker run --rm -v "$$(pwd):/workspace" -w /workspace \
		golang:$(GO_VERSION) bash -c '\
		apt-get update && apt-get install -y git make gcc libc6-dev pkg-config && \
		make build-all'

.PHONY: install-go-tools
install-go-tools:
	@echo "Installing Go development tools..."
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "Installing golangci-lint v2..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin v2.6.1; \
	else \
		INSTALLED_VERSION=$$(golangci-lint version 2>/dev/null | head -1); \
		if echo "$$INSTALLED_VERSION" | grep -qE '\bv2\.|version.*2\.'; then \
			echo "golangci-lint v2 is already installed"; \
		else \
			echo "Upgrading golangci-lint to v2..."; \
			curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin v2.6.1; \
		fi \
	fi
	@if ! command -v goimports >/dev/null 2>&1; then \
		echo "Installing goimports..."; \
		go install golang.org/x/tools/cmd/goimports@latest; \
	else \
		echo "goimports is already installed"; \
	fi

.PHONY: install
install: build
	cp $(OUTPUT_DIR)/$(OUTPUT_BIN) /usr/local/bin/$(BIN_NAME)

.PHONY: dev
dev:
	go run ./cmd

.PHONY: deps
deps:
	go mod tidy
	go mod download

.PHONY: lint
lint: install-go-tools
	golangci-lint run --config .golangci.yaml ./...

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build     - Build binary for current platform"
	@echo "  build-all - Build binaries for all platforms"
	@echo "  build-darwin - Build macOS binaries (amd64, arm64)"
	@echo "  build-linux - Build Linux binaries (amd64, arm64) with CGO support"
	@echo "  build-linux-amd64 - Build Linux amd64 binary (requires CGO)"
	@echo "  build-linux-arm64 - Build Linux arm64 binary (requires CGO)"
	@echo "  build-container - Build using containers (Podman/Docker)"
	@echo "  checksums - Generate checksums for all binaries"
	@echo "  clean     - Clean build artifacts"
	@echo "  deps      - Download dependencies"
	@echo "  dev       - Run in development mode"
	@echo "  fmt       - Format code"
	@echo "  help      - Show this help"
	@echo "  install   - Install binary to /usr/local/bin"
	@echo "  install-go-tools - Install Go development tools (golangci-lint, goimports)"
	@echo "  lint      - Run linter"
	@echo "  test      - Run unit tests"
	@echo "  test-unit - Run unit tests with coverage"
	@echo "  test-all  - Run all tests"
	@echo "  coverage  - Generate test coverage report"
	@echo "  vet       - Run go vet"