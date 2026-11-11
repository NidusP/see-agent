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

from app.adapters.model_client import ModelClientResult
from app.api.schemas import RetrievedSource
from app.parsers.common import count_tokens


class DeterministicCitationTestModel:
    """Test-only generator used to isolate real dense projection/retrieval.

    The smoke's assertion is about the configured embedding provider and Qdrant
    collection.  It must not treat an arbitrary local LLM's citation-marker
    formatting as evidence that dense retrieval failed.
    """

    provider_name = "test_injected"

    def generate(
        self,
        *,
        messages: list[dict[str, str]],
        citations: list[RetrievedSource],
        **_: object,
    ) -> ModelClientResult:
        source = citations[0]
        content = f"{source.snippet.rstrip('。')}。[[cite:{source.rank}]]"
        prompt_tokens = sum(count_tokens(message.get("content", "")) for message in messages)
        completion_tokens = count_tokens(content)
        return ModelClientResult(
            content=content,
            finish_reason="stop",
            prompt_tokens=prompt_tokens,
            completion_tokens=completion_tokens,
            total_tokens=prompt_tokens + completion_tokens,
        )

    def stream_generate(self, **kwargs: object):
        yield self.generate(**kwargs).content


def main() -> int:
    qdrant_url = os.environ.get("QDRANT_URL", "http://127.0.0.1:16333").rstrip("/")
    embedding_base_url = os.environ.get("EMBEDDING_BASE_URL", "").strip()
    embedding_model = os.environ.get("EMBEDDING_MODEL", "").strip()
    dimensions = os.environ.get("EMBEDDING_DIMENSIONS", "").strip()
    service_token = os.environ.get("SEE_AGENT_RUNTIME_SERVICE_TOKEN", "").strip()
    generation_model = os.environ.get("LOCAL_LLM_DEFAULT_MODEL_ID", "").strip() or "qwen3:14b"
    if not embedding_base_url or not embedding_model or not dimensions:
        raise SystemExit("EMBEDDING_BASE_URL, EMBEDDING_MODEL, and EMBEDDING_DIMENSIONS are required")
    collection_name = os.environ.get("QDRANT_COLLECTION") or f"see_agent_dense_smoke_{uuid.uuid4().hex}"
    os.environ["RUNTIME_RETRIEVAL_STRATEGY"] = "native_dense_v1"
    os.environ.pop("RUNTIME_RETRIEVAL_MODE", None)
    os.environ["QDRANT_URL"] = qdrant_url
    os.environ["QDRANT_COLLECTION"] = collection_name

    cleanup_collection(qdrant_url, collection_name)
    try:
        runtime_main = importlib.import_module("app.main")
        client = TestClient(runtime_main.create_app(model_client_override=DeterministicCitationTestModel()))
        headers = {"Authorization": f"Bearer {service_token}"} if service_token else {}

        with tempfile.TemporaryDirectory() as tmp_dir:
            uploaded_doc = Path(tmp_dir) / "dense-vault.txt"
            uploaded_doc.write_text(
                "Dense Vault Operations\n\n"
                "The dense vault restart verification passcode is 63918.",
                encoding="utf-8",
            )
            ingest_response = client.post(
                "/v1/ingestion/run",
                headers=headers,
                json={
                    "request_id": "req_dense_smoke_ingest",
                    "task_id": "task_dense_smoke_ingest",
                    "file_id": "file_dense_smoke",
                    "document_id": "doc_dense_smoke",
                    "revision_id": "rev_dense_smoke",
                    "delivery_id": "delivery_dense_smoke",
                    "checksum_sha256": "sha256:dense-smoke-source",
                    "attempt": 1,
                    "filename": uploaded_doc.name,
                    "mime_type": "text/plain",
                    "source_format": "txt",
                    "object_uri": str(uploaded_doc),
                    "parse_version": "parse_v1",
                    "index_version": "index_dense_smoke",
                    "requested_at": "2026-07-16T00:00:00Z",
                },
            )
            ingest_response.raise_for_status()
            assert ingest_response.json()["status"] == "succeeded"
            assert ingest_response.json()["embedding_model"] == embedding_model

            reloaded_main = importlib.reload(runtime_main)
            reloaded_client = TestClient(reloaded_main.create_app(model_client_override=DeterministicCitationTestModel()))
            chat_response = reloaded_client.post(
                "/v1/chat/run",
                headers=headers,
                json={
                    "request_id": "req_dense_smoke_chat",
                    "conversation_id": "conv_dense_smoke",
                    "message_id": "msg_dense_smoke",
                    "user_query": "What is the dense vault restart verification passcode?",
                    "model_id": generation_model,
                    "config_version": "cfg_001",
                    "retrieval_config_version": "rcfg_dense_smoke",
                    "knowledge_snapshot_id": "snap_dense_smoke",
                    "knowledge_scope": {
                        "snapshot_id": "snap_dense_smoke",
                        "manifest_hash": "sha256:dense-smoke-manifest",
                        "revision_ids": ["rev_dense_smoke"],
                        "resolved_at": "2026-07-16T00:00:00Z",
                    },
                    "stream": False,
                    "messages": [],
                    "response_overrides": {
                        "retrieval_strategy": "native_dense_v1",
                        "embedding_model": embedding_model,
                        "top_k": 3,
                        "rerank_enabled": False,
                        "evidence_min_score": 0.15,
                    },
                },
            )
            chat_response.raise_for_status()
            payload = chat_response.json()
            assert payload["refusal"] is False
            assert "63918" in payload["assistant_message"]["content"]
            assert payload["citations"][0]["revision_id"] == "rev_dense_smoke"
            assert payload["citations"][0]["document_id"] == "doc_dense_smoke"

        print(
            "runtime native dense smoke ok: "
            f"collection={collection_name} embedding_model={embedding_model} dimensions={dimensions} generation=test_injected"
        )
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
