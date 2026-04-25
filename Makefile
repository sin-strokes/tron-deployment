BINARY     := trond
MODULE     := github.com/tronprotocol/tron-deployment
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS    := -s -w -X $(MODULE)/cmd.version=$(VERSION) -X $(MODULE)/cmd.commit=$(COMMIT) -X $(MODULE)/cmd.buildTime=$(BUILD_TIME)

GOFLAGS    ?=
GOTEST     := go test
GOLINT     := golangci-lint

.PHONY: build test lint e2e build-all clean fmt vet tidy sync-templates

## build: Build the trond binary for the current platform
build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

## test: Run unit tests
test:
	$(GOTEST) ./... -race -count=1

## lint: Run golangci-lint
lint:
	$(GOLINT) run ./...

## e2e: Run end-to-end tests (requires Docker)
e2e:
	$(GOTEST) ./... -tags=e2e -race -count=1 -timeout 10m

## build-all: Cross-compile for all supported platforms
build-all:
	GOOS=linux  GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 .
	GOOS=linux  GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 .
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64 .

## clean: Remove build artifacts
clean:
	rm -rf bin/

## fmt: Format Go source files
fmt:
	go fmt ./...

## vet: Run go vet
vet:
	go vet ./...

## tidy: Tidy go.mod
tidy:
	go mod tidy

## sync-templates: Refresh mainnet + nile config templates from upstream
##                 Source-of-truth URLs:
##                   mainnet: tronprotocol/java-tron develop branch
##                   nile:    tron-nile-testnet/nile-testnet master branch
##                 The private_net_config.conf is maintained in-repo and is
##                 NOT refreshed by this target.
MAINNET_URL := https://raw.githubusercontent.com/tronprotocol/java-tron/develop/framework/src/main/resources/config.conf
NILE_URL    := https://raw.githubusercontent.com/tron-nile-testnet/nile-testnet/master/framework/src/main/resources/config-nile.conf

sync-templates:
	@echo "fetching mainnet template..."
	curl -fsSL $(MAINNET_URL) -o main_net_config.conf
	cp main_net_config.conf internal/render/templates/main_net_config.conf
	@echo "fetching nile template..."
	curl -fsSL $(NILE_URL) -o test_net_config.conf
	cp test_net_config.conf internal/render/templates/test_net_config.conf
	@echo "templates refreshed. Re-run 'make build test' to confirm."
