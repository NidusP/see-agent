from __future__ import annotations

import os
import subprocess
import sys
import time
from pathlib import Path

import pytest

from tests.common.server_utils import HttpSession, fetch_json, find_free_port, login_session, running_process


WORKSPACE_ROOT = Path(__file__).resolve().parents[2]
BACKEND_ROOT = WORKSPACE_ROOT / "services" / "backend-go"
RUNTIME_ROOT = WORKSPACE_ROOT / "services" / "ai-runtime-py"
WEB_ROOT = WORKSPACE_ROOT / "apps" / "web"
SAMPLE_DOCS = WORKSPACE_ROOT / "data" / "sample-docs"
FAKE_MODEL_SERVER = WORKSPACE_ROOT / "tests" / "fixtures" / "openai_compatible_model_server.py"
GO_BIN = os.environ.get("INTEGRATION_GO_BIN", "/Users/populus/.local/go1.26.4/bin/go")
PYTHON_BIN = os.environ.get("INTEGRATION_PYTHON_BIN") or sys.executable


@pytest.fixture(scope="module")
def model_base_url() -> str:
    port = find_free_port()
    base_url = f"http://127.0.0.1:{port}"
    with running_process(
        [PYTHON_BIN, str(FAKE_MODEL_SERVER), "--port", str(port)],
        cwd=WORKSPACE_ROOT,
        startup_url=f"{base_url}/v1/models",
    ):
        yield f"{base_url}/v1"


@pytest.fixture(scope="module")
def runtime_base_url(model_base_url: str) -> str:
    port = find_free_port()
    base_url = f"http://127.0.0.1:{port}"
    with running_process(
        [PYTHON_BIN, "-m", "uvicorn", "app.main:app", "--host", "127.0.0.1", "--port", str(port)],
        cwd=RUNTIME_ROOT,
        env={
            "SEE_AGENT_PROFILE": "smoke",
            "RUNTIME_RETRIEVAL_STRATEGY": "",
            "RUNTIME_RETRIEVAL_MODE": "",
            "QDRANT_URL": "",
            "EMBEDDING_BASE_URL": "",
            "EMBEDDING_MODEL": "",
            "EMBEDDING_DIMENSIONS": "",
            "LOCAL_LLM_BASE_URL": model_base_url,
            "LOCAL_LLM_API_KEY": "test-only",
        },
        startup_url=f"{base_url}/healthz",
    ):
        yield base_url


@pytest.fixture(scope="module")
def backend_base_url(runtime_base_url: str, tmp_path_factory: pytest.TempPathFactory) -> str:
    port = find_free_port()
    base_url = f"http://127.0.0.1:{port}"
    upload_dir = tmp_path_factory.mktemp("web-proxy-backend-uploads")
    with running_process(
        [GO_BIN, "run", "./cmd/api"],
        cwd=BACKEND_ROOT,
        env={
            "BACKEND_ADDR": f"127.0.0.1:{port}",
            "AI_RUNTIME_BASE_URL": runtime_base_url,
            "BACKEND_UPLOAD_DIR": str(upload_dir),
            "SEE_AGENT_ADMIN_USERNAME": "admin",
            "SEE_AGENT_ADMIN_PASSWORD": "admin123",
            "SEE_AGENT_USER_USERNAME": "user",
            "SEE_AGENT_USER_PASSWORD": "user123",
        },
        startup_url=f"{base_url}/healthz",
    ):
        yield base_url


@pytest.fixture(scope="module")
def web_with_backend_base_url(backend_base_url: str) -> str:
    port = find_free_port()
    base_url = f"http://127.0.0.1:{port}"
    with running_process(
        ["npm", "exec", "next", "start", "--", "--hostname", "127.0.0.1", "--port", str(port)],
        cwd=WEB_ROOT,
        env={"BACKEND_API_BASE_URL": backend_base_url},
        startup_url=f"{base_url}/api/health",
        startup_timeout_seconds=60.0,
    ):
        yield base_url


def wait_for_web_proxy_file_terminal_state(
    web_base_url: str,
    session: HttpSession,
    file_id: str,
    *,
    timeout_seconds: float = 20.0,
) -> dict[str, object]:
    deadline = time.time() + timeout_seconds

    while time.time() < deadline:
        detail_status, detail_payload = session.fetch_json(f"{web_base_url}/api/kb/files/{file_id}")
        assert detail_status == 200
        latest_task = detail_payload["latest_task"]
        if latest_task["status"] in {"succeeded", "failed"}:
            return detail_payload
        time.sleep(0.25)

    raise AssertionError(f"Timed out waiting for web proxy file {file_id} terminal state")


def test_web_chat_api_proxy_preserves_missing_snapshot_failure(web_with_backend_base_url: str) -> None:
    session = login_session(web_with_backend_base_url)
    list_status, list_payload = session.fetch_json(f"{web_with_backend_base_url}/api/chat/conversations")
    assert list_status == 200
    assert "items" in list_payload

    create_status, create_payload = session.fetch_json(
        f"{web_with_backend_base_url}/api/chat/conversations",
        method="POST",
        payload={"title": "Web proxy smoke chat"},
    )
    assert create_status == 201
    conversation_id = create_payload["conversation"]["conversation_id"]

    message_status, message_payload = session.fetch_json(
        f"{web_with_backend_base_url}/api/chat/conversations/{conversation_id}/messages",
        method="POST",
        payload={
            "content": "差旅报销需要在多久内提交？",
            "stream": False,
        },
    )
    assert message_status == 409
    assert message_payload["error"]["code"] == "knowledge_snapshot_unavailable"
    assert message_payload["error"]["message"] == "a published knowledge snapshot is not available yet"


def test_web_chat_api_proxy_stream_preserves_missing_snapshot_failure(web_with_backend_base_url: str) -> None:
    session = login_session(web_with_backend_base_url)
    create_status, create_payload = session.fetch_json(
        f"{web_with_backend_base_url}/api/chat/conversations",
        method="POST",
        payload={"title": "Web proxy stream smoke chat"},
    )
    assert create_status == 201
    conversation_id = create_payload["conversation"]["conversation_id"]

    stream_status, stream_body = session.fetch_text(
        f"{web_with_backend_base_url}/api/chat/conversations/{conversation_id}/messages",
        method="POST",
        payload={
            "content": "差旅报销需要在多久内提交？",
            "stream": True,
        },
        headers={"Accept": "text/event-stream"},
    )
    assert stream_status == 200
    assert "event: message.started" in stream_body
    assert "event: message.delta" not in stream_body
    assert "event: message.completed" not in stream_body
    assert "event: message.error" in stream_body
    assert '"code":"knowledge_snapshot_unavailable"' in stream_body
    assert "complete immutable scope" in stream_body


def test_web_knowledge_api_proxy_uploads_and_refreshes_status(web_with_backend_base_url: str) -> None:
    session = login_session(web_with_backend_base_url)
    list_status, list_payload = session.fetch_json(f"{web_with_backend_base_url}/api/kb/files")
    assert list_status == 200
    assert list_payload["total"] >= 1

    upload_status, upload_payload = session.fetch_multipart_json(
        f"{web_with_backend_base_url}/api/kb/files",
        fields={"force_reindex": "false"},
        files={
            "file": (
                "employee-handbook.txt",
                (SAMPLE_DOCS / "employee-handbook.txt").read_bytes(),
                "text/plain",
            )
        },
    )
    assert upload_status == 202
    file_id = upload_payload["file"]["file_id"]
    assert upload_payload["file"]["source_format"] == "txt"
    assert upload_payload["ingestion_task"]["status"] == "queued"

    detail_payload = wait_for_web_proxy_file_terminal_state(web_with_backend_base_url, session, file_id)
    assert detail_payload["file"]["status"] == "succeeded"
    assert detail_payload["latest_task"]["status"] == "succeeded"

    chunks_status, chunks_payload = session.fetch_json(f"{web_with_backend_base_url}/api/kb/files/{file_id}/chunks")
    assert chunks_status == 200
    assert chunks_payload["total"] >= 1
    assert chunks_payload["items"][0]["file_id"] == file_id
    assert chunks_payload["items"][0]["text"]
    assert "source_locator" in chunks_payload["items"][0]

    reindex_status, reindex_payload = session.fetch_json(
        f"{web_with_backend_base_url}/api/kb/files/{file_id}/reindex",
        method="POST",
        payload={"force_reindex": True},
    )
    assert reindex_status == 202
    assert reindex_payload["ingestion_task"]["file_id"] == file_id
    assert reindex_payload["ingestion_task"]["task_id"] != upload_payload["ingestion_task"]["task_id"]


def test_web_config_api_proxy_reads_and_updates_admin_config(web_with_backend_base_url: str) -> None:
    session = login_session(web_with_backend_base_url)

    agent_status, agent_payload = session.fetch_json(f"{web_with_backend_base_url}/api/agent/config")
    retrieval_status, retrieval_payload = session.fetch_json(f"{web_with_backend_base_url}/api/agent/retrieval-config")
    versions_status, versions_payload = session.fetch_json(f"{web_with_backend_base_url}/api/agent/config/versions")

    assert agent_status == 200
    assert agent_payload["config"]["config_version"]
    assert retrieval_status == 200
    assert retrieval_payload["config"]["retrieval_config_version"]
    assert versions_status == 200
    assert versions_payload["total"] >= 1

    update_agent_status, update_agent_payload = session.fetch_json(
        f"{web_with_backend_base_url}/api/agent/config",
        method="PUT",
        payload={
            "model_id": "qwen-local-dev-config-smoke",
            "system_prompt_version": "prompt_v1",
            "temperature": 0.3,
            "max_output_tokens": 777,
        },
    )
    assert update_agent_status == 200
    assert update_agent_payload["config"]["config_version"] != agent_payload["config"]["config_version"]
    assert update_agent_payload["config"]["model_id"] == "qwen-local-dev-config-smoke"

    updated_versions_status, updated_versions_payload = session.fetch_json(f"{web_with_backend_base_url}/api/agent/config/versions")
    assert updated_versions_status == 200
    assert any(item["config_version"] == update_agent_payload["config"]["config_version"] for item in updated_versions_payload["items"])

    update_retrieval_status, update_retrieval_payload = session.fetch_json(
        f"{web_with_backend_base_url}/api/agent/retrieval-config",
        method="PUT",
        payload={
                "retrieval_strategy": "native_dense_v1",
            "embedding_model": "bge-m3-config-smoke",
            "top_k": 4,
            "rerank_enabled": False,
            "evidence_min_score": 0.15,
        },
    )
    assert update_retrieval_status == 200
    assert update_retrieval_payload["config"]["retrieval_config_version"] != retrieval_payload["config"]["retrieval_config_version"]
    assert update_retrieval_payload["config"]["top_k"] == 4


def test_web_browser_ui_smoke_provenance_and_citations(web_with_backend_base_url: str) -> None:
    env = os.environ.copy()
    env["WEB_BASE_URL"] = web_with_backend_base_url
    env.setdefault("PLAYWRIGHT_BROWSER_CHANNEL", "chrome")

    result = subprocess.run(
        ["npm", "run", "ui-smoke", "--", "e2e/chat-provenance-ui.spec.ts"],
        cwd=WEB_ROOT,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=180,
        check=False,
    )

    assert result.returncode == 0, result.stdout


def test_web_proxy_auth_role_gate(web_with_backend_base_url: str) -> None:
    unauth_status, unauth_payload = fetch_json(f"{web_with_backend_base_url}/api/chat/conversations")
    assert unauth_status == 401
    assert unauth_payload["error"]["code"] == "unauthenticated"

    user_session = login_session(web_with_backend_base_url, username="user", password="user123")
    forbidden_status, forbidden_payload = user_session.fetch_json(f"{web_with_backend_base_url}/api/kb/files")
    assert forbidden_status == 403
    assert forbidden_payload["error"]["code"] == "knowledge_admin_required"

    config_forbidden_status, config_forbidden_payload = user_session.fetch_json(f"{web_with_backend_base_url}/api/agent/config")
    assert config_forbidden_status == 403
    assert config_forbidden_payload["error"]["code"] == "system_admin_required"

    knowledge_session = login_session(web_with_backend_base_url, username="knowledge_admin", password="knowledge_admin123")
    knowledge_status, _ = knowledge_session.fetch_json(f"{web_with_backend_base_url}/api/kb/files")
    assert knowledge_status == 200
    knowledge_config_status, knowledge_config_payload = knowledge_session.fetch_json(f"{web_with_backend_base_url}/api/agent/config")
    assert knowledge_config_status == 403
    assert knowledge_config_payload["error"]["code"] == "system_admin_required"
    knowledge_publish_status, knowledge_publish_payload = knowledge_session.fetch_json(
        f"{web_with_backend_base_url}/api/kb/revisions/rev_missing/publish",
        method="POST",
        headers={"Idempotency-Key": "knowledge-cannot-publish"},
    )
    assert knowledge_publish_status == 403
    assert knowledge_publish_payload["error"]["code"] == "system_admin_required"
