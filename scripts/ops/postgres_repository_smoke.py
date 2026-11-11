from __future__ import annotations

import os
import sys
import time
import uuid
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from tests.common.server_utils import find_free_port, login_session, running_process


BACKEND_ROOT = ROOT / "services" / "backend-go"


def main() -> int:
    database_url = os.environ.get(
        "DATABASE_URL",
        "postgres://see_agent:see_agent_dev@127.0.0.1:15432/see_agent?sslmode=disable",
    )
    go_bin = os.environ.get("INTEGRATION_GO_BIN", os.environ.get("GO_BIN", "/Users/populus/.local/go1.26.4/bin/go"))
    marker = uuid.uuid4().hex[:10]
    title = f"Postgres persistence smoke {marker}"

    first_base_url = _start_backend_and_seed(go_bin, database_url, title)
    persisted = _read_after_restart(go_bin, database_url, first_base_url["conversation_id"], first_base_url["assistant_message_id"], first_base_url["file_id"], title)

    print(
        "postgres repository smoke ok: "
        f"conversation={persisted['conversation_id']} file={persisted['file_id']}"
    )
    return 0


def _start_backend_and_seed(go_bin: str, database_url: str, title: str) -> dict[str, str]:
    port = find_free_port()
    base_url = f"http://127.0.0.1:{port}"
    env = _backend_env(database_url, port)
    with running_process(
        [go_bin, "run", "./cmd/api"],
        cwd=BACKEND_ROOT,
        env=env,
        startup_url=f"{base_url}/healthz",
        startup_timeout_seconds=60,
    ):
        session = login_session(base_url)
        create_status, create_payload = session.fetch_json(
            f"{base_url}/api/chat/conversations",
            method="POST",
            payload={"title": title},
        )
        if create_status != 201:
            raise RuntimeError(f"create conversation failed: {create_status} {create_payload}")
        conversation_id = create_payload["conversation"]["conversation_id"]

        message_status, message_payload = session.fetch_json(
            f"{base_url}/api/chat/conversations/{conversation_id}/messages",
            method="POST",
            payload={"content": "Store this in Postgres.", "stream": False},
        )
        if message_status != 200:
            raise RuntimeError(f"post message failed: {message_status} {message_payload}")
        assistant_message_id = message_payload["assistant_message"]["message_id"]

        upload_status, upload_payload = session.fetch_multipart_json(
            f"{base_url}/api/kb/files",
            files={
                "file": (
                    f"postgres-smoke-{uuid.uuid4().hex[:8]}.txt",
                    b"Postgres repository smoke upload body.",
                    "text/plain",
                )
            },
        )
        if upload_status != 202:
            raise RuntimeError(f"upload file failed: {upload_status} {upload_payload}")
        file_id = upload_payload["file"]["file_id"]

        # Give the async ingestion goroutine a short window to write any terminal status.
        time.sleep(0.5)
        return {
            "conversation_id": conversation_id,
            "assistant_message_id": assistant_message_id,
            "file_id": file_id,
        }


def _read_after_restart(go_bin: str, database_url: str, conversation_id: str, assistant_message_id: str, file_id: str, title: str) -> dict[str, str]:
    port = find_free_port()
    base_url = f"http://127.0.0.1:{port}"
    env = _backend_env(database_url, port)
    with running_process(
        [go_bin, "run", "./cmd/api"],
        cwd=BACKEND_ROOT,
        env=env,
        startup_url=f"{base_url}/healthz",
        startup_timeout_seconds=60,
    ):
        session = login_session(base_url)
        list_status, list_payload = session.fetch_json(f"{base_url}/api/chat/conversations")
        if list_status != 200:
            raise RuntimeError(f"list conversations failed: {list_status} {list_payload}")
        if not any(item["conversation_id"] == conversation_id and item["title"] == title for item in list_payload["items"]):
            raise RuntimeError(f"persisted conversation not found: {conversation_id}")

        detail_status, detail_payload = session.fetch_json(f"{base_url}/api/chat/conversations/{conversation_id}")
        if detail_status != 200:
            raise RuntimeError(f"conversation detail failed: {detail_status} {detail_payload}")
        if len(detail_payload["messages"]) < 2:
            raise RuntimeError(f"expected persisted messages, got {detail_payload}")

        sources_status, sources_payload = session.fetch_json(f"{base_url}/api/chat/conversations/{conversation_id}/sources/{assistant_message_id}")
        if sources_status != 200 or not sources_payload["items"]:
            raise RuntimeError(f"persisted sources missing: {sources_status} {sources_payload}")

        file_status, file_payload = session.fetch_json(f"{base_url}/api/kb/files/{file_id}")
        if file_status != 200:
            raise RuntimeError(f"persisted file missing: {file_status} {file_payload}")
        if file_payload["file"]["file_id"] != file_id:
            raise RuntimeError(f"unexpected persisted file payload: {file_payload}")

    return {"conversation_id": conversation_id, "file_id": file_id}


def _backend_env(database_url: str, port: int) -> dict[str, str]:
    return {
        "BACKEND_ADDR": f"127.0.0.1:{port}",
        "DATABASE_URL": database_url,
        "AI_RUNTIME_BASE_URL": "",
        "SEE_AGENT_ADMIN_USERNAME": "admin",
        "SEE_AGENT_ADMIN_PASSWORD": "admin123",
        "SEE_AGENT_USER_USERNAME": "user",
        "SEE_AGENT_USER_PASSWORD": "user123",
    }


if __name__ == "__main__":
    raise SystemExit(main())
