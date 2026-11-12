# Canopy — root build file. Web/contract tooling runs in the pinned node:24
# container (ADR-0001); Go runs on the host toolchain; Postgres via make db-up.
SHELL := /bin/bash
NODE := docker run --rm -v "$(CURDIR)":/w -w /w node:24
PW_VERSION := $(shell python3 -c "import json;print(json.load(open('web/package-lock.json'))['packages']['node_modules/@playwright/test']['version'])")
PW_IMAGE := canopy-pw:$(PW_VERSION)
PW_NODE := docker run --rm -v "$(CURDIR)":/w -w /w $(PW_IMAGE)
PG_NAME := canopy-pg
PGTEST_DSN ?= postgres://postgres:devpw@localhost:55432/canopy_t02?sslmode=disable

.DEFAULT_GOAL := check

## gates ---------------------------------------------------------------------
check: plan-validate boundary gen-check lint test-unit test-integration test-auth test-e2e test-visual test-ai-eval
	@echo "check: ALL GATES PASSED"

plan-validate:
	python3 docs/plan/validate_plan.py
	python3 scripts/packet_validate.py
	python3 scripts/dispatch_check.py --fixtures

boundary:
	python3 scripts/boundary_check.py
	python3 scripts/boundary_check.py --root scripts/fixtures/boundary-bad --expect-violations 2

dispatch-check:
	@test -n "$(TASK)" || { echo "usage: make dispatch-check TASK=Txx"; exit 2; }
	@python3 scripts/dispatch_check.py $(TASK)

## code generation ------------------------------------------------------------
gen: gen-contracts gen-sqlc gen-gqlgen gen-mcp

gen-contracts: contracts/node_modules/.installed
	$(NODE) sh -c "cd /w/contracts && npx graphql-codegen --overwrite"

gen-mcp:
	python3 scripts/gen_mcp.py

gen-sqlc:
	cd server && go tool sqlc generate

gen-gqlgen:
	cd server && go tool gqlgen generate

gen-check: gen
	git diff --exit-code -- contracts/generated server/db/generated server/internal/transport/graphql/gql server/internal/agentapi/catalog/tools.json
	@echo "gen-check: generation left no diff"

## lint / typecheck -------------------------------------------------------------
lint: contracts/node_modules/.installed web/node_modules/.installed
	cd server && test -z "$$(gofmt -l .)" && go vet ./...
	$(NODE) sh -c "cd /w/contracts && npx tsc --noEmit"
	$(NODE) sh -c "cd /w/web && npm run build --silent >/dev/null"

## tests ------------------------------------------------------------------------
test-unit: contracts/node_modules/.installed web/node_modules/.installed
	$(NODE) sh -c "cd /w/contracts && node validate.mjs"
	$(NODE) sh -c "cd /w/web && npx ng test --watch=false"
	cd server && go test ./...

test-integration: media-image
	cd server && PGTEST_DSN="$(PGTEST_DSN)" go test ./db/ ./internal/... -count=1

# T14 codec sandbox runtime image: pinned digest, decode-only tooling.
MEDIA_IMAGE := canopy-media:r2
media-image: docker/media.Dockerfile
	@docker image inspect $(MEDIA_IMAGE) >/dev/null 2>&1 || \
	  docker build -t $(MEDIA_IMAGE) -f docker/media.Dockerfile .

test-visual: web/node_modules/.installed docker/pw.Dockerfile
	@docker image inspect $(PW_IMAGE) >/dev/null 2>&1 || \
	  docker build -t $(PW_IMAGE) --build-arg PW_VERSION=$(PW_VERSION) -f docker/pw.Dockerfile .
	$(PW_NODE) sh -c "cd /w/web && npx ng build --configuration development && npx playwright test"

test-auth:
	cd server && PGTEST_DSN="$(PGTEST_DSN)" go test ./internal/identity/ ./internal/transport/http/ -count=1

# T07 auth e2e: host toolchain cross-compiles the linux binaries; the real Go
# API + admin CLI run inside the Playwright container against a disposable
# schema (web/e2e/global-setup.mjs). Dev credentials only — no secrets.
GOARCH := $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
test-e2e: web/node_modules/.installed
	cd server && GOOS=linux GOARCH=$(GOARCH) go build -o ../.artifacts/e2e/api-linux ./cmd/api && \
	  GOOS=linux GOARCH=$(GOARCH) go build -o ../.artifacts/e2e/admin-linux ./cmd/admin && \
	  go build -o ../.artifacts/e2e/worker-host ./cmd/worker
	PW_IMAGE=$(PW_IMAGE) bash scripts/e2e_with_worker.sh

## AI evaluation (T35) ------------------------------------------------------------
# Deterministic replay: recorded live outputs re-validated through the real
# ValidateResult (schema + evidence-reference integrity), per-case honesty
# expectations, and crafted hostile outputs that must be REJECTED. No
# network; skips only until results exist. Keys never appear on any line:
# the live target reads them from the caller's exported environment ($$).
test-ai test-ai-eval:
	cd server && go test ./internal/insights/ -run 'TestEvalRecorded|TestEvalAdversarial' -count=1 -timeout 5m

# Opt-in live pilot: 30 cases through the real pipeline against the
# actually configured text/vision providers (export CANOPY_EVAL_* and
# CANOPY_VISION_* first). Wall-clock budget ~30 min; self-hosted Strata
# reports no cost (documented in tests/ai-eval/README.md).
test-ai-live: media-image
	@cd server && CANOPY_EVAL_LIVE=1 CANOPY_EVAL_BASE_URL="$$CANOPY_EVAL_BASE_URL" \
	  CANOPY_EVAL_API_KEY="$$CANOPY_EVAL_API_KEY" CANOPY_EVAL_MODEL="$$CANOPY_EVAL_MODEL" \
	  CANOPY_VISION_BASE_URL="$$CANOPY_VISION_BASE_URL" CANOPY_VISION_API_KEY="$$CANOPY_VISION_API_KEY" \
	  CANOPY_VISION_MODEL="$$CANOPY_VISION_MODEL" PGTEST_DSN="$(PGTEST_DSN)" \
	  go test ./internal/insights/ -run TestEvalLive -count=1 -timeout 60m -v

# Suites that do not exist yet: loud PENDING, never success stubs (T03 rule).
test-sync test-care:
	@echo "PENDING: $@ is not implemented yet (see docs/plan/07-tasks.md); refusing to fake success" && exit 1

## portability (T38.a) -----------------------------------------------------------
# Backup/restore through the admin CLI against the stack named by
# DATABASE_URL (defaults to the disposable dev DB). No secret material
# on the command line: KEY_FILE points at a 64-hex-char file kept
# outside DEST (see server/internal/portability/RUNBOOK.md).
backup:
	cd server && DATABASE_URL="$${DATABASE_URL:-$(PGTEST_DSN)}" \
	  CANOPY_ENV=dev CANOPY_STORAGE_ROOT="$${CANOPY_STORAGE_ROOT:-$(CURDIR)/.artifacts/dev/storage}" \
	  go run ./cmd/admin backup $(if $(DEST),--dest "$(abspath $(DEST))",) $(if $(KEY_FILE),--key-file "$(abspath $(KEY_FILE))",)

# restore-test applies a backup set to a fresh blank schema in the dev
# container, prints the reconciliation summary, then drops the scratch
# schema and temp storage. Never touches a populated schema.
restore-test:
	@test -n "$(ARCHIVE)" || { echo "usage: make restore-test ARCHIVE=path/to/<set>.tar[.enc] [LEDGER=...] [KEY_FILE=...]"; exit 2; }
	@SCHEMA="t38r_$$(date +%s%N)"; \
	PSQL_OPTS="-v ON_ERROR_STOP=1"; \
	docker exec $(PG_NAME) env PGOPTIONS="-c client_min_messages=warning" psql -U postgres -d canopy_t02 -q $$PSQL_OPTS -c "CREATE SCHEMA $$SCHEMA"; \
	STORE="$$(mktemp -d)"; \
	cd server && DATABASE_URL="$(PGTEST_DSN)&search_path=$$SCHEMA" \
	  CANOPY_ENV=dev CANOPY_STORAGE_ROOT="$$STORE" \
	  go run ./cmd/admin restore --archive "$(abspath $(ARCHIVE))" $(if $(LEDGER),--ledger "$(abspath $(LEDGER))",) $(if $(KEY_FILE),--key-file "$(abspath $(KEY_FILE))",); \
	rc=$$?; rm -rf "$$STORE"; \
	docker exec $(PG_NAME) env PGOPTIONS="-c client_min_messages=warning" psql -U postgres -d canopy_t02 -q $$PSQL_OPTS -c "DROP SCHEMA IF EXISTS $$SCHEMA CASCADE"; \
	exit $$rc

## local preview ----------------------------------------------------------------
# Rebuild the web app from the current tree and (re)start the preview server on
# :4300 proxying /api to the local API on :8080. Run after any web change.
preview: web/node_modules/.installed
	$(NODE) sh -c "cd /w/web && npx ng build"
	@-pkill -f "serve-dist.mjs web/dist" >/dev/null 2>&1 || true
	@PORT=4300 API_TARGET=http://127.0.0.1:8080 nohup node scripts/serve-dist.mjs web/dist/web/browser > /dev/null 2>&1 & \
	echo "preview: http://localhost:4300 (api proxy -> http://127.0.0.1:8080)"

## dev database -----------------------------------------------------------------
db-up:
	@if [ -z "$$(docker ps -q -f name=$(PG_NAME))" ]; then \
	  docker rm -f $(PG_NAME) >/dev/null 2>&1; \
	  docker run -d --name $(PG_NAME) -p 55432:5432 -e POSTGRES_PASSWORD=devpw postgres:17 >/dev/null; \
	fi
	@until docker exec $(PG_NAME) pg_isready -U postgres >/dev/null 2>&1; do sleep 0.3; done
	@docker exec $(PG_NAME) psql -U postgres -tAc "SELECT 1 FROM pg_database WHERE datname='canopy_t02'" | grep -q 1 \
	  || docker exec $(PG_NAME) psql -U postgres -c "CREATE DATABASE canopy_t02"
	@echo "db-up: $(PG_NAME) ready (dsn: $(PGTEST_DSN))"

db-down:
	docker rm -f $(PG_NAME) >/dev/null 2>&1 || true

## dependency install (stamped) ---------------------------------------------------
contracts/node_modules/.installed: contracts/package.json contracts/package-lock.json
	$(NODE) sh -c "cd /w/contracts && npm ci --no-audit --no-fund && touch /w/contracts/node_modules/.installed"

web/node_modules/.installed: web/package.json web/package-lock.json
	$(NODE) sh -c "cd /w/web && npm ci --no-audit --no-fund && touch /w/web/node_modules/.installed"

install: contracts/node_modules/.installed web/node_modules/.installed

.PHONY: check plan-validate boundary dispatch-check gen gen-contracts gen-sqlc gen-gqlgen gen-mcp gen-check \
	lint test-unit test-integration test-e2e test-visual test-auth test-sync test-care test-ai \
	backup restore-test db-up db-down install preview
