from copy import deepcopy

import pytest
from pydantic import ValidationError

from app.api.schemas import ChatRequest, ChatResponse


def _candidate(rank: int = 1) -> dict[str, object]:
    return {
        "chunk_id": f"chunk_{rank}",
        "file_id": "file_policy",
        "document_id": "doc_policy",
        "revision_id": "rev_policy",
        "chunk_content_sha256": "sha256:chunk",
        "knowledge_snapshot_id": "snap_policy",
        "rank": rank,
        "score": 0.93,
        "source_locator": {"source_format": "md", "heading_path": ["Travel"]},
    }


def _citation(rank: int = 1) -> dict[str, object]:
    return {
        "chunk_id": f"chunk_{rank}",
        "file_id": "file_policy",
        "filename": "travel-policy.md",
        "document_id": "doc_policy",
        "revision_id": "rev_policy",
        "chunk_content_sha256": "sha256:chunk",
        "knowledge_snapshot_id": "snap_policy",
        "rank": rank,
        "score": 0.93,
        "citation_label": f"[{rank}] travel-policy.md",
        "source_locator": {"source_format": "md", "heading_path": ["Travel"]},
        "snippet": "Submit expenses within ten days.",
    }


def _knowledge_execution(*, retrieval: str, generation: str, policy: str = "performed") -> dict[str, object]:
    return {
        "policy": policy,
        **(
            {
                "policy_provider": "test",
                "policy_model_id": "test-model",
                "policy_call_count": 1,
            }
            if policy == "performed"
            else {}
        ),
        "retrieval": {
            "status": retrieval,
            **(
                {
                    "strategy": "native_dense_v1",
                    "embedding_model": "test-embedder",
                    "collection": "see_agent_chunks",
                    "candidate_count": 1,
                    "top_k": 3,
                    "rerank_enabled": False,
                    "evidence_min_score": 0.51,
                }
                if retrieval == "performed"
                else {}
            ),
        },
        "generation": {
            "status": generation,
            **({"provider": "test", "model_id": "test-model"} if generation == "performed" else {}),
        },
        "fallback_used": False,
    }


def _response(**overrides: object) -> dict[str, object]:
    value: dict[str, object] = {
        "request_id": "req_a1",
        "conversation_id": "conv_a1",
        "message_id": "msg_a1",
        "assistant_message": {"role": "assistant", "content": "Expenses must be submitted within ten days."},
        "citations": [_citation()],
        "citation_anchors": [{"start_rune": 0, "end_rune": 10, "citation_ranks": [1]}],
        "refusal": False,
        "finish_reason": "stop",
        "completed_at": "2026-08-01T00:00:00Z",
        "knowledge_snapshot_id": "snap_policy",
        "knowledge_snapshot_manifest_hash": "sha256:manifest",
        "intent": "knowledge_query",
        "answer_disposition": "grounded_answer",
        "answer_policy_version": "answer_policy_v1",
        "policy_evaluation_performed": True,
        "answer_generation_performed": True,
        "execution": _knowledge_execution(retrieval="performed", generation="performed"),
        "retrieval_candidates": [_candidate()],
        "used_citation_ranks": [1],
        "retrieval_performed": True,
        "generation_performed": True,
        "knowledge_used": True,
    }
    value.update(overrides)
    return value


@pytest.mark.parametrize(
    ("disposition", "overrides"),
    [
        ("grounded_answer", {}),
        (
            "partial_answer",
            {"coverage": {"requested_count": 3, "supported_count": 2, "uncovered_description": "The third rule is not supported by this scope."}},
        ),
        (
            "clarification_required",
            {
                "assistant_message": {"role": "assistant", "content": "Which travel policy do you mean?"},
                "citations": [],
                "citation_anchors": [],
                "answer_generation_performed": False,
                "execution": _knowledge_execution(retrieval="not_run", generation="not_run"),
                "retrieval_candidates": [],
                "used_citation_ranks": [],
                "retrieval_performed": False,
                "generation_performed": False,
                "knowledge_used": False,
                "clarification": {"missing_dimensions": ["policy_version"], "question": "Which travel policy version do you mean?"},
                "context_resolution": {
                    "intent": "knowledge_query",
                    "route": "clarification",
                    "kind": "clarification_required",
                    "resolver_performed": False,
                    "confidence": 0.2,
                },
            },
        ),
        (
            "insufficient_evidence",
            {
                "assistant_message": {"role": "assistant", "content": "The published knowledge does not support an answer."},
                "citations": [],
                "citation_anchors": [],
                "refusal": True,
                "answer_generation_performed": False,
                "execution": _knowledge_execution(retrieval="performed", generation="not_run"),
                "used_citation_ranks": [],
                "generation_performed": False,
                "knowledge_used": False,
            },
        ),
        (
            "controlled_response",
            {
                "assistant_message": {"role": "assistant", "content": "I can answer from published knowledge."},
                "citations": [],
                "citation_anchors": [],
                "knowledge_snapshot_id": None,
                "knowledge_snapshot_manifest_hash": None,
                "intent": "capability_help",
                "answer_policy_version": None,
                "policy_evaluation_performed": False,
                "answer_generation_performed": False,
                "execution": _knowledge_execution(retrieval="not_run", generation="not_run", policy="not_run"),
                "retrieval_candidates": [],
                "used_citation_ranks": [],
                "retrieval_performed": False,
                "generation_performed": False,
                "knowledge_used": False,
                "context_resolution": {
                    "intent": "capability_help",
                    "route": "controlled_reply",
                    "kind": "standalone",
                    "resolver_performed": False,
                    "confidence": 0.95,
                },
            },
        ),
    ],
)
def test_a1_contract_accepts_each_legal_disposition(disposition: str, overrides: dict[str, object]) -> None:
    response = ChatResponse.model_validate(_response(answer_disposition=disposition, **overrides))

    assert response.answer_disposition == disposition


def test_clarification_rejects_user_visible_citations() -> None:
    value = _response(
        answer_disposition="clarification_required",
        citations=[_citation()],
        citation_anchors=[],
        answer_generation_performed=False,
        execution=_knowledge_execution(retrieval="not_run", generation="not_run"),
        retrieval_candidates=[],
        used_citation_ranks=[],
        retrieval_performed=False,
        generation_performed=False,
        knowledge_used=False,
        clarification={"missing_dimensions": ["policy_version"], "question": "Which policy version?"},
    )

    with pytest.raises(ValidationError, match="clarification_required cannot contain user-visible citations or anchors"):
        ChatResponse.model_validate(value)


@pytest.mark.parametrize("field_name", ["top_k", "rerank_enabled", "evidence_min_score"])
def test_performed_retrieval_requires_applied_configuration(field_name: str) -> None:
    value = _response()
    retrieval = value["execution"]["retrieval"]  # type: ignore[index]
    retrieval.pop(field_name)  # type: ignore[union-attr]

    with pytest.raises(ValidationError, match="performed retrieval requires"):
        ChatResponse.model_validate(value)


def test_partial_answer_rejects_complete_coverage() -> None:
    value = _response(
        answer_disposition="partial_answer",
        coverage={"requested_count": 2, "supported_count": 2, "uncovered_description": "No missing coverage."},
    )

    with pytest.raises(ValidationError, match="partial coverage requires supported_count < requested_count"):
        ChatResponse.model_validate(value)


def test_controlled_response_rejects_knowledge_fields() -> None:
    value = _response(
        answer_disposition="controlled_response",
        citations=[],
        citation_anchors=[],
        intent="capability_help",
        answer_policy_version=None,
        policy_evaluation_performed=False,
        answer_generation_performed=False,
        execution=_knowledge_execution(retrieval="not_run", generation="not_run", policy="not_run"),
        retrieval_candidates=[],
        used_citation_ranks=[],
        retrieval_performed=False,
        generation_performed=False,
        knowledge_used=False,
        knowledge_snapshot_manifest_hash=None,
        context_resolution={
            "intent": "capability_help",
            "route": "controlled_reply",
            "kind": "standalone",
            "resolver_performed": False,
            "confidence": 0.95,
        },
    )

    with pytest.raises(ValidationError, match="controlled_response cannot contain knowledge fields"):
        ChatResponse.model_validate(value)


def _request_with_pending() -> dict[str, object]:
    return {
        "request_id": "req_pending",
        "conversation_id": "conv_pending",
        "message_id": "msg_pending",
        "user_query": "差旅报销",
        "model_id": "test-model",
        "config_version": "cfg_test",
        "retrieval_config_version": "retrieval_cfg_test",
        "intent": "knowledge_query",
        "answer_policy_version": "answer_policy_v1",
        "stream": False,
        "messages": [],
        "pending_clarification": {
            "clarification_id": "clrf_1",
            "state_version": 3,
            "original_user_message_id": "msg_original",
            "original_query": "总结上传知识中最影响一线执行的三条规则",
            "missing_dimensions": ["business_subject"],
            "question": "你希望聚焦哪类具体事项？",
            "turn_count": 1,
        },
    }


def test_pending_clarification_enforces_knowledge_and_state_boundaries() -> None:
    request = ChatRequest.model_validate(_request_with_pending())
    assert request.pending_clarification is not None
    assert request.pending_clarification.state_version == 3

    invalid_state = deepcopy(_request_with_pending())
    invalid_state["pending_clarification"]["state_version"] = 0  # type: ignore[index]
    with pytest.raises(ValidationError):
        ChatRequest.model_validate(invalid_state)

    non_knowledge = deepcopy(_request_with_pending())
    non_knowledge["intent"] = "capability_help"
    with pytest.raises(ValidationError, match="answer_policy_version is only allowed for context_routing_required or knowledge_query"):
        ChatRequest.model_validate(non_knowledge)

    routed = deepcopy(_request_with_pending())
    routed["intent"] = "context_routing_required"
    assert ChatRequest.model_validate(routed).pending_clarification is not None


def test_resolution_validates_its_shape_but_not_request_state() -> None:
    response = ChatResponse.model_validate(
        _response(
            resolution={
                "pending_clarification_id": "clrf_1",
                "pending_state_version": 3,
                "action": "resolved",
                "resolved_query": "差旅报销中一线员工需要遵守的三条规则",
            },
            resolved_query="差旅报销中一线员工需要遵守的三条规则",
        )
    )

    assert response.resolution is not None
    assert response.resolution.pending_state_version == 3


@pytest.mark.parametrize("field", ["policy_provider", "policy_model_id", "policy_call_count"])
def test_performed_a1_policy_requires_complete_provider_evidence(field: str) -> None:
    value = _response()
    execution = value["execution"]
    assert isinstance(execution, dict)
    execution.pop(field)

    with pytest.raises(ValidationError, match="performed A1 policy requires provider, model id and call count"):
        ChatResponse.model_validate(value)


def test_a1_models_reject_unknown_fields() -> None:
    pending = _request_with_pending()
    pending["pending_clarification"]["unexpected"] = True  # type: ignore[index]
    with pytest.raises(ValidationError):
        ChatRequest.model_validate(pending)

    response = _response()
    response["unexpected"] = True
    with pytest.raises(ValidationError):
        ChatResponse.model_validate(response)
