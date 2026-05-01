BINARY     := trond
MODULE     := github.com/tronprotocol/tron-deployment
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS    := -s -w -X $(MODULE)/cmd.version=$(VERSION) -X $(MODULE)/cmd.commit=$(COMMIT) -X $(MODULE)/cmd.buildTime=$(BUILD_TIME)

# --- Project-local Go toolchain --------------------------------------
#
# We install Go into .go-toolchain/<ver>/ and route every Make target
# through it via $(GO). Module cache + tool binaries land under .gopath/
# in the same project so a fresh `make clean-all` removes every byte
# this repo ever downloaded.
#
# Why: avoids the "which Go does the user have" question entirely. A
# fresh clone followed by `make build` produces an identical binary
# regardless of what's installed on the host, and the build never
# pollutes the user's $HOME/go cache.
#
# The user can still set USE_SYSTEM_GO=1 to fall back to whatever `go`
# resolves on PATH (useful in CI runners that already pinned Go via
# actions/setup-go and want to skip the download step).

GO_VERSION ?= 1.25.9

ifeq ($(USE_SYSTEM_GO),1)
GO         := go
GO_BOOTSTRAP :=
else
GO         := $(CURDIR)/.go-toolchain/$(GO_VERSION)/bin/go
GO_BOOTSTRAP := bootstrap-go
export GOROOT := $(CURDIR)/.go-toolchain/$(GO_VERSION)
export GOPATH := $(CURDIR)/.gopath
# GOBIN must override the user's shell-exported GOBIN. Without this,
# `go install` writes binaries into the user's $HOME/go/bin (where the
# user has GOBIN pointing) and our recipes can't find them under
# $(GOPATH)/bin. Forcing it here keeps the install fully scoped.
export GOBIN  := $(GOPATH)/bin
export PATH   := $(GOROOT)/bin:$(GOBIN):$(PATH)
endif

GOFLAGS    ?=

.PHONY: build test lint e2e build-all clean clean-all fmt vet tidy sync-templates docs man cover vuln bootstrap-go

## bootstrap-go: Download + verify the project-local Go toolchain
##               (idempotent; safe to re-run; no-op if already current)
bootstrap-go:
	@GO_VERSION=$(GO_VERSION) ./scripts/bootstrap-go.sh

## build: Build the trond binary for the current platform
build: $(GO_BOOTSTRAP)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

## test: Run unit tests
test: $(GO_BOOTSTRAP)
	$(GO) test ./... -race -count=1

## lint: Run golangci-lint (compiled with the project Go toolchain)
##       so the linter and the project agree on the language version.
lint: $(GO_BOOTSTRAP)
	@$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
	$(GOPATH)/bin/golangci-lint run --timeout=5m ./...

## e2e: Run end-to-end tests (requires Docker)
e2e: $(GO_BOOTSTRAP)
	$(GO) test ./... -tags=e2e -race -count=1 -timeout 10m

## build-all: Cross-compile for all supported platforms
build-all: $(GO_BOOTSTRAP)
	GOOS=linux  GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 .
	GOOS=linux  GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 .
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64 .

## clean: Remove build artifacts (keeps the toolchain so re-builds are fast)
clean:
	rm -rf bin/

## clean-all: clean + remove the project-local Go toolchain and gopath.
##            Use this when bumping GO_VERSION or to reclaim disk.
##            Module cache entries are 0444 by design; chmod first.
clean-all: clean
	@if [ -d .gopath ]; then chmod -R u+w .gopath; fi
	rm -rf .go-toolchain/ .gopath/

## fmt: Format Go source files
fmt: $(GO_BOOTSTRAP)
	$(GO) fmt ./...

## vet: Run go vet
vet: $(GO_BOOTSTRAP)
	$(GO) vet ./...

## tidy: Tidy go.mod
tidy: $(GO_BOOTSTRAP)
	$(GO) mod tidy

## docs: Generate per-command markdown reference (dist/docs/)
docs: $(GO_BOOTSTRAP)
	@mkdir -p dist/docs
	$(GO) run ./cmd/gendoc md dist/docs

## man: Generate man(1) pages (dist/man/)
man: $(GO_BOOTSTRAP)
	@mkdir -p dist/man
	$(GO) run ./cmd/gendoc man dist/man

## cover: Run tests with coverage and print per-function summary
cover: $(GO_BOOTSTRAP)
	$(GO) test -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out | tail

## vuln: Run govulncheck against the module
vuln: $(GO_BOOTSTRAP)
	@$(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	$(GOPATH)/bin/govulncheck ./...

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
