.DEFAULT_GOAL := help

.PHONY: help preflight-proxy preflight-container registry-auth-check setup-go-auth clean-go-auth tools fmt format tidy vet lint test build qa qa-container serve serve-down up up-api gen-mounts enable-meili-embedding-feature smoketest diagnose ingest ingest-cancel ingest-drop model-images-build model-images-push hooks-install

GO_DIR ?= go
REGISTRY_GO_CHECK_URL ?=
GO_TOOLCHAIN_IMAGE ?= golang:1.25
GO_MOD_CACHE_VOLUME ?= lappy-rag-gomodcache
GO_BUILD_CACHE_VOLUME ?= lappy-rag-gobuildcache
CONTAINER_CLI ?= podman
DOCKER_PUSH_DOMAIN ?=
DOCKER_REGISTRY_PROXY ?=
GONOSUMCHECK ?= *
CORP_CA_CERT_URL ?=
GOPROXY ?=
PACKAGE_REGISTRY_HOST ?=
REGISTRY_VENDOR ?=
GOPRIVATE ?=
GONOSUMDB ?=
REGISTRY_USER ?=
REGISTRY_TOKEN ?=
COMPOSE_ENV_FILE ?= docker/env/.env
ifneq (,$(wildcard $(COMPOSE_ENV_FILE)))
COMPOSE_ENV_ARGS := --env-file $(COMPOSE_ENV_FILE)
else
COMPOSE_ENV_ARGS :=
endif

COMPOSE_MOUNTS_FILE ?= docker/compose.mounts.yml
COMPOSE_FILES := -f docker/compose.yml -f $(COMPOSE_MOUNTS_FILE)

help: ## List local-dev make verbs
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-24s %s\n", $$1, $$2}'
	@printf '\n  CLI catalog: run ingest-api / mcp-server / ingest-job help\n'

preflight-proxy:
	uv run scripts/preflight_proxy.py

registry-auth-check:
	uv run scripts/registry_auth_check.py

setup-go-auth:
	uv run scripts/setup_go_auth.py

clean-go-auth:
	uv run scripts/clean_go_auth.py

preflight-container:
ifeq ($(OS),Windows_NT)
	@where $(CONTAINER_CLI) >NUL 2>&1 || (echo Error: $(CONTAINER_CLI) is required for qa-container. & exit /b 1)
else
	@command -v $(CONTAINER_CLI) >/dev/null 2>&1 || { echo "Error: $(CONTAINER_CLI) is required for qa-container."; exit 1; }
endif

tools:
	cd $(GO_DIR) && go tool task --list

format: ## Format Go sources with gofumpt
	cd $(GO_DIR) && go tool task format

fmt: format

tidy: ## go mod tidy
	cd $(GO_DIR) && go mod tidy

vet: ## Run go vet
	cd $(GO_DIR) && go tool task vet

lint: ## Run golangci-lint
	cd $(GO_DIR) && go tool task lint

test: ## Run unit tests
	cd $(GO_DIR) && go tool task test

build: ## Compile all commands
	cd $(GO_DIR) && go tool task build

hooks-install: ## Install Lefthook git hooks for this clone or worktree
	@command -v lefthook >/dev/null 2>&1 || { \
		if command -v brew >/dev/null 2>&1; then brew install lefthook; \
		else go install github.com/evilmartians/lefthook@latest; fi; }
	@command -v lefthook >/dev/null 2>&1 || { echo "lefthook not on PATH; add $$(go env GOPATH)/bin"; exit 1; }
	lefthook install

qa:
	cd $(GO_DIR) && go tool task format && go tool task lint && go tool task vet && go tool task test

qa-container: preflight-proxy preflight-container
	$(CONTAINER_CLI) run --rm \
		-e GOPROXY \
		-e GOPRIVATE \
		-e GONOSUMDB \
		-e GONOSUMCHECK \
		-e REGISTRY_USER \
		-e REGISTRY_TOKEN \
		-e PACKAGE_REGISTRY_HOST \
		-v "$(GO_MOD_CACHE_VOLUME):/go/pkg/mod" \
		-v "$(GO_BUILD_CACHE_VOLUME):/root/.cache/go-build" \
		-v "$(CURDIR):/workspace" \
		-w /workspace/$(GO_DIR) \
		$(GO_TOOLCHAIN_IMAGE) \
		sh -lc 'if [ -n "$$REGISTRY_USER" ] && [ -n "$$REGISTRY_TOKEN" ] && [ -n "$$PACKAGE_REGISTRY_HOST" ]; then REGISTRY_LOGIN="$${REGISTRY_USER%@*}"; printf "machine %s\nlogin %s\npassword %s\n" "$$PACKAGE_REGISTRY_HOST" "$$REGISTRY_LOGIN" "$$REGISTRY_TOKEN" > "$$HOME/.netrc"; chmod 600 "$$HOME/.netrc"; fi; GONOSUMCHECK="$${GONOSUMCHECK:-*}" go mod download && GONOSUMCHECK="$${GONOSUMCHECK:-*}" go tool task format && GONOSUMCHECK="$${GONOSUMCHECK:-*}" go tool task lint && GONOSUMCHECK="$${GONOSUMCHECK:-*}" go tool task vet && GONOSUMCHECK="$${GONOSUMCHECK:-*}" go tool task test'

serve: ## Start the docker compose stack
serve: preflight-proxy registry-auth-check gen-mounts
ifeq ($(CONTAINER_CLI),podman)
	$(CONTAINER_CLI) build \
		-f docker/localai/Dockerfile \
		-t docker_localai \
		--build-arg DOCKER_REGISTRY_PROXY=$(DOCKER_REGISTRY_PROXY) \
		--build-arg CORP_CA_CERT_URL=$(CORP_CA_CERT_URL) \
		docker
	uv run scripts/podman_build_rag_api.py \
		--registry-proxy "$(DOCKER_REGISTRY_PROXY)" \
		--corp-ca-cert-url "$(CORP_CA_CERT_URL)" \
		--goproxy "$(GOPROXY)" \
		--gonosumcheck "$(GONOSUMCHECK)" \
		--goprivate "$(GOPRIVATE)" \
		--gonosumdb "$(GONOSUMDB)" \
		--package-registry-host "$(PACKAGE_REGISTRY_HOST)"
	-$(CONTAINER_CLI) compose $(COMPOSE_ENV_ARGS) $(COMPOSE_FILES) down --remove-orphans 2>nul
	$(CONTAINER_CLI) compose $(COMPOSE_ENV_ARGS) $(COMPOSE_FILES) up -d --no-build
	$(MAKE) enable-meili-embedding-feature
	-$(MAKE) diagnose
else
	$(CONTAINER_CLI) compose $(COMPOSE_ENV_ARGS) $(COMPOSE_FILES) down --remove-orphans || true
	$(CONTAINER_CLI) compose $(COMPOSE_ENV_ARGS) $(COMPOSE_FILES) up -d --build
	$(MAKE) enable-meili-embedding-feature
	-$(MAKE) diagnose
endif

up: serve

serve-down: ## Stop this compose project (keep named volumes)
	$(CONTAINER_CLI) compose $(COMPOSE_ENV_ARGS) $(COMPOSE_FILES) down --remove-orphans

up-api: gen-mounts
ifeq ($(CONTAINER_CLI),podman)
	uv run scripts/podman_build_rag_api.py \
		--registry-proxy "$(DOCKER_REGISTRY_PROXY)" \
		--corp-ca-cert-url "$(CORP_CA_CERT_URL)" \
		--goproxy "$(GOPROXY)" \
		--gonosumcheck "$(GONOSUMCHECK)" \
		--goprivate "$(GOPRIVATE)" \
		--gonosumdb "$(GONOSUMDB)" \
		--package-registry-host "$(PACKAGE_REGISTRY_HOST)"
	$(CONTAINER_CLI) compose $(COMPOSE_ENV_ARGS) $(COMPOSE_FILES) up -d --no-build --force-recreate rag-api
else
	$(CONTAINER_CLI) compose $(COMPOSE_ENV_ARGS) $(COMPOSE_FILES) up -d --build rag-api
endif

gen-mounts:
	uv run scripts/gen_compose_mounts.py

enable-meili-embedding-feature:
	uv run scripts/enable_meili_embedding.py

ingest:
	uv run scripts/reingest_full.py "$(PROJECT)"

ingest-cancel:
	uv run scripts/cancel_ingest.py "$(PROJECT)"

ingest-drop:
	uv run scripts/drop_indexes.py "$(PROJECT)"

model-images-build:
	$(CONTAINER_CLI) build -f docker/bge-model/Dockerfile -t xynova/bge-base-model:latest docker/
	$(CONTAINER_CLI) build -f docker/gemma4-model/Dockerfile -t xynova/gemma4-12b-model:latest docker/

model-images-push:
	@[ -n "$(DOCKER_PUSH_DOMAIN)" ] || { echo "Error: DOCKER_PUSH_DOMAIN is empty."; exit 1; }
	$(CONTAINER_CLI) tag xynova/bge-base-model:latest $(DOCKER_PUSH_DOMAIN)/xynova/bge-base-model:latest
	$(CONTAINER_CLI) tag xynova/gemma4-12b-model:latest $(DOCKER_PUSH_DOMAIN)/xynova/gemma4-12b-model:latest
	$(CONTAINER_CLI) push $(DOCKER_PUSH_DOMAIN)/xynova/bge-base-model:latest
	$(CONTAINER_CLI) push $(DOCKER_PUSH_DOMAIN)/xynova/gemma4-12b-model:latest

diagnose:
	uv run scripts/diagnose.py

smoketest:
	uv run scripts/smoketest.py
