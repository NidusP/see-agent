.PHONY: help dev test lint format typecheck eval-baseline eval-runtime-baseline eval-runtime-gate eval-runtime-k7-gate integration python-check \
	web-install web-dev web-build web-lint web-ui-smoke \
	backend-dev backend-rag-dev backend-fullstack-dev backend-test \
	runtime-install runtime-dev runtime-rag-dev runtime-fullstack-dev runtime-test runtime-qdrant-smoke runtime-native-qdrant-smoke runtime-native-dense-smoke local-model-smoke provider-stability-probe minio-ingestion-smoke postgres-repository-smoke postgres-k2-smoke postgres-k7-smoke postgres-k9-ownership-smoke redis-ingestion-smoke redis-session-smoke bootstrap-rag-knowledge fullstack-preflight fullstack-smoke \
	infra-up infra-down infra-ps infra-logs infra-check fullstack-preflight-test

GO_BIN ?= /Users/populus/.local/go1.26.4/bin/go
PYTHON_BOOTSTRAP_BIN ?= $(shell sh -c '\
for py in python3.14 python3.13 python3.12 python3; do \
	if command -v $$py >/dev/null 2>&1 && \
		$$py -c "import sys; raise SystemExit(0 if sys.version_info >= (3, 12) else 1)" >/dev/null 2>&1; then \
		command -v $$py; \
		exit 0; \
	fi; \
done')
VENV_DIR ?= .venv
VENV_PYTHON = $(abspath $(VENV_DIR))/bin/python
PYTHON_BIN ?= $(if $(wildcard $(VENV_PYTHON)),$(VENV_PYTHON),$(PYTHON_BOOTSTRAP_BIN))
PNPM ?= pnpm
PNPM_INSTALL = $(PNPM) install --frozen-lockfile
COMPOSE_FILE = infra/compose/docker-compose.yml

help:
	@printf "Available targets:\n"
	@printf "  make web-install      # Install Next.js dependencies\n"
	@printf "  make web-dev          # Run the Next.js app on port 3000\n"
	@printf "  make web-build        # Build the Next.js app\n"
	@printf "  make web-lint         # Lint the Next.js app\n"
	@printf "  make web-ui-smoke     # Run Playwright UI smoke against WEB_BASE_URL or localhost:3000\n"
	@printf "  make backend-dev      # Run the Go API in explicit smoke profile on port 8080\n"
	@printf "  make backend-fullstack-dev # Run Backend in fullstack_dev; requires explicit dependency and credential env\n"
	@printf "  make backend-test     # Run Go tests\n"
	@printf "  make runtime-install  # Bootstrap .venv and install Python runtime dependencies\n"
	@printf "  make runtime-dev      # Run the Python AI runtime in explicit smoke profile on port 8000\n"
	@printf "  make runtime-fullstack-dev # Run Runtime in fullstack_dev; requires explicit provider and object-store env\n"
	@printf "  make fullstack-preflight # Verify explicit configuration plus PostgreSQL, Redis, MinIO, Qdrant and model reachability\n"
	@printf "  make fullstack-preflight-test # Verify service-token policy rejects invalid local fullstack secrets\n"
	@printf "  make fullstack-smoke  # Verify Backend and Runtime readiness after fullstack startup\n"
	@printf "  make runtime-test     # Run Python tests with the preferred Python bin\n"
	@printf "  make runtime-qdrant-smoke # Verify Runtime Qdrant persistence against QDRANT_URL\n"
	@printf "  make runtime-native-qdrant-smoke # Verify real native Qdrant vectors/query/filter without an embedding provider\n"
	@printf "  make runtime-native-dense-smoke # Verify native dense Qdrant retrieval; requires EMBEDDING_* provider settings\n"
	@printf "  make local-model-smoke # Verify the configured Ollama model without fallback\n"
	@printf "  make provider-stability-probe # Record real provider latency and failure evidence without product state\n"
	@printf "  make minio-ingestion-smoke # Verify Backend/Runtime ingestion through MinIO\n"
	@printf "  make postgres-repository-smoke # Verify Backend metadata persistence through Postgres\n"
	@printf "  make postgres-k2-smoke # Verify K2 migrations, legacy backfill and snapshot publication against Postgres\n"
	@printf "  make postgres-k7-smoke # Verify K7 execution/eval/delivery evidence against isolated Postgres schema\n"
	@printf "  make postgres-k9-ownership-smoke # Verify conversation ownership migration/backfill and isolation against Postgres\n"
	@printf "  make redis-ingestion-smoke # Verify Backend ingestion dispatch through Redis\n"
	@printf "  make redis-session-smoke # Verify Backend session persistence through Redis\n"
	@printf "  make bootstrap-rag-knowledge # Upload, index and publish local development knowledge\n"
	@printf "  make infra-up        # Start local infra dependencies in Docker\n"
	@printf "  make infra-down      # Stop local infra dependencies\n"
	@printf "  make infra-ps        # Show local infra container status\n"
	@printf "  make infra-logs      # Tail local infra logs\n"
	@printf "  make infra-check     # Validate local infra config; set RUNNING=1 to check running containers\n"
	@printf "  make dev             # Start the checked full-stack development environment\n"
	@printf "  make test            # Run tests across backend and runtime\n"
	@printf "  make lint            # Run lint checks for the web app\n"
	@printf "  make format          # Placeholder for repo-wide formatting\n"
	@printf "  make typecheck       # Run TypeScript checks for the web app\n"
	@printf "  make eval-baseline   # Run the deterministic mock eval baseline\n"
	@printf "  make eval-runtime-baseline # Run eval through the local Runtime chat pipeline\n"
	@printf "  make eval-runtime-gate # Run runtime-local eval and enforce the versioned quality gate\n"
	@printf "  make eval-runtime-k7-gate # Run the prompt_v2 K7 quality experiment gate\n"
	@printf "  make integration     # Run backend/runtime integration and web smoke tests\n"

web-install:
	cd apps/web && $(PNPM_INSTALL)

web-dev:
	cd apps/web && $(PNPM) run dev

web-build:
	cd apps/web && $(PNPM) run build

web-lint:
	cd apps/web && $(PNPM) run lint

web-ui-smoke:
	cd apps/web && $(PNPM) run ui-smoke

backend-dev:
	cd services/backend-go && SEE_AGENT_PROFILE=smoke $(GO_BIN) run ./cmd/api

backend-rag-dev:
	@printf "backend-rag-dev is retired; use make backend-fullstack-dev with explicit fullstack_dev environment.\n" >&2
	@exit 2

backend-fullstack-dev: fullstack-preflight
	cd services/backend-go && SEE_AGENT_PROFILE=fullstack_dev $(GO_BIN) run ./cmd/api

backend-test:
	cd services/backend-go && $(GO_BIN) test ./...

python-check:
	@if [ -x "$(VENV_PYTHON)" ]; then \
		exit 0; \
	fi; \
	if [ -z "$(PYTHON_BOOTSTRAP_BIN)" ]; then \
		echo "Python 3.12+ not found. Install python3.13 or python3.14, then rerun make runtime-install."; \
		exit 1; \
	fi

runtime-install: python-check
	@if [ ! -x "$(VENV_PYTHON)" ]; then \
		$(PYTHON_BOOTSTRAP_BIN) -m venv $(VENV_DIR); \
	fi
	$(VENV_PYTHON) -m pip install -r services/ai-runtime-py/requirements.txt

runtime-dev: python-check
	cd services/ai-runtime-py && SEE_AGENT_PROFILE=smoke $(PYTHON_BIN) -m uvicorn app.main:app --host 0.0.0.0 --port 8000 --reload

runtime-rag-dev: python-check
	@printf "runtime-rag-dev is retired; use make runtime-fullstack-dev with explicit fullstack_dev environment.\n" >&2
	@exit 2

runtime-fullstack-dev: fullstack-preflight
	cd services/ai-runtime-py && SEE_AGENT_PROFILE=fullstack_dev $(PYTHON_BIN) -m uvicorn app.main:app --host 0.0.0.0 --port 8000 --reload

runtime-test: python-check
	cd services/ai-runtime-py && SEE_AGENT_PROFILE=test $(PYTHON_BIN) -m pytest

runtime-qdrant-smoke: python-check
	QDRANT_URL="$${QDRANT_URL:-http://127.0.0.1:16333}" $(PYTHON_BIN) scripts/ops/runtime_qdrant_smoke.py

runtime-native-qdrant-smoke: python-check
	QDRANT_URL="$${QDRANT_URL:-http://127.0.0.1:16333}" $(PYTHON_BIN) scripts/ops/runtime_native_qdrant_smoke.py

runtime-native-dense-smoke: python-check
	QDRANT_URL="$${QDRANT_URL:-http://127.0.0.1:16333}" $(PYTHON_BIN) scripts/ops/runtime_native_dense_smoke.py

local-model-smoke: python-check
	LOCAL_LLM_BASE_URL="$${LOCAL_LLM_BASE_URL:-http://127.0.0.1:11434/v1}" LOCAL_LLM_DEFAULT_MODEL_ID="$${LOCAL_LLM_DEFAULT_MODEL_ID:-qwen3:14b}" LOCAL_LLM_TIMEOUT_SECONDS="$${LOCAL_LLM_TIMEOUT_SECONDS:-90}" $(PYTHON_BIN) scripts/ops/local_model_smoke.py

provider-stability-probe: python-check
	LOCAL_LLM_BASE_URL="$${LOCAL_LLM_BASE_URL:-http://127.0.0.1:11434/v1}" LOCAL_LLM_DEFAULT_MODEL_ID="$${LOCAL_LLM_DEFAULT_MODEL_ID:-qwen3:14b}" LOCAL_LLM_PROVIDER="$${LOCAL_LLM_PROVIDER:-ollama_native}" LOCAL_LLM_TIMEOUT_SECONDS="$${LOCAL_LLM_TIMEOUT_SECONDS:-90}" $(PYTHON_BIN) scripts/ops/provider_stability_probe.py

minio-ingestion-smoke: python-check
	OBJECT_STORE_ENDPOINT="$${OBJECT_STORE_ENDPOINT:-http://127.0.0.1:19000}" OBJECT_STORE_BUCKET="$${OBJECT_STORE_BUCKET:-knowledge-docs}" INTEGRATION_GO_BIN="$(GO_BIN)" INTEGRATION_PYTHON_BIN="$(PYTHON_BIN)" $(PYTHON_BIN) scripts/ops/minio_ingestion_smoke.py

postgres-repository-smoke: python-check
	DATABASE_URL="$${DATABASE_URL:-postgres://see_agent:see_agent_dev@127.0.0.1:15432/see_agent?sslmode=disable}" INTEGRATION_GO_BIN="$(GO_BIN)" $(PYTHON_BIN) scripts/ops/postgres_repository_smoke.py

postgres-k2-smoke: python-check
	cd services/backend-go && K2_TEST_DATABASE_URL="$${K2_TEST_DATABASE_URL:-postgres://see_agent:see_agent_dev@127.0.0.1:15432/see_agent?sslmode=disable}" $(GO_BIN) test ./internal/repository -run TestPostgresKnowledgeEvolution -count=1

postgres-k7-smoke: python-check
	cd services/backend-go && K7_TEST_DATABASE_URL="$${K7_TEST_DATABASE_URL:-postgres://see_agent:see_agent_dev@127.0.0.1:15432/see_agent?sslmode=disable}" $(GO_BIN) test ./internal/repository -run TestPostgresK7EvidenceAndEvalRegistration -count=1

postgres-k9-ownership-smoke: python-check
	cd services/backend-go && K9_TEST_DATABASE_URL="$${K9_TEST_DATABASE_URL:-postgres://see_agent:see_agent_dev@127.0.0.1:15432/see_agent?sslmode=disable}" $(GO_BIN) test ./internal/repository -run TestPostgresConversationOwnershipMigrationAndIsolation -count=1

redis-ingestion-smoke: python-check
	REDIS_URL="$${REDIS_URL:-redis://127.0.0.1:16379/0}" INTEGRATION_GO_BIN="$(GO_BIN)" INTEGRATION_PYTHON_BIN="$(PYTHON_BIN)" $(PYTHON_BIN) scripts/ops/redis_ingestion_smoke.py

redis-session-smoke: python-check
	REDIS_URL="$${REDIS_URL:-redis://127.0.0.1:16379/0}" INTEGRATION_GO_BIN="$(GO_BIN)" $(PYTHON_BIN) scripts/ops/redis_session_smoke.py

bootstrap-rag-knowledge:
	BACKEND_API_BASE_URL="$${BACKEND_API_BASE_URL:-http://127.0.0.1:8080}" node scripts/ops/bootstrap_rag_knowledge.mjs

infra-up:
	docker compose -f $(COMPOSE_FILE) up -d

infra-down:
	docker compose -f $(COMPOSE_FILE) down

infra-ps:
	docker compose -f $(COMPOSE_FILE) ps

infra-logs:
	docker compose -f $(COMPOSE_FILE) logs -f --tail=100

infra-check:
	COMPOSE_FILE=$(COMPOSE_FILE) sh scripts/ops/infra_check.sh

fullstack-preflight:
	sh scripts/ops/fullstack_preflight.sh

fullstack-preflight-test:
	sh scripts/ops/test_fullstack_preflight.sh

fullstack-smoke:
	@curl -fsS --max-time 3 "$${AI_RUNTIME_BASE_URL:?AI_RUNTIME_BASE_URL is required}/readyz" >/dev/null
	@curl -fsS --max-time 3 "http://127.0.0.1:$${BACKEND_PORT:-8080}/readyz" >/dev/null
	@printf "fullstack readiness smoke passed\n"

dev:
	@sh scripts/ops/dev_start.sh

test: backend-test runtime-test

lint: web-lint

format:
	@echo "Formatting is not wired yet. Add service-specific formatters in the next iteration."

typecheck:
	cd apps/web && $(PNPM) run typecheck

eval-baseline: python-check
	$(PYTHON_BIN) scripts/eval/run_baseline.py --mode mock --dataset data/eval-datasets/baseline/v1-seed.jsonl --report-dir data/eval-datasets/reports

eval-runtime-baseline: python-check
	$(PYTHON_BIN) scripts/eval/run_baseline.py --mode runtime-local --dataset data/eval-datasets/baseline/v1-seed.jsonl --report-dir data/eval-datasets/reports/runtime

eval-runtime-gate: python-check
	$(PYTHON_BIN) scripts/eval/run_baseline.py --mode runtime-local --dataset data/eval-datasets/baseline/v1-seed.jsonl --report-dir data/eval-datasets/reports/runtime --system-prompt-version prompt_v1 --quality-gate data/eval-datasets/baseline/r3-llama3.2-quality-gate.json

eval-runtime-k7-gate: python-check
	$(PYTHON_BIN) scripts/eval/run_baseline.py --mode runtime-local --dataset data/eval-datasets/baseline/v1-seed.jsonl --report-dir data/eval-datasets/reports/runtime/prompt-v2 --system-prompt-version prompt_v2 --quality-gate data/eval-datasets/baseline/k7-llama3.2-prompt-v2-quality-gate.json

integration: python-check
	cd apps/web && $(PNPM) run build
	INTEGRATION_GO_BIN=$(GO_BIN) INTEGRATION_PYTHON_BIN=$(PYTHON_BIN) $(PYTHON_BIN) -m pytest tests/integration tests/e2e
