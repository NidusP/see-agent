#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
PREFLIGHT="$ROOT_DIR/scripts/ops/fullstack_preflight.sh"
VALID_TOKEN='0123456789abcdef0123456789abcdef'
VALID_PREVIOUS='fedcba9876543210fedcba9876543210'

fail() {
	printf '[test-fullstack-preflight] ERROR: %s\n' "$1" >&2
	exit 1
}

expect_failure() {
	expected="$1"
	shift
	set +e
	output=$(env -i PATH="$PATH" "$@" /bin/sh "$PREFLIGHT" 2>&1)
	status=$?
	set -e
	[ "$status" -ne 0 ] || fail "preflight unexpectedly succeeded"
	printf '%s' "$output" | grep -F "$expected" >/dev/null || fail "expected ${expected}, got: ${output}"
	if printf '%s' "$output" | grep -F 'docker is required' >/dev/null; then
		fail "invalid token reached dependency checks: ${output}"
	fi
}

expect_failure 'SEE_AGENT_RUNTIME_SERVICE_TOKEN must be configured' \
	SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS=
expect_failure 'SEE_AGENT_RUNTIME_SERVICE_TOKEN must be configured' \
	SEE_AGENT_RUNTIME_SERVICE_TOKEN='                                '
expect_failure 'repository placeholder' \
	SEE_AGENT_RUNTIME_SERVICE_TOKEN=replace-with-local-dev-secret
expect_failure 'at least 32 characters' \
	SEE_AGENT_RUNTIME_SERVICE_TOKEN=short-secret
expect_failure 'PREVIOUS must differ' \
	SEE_AGENT_RUNTIME_SERVICE_TOKEN="$VALID_TOKEN" \
	SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS="$VALID_TOKEN"

# A valid token reaches the ordinary configuration checks instead of failing the
# secret policy; no dependency probes run because DATABASE_URL is intentionally absent.
expect_failure 'missing required environment variable: DATABASE_URL' \
	SEE_AGENT_RUNTIME_SERVICE_TOKEN="$VALID_TOKEN" \
	SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS="$VALID_PREVIOUS"

grep -Fx 'SEE_AGENT_RUNTIME_SERVICE_TOKEN=' "$ROOT_DIR/infra/compose/.env.example" >/dev/null || fail 'template contains a functional runtime service token'
printf '[test-fullstack-preflight] passed\n'
