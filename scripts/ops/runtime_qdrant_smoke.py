from __future__ import annotations

import importlib
import os
import sys
import tempfile
import uuid
from pathlib import Path

import httpx
from fastapi.testclient import TestClient


ROOT = Path(__file__).resolve().parents[2]
RUNTIME_ROOT = ROOT / "services" / "ai-runtime-py"
sys.path.insert(0, str(RUNTIME_ROOT))


def main() -> int:
    qdrant_url = os.environ.get("QDRANT_URL", "http://127.0.0.1:16333").rstrip("/")
    collection_name = os.environ.get("QDRANT_COLLECTION") or f"see_agent_smoke_{uuid.uuid4().hex}"
    os.environ["QDRANT_URL"] = qdrant_url
    os.environ["QDRANT_COLLECTION"] = collection_name

    cleanup_collection(qdrant_url, collection_name)
    try:
        runtime_main = importlib.import_module("app.main")
        client = TestClient(runtime_main.app)

        with tempfile.TemporaryDirectory() as tmp_dir:
            uploaded_doc = Path(tmp_dir) / "qdrant-vault.txt"
            uploaded_doc.write_text(
                "Qdrant Vault Operations\n\n"
                "The Qdrant vault restart verification passcode is 91827.",
                encoding="utf-8",
            )
            ingest_response = client.post(
                "/v1/ingestion/run",
                json={
                    "request_id": "req_qdrant_smoke_ingest",
                    "task_id": "task_qdrant_smoke_ingest",
                    "file_id": "file_qdrant_smoke",
                    "filename": uploaded_doc.name,
                    "mime_type": "text/plain",
                    "source_format": "txt",
                    "object_uri": str(uploaded_doc),
                    "parse_version": "parse_v1",
                    "index_version": "index_qdrant_smoke",
                    "requested_at": "2026-07-02T00:00:00Z",
                },
            )
            ingest_response.raise_for_status()
            assert ingest_response.json()["status"] == "succeeded"

            reloaded_main = importlib.reload(runtime_main)
            reloaded_client = TestClient(reloaded_main.app)
            chat_response = reloaded_client.post(
                "/v1/chat/run",
                json={
                    "request_id": "req_qdrant_smoke_chat",
                    "conversation_id": "conv_qdrant_smoke",
                    "message_id": "msg_qdrant_smoke",
                    "user_query": "What is the Qdrant vault restart verification passcode?",
                    "model_id": "qwen-local",
                    "config_version": "cfg_001",
                    "retrieval_config_version": "rcfg_001",
                    "knowledge_snapshot_id": "snapshot_active",
                    "stream": False,
                    "source_filters": {
                        "file_ids": ["file_qdrant_smoke"],
                    },
                    "messages": [],
                },
            )
            chat_response.raise_for_status()
            payload = chat_response.json()
            assert payload["refusal"] is False
            assert "91827" in payload["assistant_message"]["content"]
            assert payload["citations"][0]["file_id"] == "file_qdrant_smoke"

        print(f"runtime qdrant smoke ok: collection={collection_name}")
        return 0
    finally:
        cleanup_collection(qdrant_url, collection_name)


def cleanup_collection(qdrant_url: str, collection_name: str) -> None:
    try:
        httpx.delete(f"{qdrant_url}/collections/{collection_name}", timeout=5.0, trust_env=False)
    except httpx.HTTPError:
        pass


if __name__ == "__main__":
    raise SystemExit(main())
