#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
cd "$ROOT_DIR"

COMPOSE_FILE="${COMPOSE_FILE:-infra/compose/docker-compose.yml}"
export COMPOSE_FILE
SECRET_FILE="${SEE_AGENT_RUNTIME_SECRETS_FILE:-$HOME/.config/see-agent/fullstack-secrets.env}"
LOG_ROOT="${SEE_AGENT_DEV_LOG_DIR:-$ROOT_DIR/.local/dev/logs}"
RUN_ID="$(date +%Y%m%d-%H%M%S)"
LOG_DIR="$LOG_ROOT/$RUN_ID"

RUNTIME_PID=""
BACKEND_PID=""
WEB_PID=""
CLEANED=0

RUNTIME_BASE_URL=""
BACKEND_PORT=""
WEB_PORT=""
BACKEND_API_BASE_URL=""
PYTHON_BIN=""
GO_BIN=""
PNPM_BIN=""

mkdir -p "$LOG_DIR"

info() {
	printf '[dev] %s\n' "$1"
}

tail_log() {
	label="$1"
	file="$2"
	if [ -f "$file" ]; then
		printf '\n[dev] %s 日志尾部（%s）：\n' "$label" "$file" >&2
		tail -n 80 "$file" >&2 || true
	fi
}

kill_tree() {
	pid="$1"
	[ -n "$pid" ] || return 0

	children="$(ps -Ao pid=,ppid= 2>/dev/null | awk -v parent="$pid" '$2 == parent {print $1}')"
	for child in $children; do
		kill_tree "$child"
	done
	kill "$pid" 2>/dev/null || true
}

cleanup() {
	[ "$CLEANED" -eq 0 ] || return 0
	CLEANED=1

	if [ -n "$WEB_PID" ]; then
		info "停止本次启动的 Web 进程"
		kill_tree "$WEB_PID"
	fi
	if [ -n "$BACKEND_PID" ]; then
		info "停止本次启动的 Backend 进程"
		kill_tree "$BACKEND_PID"
	fi
	if [ -n "$RUNTIME_PID" ]; then
		info "停止本次启动的 Runtime 进程"
		kill_tree "$RUNTIME_PID"
	fi
}

fail() {
	printf '[dev] ERROR: %s\n' "$1" >&2
	exit 1
}

trap 'exit 130' INT TERM
trap cleanup EXIT

run_step() {
	label="$1"
	shift
	info "$label"
	if ! "$@"; then
		fail "$label 失败"
	fi
}

require_command() {
	command_name="$1"
	if ! command -v "$command_name" >/dev/null 2>&1; then
		fail "缺少命令 ${command_name}。请先安装对应的本地开发工具。"
	fi
}

find_python() {
	for candidate in python3.14 python3.13 python3.12 python3; do
		if command -v "$candidate" >/dev/null 2>&1 && \
			"$candidate" -c 'import sys; raise SystemExit(0 if sys.version_info >= (3, 12) else 1)' >/dev/null 2>&1; then
			command -v "$candidate"
			return 0
		fi
	done
	return 1
}

prepare_runtime_secret() {
	secret_dir="$(dirname -- "$SECRET_FILE")"
	mkdir -p "$secret_dir"
	chmod 700 "$secret_dir"

	valid=0
	if [ -f "$SECRET_FILE" ]; then
		secret_token="$(sed -n 's/^export SEE_AGENT_RUNTIME_SERVICE_TOKEN=//p' "$SECRET_FILE" 2>/dev/null | tail -n 1 || true)"
		case "$secret_token" in
			''|replace-with-local-dev-secret)
				valid=0
				;;
			*)
				if [ "${#secret_token}" -ge 32 ]; then
					valid=1
				fi
				;;
		esac
	fi

	if [ "$valid" -eq 0 ]; then
		info "Runtime service token 缺失或无效，重新生成"
		token="$(openssl rand -hex 32)"
		(umask 077; printf 'export SEE_AGENT_RUNTIME_SERVICE_TOKEN=%s\n' "$token" > "$SECRET_FILE")
		unset token
	else
		info "Runtime service token 已存在且长度有效"
	fi

	chmod 600 "$SECRET_FILE"
	[ -r "$SECRET_FILE" ] || fail "无法读取 Runtime service token 文件：$SECRET_FILE"
}

load_environment() {
	[ -f infra/compose/.env.example ] || fail "缺少 infra/compose/.env.example"
	set -a
	. infra/compose/.env.example
	. "$SECRET_FILE"
	set +a

	RUNTIME_BASE_URL="${AI_RUNTIME_BASE_URL:-http://127.0.0.1:8000}"
	RUNTIME_BASE_URL="${RUNTIME_BASE_URL%/}"
	BACKEND_PORT="${BACKEND_PORT:-8080}"
	WEB_PORT="${WEB_PORT:-3000}"
	BACKEND_API_BASE_URL="${BACKEND_API_BASE_URL:-http://127.0.0.1:$BACKEND_PORT}"
}

check_toolchain() {
	for command_name in docker curl openssl ollama node pnpm; do
		require_command "$command_name"
	done

	docker info >/dev/null 2>&1 || fail "Docker daemon 未运行。请先启动 Docker Desktop。"
	docker compose version >/dev/null 2>&1 || fail "Docker Compose 不可用。请确认 Docker Desktop 已包含 Compose。"

	PNPM_BIN="$(command -v pnpm)"
	pnpm_version="$(pnpm --version 2>/dev/null || true)"
	[ "$pnpm_version" = "10.13.1" ] || fail "pnpm 版本必须是 10.13.1，当前为 ${pnpm_version:-未知}。"

	if [ -n "${GO_BIN:-}" ] && [ -x "$GO_BIN" ]; then
		:
	else
		GO_BIN="$(command -v go 2>/dev/null || true)"
	fi
	if [ -z "$GO_BIN" ] || [ ! -x "$GO_BIN" ]; then
		GO_BIN="$(make -s -pn 2>/dev/null | sed -n 's/^GO_BIN = //p' | head -n 1)"
	fi
	[ -n "$GO_BIN" ] && [ -x "$GO_BIN" ] || fail "未找到 Go 编译器。请安装 Go，或设置 GO_BIN。"

	PYTHON_BOOTSTRAP_BIN="$(find_python 2>/dev/null || true)"
	[ -n "$PYTHON_BOOTSTRAP_BIN" ] || fail "未找到 Python 3.12+。请安装 Python 3.12、3.13 或 3.14。"
}

check_ollama() {
	info "检查 Ollama 服务"
	ollama_base_url="${LOCAL_LLM_BASE_URL%/}"
	curl -fsS --max-time 5 "$ollama_base_url/models" >/dev/null 2>&1 || \
		fail "Ollama 服务不可用。请先在另一个终端执行：ollama serve"

	for model in "${LOCAL_LLM_DEFAULT_MODEL_ID:-qwen3:14b}" "${EMBEDDING_MODEL:-qwen3-embedding:0.6b}"; do
		if ! ollama list 2>/dev/null | awk -v target="$model" 'NR > 1 && $1 == target {found = 1} END {exit found ? 0 : 1}'; then
			fail "Ollama 缺少模型 ${model}。请执行：ollama pull ${model}"
		fi
	done
}

ensure_runtime_dependencies() {
	stamp="$ROOT_DIR/.venv/.see-agent-runtime-install.stamp"
	require_install=0

	if [ ! -x "$ROOT_DIR/.venv/bin/python" ] || [ ! -f "$stamp" ]; then
		require_install=1
	elif [ "services/ai-runtime-py/requirements.txt" -nt "$stamp" ]; then
		require_install=1
	elif ! "$ROOT_DIR/.venv/bin/python" -c 'import fastapi, httpx, uvicorn' >/dev/null 2>&1; then
		require_install=1
	fi

	if [ "$require_install" -eq 1 ]; then
		run_step "安装 Python Runtime 依赖" make runtime-install PYTHON_BOOTSTRAP_BIN="$PYTHON_BOOTSTRAP_BIN"
		touch "$stamp"
	else
		info "Python Runtime 依赖已就绪"
	fi

	PYTHON_BIN="$ROOT_DIR/.venv/bin/python"
}

ensure_backend_dependencies() {
	info "检查 Go Backend 依赖"
	if ! (cd services/backend-go && "$GO_BIN" mod download); then
		fail "Go Backend 依赖下载失败，请检查网络或 Go 环境"
	fi
}

ensure_web_dependencies() {
	marker="$ROOT_DIR/apps/web/node_modules/.modules.yaml"
	require_install=0

	if [ ! -f "$marker" ] || [ apps/web/package.json -nt "$marker" ] || [ apps/web/pnpm-lock.yaml -nt "$marker" ]; then
		require_install=1
	fi

	if [ "$require_install" -eq 1 ]; then
		run_step "安装 Web 依赖" make web-install PNPM="$PNPM_BIN"
	else
		info "Web 依赖已就绪"
	fi
}

url_ready() {
	url="$1"
	curl -fsS --max-time 3 "$url" >/dev/null 2>&1
}

port_in_use() {
	port="$1"
	if command -v lsof >/dev/null 2>&1; then
		lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1
		return $?
	fi
	return 1
}

check_or_report_port() {
	name="$1"
	port="$2"
	url="$3"
	if url_ready "$url"; then
		info "$name 已经就绪，复用现有进程"
		return 1
	fi
	if port_in_use "$port"; then
		fail "$name 端口 $port 已被占用，但 $url 未就绪。请先停止占用该端口的进程。"
	fi
	return 0
}

wait_for_service() {
	name="$1"
	url="$2"
	pid="$3"
	timeout_seconds="$4"
	elapsed=0

	info "等待 $name 就绪（最多 ${timeout_seconds}s）"
	while [ "$elapsed" -lt "$timeout_seconds" ]; do
		if url_ready "$url"; then
			info "$name 已就绪：$url"
			return 0
		fi
		if [ -n "$pid" ] && ! kill -0 "$pid" 2>/dev/null; then
			return 1
		fi
		sleep 2
		elapsed=$((elapsed + 2))
	done
	return 1
}

start_runtime() {
	RUNTIME_LOG="$LOG_DIR/runtime.log"
	(
		cd services/ai-runtime-py
		exec env SEE_AGENT_PROFILE=fullstack_dev "$PYTHON_BIN" -m uvicorn app.main:app --host 0.0.0.0 --port 8000 --reload
	) >"$RUNTIME_LOG" 2>&1 &
	RUNTIME_PID=$!
}

start_backend() {
	BACKEND_LOG="$LOG_DIR/backend.log"
	(
		cd services/backend-go
		exec env SEE_AGENT_PROFILE=fullstack_dev "$GO_BIN" run ./cmd/api
	) >"$BACKEND_LOG" 2>&1 &
	BACKEND_PID=$!
}

start_web() {
	WEB_LOG="$LOG_DIR/web.log"
	(
		cd apps/web
		exec env BACKEND_API_BASE_URL="$BACKEND_API_BASE_URL" "$PNPM_BIN" run dev
	) >"$WEB_LOG" 2>&1 &
	WEB_PID=$!
}

check_child_processes() {
	if [ -n "$RUNTIME_PID" ] && ! kill -0 "$RUNTIME_PID" 2>/dev/null; then
		tail_log "Runtime" "$RUNTIME_LOG"
		fail "Runtime 进程已退出"
	fi
	if [ -n "$BACKEND_PID" ] && ! kill -0 "$BACKEND_PID" 2>/dev/null; then
		tail_log "Backend" "$BACKEND_LOG"
		fail "Backend 进程已退出"
	fi
	if [ -n "$WEB_PID" ] && ! kill -0 "$WEB_PID" 2>/dev/null; then
		tail_log "Web" "$WEB_LOG"
		fail "Web 进程已退出"
	fi
}

check_toolchain
prepare_runtime_secret
load_environment
check_ollama

run_step "检查本地基础设施配置" make infra-check
run_step "启动 Postgres、Redis、MinIO、Qdrant" make infra-up
run_step "检查基础设施健康状态" env RUNNING=1 make infra-check

ensure_runtime_dependencies
ensure_backend_dependencies
ensure_web_dependencies

run_step "执行全栈启动预检" make fullstack-preflight

runtime_url="$RUNTIME_BASE_URL/readyz"
backend_url="http://127.0.0.1:$BACKEND_PORT/readyz"
web_url="http://127.0.0.1:$WEB_PORT/"

if check_or_report_port "Runtime" 8000 "$runtime_url"; then
	info "启动 Runtime，日志：$LOG_DIR/runtime.log"
	start_runtime
	if ! wait_for_service "Runtime" "$runtime_url" "$RUNTIME_PID" "${DEV_RUNTIME_READY_TIMEOUT_SECONDS:-180}"; then
		tail_log "Runtime" "$RUNTIME_LOG"
		fail "Runtime 未能就绪"
	fi
fi

if check_or_report_port "Backend" "$BACKEND_PORT" "$backend_url"; then
	info "启动 Backend，日志：$LOG_DIR/backend.log"
	start_backend
	if ! wait_for_service "Backend" "$backend_url" "$BACKEND_PID" "${DEV_BACKEND_READY_TIMEOUT_SECONDS:-120}"; then
		tail_log "Backend" "$BACKEND_LOG"
		fail "Backend 未能就绪"
	fi
fi

if check_or_report_port "Web" "$WEB_PORT" "$web_url"; then
	info "启动 Web，日志：$LOG_DIR/web.log"
	start_web
	if ! wait_for_service "Web" "$web_url" "$WEB_PID" "${DEV_WEB_READY_TIMEOUT_SECONDS:-90}"; then
		tail_log "Web" "$WEB_LOG"
		fail "Web 未能就绪"
	fi
fi

info "执行全栈就绪检查"
if ! make fullstack-smoke; then
	tail_log "Runtime" "${RUNTIME_LOG:-}"
	tail_log "Backend" "${BACKEND_LOG:-}"
	tail_log "Web" "${WEB_LOG:-}"
	fail "全栈就绪检查失败"
fi

printf '\n'
info "全栈开发环境已就绪"
info "Web：http://127.0.0.1:$WEB_PORT"
info "Backend：http://127.0.0.1:$BACKEND_PORT"
info "Runtime：$RUNTIME_BASE_URL"
info "应用日志：$LOG_DIR"
info "可选：导入示例知识：make bootstrap-rag-knowledge"
info "按 Ctrl-C 停止本次启动的 Runtime、Backend、Web；Docker 依赖和数据卷保持运行"

while :; do
	check_child_processes
	sleep 2
done
