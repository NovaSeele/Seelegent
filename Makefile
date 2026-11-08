# Build, test and lint Seelegent. On Windows without make, use scripts/build.ps1 (see docs/development.md).

SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := build

GO   ?= go
PNPM ?= pnpm

MODULE  := github.com/NovaSeele/Seelegent
BIN_DIR := bin
EXE     := $(if $(filter Windows_NT,$(OS)),.exe,)
BINARY  := seelegent$(EXE)
# The companion launcher, shipped next to seelegent (docs/release.md). It must be built with the same flags and
# -ldflags as seelegent: the daemon uses it only when its build information matches its own.
LAUNCHER := seelegent-launch$(EXE)

VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(MODULE)/internal/buildinfo.version=$(VERSION) \
	-X $(MODULE)/internal/buildinfo.commit=$(COMMIT) \
	-X $(MODULE)/internal/buildinfo.date=$(DATE)

# golangci-lint is built with the module's toolchain so it understands the targeted Go version.
GO_TOOLCHAIN          := $(shell sed -n 's/^toolchain //p' go.mod)
GOLANGCI_LINT_VERSION ?= v2.15.0
GOLANGCI_LINT         ?= GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# GoReleaser, the version the release workflow pins (make snapshot, make release-check).
GORELEASER_VERSION ?= v2.20.0
GORELEASER         ?= GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

# The release targets (.goreleaser.yaml builds the same list).
CROSS_TARGETS := windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

# Extra flags for go test, for example GOTESTFLAGS=-race.
GOTESTFLAGS ?=

# Light mode (docs/development.md): test packages and lint jobs run at once. The machine froze with go test ./...
# and lint at full width beside running workers; raise them on an idle machine, e.g. make test TEST_P=12.
TEST_P ?= 4
LINT_J ?= 4

# Flags of the resource benchmark (scripts/bench), e.g. BENCH_ARGS="-scenario load -duration 5m".
BENCH_ARGS ?=
BENCH_BIN  := $(BIN_DIR)/seelegent-bench$(EXE)

.PHONY: web build cross snapshot release-check test test-web lint lint-web fmt generate clean dev-daemon bench

web: ## Build the dashboard into web/dist
	cd web && $(PNPM) install --frozen-lockfile && $(PNPM) build

build: web ## Build bin/seelegent with the dashboard embedded, and its companion launcher bin/seelegent-launch
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) ./cmd/seelegent
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(LAUNCHER) ./cmd/seelegent-launch

cross: web ## Build seelegent and seelegent-launch for every release target into dist/cross/<os>_<arch>/ (no archives)
	for t in $(CROSS_TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		for b in seelegent seelegent-launch; do \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
				-o dist/cross/$${os}_$${arch}/$$b$$ext ./cmd/$$b; \
		done; \
	done

snapshot: ## Build the release archives and checksums.txt into dist/ with GoReleaser, publishing nothing
	$(GORELEASER) release --snapshot --clean

release-check: ## Validate .goreleaser.yaml
	$(GORELEASER) check

test: ## Run the Go tests (TEST_P packages at once)
	$(GO) test -p $(TEST_P) $(GOTESTFLAGS) ./...

test-web: ## Run the dashboard tests
	cd web && $(PNPM) test

lint: ## Lint the Go code and the bench (LINT_J jobs, GOMEMLIMIT=2GiB)
	$(GO) vet -p $(TEST_P) ./...
	$(GO) vet -p $(TEST_P) -tags bench ./scripts/bench/
	GOOS=windows $(GO) vet -p $(TEST_P) -tags bench ./scripts/bench/
	GOOS=darwin $(GO) vet -p $(TEST_P) -tags bench ./scripts/bench/
	GOMEMLIMIT=2GiB $(GOLANGCI_LINT) run --concurrency $(LINT_J) ./...

lint-web: ## Lint and type-check the dashboard
	cd web && $(PNPM) lint && $(PNPM) typecheck

fmt: ## Format Go and web sources
	$(GOLANGCI_LINT) fmt ./...
	cd web && $(PNPM) format

generate: ## Generate web/src/api/types.gen.ts from internal/api (internal/api/tsgen)
	$(GO) generate ./internal/api

clean: ## Remove build output
	rm -rf $(BIN_DIR) dist
	find web/dist -mindepth 1 ! -name .gitkeep -exec rm -rf {} +

bench: ## Run the resource benchmark (scripts/bench; BENCH_ARGS, default: idle for 10 minutes)
	$(GO) test -tags bench -c -o $(BENCH_BIN) ./scripts/bench
	./$(BENCH_BIN) $(BENCH_ARGS)

dev-daemon: ## Run a development daemon on port 47831 with a fresh temp SEELEGENT_HOME (accepts the Vite dev server)
	SEELEGENT_HOME="$$(mktemp -d)" $(GO) run ./cmd/seelegent daemon --port 47831 --dev-origin http://localhost:5173
