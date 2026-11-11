import json
from pathlib import Path

import pytest
from pydantic import ValidationError

from app.api.schemas import ChatRequest, ChatResponse, IngestionRunRequest, IngestionRunResponse


CONTRACT_FIXTURES = Path(__file__).resolve().parents[3] / "packages" / "contracts" / "fixtures"


def load_fixture(name: str) -> dict[str, object]:
    return json.loads((CONTRACT_FIXTURES / name).read_text(encoding="utf-8"))


def test_legacy_ingestion_task_remains_compatible() -> None:
    request = IngestionRunRequest.model_validate(load_fixture("legacy-ingestion-task.json"))

    assert request.revision_id is None


def test_revision_ingestion_task_requires_immutable_identity() -> None:
    request = IngestionRunRequest.model_validate(load_fixture("revision-ingestion-task.json"))

    assert request.document_id == "doc_travel_policy"
    assert request.revision_id == "rev_travel_policy_002"

    invalid = request.model_dump()
    invalid["checksum_sha256"] = None
    with pytest.raises(ValidationError, match="revision_id requires"):
        IngestionRunRequest.model_validate(invalid)


def test_snapshot_scoped_chat_and_revalidation_contracts_are_valid() -> None:
    scoped = ChatRequest.model_validate(load_fixture("snapshot-scoped-chat.json"))
    revalidation = ChatRequest.model_validate(load_fixture("revalidate-chat.json"))

    assert scoped.knowledge_scope is not None
    assert scoped.knowledge_scope.revision_ids == ["rev_travel_policy_002"]
    assert revalidation.revalidation_context is not None


def test_revalidation_and_scope_mismatch_are_rejected() -> None:
    invalid = load_fixture("snapshot-scoped-chat.json")
    invalid["answer_mode"] = "revalidate"
    with pytest.raises(ValidationError, match="revalidation_context is required"):
        ChatRequest.model_validate(invalid)

    invalid = load_fixture("snapshot-scoped-chat.json")
    invalid["knowledge_scope"] = {**invalid["knowledge_scope"], "snapshot_id": "snap_other"}
    with pytest.raises(ValidationError, match="knowledge_scope.snapshot_id"):
        ChatRequest.model_validate(invalid)


def test_legacy_performed_retrieval_without_applied_configuration_is_not_a_runtime_response() -> None:
    with pytest.raises(ValidationError, match="performed retrieval requires"):
        ChatResponse.model_validate(load_fixture("revision-provenance-response.json"))


def test_successful_revision_ingestion_result_contract_is_valid() -> None:
    result = IngestionRunResponse.model_validate(
        {
            "request_id": "req_revision_001",
            "task_id": "task_revision_001",
            "file_id": "file_compat_001",
                "document_id": "doc_travel_policy",
                "revision_id": "rev_travel_policy_002",
                "delivery_id": "delivery_revision_001",
                "status": "succeeded",
                "updated_at": "2026-07-14T00:01:00Z",
                "chunk_manifest_sha256": "sha256:chunks-001",
                "embedding_model": "baseline-lexical-embedder",
            }
        )

    assert result.chunk_manifest_sha256 == "sha256:chunks-001"


def test_citation_anchor_contract_rejects_empty_or_inverted_ranges() -> None:
    invalid = load_fixture("revision-provenance-response.json")
    invalid["citation_anchors"] = [{"start_rune": 3, "end_rune": 3, "citation_ranks": [1]}]

    with pytest.raises(ValidationError, match="end_rune"):
        ChatResponse.model_validate(invalid)

    invalid["citation_anchors"] = [{"start_rune": 0, "end_rune": 3, "citation_ranks": [1, 1]}]
    with pytest.raises(ValidationError, match="citation_ranks"):
        ChatResponse.model_validate(invalid)


def test_revision_ingestion_task_requires_delivery_id() -> None:
    invalid = load_fixture("revision-ingestion-task.json")
    invalid.pop("delivery_id")

    with pytest.raises(ValidationError, match="delivery_id"):
        IngestionRunRequest.model_validate(invalid)
