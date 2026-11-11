from pathlib import Path
from collections.abc import Iterator

import pytest
from fastapi.testclient import TestClient

from app.adapters.model_client import ModelClientResult
from app.api.schemas import RetrievedSource
from app.main import build_runtime_readiness_check, create_app, validate_runtime_profile
from app.pipelines.retrieval_pipeline import build_default_corpus_documents
from app.parsers.common import count_tokens


class DeterministicTestModelClient:
    def generate(
        self,
        *,
        model_id: str,
        messages: list[dict[str, str]],
        user_query: str,
        citations: list[RetrievedSource],
        **_: object,
    ) -> ModelClientResult:
        source = max(
            citations,
            key=lambda item: (any(char.isdigit() for char in item.snippet), len(item.snippet)),
        )
        content = f"根据测试资料，{source.snippet.rstrip('。')}。[[cite:{source.rank}]]"
        prompt_tokens = sum(count_tokens(message.get("content", "")) for message in messages)
        completion_tokens = count_tokens(content)
        return ModelClientResult(
            content=content,
            finish_reason="stop",
            prompt_tokens=prompt_tokens,
            completion_tokens=completion_tokens,
            total_tokens=prompt_tokens + completion_tokens,
        )

    def stream_generate(self, **kwargs: object) -> Iterator[str]:
        result = self.generate(**kwargs)
        for start in range(0, len(result.content), 12):
            yield result.content[start : start + 12]


client = TestClient(create_app(model_client_override=DeterministicTestModelClient(), corpus_documents_override=build_default_corpus_documents()))
SAMPLE_DOCS = Path(__file__).resolve().parents[3] / "data" / "sample-docs"
PREVIEW_CASES = [
    ("txt", "employee-handbook.txt", "block_label", "paragraph_count"),
    ("md", "employee-handbook.md", "heading_path", "heading_count"),
    ("json", "policy-catalog.json", "json_path", "entry_count"),
    ("pdf", "sample-policy.pdf", "page_no", "page_count"),
    ("docx", "sample-handbook.docx", "heading_path", "table_count"),
    ("xlsx", "sample-budget.xlsx", "sheet_name", "sheet_count"),
    ("pptx", "sample-training-deck.pptx", "slide_no", "slide_count"),
]


def test_healthcheck() -> None:
    response = client.get("/healthz")

    assert response.status_code == 200
    payload = response.json()
    assert payload["status"] == "ok"
    assert payload["service"] == "ai-runtime"


def test_fullstack_profile_requires_explicit_object_store_credentials(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("SEE_AGENT_PROFILE", "fullstack_dev")
    monkeypatch.setenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN", "r" * 32)
    monkeypatch.delenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS", raising=False)
    monkeypatch.setenv("QDRANT_URL", "http://localhost:6333")
    monkeypatch.setenv("QDRANT_COLLECTION", "see-agent")
    monkeypatch.setenv("RUNTIME_RETRIEVAL_STRATEGY", "lexical_compat_v1")
    monkeypatch.setenv("LOCAL_LLM_BASE_URL", "http://localhost:11434/v1")
    monkeypatch.setenv("LOCAL_LLM_DEFAULT_MODEL_ID", "test-model")
    monkeypatch.setenv("OBJECT_STORE_ENDPOINT", "http://localhost:9000")
    monkeypatch.setenv("OBJECT_STORE_BUCKET", "knowledge-docs")
    monkeypatch.delenv("OBJECT_STORE_ACCESS_KEY", raising=False)
    monkeypatch.delenv("OBJECT_STORE_SECRET_KEY", raising=False)

    with pytest.raises(ValueError, match="OBJECT_STORE_ACCESS_KEY"):
        validate_runtime_profile()


@pytest.mark.parametrize(
    ("current", "previous", "error"),
    [
        ("", "", "SEE_AGENT_RUNTIME_SERVICE_TOKEN"),
        ("replace-with-local-dev-secret", "", "placeholder"),
        ("short-secret", "", "at least 32"),
        ("c" * 32, "short-secret", "PREVIOUS.*at least 32"),
        ("c" * 32, "c" * 32, "PREVIOUS must differ"),
    ],
)
def test_fullstack_profile_rejects_invalid_runtime_service_tokens(
    monkeypatch: pytest.MonkeyPatch, current: str, previous: str, error: str
) -> None:
    _set_valid_runtime_fullstack_environment(monkeypatch)
    monkeypatch.setenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN", current)
    monkeypatch.setenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS", previous)

    with pytest.raises(ValueError, match=error):
        validate_runtime_profile()


def test_fullstack_profile_accepts_valid_runtime_service_token_rotation(monkeypatch: pytest.MonkeyPatch) -> None:
    _set_valid_runtime_fullstack_environment(monkeypatch)
    monkeypatch.setenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN", "c" * 32)
    monkeypatch.setenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS", "p" * 32)

    validate_runtime_profile()


def _set_valid_runtime_fullstack_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    for key, value in {
        "SEE_AGENT_PROFILE": "fullstack_dev",
        "SEE_AGENT_RUNTIME_SERVICE_TOKEN": "c" * 32,
        "QDRANT_URL": "http://localhost:6333",
        "QDRANT_COLLECTION": "see_agent_dense_qwen_chunks_v1",
        "RUNTIME_RETRIEVAL_STRATEGY": "native_dense_v1",
        "LOCAL_LLM_BASE_URL": "http://localhost:11434/v1",
        "LOCAL_LLM_DEFAULT_MODEL_ID": "test-model",
        "OBJECT_STORE_ENDPOINT": "http://localhost:9000",
        "OBJECT_STORE_BUCKET": "knowledge-docs",
        "OBJECT_STORE_ACCESS_KEY": "test-access",
        "OBJECT_STORE_SECRET_KEY": "test-secret",
    }.items():
        monkeypatch.setenv(key, value)
    monkeypatch.delenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS", raising=False)


def test_fullstack_readiness_reports_missing_dependency_configuration(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("SEE_AGENT_PROFILE", "fullstack_dev")
    monkeypatch.delenv("QDRANT_URL", raising=False)

    ready, reason = build_runtime_readiness_check()()

    assert ready is False
    assert reason == "qdrant configuration is missing"


def test_chat_run_contract_shape() -> None:
    response = client.post(
        "/v1/chat/run",
        json={
            "request_id": "req_123",
            "conversation_id": "conv_123",
            "message_id": "msg_123",
            "user_query": "差旅报销需要几天内提交？",
            "model_id": "qwen-local",
            "config_version": "cfg_001",
            "retrieval_config_version": "rcfg_001",
                "knowledge_snapshot_id": "snap_001",
                "knowledge_scope": {"snapshot_id": "snap_001", "manifest_hash": "sha256:test", "revision_ids": ["rev_test_fixture"], "resolved_at": "2026-07-24T00:00:00Z"},
                    "response_overrides": {"retrieval_strategy": "native_dense_v1", "evidence_min_score": 0.15},
            "stream": False,
            "source_filters": {
                "source_formats": ["txt"]
            },
            "messages": [
                {
                    "message_id": "msg_history_123",
                    "role": "user",
                    "content": "差旅报销需要几天内提交？"
                }
            ]
        },
    )

    assert response.status_code == 200
    payload = response.json()
    assert payload["request_id"] == "req_123"
    assert payload["assistant_message"]["role"] == "assistant"
    assert payload["assistant_message"]["content"]
    assert "10" in payload["assistant_message"]["content"]
    assert payload["citations"][0]["source_locator"]["source_format"] == "txt"
    assert payload["refusal"] is False


def test_chat_stream_returns_delta_and_completed_events() -> None:
    response = client.post(
        "/v1/chat/stream",
        json={
            "request_id": "req_stream_123",
            "conversation_id": "conv_stream_123",
            "message_id": "msg_stream_123",
            "user_query": "差旅报销需要几天内提交？",
            "model_id": "qwen-local",
            "config_version": "cfg_001",
            "retrieval_config_version": "rcfg_001",
                "knowledge_snapshot_id": "snap_001",
                "knowledge_scope": {"snapshot_id": "snap_001", "manifest_hash": "sha256:test", "revision_ids": ["rev_test_fixture"], "resolved_at": "2026-07-24T00:00:00Z"},
                    "response_overrides": {"retrieval_strategy": "native_dense_v1", "evidence_min_score": 0.15},
            "stream": True,
            "source_filters": {
                "source_formats": ["txt"]
            },
            "messages": [],
        },
    )

    assert response.status_code == 200
    assert response.headers["content-type"].startswith("text/event-stream")
    body = response.text
    assert "event: message.delta" in body
    assert "event: message.completed" in body
    assert "chat_runtime_response" in body
    assert "10" in body


def test_chat_run_refusal_when_retrieval_misses() -> None:
    response = client.post(
        "/v1/chat/run",
        json={
            "request_id": "req_refusal_001",
            "conversation_id": "conv_refusal_001",
            "message_id": "msg_refusal_001",
            "user_query": "What is the launch date for the Mars colony program?",
            "model_id": "qwen-local",
            "config_version": "cfg_001",
            "retrieval_config_version": "rcfg_001",
                "knowledge_snapshot_id": "snap_001",
                "knowledge_scope": {"snapshot_id": "snap_001", "manifest_hash": "sha256:test", "revision_ids": ["rev_test_fixture"], "resolved_at": "2026-07-24T00:00:00Z"},
                    "response_overrides": {"retrieval_strategy": "native_dense_v1", "evidence_min_score": 0.15},
            "stream": False,
            "messages": [
                {
                    "message_id": "msg_history_refusal_001",
                    "role": "user",
                    "content": "What is the launch date for the Mars colony program?"
                }
            ]
        },
    )

    assert response.status_code == 200
    payload = response.json()
    assert payload["refusal"] is True
    assert payload["finish_reason"] == "refusal"
    assert "证据不足" in payload["assistant_message"]["content"]


@pytest.mark.parametrize(
    ("source_format", "filename", "locator_field", "metadata_field"),
    PREVIEW_CASES,
)
def test_ingestion_preview_returns_format_metadata(
    source_format: str,
    filename: str,
    locator_field: str,
    metadata_field: str,
) -> None:
    response = client.post(
        "/v1/ingestion/preview",
        json={
            "file_id": f"file_{source_format}",
            "source_format": source_format,
            "object_uri": str(SAMPLE_DOCS / filename),
        },
    )

    assert response.status_code == 200
    payload = response.json()
    assert payload["file_id"] == f"file_{source_format}"
    assert payload["supported"] is True
    assert locator_field in payload["locator_fields"]
    assert metadata_field in payload["preview_metadata"]


@pytest.mark.parametrize(("source_format", "filename"), [(case[0], case[1]) for case in PREVIEW_CASES])
def test_ingestion_run_contract_shape(source_format: str, filename: str) -> None:
    response = client.post(
        "/v1/ingestion/run",
        json={
            "request_id": f"req_ingest_{source_format}",
            "task_id": f"task_{source_format}",
            "file_id": f"file_{source_format}",
            "filename": filename,
            "mime_type": "application/octet-stream",
            "source_format": source_format,
            "object_uri": str(SAMPLE_DOCS / filename),
            "parse_version": "parse_v1",
            "index_version": "index_v1",
            "requested_at": "2026-06-20T00:00:00Z",
        },
    )

    assert response.status_code == 200
    payload = response.json()
    assert payload["task_id"] == f"task_{source_format}"
    assert payload["status"] == "succeeded"
    assert payload["chunks_created"] > 0


def test_ingested_file_is_available_to_chat_retrieval(tmp_path: Path) -> None:
    uploaded_doc = tmp_path / "zeta-vault.txt"
    uploaded_doc.write_text(
        "Zeta Vault Operations\n\n"
        "The Zeta vault emergency drill passcode is 73491. "
        "Only the incident commander may rotate this passcode.",
        encoding="utf-8",
    )

    ingest_response = client.post(
        "/v1/ingestion/run",
        json={
            "request_id": "req_ingest_zeta_vault",
            "task_id": "task_zeta_vault",
                "file_id": "file_zeta_vault",
                "document_id": "doc_zeta_vault",
                "revision_id": "rev_zeta_vault",
                "delivery_id": "delivery_zeta_vault",
                "checksum_sha256": "sha256:zeta-vault",
            "filename": uploaded_doc.name,
            "mime_type": "text/plain",
            "source_format": "txt",
            "object_uri": str(uploaded_doc),
            "parse_version": "parse_v1",
            "index_version": "index_v1",
            "requested_at": "2026-07-02T00:00:00Z",
        },
    )

    assert ingest_response.status_code == 200
    ingest_payload = ingest_response.json()
    assert ingest_payload["status"] == "succeeded"
    assert ingest_payload["chunks_created"] >= 1

    chat_response = client.post(
        "/v1/chat/run",
        json={
            "request_id": "req_chat_zeta_vault",
            "conversation_id": "conv_zeta_vault",
            "message_id": "msg_zeta_vault",
            "user_query": "What is the Zeta vault emergency drill passcode?",
            "model_id": "qwen-local",
            "config_version": "cfg_001",
            "retrieval_config_version": "rcfg_001",
                "knowledge_snapshot_id": "snapshot_active",
                    "knowledge_scope": {"snapshot_id": "snapshot_active", "manifest_hash": "sha256:test", "revision_ids": ["rev_zeta_vault"], "resolved_at": "2026-07-24T00:00:00Z"},
                    "response_overrides": {"retrieval_strategy": "native_dense_v1", "evidence_min_score": 0.15},
            "stream": False,
            "source_filters": {
                "file_ids": ["file_zeta_vault"]
            },
            "messages": [],
        },
    )

    assert chat_response.status_code == 200
    chat_payload = chat_response.json()
    assert chat_payload["refusal"] is False
    assert "73491" in chat_payload["assistant_message"]["content"]
    assert chat_payload["citations"][0]["file_id"] == "file_zeta_vault"


def test_ingestion_run_returns_failed_error_summary_for_invalid_docx(tmp_path: Path) -> None:
    broken_docx = tmp_path / "broken.docx"
    broken_docx.write_bytes(b"not-a-valid-docx-archive")

    response = client.post(
        "/v1/ingestion/run",
        json={
            "request_id": "req_ingest_broken_docx",
            "task_id": "task_broken_docx",
            "file_id": "file_broken_docx",
            "filename": "broken.docx",
            "mime_type": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
            "source_format": "docx",
            "object_uri": str(broken_docx),
            "parse_version": "parse_v1",
            "index_version": "index_v1",
            "requested_at": "2026-06-22T00:00:00Z",
        },
    )

    assert response.status_code == 200
    payload = response.json()
    assert payload["status"] == "failed"
    assert payload["error"]["code"] == "unexpected_parse_failure"
