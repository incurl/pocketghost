# PocketHost — Docker-free Go control plane
#
# Everything is built from this Makefile. There is no Node.js, no pnpm, no
# TypeScript and no container runtime involved at any point.

BINARY      ?= pocketghost
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 1.0.0-go)
BUILD_DIR   ?= bin
GO          ?= go
LDFLAGS     := -s -w -X github.com/incurl/pocketghost/internal/api.Version=$(VERSION)
GOFLAGS     := -trimpath
TAGS        :=

.DEFAULT_GOAL := build

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the static (CGO-free) binary
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -tags '$(TAGS)' -ldflags '$(LDFLAGS)' \
		-o $(BUILD_DIR)/$(BINARY) ./cmd/pocketghost
	@echo "built $(BUILD_DIR)/$(BINARY) ($(VERSION))"

.PHONY: release
release: ## Cross-compile linux/amd64 + linux/arm64 static binaries
	@mkdir -p $(BUILD_DIR)
	@for target in linux/amd64 linux/arm64; do \
		os=$${target%/*}; arch=$${target#*/}; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS) \
			-ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY)-$$os-$$arch ./cmd/pocketghost || exit 1; \
	done

.PHONY: run
run: ## Run the daemon (API + tenant proxy + scheduler) in the foreground
	NODE_ENV=development $(GO) run ./cmd/pocketghost serve

.PHONY: edge
edge: ## Run the public firewall/edge listener
	NODE_ENV=development $(GO) run ./cmd/pocketghost firewall --http :8080 --https :8443

.PHONY: test
test: ## Run the unit test suite
	$(GO) test -count=1 -race ./...

.PHONY: test-short
test-short: ## Run tests without the process-spawning integration tests
	$(GO) test -count=1 -short ./...

.PHONY: coverage
coverage: ## Write coverage.out and print the total
	$(GO) test -count=1 -coverprofile=coverage.out ./...
	@$(GO) tool cover -func=coverage.out | tail -1

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: fmt
fmt: ## gofmt the tree
	$(GO) fmt ./...

.PHONY: lint
lint: vet ## Vet + gofmt verification (CI gate)
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; exit 1; fi

.PHONY: check
check: lint test ## Full CI gate

.PHONY: dashboard
dashboard: ## Compile the Svelte dashboard and embed it into the binary
	@test -n "$(UPSTREAM)" || { echo "usage: make dashboard UPSTREAM=/path/to/pockethost-monorepo"; exit 1; }
	./scripts/build-dashboard.sh "$(UPSTREAM)"
	@echo "dashboard embedded; re-run 'make build'"

.PHONY: docker-free
docker-free: ## Verify no Docker/container code or dependencies remain
	@! $(GO) list -deps ./... | grep -Ei 'docker|containerd|moby|libpod' || \
		{ echo "container SDK dependency found"; exit 1; }
	@! grep -rnE '"(github\.com/(docker|moby|containerd)|github\.com/docker/docker)' \
		--include='*.go' . || { echo "container SDK import found"; exit 1; }
	@! grep -rnE 'exec\.Command(Context)?\(\s*"(docker|podman|nerdctl|ctr)"' \
		--include='*.go' . || { echo "container CLI invocation found"; exit 1; }
	@! grep -rnE 'docker\.(sock|io)|/var/run/docker' --include='*.go' . || \
		{ echo "container runtime socket reference found"; exit 1; }
	@echo "no Docker/containerd code, CLI calls or dependencies"

.PHONY: static-check
static-check: ## Prove the binary is statically linked
	@file $(BUILD_DIR)/$(BINARY) | tee /dev/stderr | grep -q 'statically linked' \
		|| { echo "binary is not statically linked"; exit 1; }

.PHONY: smoke
smoke: build ## Boot the daemon, hit health endpoints, and shut down
	@tmp=$$(mktemp -d); \
	PH_HOME=$$tmp/data NODE_ENV=development DAEMON_PORT=3999 $(BUILD_DIR)/$(BINARY) serve & \
	pid=$$!; \
	for i in $$(seq 1 50); do curl -fsS http://127.0.0.1:3999/_api/daemon/health >/dev/null 2>&1 && break; sleep 0.2; done; \
	curl -fsS http://127.0.0.1:3999/_api/daemon/health; echo; \
	kill -INT $$pid; wait $$pid 2>/dev/null; rm -rf $$tmp; echo "smoke ok"

.PHONY: clean
clean: ## Remove build artefacts
	rm -rf $(BUILD_DIR) coverage.out

.PHONY: migrate
migrate: ## Import a legacy PocketBase mothership database (make migrate FROM=/path/data.db)
	@test -n "$(FROM)" || { echo "usage: make migrate FROM=/path/to/data.db"; exit 1; }
	$(GO) run ./cmd/pocketghost migrate --from "$(FROM)"
