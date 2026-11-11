#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-infra/compose/docker-compose.yml}"
RUNNING="${RUNNING:-0}"

cd "$ROOT_DIR"

info() {
	printf '[infra-check] %s\n' "$1"
}

fail() {
	printf '[infra-check] ERROR: %s\n' "$1" >&2
	exit 1
}

require_command() {
	if ! command -v "$1" >/dev/null 2>&1; then
		fail "$1 is required but was not found"
	fi
}

require_file() {
	if [ ! -f "$1" ]; then
		fail "missing required file: $1"
	fi
}

require_service() {
	service="$1"
	if ! printf '%s\n' "$COMPOSE_SERVICES" | grep -qx "$service"; then
		fail "compose service is missing: $service"
	fi
}

check_running_service() {
	service="$1"
	if ! printf '%s\n' "$RUNNING_SERVICES" | grep -qx "$service"; then
		fail "compose service is not running: $service"
	fi
}

info "checking required local files"
require_file "$COMPOSE_FILE"
require_file "infra/compose/.env.example"
require_file "agent-registry/registry/services.yaml"
require_file "docs/runbooks/local-stack-bootstrap.md"

info "checking Docker Compose configuration"
require_command docker
docker compose -f "$COMPOSE_FILE" config --quiet
COMPOSE_SERVICES="$(docker compose -f "$COMPOSE_FILE" config --services)"

for service in postgres redis minio qdrant; do
	require_service "$service"
done

info "checking registry mentions core infra services"
for service in postgres redis minio qdrant local-model; do
	if ! grep -q "name: $service" agent-registry/registry/services.yaml; then
		fail "registry service is missing: $service"
	fi
done

info "checking environment template keys"
for key in DATABASE_URL REDIS_URL REDIS_INGESTION_QUEUE AUTH_SESSION_STORE REDIS_SESSION_PREFIX OBJECT_STORE_ENDPOINT OBJECT_STORE_BUCKET OBJECT_STORE_ACCESS_KEY OBJECT_STORE_SECRET_KEY OBJECT_STORE_REGION OBJECT_STORE_TIMEOUT_SECONDS QDRANT_URL QDRANT_COLLECTION QDRANT_TIMEOUT_SECONDS RUNTIME_RETRIEVAL_STRATEGY LOCAL_LLM_BASE_URL LOCAL_LLM_API_KEY; do
	if ! grep -q "^$key=" infra/compose/.env.example; then
		fail "infra/compose/.env.example is missing $key"
	fi
done

if [ "$RUNNING" = "1" ]; then
	info "checking running containers"
	RUNNING_SERVICES="$(docker compose -f "$COMPOSE_FILE" ps --services --status running)"
	for service in postgres redis minio qdrant; do
		check_running_service "$service"
	done

	info "checking container health endpoints"
	docker compose -f "$COMPOSE_FILE" exec -T postgres pg_isready -U see_agent -d see_agent >/dev/null
	docker compose -f "$COMPOSE_FILE" exec -T redis redis-cli ping | grep -qx PONG
	require_command curl
	curl -fsS "http://localhost:${SEE_AGENT_MINIO_API_PORT:-19000}/minio/health/live" >/dev/null
	curl -fsS "http://localhost:${SEE_AGENT_QDRANT_HTTP_PORT:-16333}/healthz" >/dev/null
else
	info "skipping running container checks; use RUNNING=1 make infra-check after make infra-up"
fi

info "ok"
