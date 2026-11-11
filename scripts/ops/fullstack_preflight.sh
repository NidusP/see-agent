#!/usr/bin/env sh
set -eu

fail() {
	printf '[fullstack-preflight] ERROR: %s\n' "$1" >&2
	exit 1
}

require() {
	name="$1"
	eval "value=\${$name-}"
	[ -n "${value}" ] || fail "missing required environment variable: ${name}"
}

validate_runtime_service_token() {
	name="$1"
	value="$2"
	[ -n "$value" ] || fail "${name} must be configured with a non-empty secret"
	[ "$value" != "replace-with-local-dev-secret" ] || fail "${name} must not use the repository placeholder"
	[ "${#value}" -ge 32 ] || fail "${name} must be at least 32 characters"
}

trim_runtime_service_token() {
	printf '%s' "$1" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//'
}

current_runtime_token="$(trim_runtime_service_token "${SEE_AGENT_RUNTIME_SERVICE_TOKEN-}")"
previous_runtime_token="$(trim_runtime_service_token "${SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS-}")"
validate_runtime_service_token "SEE_AGENT_RUNTIME_SERVICE_TOKEN" "$current_runtime_token"
if [ -n "$previous_runtime_token" ]; then
	validate_runtime_service_token "SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS" "$previous_runtime_token"
	[ "$previous_runtime_token" != "$current_runtime_token" ] || fail 'SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS must differ from SEE_AGENT_RUNTIME_SERVICE_TOKEN'
fi

for name in \
	DATABASE_URL REDIS_URL AI_RUNTIME_BASE_URL SEE_AGENT_RUNTIME_SERVICE_TOKEN \
	AUTH_SESSION_STORE K3_DELIVERY_ENABLED \
	OBJECT_STORE_ENDPOINT OBJECT_STORE_BUCKET OBJECT_STORE_ACCESS_KEY OBJECT_STORE_SECRET_KEY \
	QDRANT_URL QDRANT_COLLECTION RUNTIME_RETRIEVAL_STRATEGY \
	LOCAL_LLM_BASE_URL LOCAL_LLM_PROVIDER LOCAL_LLM_DEFAULT_MODEL_ID \
	LOCAL_LLM_TIMEOUT_SECONDS BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS \
	SEE_AGENT_SYSTEM_ADMIN_USERNAME SEE_AGENT_SYSTEM_ADMIN_PASSWORD \
	SEE_AGENT_KNOWLEDGE_ADMIN_USERNAME SEE_AGENT_KNOWLEDGE_ADMIN_PASSWORD \
	SEE_AGENT_USER_USERNAME SEE_AGENT_USER_PASSWORD; do
	require "$name"
done

[ "$AUTH_SESSION_STORE" = "redis" ] || fail 'AUTH_SESSION_STORE must be redis'
[ "$K3_DELIVERY_ENABLED" = "true" ] || fail 'K3_DELIVERY_ENABLED must be true'

case "$LOCAL_LLM_PROVIDER" in
	openai_compatible|ollama_native) ;;
	*) fail 'LOCAL_LLM_PROVIDER must be openai_compatible or ollama_native' ;;
esac

for name in LOCAL_LLM_TIMEOUT_SECONDS BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS; do
	eval "value=\${$name-}"
	case "$value" in
		*[!0-9]*|'') fail "${name} must be a positive integer" ;;
	esac
	[ "$value" -gt 0 ] || fail "${name} must be a positive integer"
done
[ "$BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS" -ge 5 ] || fail 'BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS must be at least 5'
[ "$BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS" -le "$LOCAL_LLM_TIMEOUT_SECONDS" ] || fail 'BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS must not exceed LOCAL_LLM_TIMEOUT_SECONDS'

[ "$RUNTIME_RETRIEVAL_STRATEGY" = "native_dense_v1" ] || fail 'RUNTIME_RETRIEVAL_STRATEGY must be native_dense_v1'
for name in EMBEDDING_BASE_URL EMBEDDING_MODEL EMBEDDING_DIMENSIONS; do
	require "$name"
done
case "$EMBEDDING_DIMENSIONS" in
	*[!0-9]*|'') fail 'EMBEDDING_DIMENSIONS must be a positive integer' ;;
esac
[ "$EMBEDDING_DIMENSIONS" -gt 0 ] || fail 'EMBEDDING_DIMENSIONS must be a positive integer'
[ "$QDRANT_COLLECTION" != "see_agent_chunks" ] || fail 'native_dense_v1 requires a dedicated Qdrant collection, not see_agent_chunks'

command -v docker >/dev/null 2>&1 || fail 'docker is required for local infrastructure checks'
command -v curl >/dev/null 2>&1 || fail 'curl is required for endpoint checks'

COMPOSE_FILE="${COMPOSE_FILE:-infra/compose/docker-compose.yml}"
RUNNING=1 COMPOSE_FILE="$COMPOSE_FILE" sh scripts/ops/infra_check.sh

curl -fsS --max-time 3 "${QDRANT_URL%/}/healthz" >/dev/null || fail 'Qdrant health check failed'
curl -fsS --max-time 3 "${OBJECT_STORE_ENDPOINT%/}/minio/health/live" >/dev/null || fail 'object store health check failed'
curl -fsS --max-time 10 "${LOCAL_LLM_BASE_URL%/}/models" >/dev/null || fail 'model provider /models check failed'

printf '[fullstack-preflight] infrastructure, object store, vector store and model provider are reachable\n'
