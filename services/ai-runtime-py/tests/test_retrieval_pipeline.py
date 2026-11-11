from __future__ import annotations

import pytest

from app.adapters.embeddings import LexicalEmbeddingAdapter
from app.adapters.vector_store import InMemoryVectorStore
from app.api.schemas import ChatRequest, KnowledgeScope, ResponseOverrides, SourceFilters
from app.domain.models import ChunkRecord, SourceLocatorData
from app.parsers.registry import build_default_registry
from app.pipelines.retrieval_pipeline import (
    RetrievalExecutionError,
    RetrievalPipeline,
    build_default_corpus_documents,
)


@pytest.mark.parametrize(
    ("query", "expected_snippet"),
    [
        (
            "Word 样例中，超过多少金额的酒店条目需要额外业务说明？",
            "Any single hotel line item above CNY 800 must include a short business justification.",
        ),
        (
            "Word 样例中，年假申请需要提前多久提交？",
            "Annual leave requests must be submitted at least 3 business days in advance.",
        ),
        (
            "Word 样例中，超过5个连续工作日的请假需要哪些确认？",
            "Requests longer than 5 consecutive business days require both direct-manager approval and HR confirmation.",
        ),
        (
            "Word 样例中，经理需要在哪里确认覆盖安排？",
            "Managers should confirm coverage expectations in the same approval thread.",
        ),
        (
            "Word 样例中，缺少收据时什么例外可以允许报销？",
            "Receipts are mandatory unless finance leadership grants an exception in writing.",
        ),
    ],
)
def test_docx_baseline_questions_retrieve_non_heading_evidence(
    query: str,
    expected_snippet: str,
) -> None:
    result = _default_pipeline().execute(_request(query, source_formats=["docx"], top_k=1))

    assert result.refusal_reason is None
    assert len(result.citations) == 1
    assert result.citations[0].source_locator.source_format == "docx"
    assert result.citations[0].snippet == expected_snippet


def test_docx_aliases_are_scoped_to_docx_filter_or_explicit_reference() -> None:
    english_leave_text = "Annual leave requests must be submitted at least 3 business days in advance."
    no_alias_chunk = _chunk(
        "md-leave-policy",
        "paragraph",
        english_leave_text,
        source_format="md",
    )
    explicit_docx_chunk = _chunk(
        "docx-leave-policy",
        "paragraph",
        english_leave_text,
    )

    no_alias_result = _pipeline_with_chunks(no_alias_chunk).execute(
        _request("年假需要提前几天申请？", source_formats=["md"])
    )
    explicit_docx_result = _pipeline_with_chunks(explicit_docx_chunk).execute(
        _request("DOCX 中年假需要提前几天申请？")
    )

    assert no_alias_result.refusal_reason == "retrieval_miss"
    assert no_alias_result.citations == []
    assert explicit_docx_result.refusal_reason is None
    assert explicit_docx_result.citations[0].source_locator.source_format == "docx"


def test_heading_only_match_returns_empty_citations_and_refusal() -> None:
    result = _pipeline_with_chunks(_chunk("docx-heading", "heading", "Annual Leave")).execute(
        _request("年假", source_formats=["docx"])
    )

    assert result.refusal_reason == "retrieval_miss"
    assert result.citations == []


@pytest.mark.parametrize(
    "query",
    [
        "员工餐厅本周五午餐菜单",
        "公司员工家庭住址列表",
    ],
)
def test_generic_cjk_queries_remain_refusals(query: str) -> None:
    result = _default_pipeline().execute(_request(query))

    assert result.refusal_reason is not None


def test_existing_chinese_txt_must_answer_path_remains_available() -> None:
    result = _default_pipeline().execute(
        _request("差旅报销需要几天内提交？", source_formats=["txt"])
    )

    assert result.refusal_reason is None
    assert result.citations[0].source_locator.source_format == "txt"


def test_native_dense_embedding_model_must_match_retrieval_config() -> None:
    embedder = _StaticDenseEmbeddingAdapter()
    vector_store = InMemoryVectorStore()
    chunk = _chunk("dense-policy", "paragraph", "Dense retrieval policy evidence.", source_format="txt")
    vector_store.upsert(chunk, embedder.embed(chunk.text))
    pipeline = RetrievalPipeline(build_default_registry(), embedder, vector_store, corpus_documents=())

    matched = pipeline.execute(
        _request("policy", response_overrides=ResponseOverrides(top_k=1, embedding_model="embeddinggemma"))
    )
    assert matched.refusal_reason is None
    assert matched.citations[0].chunk_id == "dense-policy"

    with pytest.raises(RetrievalExecutionError, match="embedding model"):
        pipeline.execute(
            _request("policy", response_overrides=ResponseOverrides(top_k=1, embedding_model="other-embedder"))
        )

    with pytest.raises(RetrievalExecutionError, match="embedding model"):
        pipeline.execute(_request("policy", response_overrides=ResponseOverrides(top_k=1)))


def test_active_runtime_profile_rejects_mismatched_strategy_before_index_or_embedding() -> None:
    embedder = LexicalEmbeddingAdapter()
    pipeline = RetrievalPipeline(
        build_default_registry(),
        embedder,
        InMemoryVectorStore(),
        corpus_documents=(),
        active_retrieval_strategy="lexical_compat_v1",
    )
    index_attempted = False

    def unexpected_index() -> None:
        nonlocal index_attempted
        index_attempted = True
        raise AssertionError("mismatched strategy must fail before indexing")

    pipeline._ensure_index = unexpected_index  # type: ignore[method-assign]
    with pytest.raises(RetrievalExecutionError, match="strategy"):
        pipeline.execute(
            _request(
                "policy",
                response_overrides=ResponseOverrides(
                    top_k=1,
                    retrieval_strategy="native_dense_v1",
                ),
            )
        )
    assert index_attempted is False


def test_native_dense_profile_requires_immutable_evidence_boundary() -> None:
    pipeline = RetrievalPipeline(
        build_default_registry(),
        LexicalEmbeddingAdapter(),
        InMemoryVectorStore(),
        corpus_documents=(),
        active_retrieval_strategy="native_dense_v1",
    )

    with pytest.raises(RetrievalExecutionError, match="evidence_min_score"):
        pipeline.execute(
            _request(
                "policy",
                response_overrides=ResponseOverrides(
                    top_k=1,
                    retrieval_strategy="native_dense_v1",
                ),
            )
        )


@pytest.mark.parametrize(
    ("default_top_k", "min_score", "error"),
    [(0, 0.15, "default_top_k"), (3, 0.0, "min_score"), (3, 1.01, "min_score")],
)
def test_retrieval_defaults_require_valid_execution_boundaries(
    default_top_k: int,
    min_score: float,
    error: str,
) -> None:
    with pytest.raises(ValueError, match=error):
        RetrievalPipeline(
            build_default_registry(),
            LexicalEmbeddingAdapter(),
            InMemoryVectorStore(),
            default_top_k=default_top_k,
            min_score=min_score,
        )


def _default_pipeline() -> RetrievalPipeline:
    embedder = LexicalEmbeddingAdapter()
    return RetrievalPipeline(
        build_default_registry(), embedder, InMemoryVectorStore(), corpus_documents=build_default_corpus_documents()
    )


def _pipeline_with_chunks(*chunks: ChunkRecord) -> RetrievalPipeline:
    embedder = LexicalEmbeddingAdapter()
    vector_store = InMemoryVectorStore()
    for chunk in chunks:
        vector_store.upsert(chunk, embedder.embed(chunk.text))
    return RetrievalPipeline(
        build_default_registry(),
        embedder,
        vector_store,
        corpus_documents=(),
    )


def _chunk(
    chunk_id: str,
    block_type: str,
    text: str,
    *,
    source_format: str = "docx",
) -> ChunkRecord:
    return ChunkRecord(
        chunk_id=chunk_id,
        file_id=f"sample_handbook_{source_format}",
        document_id=f"sample_handbook_{source_format}",
        filename=f"sample-handbook.{source_format}",
        source_format=source_format,
        block_type=block_type,
        text=text,
        token_count=len(text.split()),
        heading_path=["Leave And Time Off"],
        source_locator=SourceLocatorData(
            source_format=source_format,
            heading_path=["Leave And Time Off"],
            paragraph_index=1,
        ),
        embedding_model="baseline-lexical-embedder",
        index_version="test_index_v1",
        revision_id="rev_test_fixture",
        checksum_sha256="sha256:test-fixture",
        chunk_content_sha256="sha256:chunk-test-fixture",
    )


def _request(
    user_query: str,
    *,
    source_formats: list[str] | None = None,
    top_k: int | None = None,
    response_overrides: ResponseOverrides | None = None,
) -> ChatRequest:
    return ChatRequest(
        request_id="req_retrieval",
        conversation_id="conv_retrieval",
        message_id="msg_retrieval",
        user_query=user_query,
        model_id="qwen-local",
        config_version="cfg_001",
        retrieval_config_version="rcfg_001",
        knowledge_snapshot_id="snapshot_active",
        knowledge_scope=KnowledgeScope(
            snapshot_id="snapshot_active",
                manifest_hash="sha256:test-fixture",
                revision_ids=["rev_test_fixture"],
                resolved_at="2026-07-24T00:00:00Z",
        ),
        stream=False,
        messages=[],
        source_filters=SourceFilters(source_formats=source_formats) if source_formats else None,
        response_overrides=response_overrides or (ResponseOverrides(top_k=top_k) if top_k else None),
    )


class _StaticDenseEmbeddingAdapter:
    model_name = "embeddinggemma"
    requires_config_model_match = True

    def embed(self, _text: str) -> list[float]:
        return [0.3, 0.4, 0.5]
