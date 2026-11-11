from __future__ import annotations

import os
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from tests.common.server_utils import fetch_text, find_free_port, login_session, running_process


BACKEND_ROOT = ROOT / "services" / "backend-go"
RUNTIME_ROOT = ROOT / "services" / "ai-runtime-py"


def main() -> int:
    endpoint = os.environ.get("OBJECT_STORE_ENDPOINT", "http://127.0.0.1:19000").rstrip("/")
    bucket = os.environ.get("OBJECT_STORE_BUCKET", "knowledge-docs")
    access_key = os.environ.get("OBJECT_STORE_ACCESS_KEY", os.environ.get("MINIO_ROOT_USER", "see_agent"))
    secret_key = os.environ.get("OBJECT_STORE_SECRET_KEY", os.environ.get("MINIO_ROOT_PASSWORD", "see_agent_dev"))
    region = os.environ.get("OBJECT_STORE_REGION", "us-east-1")
    go_bin = os.environ.get("INTEGRATION_GO_BIN", os.environ.get("GO_BIN", "/Users/populus/.local/go1.26.4/bin/go"))
    python_bin = os.environ.get("INTEGRATION_PYTHON_BIN", sys.executable)

    status, body = fetch_text(f"{endpoint}/minio/health/live")
    if status != 200:
        raise RuntimeError(f"MinIO health check failed with status {status}: {body}")

    runtime_port = find_free_port()
    backend_port = find_free_port()
    runtime_base_url = f"http://127.0.0.1:{runtime_port}"
    backend_base_url = f"http://127.0.0.1:{backend_port}"
    object_env = {
        "OBJECT_STORE_ENDPOINT": endpoint,
        "OBJECT_STORE_BUCKET": bucket,
        "OBJECT_STORE_ACCESS_KEY": access_key,
        "OBJECT_STORE_SECRET_KEY": secret_key,
        "OBJECT_STORE_REGION": region,
        "OBJECT_STORE_TIMEOUT_SECONDS": "10",
        "SEE_AGENT_ADMIN_USERNAME": "admin",
        "SEE_AGENT_ADMIN_PASSWORD": "admin123",
        "SEE_AGENT_USER_USERNAME": "user",
        "SEE_AGENT_USER_PASSWORD": "user123",
    }

    with running_process(
        [python_bin, "-m", "uvicorn", "app.main:app", "--host", "127.0.0.1", "--port", str(runtime_port)],
        cwd=RUNTIME_ROOT,
        env=object_env,
        startup_url=f"{runtime_base_url}/healthz",
    ):
        with running_process(
            [go_bin, "run", "./cmd/api"],
            cwd=BACKEND_ROOT,
            env={
                **object_env,
                "BACKEND_ADDR": f"127.0.0.1:{backend_port}",
                "AI_RUNTIME_BASE_URL": runtime_base_url,
            },
            startup_url=f"{backend_base_url}/healthz",
        ):
            session = login_session(backend_base_url)
            upload_status, upload_payload = session.fetch_multipart_json(
                f"{backend_base_url}/api/kb/files",
                files={
                    "file": (
                        "minio-vault-runbook.txt",
                        (
                            "MinIO Vault Operations\n\n"
                            "The MinIO object store verification code is 58261. "
                            "Only administrators may rotate this code."
                        ).encode("utf-8"),
                        "text/plain",
                    )
                },
            )
            if upload_status != 202:
                raise RuntimeError(f"upload failed with status {upload_status}: {upload_payload}")

            object_uri = upload_payload["file"]["object_uri"]
            if not object_uri.startswith(f"s3://{bucket}/kb/"):
                raise RuntimeError(f"expected s3 object_uri, got {object_uri}")

            file_id = upload_payload["file"]["file_id"]
            detail_payload = _wait_for_ingestion(backend_base_url, session, file_id)
            if detail_payload["latest_task"]["status"] != "succeeded":
                raise RuntimeError(f"ingestion failed: {detail_payload}")

            create_status, create_payload = session.fetch_json(
                f"{backend_base_url}/api/chat/conversations",
                method="POST",
                payload={"title": "MinIO smoke"},
            )
            if create_status != 201:
                raise RuntimeError(f"create conversation failed: {create_status} {create_payload}")
            conversation_id = create_payload["conversation"]["conversation_id"]

            message_status, message_payload = session.fetch_json(
                f"{backend_base_url}/api/chat/conversations/{conversation_id}/messages",
                method="POST",
                payload={
                    "content": "What is the MinIO object store verification code?",
                    "stream": False,
                },
            )
            if message_status != 200:
                raise RuntimeError(f"chat failed: {message_status} {message_payload}")
            content = message_payload["assistant_message"]["content"]
            if "58261" not in content:
                raise RuntimeError(f"expected uploaded MinIO content to be retrievable, got: {content}")

    print(f"minio ingestion smoke ok: bucket={bucket} endpoint={endpoint}")
    return 0


def _wait_for_ingestion(backend_base_url: str, session, file_id: str) -> dict[str, object]:
    deadline = time.time() + 25
    while time.time() < deadline:
        status, payload = session.fetch_json(f"{backend_base_url}/api/kb/files/{file_id}")
        if status != 200:
            raise RuntimeError(f"detail failed with status {status}: {payload}")
        if payload["latest_task"]["status"] in {"succeeded", "failed"}:
            return payload
        time.sleep(0.25)
    raise RuntimeError(f"timed out waiting for ingestion: {file_id}")


if __name__ == "__main__":
    raise SystemExit(main())
