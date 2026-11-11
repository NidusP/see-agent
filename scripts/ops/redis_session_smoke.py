from __future__ import annotations

import os
import socket
import sys
import uuid
from pathlib import Path
from urllib.parse import urlparse

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from tests.common.server_utils import find_free_port, login_session, running_process


BACKEND_ROOT = ROOT / "services" / "backend-go"


def main() -> int:
    redis_url = os.environ.get("REDIS_URL", "redis://127.0.0.1:16379/0")
    session_prefix = os.environ.get("REDIS_SESSION_PREFIX", f"see_agent:session:smoke:{uuid.uuid4().hex[:10]}:")
    go_bin = os.environ.get("INTEGRATION_GO_BIN", os.environ.get("GO_BIN", "/Users/populus/.local/go1.26.4/bin/go"))

    _assert_redis_available(redis_url)

    backend_port = find_free_port()
    backend_base_url = f"http://127.0.0.1:{backend_port}"
    env = {
        "BACKEND_ADDR": f"127.0.0.1:{backend_port}",
        "AI_RUNTIME_BASE_URL": "",
        "AUTH_SESSION_STORE": "redis",
        "REDIS_URL": redis_url,
        "REDIS_SESSION_PREFIX": session_prefix,
        "SEE_AGENT_ADMIN_USERNAME": "admin",
        "SEE_AGENT_ADMIN_PASSWORD": "admin123",
        "SEE_AGENT_USER_USERNAME": "user",
        "SEE_AGENT_USER_PASSWORD": "user123",
    }

    with running_process(
        [go_bin, "run", "./cmd/api"],
        cwd=BACKEND_ROOT,
        env=env,
        startup_url=f"{backend_base_url}/healthz",
    ):
        session = login_session(backend_base_url)
        status, payload = session.fetch_json(f"{backend_base_url}/api/auth/me")
        if status != 200 or payload["user"]["role"] != "admin":
            raise RuntimeError(f"unexpected auth/me before restart: {status} {payload}")

    with running_process(
        [go_bin, "run", "./cmd/api"],
        cwd=BACKEND_ROOT,
        env=env,
        startup_url=f"{backend_base_url}/healthz",
    ):
        status, payload = session.fetch_json(f"{backend_base_url}/api/auth/me")
        if status != 200 or payload["user"]["role"] != "admin":
            raise RuntimeError(f"redis session did not survive backend restart: {status} {payload}")

        logout_status, logout_payload = session.fetch_json(
            f"{backend_base_url}/api/auth/logout",
            method="POST",
            payload={},
        )
        if logout_status != 200:
            raise RuntimeError(f"logout failed: {logout_status} {logout_payload}")

        status, payload = session.fetch_json(f"{backend_base_url}/api/auth/me")
        if status != 401:
            raise RuntimeError(f"expected session to be cleared after logout, got {status} {payload}")

    print(f"redis session smoke ok: prefix={session_prefix} redis={redis_url}")
    return 0


def _assert_redis_available(redis_url: str) -> None:
    parsed = urlparse(redis_url)
    host = parsed.hostname or "127.0.0.1"
    port = parsed.port or 6379
    with socket.create_connection((host, port), timeout=3) as sock:
        sock.sendall(b"*1\r\n$4\r\nPING\r\n")
        response = sock.recv(64)
    if not response.startswith(b"+PONG"):
        raise RuntimeError(f"Redis PING failed: {response!r}")


if __name__ == "__main__":
    raise SystemExit(main())
