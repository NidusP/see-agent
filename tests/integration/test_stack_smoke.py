from __future__ import annotations

import os
import sys
import time
from pathlib import Path

import pytest

from tests.common.server_utils import HttpSession, fetch_json, find_free_port, login_session, running_process


WORKSPACE_ROOT = Path(__file__).resolve().parents[2]
BACKEND_ROOT = WORKSPACE_ROOT / "services" / "backend-go"
RUNTIME_ROOT = WORKSPACE_ROOT / "services" / "ai-runtime-py"
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
    upload_dir = tmp_path_factory.mktemp("backend-uploads")
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


def wait_for_ingestion_terminal_state(
    backend_base_url: str,
    session: HttpSession,
    file_id: str,
    *,
    timeout_seconds: float = 20.0,
) -> tuple[dict[str, object], set[str]]:
    deadline = time.time() + timeout_seconds
    observed_statuses: set[str] = set()

    while time.time() < deadline:
        detail_status, detail_payload = session.fetch_json(f"{backend_base_url}/api/kb/files/{file_id}")
        assert detail_status == 200
        latest_task = detail_payload["latest_task"]
        status = latest_task["status"]
        observed_statuses.add(status)
        if status in {"succeeded", "failed"}:
            return detail_payload, observed_statuses
        time.sleep(0.25)

    raise AssertionError(f"Timed out waiting for file {file_id} to reach terminal ingestion state")


def test_runtime_preview_and_run_smoke(runtime_base_url: str) -> None:
    preview_status, preview_payload = fetch_json(
        f"{runtime_base_url}/v1/ingestion/preview",
        method="POST",
        payload={
            "file_id": "file_md_preview",
            "source_format": "md",
            "object_uri": str(SAMPLE_DOCS / "employee-handbook.md"),
        },
    )

    assert preview_status == 200
    assert preview_payload["parser_strategy"] == "markdown_parser"
    assert "heading_path" in preview_payload["locator_fields"]
    assert preview_payload["preview_metadata"]["heading_count"] >= 1

    run_status, run_payload = fetch_json(
        f"{runtime_base_url}/v1/ingestion/run",
        method="POST",
        payload={
            "request_id": "req_md_run",
            "task_id": "task_md_run",
            "file_id": "file_md_run",
            "filename": "employee-handbook.md",
            "mime_type": "text/markdown",
            "source_format": "md",
            "object_uri": str(SAMPLE_DOCS / "employee-handbook.md"),
            "parse_version": "parse_v1",
            "index_version": "index_v1",
            "requested_at": "2026-06-20T00:00:00Z",
        },
    )

    assert run_status == 200
    assert run_payload["status"] == "succeeded"
    assert run_payload["chunks_created"] >= 1


def test_backend_chat_without_immutable_scope_fails_closed(backend_base_url: str) -> None:
    session = login_session(backend_base_url)
    create_status, create_payload = session.fetch_json(
        f"{backend_base_url}/api/chat/conversations",
        method="POST",
        payload={"title": "Smoke chat"},
    )
    assert create_status == 201
    conversation_id = create_payload["conversation"]["conversation_id"]

    message_status, message_payload = session.fetch_json(
        f"{backend_base_url}/api/chat/conversations/{conversation_id}/messages",
        method="POST",
        payload={
            "content": "差旅报销需要在多久内提交？",
            "stream": False,
        },
    )
    assert message_status == 409
    assert message_payload["error"]["code"] == "knowledge_snapshot_unavailable"
    assert message_payload["error"]["message"] == "a published knowledge snapshot is not available yet"


def test_backend_chat_stream_without_immutable_scope_fails_closed(backend_base_url: str) -> None:
    session = login_session(backend_base_url)
    create_status, create_payload = session.fetch_json(
        f"{backend_base_url}/api/chat/conversations",
        method="POST",
        payload={"title": "Smoke stream chat"},
    )
    assert create_status == 201
    conversation_id = create_payload["conversation"]["conversation_id"]

    stream_status, stream_body = session.fetch_text(
        f"{backend_base_url}/api/chat/conversations/{conversation_id}/messages",
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


def test_backend_upload_detail_chunks_and_reindex_smoke(backend_base_url: str) -> None:
    session = login_session(backend_base_url)
    upload_status, upload_payload = session.fetch_multipart_json(
        f"{backend_base_url}/api/kb/files",
        fields={"force_reindex": "true"},
        files={
            "file": (
                "sample-policy.pdf",
                (SAMPLE_DOCS / "sample-policy.pdf").read_bytes(),
                "application/pdf",
            )
        },
    )

    assert upload_status == 202
    file_id = upload_payload["file"]["file_id"]
    assert upload_payload["file"]["source_format"] == "pdf"
    assert upload_payload["ingestion_task"]["status"] == "queued"

    detail_payload, observed_statuses = wait_for_ingestion_terminal_state(backend_base_url, session, file_id)
    assert detail_payload["latest_task"]["task_id"] == upload_payload["ingestion_task"]["task_id"]
    assert detail_payload["latest_task"]["status"] == "succeeded"
    assert detail_payload["file"]["status"] == "succeeded"
    assert observed_statuses <= {"running", "succeeded"}

    chunk_status, chunk_payload = session.fetch_json(f"{backend_base_url}/api/kb/files/{file_id}/chunks")
    assert chunk_status == 200
    assert chunk_payload["items"][0]["source_locator"]["source_format"] == "pdf"

    reindex_status, reindex_payload = session.fetch_json(
        f"{backend_base_url}/api/kb/files/{file_id}/reindex",
        method="POST",
        payload={"force_reindex": True},
    )
    assert reindex_status == 202
    assert reindex_payload["ingestion_task"]["task_id"] != upload_payload["ingestion_task"]["task_id"]
    reindexed_detail, _ = wait_for_ingestion_terminal_state(backend_base_url, session, file_id)
    assert reindexed_detail["latest_task"]["task_id"] == reindex_payload["ingestion_task"]["task_id"]
    assert reindexed_detail["latest_task"]["status"] == "succeeded"
    assert reindexed_detail["file"]["parse_version"] == "parse_v2"
    assert reindexed_detail["file"]["index_version"] == "index_v2"


def test_uploaded_file_is_not_chat_retrievable_before_snapshot_publication(backend_base_url: str) -> None:
    session = login_session(backend_base_url)
    upload_status, upload_payload = session.fetch_multipart_json(
        f"{backend_base_url}/api/kb/files",
        fields={"force_reindex": "false"},
        files={
            "file": (
                "zeta-vault-runbook.txt",
                (
                    "Zeta Vault Operations\n\n"
                    "The Zeta vault emergency drill passcode is 73491. "
                    "Only the incident commander may rotate this passcode."
                ).encode("utf-8"),
                "text/plain",
            )
        },
    )

    assert upload_status == 202
    file_id = upload_payload["file"]["file_id"]
    detail_payload, _ = wait_for_ingestion_terminal_state(backend_base_url, session, file_id)
    assert detail_payload["file"]["status"] == "succeeded"

    create_status, create_payload = session.fetch_json(
        f"{backend_base_url}/api/chat/conversations",
        method="POST",
        payload={"title": "Uploaded file retrieval"},
    )
    assert create_status == 201
    conversation_id = create_payload["conversation"]["conversation_id"]

    message_status, message_payload = session.fetch_json(
        f"{backend_base_url}/api/chat/conversations/{conversation_id}/messages",
        method="POST",
        payload={
            "content": "What is the Zeta vault emergency drill passcode?",
            "stream": False,
        },
    )
    assert message_status == 409
    assert message_payload["error"]["code"] == "knowledge_snapshot_unavailable"
    assert message_payload["error"]["message"] == "a published knowledge snapshot is not available yet"


def test_backend_ingestion_failure_path_returns_error_category(backend_base_url: str) -> None:
    session = login_session(backend_base_url)
    upload_status, upload_payload = session.fetch_multipart_json(
        f"{backend_base_url}/api/kb/files",
        files={
            "file": (
                "broken.docx",
                b"not-a-valid-docx-archive",
                "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
            )
        },
    )

    assert upload_status == 202
    assert upload_payload["ingestion_task"]["status"] == "queued"

    file_id = upload_payload["file"]["file_id"]
    detail_payload, observed_statuses = wait_for_ingestion_terminal_state(backend_base_url, session, file_id)

    assert detail_payload["file"]["status"] == "failed"
    assert detail_payload["latest_task"]["status"] == "failed"
    assert observed_statuses <= {"running", "failed"}
    assert detail_payload["latest_task"]["error"]["code"] in {"parse_failure", "unexpected_parse_failure"}
    assert detail_payload["latest_task"]["error"]["message"]


def test_backend_auth_role_gate_smoke(backend_base_url: str) -> None:
    unauth_status, unauth_payload = fetch_json(f"{backend_base_url}/api/chat/conversations")
    assert unauth_status == 401
    assert unauth_payload["error"]["code"] == "unauthenticated"

    user_session = login_session(backend_base_url, username="user", password="user123")
    forbidden_status, forbidden_payload = user_session.fetch_json(f"{backend_base_url}/api/kb/files")
    assert forbidden_status == 403
    assert forbidden_payload["error"]["code"] == "knowledge_admin_required"

    knowledge_session = login_session(backend_base_url, username="knowledge_admin", password="knowledge_admin123")
    knowledge_status, _ = knowledge_session.fetch_json(f"{backend_base_url}/api/kb/files")
    assert knowledge_status == 200
    knowledge_config_status, knowledge_config_payload = knowledge_session.fetch_json(f"{backend_base_url}/api/agent/config")
    assert knowledge_config_status == 403
    assert knowledge_config_payload["error"]["code"] == "system_admin_required"

    admin_session = login_session(backend_base_url)
    me_status, me_payload = admin_session.fetch_json(f"{backend_base_url}/api/auth/me")
    assert me_status == 200
    assert me_payload["user"]["role"] == "system_admin"
