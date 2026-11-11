from __future__ import annotations

import json
from typing import Any

import httpx
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from pydantic import ValidationError

from app.adapters.embeddings import LexicalEmbeddingAdapter
from app.adapters.model_client import ModelClientResult
from app.adapters.vector_store import (
    InMemoryVectorStore,
    QdrantVectorStore,
    ScopedRetrievalError,
    SearchMatch,
)
from app.api.chat import build_chat_router
from app.api.schemas import ChatRequest, KnowledgeScope, ResponseOverrides
from app.domain.models import ChunkRecord, SourceLocatorData
from app.parsers.registry import build_default_registry
from app.pipelines.answer_pipeline import AnswerPipeline
from app.pipelines.retrieval_pipeline import KnowledgeScopeError, RetrievalExecutionError, RetrievalPipeline


def test_snapshot_scope_isolates_a_and_b_and_excludes_orphan_legacy_points() -> None:
    embedder = LexicalEmbeddingAdapter()
    store = InMemoryVectorStore()
    revision_a = _chunk("rev_policy_a", "The travel reimbursement deadline is ten days.")
    revision_b = _chunk("rev_policy_b", "The travel reimbursement deadline is three days.")
    orphan = _chunk(None, "The travel reimbursement deadline is one day.")
    for chunk in (revision_a, revision_b, orphan):
        store.upsert(chunk, embedder.embed(chunk.text))

    pipeline = _retrieval_pipeline(store)
    result_a = pipeline.execute(_scoped_request("snap_a", ["rev_policy_a"]))
    result_b = pipeline.execute(_scoped_request("snap_b", ["rev_policy_b"]))

    assert [citation.revision_id for citation in result_a.citations] == ["rev_policy_a"]
    assert [citation.revision_id for citation in result_b.citations] == ["rev_policy_b"]
    assert result_a.citations[0].knowledge_snapshot_id == "snap_a"
    assert result_b.citations[0].knowledge_snapshot_id == "snap_b"
    assert "one day" not in result_a.citations[0].snippet
    assert "one day" not in result_b.citations[0].snippet


def test_scoped_answer_echoes_snapshot_manifest_and_immutable_citation_provenance() -> None:
    embedder = LexicalEmbeddingAdapter()
    store = InMemoryVectorStore()
    chunk = _chunk("rev_policy_a", "The travel reimbursement deadline is ten days.")
    store.upsert(chunk, embedder.embed(chunk.text))
    response = AnswerPipeline(_retrieval_pipeline(store), _ModelStub()).execute(
        _scoped_request("snap_a", ["rev_policy_a"])
    )

    assert response.knowledge_snapshot_id == "snap_a"
    assert response.knowledge_snapshot_manifest_hash == "sha256:manifest-snap_a"
    assert response.freshness_status == "current"
    assert response.citations[0].document_id == "doc_policy"
    assert response.citations[0].revision_id == "rev_policy_a"
    assert response.citations[0].chunk_content_sha256 == "sha256:chunk-rev_policy_a"
    assert response.citations[0].knowledge_snapshot_id == "snap_a"


def test_prompt_v3_model_insufficient_evidence_marker_returns_a_safe_scoped_refusal() -> None:
    embedder = LexicalEmbeddingAdapter()
    store = InMemoryVectorStore()
    store.upsert(_chunk("rev_policy_a", "Travel reimbursement must be submitted within ten days."), embedder.embed("Travel reimbursement must be submitted within ten days."))
    request = _scoped_request("snap_a", ["rev_policy_a"])
    request.response_overrides = ResponseOverrides(system_prompt_version="prompt_v3")

    response = AnswerPipeline(_retrieval_pipeline(store), _InsufficientEvidenceModelStub()).execute(request)

    assert response.answer_disposition == "insufficient_evidence"
    assert response.refusal is True
    assert response.retrieval_performed is True
    assert response.generation_performed is True
    assert response.knowledge_used is False
    assert response.citations == []
    assert response.citation_anchors == []
    assert response.used_citation_ranks == []
    assert "[[disposition:" not in response.assistant_message.content
    assert "十天" not in response.assistant_message.content


def test_prompt_v3_stream_hides_insufficient_evidence_marker() -> None:
    embedder = LexicalEmbeddingAdapter()
    store = InMemoryVectorStore()
    store.upsert(_chunk("rev_policy_a", "Travel reimbursement must be submitted within ten days."), embedder.embed("Travel reimbursement must be submitted within ten days."))
    request = _scoped_request("snap_a", ["rev_policy_a"])
    request.response_overrides = ResponseOverrides(system_prompt_version="prompt_v3")

    events = list(AnswerPipeline(_retrieval_pipeline(store), _InsufficientEvidenceModelStub()).stream(request))

    visible = "".join(event.payload["delta"] for event in events if event.event == "message.delta")
    completed = next(event.payload["chat_runtime_response"] for event in events if event.event == "message.completed")
    assert "[[disposition:" not in visible
    assert completed.answer_disposition == "insufficient_evidence"
    assert completed.citations == []


def test_scoped_retrieval_rejects_scope_mismatch_and_missing_revision_provenance() -> None:
    invalid = _scoped_request("snap_a", ["rev_policy_a"]).model_dump()
    invalid["knowledge_snapshot_id"] = "snap_b"
    with pytest.raises(ValidationError, match="knowledge_scope.snapshot_id"):
        ChatRequest.model_validate(invalid)

    embedder = LexicalEmbeddingAdapter()
    store = InMemoryVectorStore()
    missing_hash = _chunk("rev_policy_a", "The travel reimbursement deadline is ten days.")
    missing_hash.chunk_content_sha256 = None
    store.upsert(missing_hash, embedder.embed(missing_hash.text))

    with pytest.raises(KnowledgeScopeError, match="immutable provenance"):
        _retrieval_pipeline(store).execute(_scoped_request("snap_a", ["rev_policy_a"]))


def test_scoped_retrieval_rejects_a_vector_store_payload_outside_the_allow_list() -> None:
    leaked = _chunk("rev_policy_b", "The travel reimbursement deadline is three days.")
    store = _LeakyVectorStore(leaked)

    with pytest.raises(KnowledgeScopeError, match="outside the requested revision scope"):
        _retrieval_pipeline(store).execute(_scoped_request("snap_a", ["rev_policy_a"]))

    assert store.revision_ids == ["rev_policy_a"]


def test_qdrant_scoped_search_sends_match_any_filter_and_rejects_filter_failure() -> None:
    points: dict[str, dict[str, Any]] = {}
    scoped_filters: list[dict[str, Any]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        if request.method == "GET" and request.url.path == "/collections/scoped":
            return httpx.Response(200, json={"result": {}})
        if request.method == "PUT" and request.url.path == "/collections/scoped/index":
            return httpx.Response(200, json={"result": True})
        if request.method == "PUT" and request.url.path == "/collections/scoped/points":
            for point in json.loads(request.content)["points"]:
                points[str(point["id"])] = point["payload"]
            return httpx.Response(200, json={"result": {"operation_id": 1}})
        if request.method == "POST" and request.url.path == "/collections/scoped/points/scroll":
            body = json.loads(request.content)
            scoped_filters.append(body["filter"])
            allowed = body["filter"]["must"][0]["match"]["any"]
            return httpx.Response(
                200,
                json={
                    "result": {
                        "points": [
                            {"id": point_id, "payload": payload}
                            for point_id, payload in points.items()
                            if payload["revision_id"] in allowed
                        ],
                        "next_page_offset": None,
                    }
                },
            )
        return httpx.Response(500, json={"error": "unexpected request"})

    client = httpx.Client(transport=httpx.MockTransport(handler), base_url="http://qdrant.test")
    store = QdrantVectorStore("http://qdrant.test", collection_name="scoped", client=client)
    embedder = LexicalEmbeddingAdapter()
    for chunk in (_chunk("rev_policy_a", "The deadline is ten days."), _chunk("rev_policy_b", "The deadline is three days.")):
        store.upsert(chunk, embedder.embed(chunk.text))

    matches = store.search(
        "What is the deadline?",
        embedder.embed("What is the deadline?"),
        top_k=3,
        revision_ids=["rev_policy_a"],
    )

    assert [match.chunk.revision_id for match in matches] == ["rev_policy_a"]
    assert scoped_filters == [
        {"must": [{"key": "revision_id", "match": {"any": ["rev_policy_a"]}}]}
    ]

    failing_client = httpx.Client(
        transport=httpx.MockTransport(_filter_failure_handler),
        base_url="http://qdrant.test",
    )
    failing_store = QdrantVectorStore("http://qdrant.test", collection_name="failure", client=failing_client)
    with pytest.raises(ScopedRetrievalError, match="filter execution failed"):
        failing_store.search(
            "What is the deadline?",
            embedder.embed("What is the deadline?"),
            top_k=3,
            revision_ids=["rev_policy_a"],
        )


def test_scope_error_has_a_stable_retryable_http_envelope() -> None:
    app = FastAPI()
    app.include_router(build_chat_router(_ScopeFailingChatPipeline()))
    client = TestClient(app)

    response = client.post("/v1/chat/run", json=_scoped_request("snap_a", ["rev_policy_a"]).model_dump())

    assert response.status_code == 503
    assert response.json()["error"] == {
        "code": "knowledge_snapshot_unavailable",
        "message": "Qdrant revision filter execution failed",
        "retryable": True,
    }


def test_embedding_provider_failure_has_a_stable_retryable_http_envelope() -> None:
    app = FastAPI()
    app.include_router(build_chat_router(_EmbeddingFailingChatPipeline()))
    client = TestClient(app)

    response = client.post("/v1/chat/run", json=_scoped_request("snap_a", ["rev_policy_a"]).model_dump())

    assert response.status_code == 503
    assert response.json()["error"] == {
        "code": "retrieval_unavailable",
        "message": "embedding provider request failed",
        "retryable": True,
    }


class _ModelStub:
    provider_name = "test_injected"

    def generate(self, **_: object) -> ModelClientResult:
        return ModelClientResult(
            content="The deadline is ten days.[[cite:1]]",
            finish_reason="stop",
            prompt_tokens=7,
            completion_tokens=5,
            total_tokens=12,
        )

    def stream_generate(self, **_: object):
        yield "The deadline is ten days.[[cite:1]]"


class _InsufficientEvidenceModelStub:
    provider_name = "test_injected"

    def generate(self, **_: object) -> ModelClientResult:
        return ModelClientResult(
            content="[[disposition:insufficient_evidence]]The published knowledge does not confirm the requested annual leave entitlement.",
            finish_reason="stop",
            prompt_tokens=7,
            completion_tokens=8,
            total_tokens=15,
        )

    def stream_generate(self, **_: object):
        yield "[[disposition:insufficient_evidence]]"
        yield "The published knowledge does not confirm the requested annual leave entitlement."


class _LeakyVectorStore:
    def __init__(self, chunk: ChunkRecord) -> None:
        self._chunk = chunk
        self.revision_ids: list[str] | None = None

    def search(self, _query_text, _query_embedding, *, top_k, revision_ids=None, **_kwargs):
        self.revision_ids = revision_ids
        return [SearchMatch(chunk=self._chunk, score=1.0)][:top_k]


class _ScopeFailingChatPipeline:
    def execute(self, _request: ChatRequest):
        raise KnowledgeScopeError("Qdrant revision filter execution failed")

    def stream(self, _request: ChatRequest):
        raise KnowledgeScopeError("Qdrant revision filter execution failed")
        yield


class _EmbeddingFailingChatPipeline:
    def execute(self, _request: ChatRequest):
        raise RetrievalExecutionError("embedding provider request failed")

    def stream(self, _request: ChatRequest):
        raise RetrievalExecutionError("embedding provider request failed")
        yield


def _filter_failure_handler(request: httpx.Request) -> httpx.Response:
    if request.method == "GET" and request.url.path == "/collections/failure":
        return httpx.Response(200, json={"result": {}})
    if request.method == "PUT" and request.url.path == "/collections/failure/index":
        return httpx.Response(200, json={"result": True})
    if request.method == "POST" and request.url.path == "/collections/failure/points/scroll":
        body = json.loads(request.content)
        assert body["filter"] == {
            "must": [{"key": "revision_id", "match": {"any": ["rev_policy_a"]}}]}
        return httpx.Response(503, json={"status": "unavailable"})
    return httpx.Response(500, json={"error": "unexpected request"})


def _retrieval_pipeline(store) -> RetrievalPipeline:
    return RetrievalPipeline(
        build_default_registry(),
        LexicalEmbeddingAdapter(),
        store,
        corpus_documents=(),
        min_score=0.01,
    )


def _scoped_request(snapshot_id: str, revision_ids: list[str]) -> ChatRequest:
    return ChatRequest(
        request_id=f"req_{snapshot_id}",
        conversation_id="conv_policy",
        message_id=f"msg_{snapshot_id}",
        user_query="What is the travel reimbursement deadline?",
        model_id="local-model",
        config_version="cfg_001",
        retrieval_config_version="retrieval_001",
        knowledge_snapshot_id=snapshot_id,
        knowledge_scope=KnowledgeScope(
            snapshot_id=snapshot_id,
            manifest_hash=f"sha256:manifest-{snapshot_id}",
            revision_ids=revision_ids,
            resolved_at="2026-07-14T00:00:00Z",
        ),
        stream=False,
        messages=[],
    )


def _chunk(revision_id: str | None, text: str) -> ChunkRecord:
    chunk_suffix = revision_id or "legacy"
    return ChunkRecord(
        chunk_id=f"chunk_{chunk_suffix}",
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
        checksum_sha256=f"sha256:source-{chunk_suffix}",
        parse_version="parse_v2",
        chunk_content_sha256=f"sha256:chunk-{chunk_suffix}",
        visibility="revision" if revision_id else "legacy",
    )
