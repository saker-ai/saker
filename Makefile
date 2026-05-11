.PHONY: test test-unit test-race test-integration test-short coverage lint notices oss-check build saker saker-full install clean test-pipeline test-pipeline-race test-pipeline-bench test-pipeline-stress test-all test-eval test-eval-bench test-eval-llm test-eval-all test-eval-tb2 test-eval-tb2-smoke eval-tb2 eval-tb2-smoke demo-pipeline server web-deps web-dev web-clean web-build web-editor-deps web-editor-dev web-editor-build web-editor-clean desktop run e2e-build e2e-run e2e-clean changelog swagger check-no-binaries

GO ?= go
PKG ?= ./...
CMD ?= ./cmd/saker
BIN_DIR ?= bin
BINARY ?= $(BIN_DIR)/saker
COVERAGE_FILE ?= coverage.out

test:
	$(GO) test -timeout 120s $(PKG)

# Fast unit tests only (skip integration, enable race detector)
test-unit:
	$(GO) test -short -race -timeout 120s ./pkg/...

# Race detector on all tests
test-race:
	$(GO) test -race -timeout 300s $(PKG)

# Integration tests only (build tag)
test-integration:
	$(GO) test -tags integration -timeout 300s ./test/integration/...

# Short mode — skip slow tests, ideal for development loop
test-short:
	$(GO) test -short -timeout 60s $(PKG)

coverage:
	$(GO) test -covermode=atomic -coverprofile=$(COVERAGE_FILE) $(PKG)
	$(GO) tool cover -func=$(COVERAGE_FILE)

lint: check-no-binaries
	golangci-lint run

# check-no-binaries fails the build if any GIT-TRACKED file is an executable
# (ELF / Mach-O / PE / shell-script binary > 1MB). This is a pre-commit /
# CI guard against accidentally committing build artifacts. The 728 MB
# `cli` / `saker` / `bin/saker` binaries that bloated the working tree on
# 2026-05 must not slip past `.gitignore` again (e.g. via `git add -f`).
#
# Allowed binary file types are explicitly listed; everything else with
# executable magic bytes triggers a failure with a punch list of offenders.
# Run standalone: `make check-no-binaries` — exits non-zero on violation.
check-no-binaries:
	@offenders=$$(git ls-files -z 2>/dev/null | xargs -0 -r file -F '|' 2>/dev/null \
		| awk -F'|' '/(ELF |Mach-O |PE32|PE32\+|MS-DOS executable)/ {print $$1}' \
		| grep -v '^scripts/' || true); \
	if [ -n "$$offenders" ]; then \
		echo "ERROR: tracked binary files detected (move to release artifacts, not the repo):"; \
		echo "$$offenders" | sed 's/^/  /'; \
		exit 1; \
	fi

notices:
	node scripts/generate-third-party-notices.mjs

oss-check: notices
	scripts/check-open-source-readiness.sh

build: web-build web-editor-build saker

LDFLAGS ?= -s -w

saker:
	@# Clean stale Next.js chunks first — content-hashed filenames mean
	@# every rebuild leaves the previous build's chunks in the bundle and
	@# blows up the binary. We only wipe the _next/ subtree so a missing
	@# fresh build doesn't blank out the embedded frontend wholesale.
	@if [ -d web/out ]; then rm -rf cmd/saker/frontend/dist/_next; fi
	mkdir -p cmd/saker/frontend/dist
	@if [ -d web/out ]; then cp -r web/out/* cmd/saker/frontend/dist/; fi
	@if [ -d web-editor-next/out ]; then rm -rf cmd/saker/editor/dist/_next; fi
	mkdir -p cmd/saker/editor/dist
	@if [ -d web-editor-next/out ]; then cp -r web-editor-next/out/* cmd/saker/editor/dist/; fi
	@find cmd/saker/editor/dist -maxdepth 2 -name '__next.*.txt' -delete 2>/dev/null || true
	@touch cmd/saker/editor/dist/.gitkeep
	mkdir -p $(BIN_DIR)
	$(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY) $(CMD)

saker-full:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=1 $(GO) build -tags govm -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY) $(CMD)

install:
	$(GO) install -ldflags="$(LDFLAGS)" -trimpath $(CMD)

clean:
	rm -rf $(BIN_DIR) $(COVERAGE_FILE)

# Pipeline integration tests
test-pipeline:
	$(GO) test ./test/pipeline/... -v -timeout 120s

test-pipeline-race:
	$(GO) test ./test/pipeline/... -race -count 3 -timeout 300s

test-pipeline-bench:
	$(GO) test ./test/pipeline/... -bench . -benchtime 3s -timeout 300s

test-pipeline-stress:
	$(GO) test ./test/pipeline/... -run "TestConcurrent" -race -count 10 -timeout 600s

# Pipeline CLI demo (no API key required)
demo-pipeline:
	$(GO) run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json --timeline --lineage dot

# Server (with embedded frontend + editor sub-app — builds both first)
server: web-clean web-build web-editor-clean web-editor-build
	rm -rf cmd/saker/frontend/dist
	mkdir -p cmd/saker/frontend/dist
	cp -r web/out/* cmd/saker/frontend/dist/
	rm -rf cmd/saker/editor/dist
	mkdir -p cmd/saker/editor/dist
	cp -r web-editor-next/out/* cmd/saker/editor/dist/
	@find cmd/saker/editor/dist -maxdepth 2 -name '__next.*.txt' -delete 2>/dev/null || true
	@touch cmd/saker/editor/dist/.gitkeep
	mkdir -p $(BIN_DIR)
	$(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY) $(CMD)
	@echo "Built $(BINARY) with embedded frontend + /editor/ sub-app (use --server to start)"

# Server (Go only, no frontend embed — for development)
server-dev:
	mkdir -p cmd/saker/frontend/dist
	@touch cmd/saker/frontend/dist/.gitkeep
	mkdir -p cmd/saker/editor/dist
	@touch cmd/saker/editor/dist/.gitkeep
	mkdir -p $(BIN_DIR)
	$(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY) $(CMD)

# Web frontend (pnpm workspace)
web-deps:
	pnpm install

web-dev: web-deps
	pnpm --filter saker-web run dev

web-clean:
	rm -rf web/.next web/out

web-build: web-deps
	pnpm --filter saker-web run build

# Web editor subapp (OpenCut-derived, served at /editor/)
web-editor-deps:
	pnpm install

web-editor-dev: web-editor-deps
	pnpm --filter saker-web-editor run dev

web-editor-clean:
	rm -rf web-editor-next/.next web-editor-next/out

web-editor-build: web-editor-deps
	pnpm --filter saker-web-editor run build

# Desktop (requires web build first)
desktop: web-build
	rm -rf cmd/desktop/frontend/dist
	mkdir -p cmd/desktop/frontend/dist
	cp -r web/out/* cmd/desktop/frontend/dist/
	CGO_ENABLED=1 $(GO) build -tags desktop -ldflags="$(LDFLAGS)" -trimpath -o $(BIN_DIR)/saker-desktop ./cmd/desktop

# Build and run server (frontend + backend + start)
run: server
	@lsof -ti :10112 | xargs -r kill 2>/dev/null && echo "Killed process on port 10112" || true
	$(BINARY) --server

# Eval suites (offline — no API key needed)
test-eval:
	$(GO) test ./eval/... -v -timeout 300s

test-eval-bench:
	$(GO) test ./eval/suites/performance/... -bench . -benchtime 3s -timeout 300s

# Online LLM eval suites (requires ANTHROPIC_API_KEY)
test-eval-llm:
	$(GO) test -tags integration ./eval/suites/llm_tool_selection/... ./eval/suites/llm_multi_turn/... ./eval/suites/llm_safety/... ./eval/suites/llm_system_prompt/... -v -timeout 600s

# All evals (offline + online)
test-eval-all: test-eval test-eval-llm

# Terminal-Bench 2 (native Go runner) — unit tests, no docker required
test-eval-tb2:
	$(GO) test -race -count=1 -timeout 120s ./pkg/eval/dataset/... ./pkg/eval/terminalbench/... ./pkg/sandbox/dockerenv/...

# Terminal-Bench 2 docker smoke test — requires a working docker daemon
test-eval-tb2-smoke:
	$(GO) test -tags=integration_docker -count=1 -timeout 10m ./pkg/eval/terminalbench/...

# Run the Terminal-Bench 2 evaluator end-to-end (clones dataset, builds CLI, runs `saker eval terminalbench`).
# Forward extra flags after `--`, e.g. `make eval-tb2 ARGS="--concurrency 8 --filter cli-*"`.
ARGS ?=
eval-tb2:
	scripts/run-eval-tb2.sh $(ARGS)

# Single-task plumbing check using the full pipeline (real model + docker).
eval-tb2-smoke:
	scripts/run-eval-tb2.sh --smoke $(ARGS)

# Full regression (CI) — unit with race + pipeline + integration + eval
test-all: test-unit test-pipeline test-pipeline-race test-integration test-eval

# E2E tests (Docker-based, requires ANTHROPIC_API_KEY)
DOCKER_COMPOSE ?= $(shell docker compose version >/dev/null 2>&1 && echo "docker compose" || echo "docker-compose")

e2e-build:
	$(DOCKER_COMPOSE) -f e2e/docker-compose.e2e.yml build

e2e-run:
	$(DOCKER_COMPOSE) -f e2e/docker-compose.e2e.yml up --abort-on-container-exit --exit-code-from e2e-runner

e2e-clean:
	$(DOCKER_COMPOSE) -f e2e/docker-compose.e2e.yml down -v
	rm -rf e2e/reports/*.json

# Generate CHANGELOG from conventional commits using git-cliff
changelog:
	git-cliff -c cliff.toml -o CHANGELOG.md

# Generate OpenAPI/Swagger specification from swaggo annotations
swagger:
	swag init -g cmd/saker/main.go -o docs/swagger --parseDependency --parseInternal
