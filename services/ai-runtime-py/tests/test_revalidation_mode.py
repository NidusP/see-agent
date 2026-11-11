from __future__ import annotations

import json
from pathlib import Path

import pytest

from app.adapters.embeddings import LexicalEmbeddingAdapter
from app.adapters.model_client import ModelClientResult
from app.adapters.vector_store import InMemoryVectorStore
from app.api.schemas import ChatRequest, ChatResponse, KnowledgeScope, RevalidationContext
from app.domain.models import ChunkRecord, SourceLocatorData
from app.parsers.registry import build_default_registry
from app.pipelines.answer_pipeline import (
    AnswerPipeline,
    _parse_revalidation_verdict,
)
from app.pipelines.retrieval_pipeline import KnowledgeScopeError, RetrievalPipeline


def test_revalidation_uses_only_latest_scope_and_builds_minimum_review_prompt() -> None:
    embedder = LexicalEmbeddingAdapter()
    store = InMemoryVectorStore()
    old_chunk = _chunk("rev_old", "The historical policy says the limit is 800.")
    latest_chunk = _chunk("rev_latest", "The latest policy says the limit is 900.")
    store.upsert(old_chunk, embedder.embed(old_chunk.text))
    store.upsert(latest_chunk, embedder.embed(latest_chunk.text))
    model = _ModelStub("[[VERDICT:needs_update]] The latest published policy sets the limit at 900.[[cite:1]]")

    response = AnswerPipeline(_retrieval_pipeline(store), model).execute(_request())

    assert response.revalidation_outcome == "needs_update"
    assert response.freshness_status == "revalidated"
    assert response.knowledge_snapshot_id == "snap_latest"
    assert response.knowledge_snapshot_manifest_hash == "sha256:manifest-latest"
    assert [citation.revision_id for citation in response.citations] == ["rev_latest"]
    assert "VERDICT" not in response.assistant_message.content
    assert model.messages is not None
    prompt = model.messages[1]["content"]
    assert "Historical Answer Under Review - Not Current Truth" in prompt
    assert "The historical policy says the limit is 800." in prompt
    assert "rev_old" in prompt
    assert "event_policy_replaced" in prompt
    assert "The latest policy says the limit is 900." in prompt


@pytest.mark.parametrize(
    ("content", "expected"),
    [
        ("[[VERDICT:still_valid]] The current policy remains unchanged.", ("still_valid", "The current policy remains unchanged.")),
        ("Before review. [[VERDICT:needs_update]] The current limit is 900.", ("needs_update", "Before review. The current limit is 900.")),
        ("[[VERDICT:insufficient_evidence]] The current sources do not decide this.", ("insufficient_evidence", "The current sources do not decide this.")),
    ],
)
def test_revalidation_verdict_parser_strips_exactly_one_control_marker(
    content: str,
    expected: tuple[str, str],
) -> None:
    assert _parse_revalidation_verdict(content) == expected


@pytest.mark.parametrize(
    "content",
    [
        "The current limit is 900.",
        "[[VERDICT:still_valid]][[VERDICT:needs_update]] Conflicting output.",
        "[[VERDICT:still_valid]]",
    ],
)
def test_revalidation_verdict_parser_rejects_missing_multiple_or_empty_markers(content: str) -> None:
    assert _parse_revalidation_verdict(content) is None


def test_revalidation_without_evidence_or_with_malformed_verdict_returns_insufficient_evidence() -> None:
    empty_response = AnswerPipeline(_retrieval_pipeline(InMemoryVectorStore()), _ModelStub("unused")).execute(_request())

    assert empty_response.revalidation_outcome == "insufficient_evidence"
    assert empty_response.refusal is False
    assert empty_response.citations == []

    embedder = LexicalEmbeddingAdapter()
    store = InMemoryVectorStore()
    latest_chunk = _chunk("rev_latest", "The latest policy says the limit is 900.")
    store.upsert(latest_chunk, embedder.embed(latest_chunk.text))
    malformed_response = AnswerPipeline(
        _retrieval_pipeline(store),
        _ModelStub("The current policy says the limit is 900."),
    ).execute(_request())

    assert malformed_response.revalidation_outcome == "insufficient_evidence"
    assert malformed_response.citations == []
    assert "VERDICT" not in malformed_response.assistant_message.content


def test_revalidation_requires_latest_scope_without_legacy_fallback() -> None:
    request = _request().model_copy(update={"knowledge_scope": None})

    with pytest.raises(KnowledgeScopeError, match="latest published knowledge scope"):
        AnswerPipeline(_retrieval_pipeline(InMemoryVectorStore()), _ModelStub("unused")).execute(request)


def test_revalidation_stream_strips_marker_before_sending_any_delta() -> None:
    embedder = LexicalEmbeddingAdapter()
    store = InMemoryVectorStore()
    latest_chunk = _chunk("rev_latest", "The latest policy says the limit is 900.")
    store.upsert(latest_chunk, embedder.embed(latest_chunk.text))
    model = _ModelStub(
        "[[VERDICT:needs_update]] The latest published policy sets the limit at 900.[[cite:1]]",
        stream_chunks=["[[VERDICT:", "needs_update]] The latest ", "published policy sets the limit at 900.[[cite:1]]"],
    )

    events = list(AnswerPipeline(_retrieval_pipeline(store), model).stream(_request(stream=True)))

    delta_text = "".join(event.payload["delta"] for event in events if event.event == "message.delta")
    completed = next(event for event in events if event.event == "message.completed")
    response = completed.payload["chat_runtime_response"]
    assert "VERDICT" not in delta_text
    assert response.revalidation_outcome == "needs_update"
    assert response.assistant_message.content == delta_text
    assert response.citations[0].revision_id == "rev_latest"


def test_frozen_revalidation_request_fixture_and_response_outcome_are_valid() -> None:
    fixtures = Path(__file__).resolve().parents[3] / "packages" / "contracts" / "fixtures"
    request = ChatRequest.model_validate(json.loads((fixtures / "revalidate-chat.json").read_text(encoding="utf-8")))

    response = ChatResponse(
        request_id=request.request_id,
        conversation_id=request.conversation_id,
        message_id=request.message_id,
        assistant_message={"role": "assistant", "content": "现有证据不足以确认。"},
        citations=[],
        refusal=False,
        finish_reason="stop",
        completed_at="2026-07-15T00:00:00Z",
        knowledge_snapshot_id=request.knowledge_scope.snapshot_id if request.knowledge_scope else None,
        knowledge_snapshot_manifest_hash=request.knowledge_scope.manifest_hash if request.knowledge_scope else None,
        freshness_status="revalidated",
        revalidation_outcome="insufficient_evidence",
        intent="knowledge_query",
        answer_disposition="insufficient_evidence",
        execution={
            "policy": "performed",
            "retrieval": {
                "status": "performed",
                "strategy": "lexical_compat_v1",
                "embedding_model": "test-embedder",
                "collection": "memory_test",
                "candidate_count": 0,
                "top_k": 3,
                "rerank_enabled": False,
                "evidence_min_score": 0.15,
            },
            "generation": {"status": "not_run"},
            "fallback_used": False,
        },
        used_citation_ranks=[],
        retrieval_performed=True,
        generation_performed=False,
        knowledge_used=False,
    )

    assert request.answer_mode == "revalidate"
    assert response.revalidation_outcome == "insufficient_evidence"


class _ModelStub:
    provider_name = "test_injected"
    def __init__(self, content: str, stream_chunks: list[str] | None = None) -> None:
        self._content = content
        self._stream_chunks = stream_chunks or [content]
        self.messages: list[dict[str, str]] | None = None

    def generate(self, *, messages: list[dict[str, str]], **_: object) -> ModelClientResult:
        self.messages = messages
        return ModelClientResult(
            content=self._content,
            finish_reason="stop",
            prompt_tokens=11,
            completion_tokens=9,
            total_tokens=20,
        )

    def stream_generate(self, *, messages: list[dict[str, str]], **_: object):
        self.messages = messages
        yield from self._stream_chunks


def _retrieval_pipeline(store: InMemoryVectorStore) -> RetrievalPipeline:
    return RetrievalPipeline(
        build_default_registry(),
        LexicalEmbeddingAdapter(),
        store,
        corpus_documents=(),
        min_score=0.01,
    )


def _request(*, stream: bool = False) -> ChatRequest:
    return ChatRequest(
        request_id="req_revalidate",
        conversation_id="conv_revalidate",
        message_id="msg_revalidate",
        user_query="Is the earlier policy answer still accurate?",
        model_id="local-model",
        config_version="cfg_001",
        retrieval_config_version="retrieval_001",
        knowledge_snapshot_id="snap_latest",
        knowledge_scope=KnowledgeScope(
            snapshot_id="snap_latest",
            manifest_hash="sha256:manifest-latest",
            revision_ids=["rev_latest"],
            resolved_at="2026-07-15T00:00:00Z",
        ),
        answer_mode="revalidate",
        revalidation_context=RevalidationContext(
            original_message_id="msg_historical",
            original_snapshot_id="snap_old",
            original_answer="The historical policy says the limit is 800.",
            original_citation_revision_ids=["rev_old"],
            related_update_event_ids=["event_policy_replaced"],
        ),
        stream=stream,
        messages=[],
    )


def _chunk(revision_id: str, text: str) -> ChunkRecord:
    return ChunkRecord(
        chunk_id=f"chunk_{revision_id}",
        file_id="file_policy",
        document_id="doc_policy",
        filename="policy.txt",
        source_format="txt",
        block_type="paragraph",
        text=text,
        token_count=len(text.split()),
        heading_path=[],
        source_locator=SourceLocatorData(source_format="txt", block_label="paragraph-1"),
        embedding_model="baseline-lexical-embedder",
        index_version="index_v2",
        revision_id=revision_id,
        checksum_sha256=f"sha256:source-{revision_id}",
        parse_version="parse_v2",
        chunk_content_sha256=f"sha256:chunk-{revision_id}",
        visibility="revision",
    )
