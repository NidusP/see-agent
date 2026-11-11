from __future__ import annotations

import logging
import re
from dataclasses import dataclass, replace
from datetime import datetime, timezone
from pathlib import Path

import httpx

from app.adapters.embeddings import EmbeddingAdapter, EmbeddingProviderError, build_docx_query_representation
from app.adapters.vector_store import ScopedRetrievalError, SearchMatch, VectorStore
from app.api.schemas import ChatRequest, IngestionRunRequest, RetrievedSource, SourceLocator
from app.parsers.registry import ParserRegistry
from app.pipelines.chunking import chunk_document


logger = logging.getLogger(__name__)
_WORKSPACE_ROOT = Path(__file__).resolve().parents[4]
_SAMPLE_DOCS_ROOT = _WORKSPACE_ROOT / "data" / "sample-docs"
_DOCX_QUERY_REFERENCE_RE = re.compile(r"(?<![a-z0-9])(?:docx|word)(?![a-z0-9])", re.IGNORECASE)


@dataclass(frozen=True)
class CorpusDocument:
    file_id: str
    filename: str
    source_format: str
    mime_type: str
    object_uri: str
    document_id: str = ""
    revision_id: str = ""
    checksum_sha256: str = ""
    delivery_id: str = ""


@dataclass(frozen=True)
class RetrievalResult:
    citations: list[RetrievedSource]
    context_text: str
    top_score: float
    top_k: int
    rerank_enabled: bool
    evidence_min_score: float
    refusal_reason: str | None = None


class KnowledgeScopeError(RuntimeError):
    code = "knowledge_snapshot_unavailable"
    retryable = True


class RetrievalExecutionError(RuntimeError):
    code = "retrieval_unavailable"
    retryable = True


class RetrievalPipeline:
    def __init__(
        self,
        registry: ParserRegistry,
        embedding_adapter: EmbeddingAdapter,
        vector_store: VectorStore,
        *,
        default_top_k: int = 3,
        min_score: float = 0.15,
        corpus_documents: tuple[CorpusDocument, ...] | None = None,
        active_retrieval_strategy: str | None = None,
    ) -> None:
        if default_top_k < 1:
            raise ValueError("default_top_k must be positive")
        if not 0 < min_score <= 1:
            raise ValueError("min_score must be greater than 0 and at most 1")
        self._registry = registry
        self._embedding_adapter = embedding_adapter
        self._vector_store = vector_store
        self._default_top_k = default_top_k
        self._min_score = min_score
        # Product Runtime never bootstraps sample knowledge. Test fixtures may
        # pass a corpus explicitly; production content arrives only through
        # the ingestion pipeline and the configured vector store.
        self._corpus_documents = () if corpus_documents is None else corpus_documents
        self._active_retrieval_strategy = active_retrieval_strategy
        self._index_ready = False

    def execution_profile(self) -> tuple[str, str, str]:
        """Return only configuration facts; per-request facts stay in the response observation."""
        strategy = self._active_retrieval_strategy or "native_dense_v1"
        collection = str(getattr(self._vector_store, "collection_name", "unknown_collection"))
        return strategy, self._embedding_adapter.model_name, collection

    def execute(self, request: ChatRequest, *, query_override: str | None = None) -> RetrievalResult:
        scope = request.knowledge_scope
        revision_ids = self._scope_revision_ids(request)
        self._validate_retrieval_strategy(request)
        self._ensure_index()

        filters = request.source_filters
        file_ids = filters.file_ids if filters and filters.file_ids else None
        source_formats = filters.source_formats if filters and filters.source_formats else None
        top_k = (
            request.response_overrides.top_k
            if request.response_overrides and request.response_overrides.top_k is not None
            else self._default_top_k
        )
        evidence_min_score = self._evidence_min_score(request)
        rerank_enabled = bool(request.response_overrides and request.response_overrides.rerank_enabled)

        query_text = self._query_text(request, query_override=query_override)
        self._validate_embedding_model(request)
        try:
            query_embedding = self._embedding_adapter.embed(query_text)
        except EmbeddingProviderError as exc:
            raise RetrievalExecutionError(str(exc)) from exc
        try:
            matches = self._vector_store.search(
                query_text,
                query_embedding,
                top_k=max(top_k * 4, top_k + 3),
                file_ids=file_ids,
                source_formats=source_formats,
                revision_ids=revision_ids,
            )
        except ScopedRetrievalError as exc:
            raise KnowledgeScopeError(str(exc)) from exc
        except (httpx.HTTPError, ValueError) as exc:
            raise RetrievalExecutionError(f"native retrieval execution failed: {exc}") from exc

        if rerank_enabled:
            matches = self._lexical_rerank(query_text, matches)

        if scope:
            self._validate_scope_matches(matches, scope.revision_ids)
        evidence_matches = [match for match in matches if match.chunk.block_type != "heading"][:top_k]

        if not evidence_matches:
            return RetrievalResult(
                citations=[],
                context_text="",
                top_score=0.0,
                top_k=top_k,
                rerank_enabled=rerank_enabled,
                evidence_min_score=evidence_min_score,
                refusal_reason="retrieval_miss",
            )

        top_score = evidence_matches[0].score
        citations = [
            self._to_retrieved_source(index + 1, match, scope.snapshot_id if scope else None)
            for index, match in enumerate(evidence_matches)
        ]
        if top_score < evidence_min_score:
            return RetrievalResult(
                citations=citations[:1],
                context_text="",
                top_score=top_score,
                top_k=top_k,
                rerank_enabled=rerank_enabled,
                evidence_min_score=evidence_min_score,
                refusal_reason="retrieval_low_confidence",
            )

        return RetrievalResult(
            citations=citations,
            context_text=self._build_context_text(citations),
            top_score=top_score,
            top_k=top_k,
            rerank_enabled=rerank_enabled,
            evidence_min_score=evidence_min_score,
            refusal_reason=None,
        )

    @staticmethod
    def _query_text(request: ChatRequest, *, query_override: str | None = None) -> str:
        query = query_override or request.user_query
        source_formats = request.source_filters.source_formats if request.source_filters else None
        if source_formats == ["docx"] or _DOCX_QUERY_REFERENCE_RE.search(query):
            return build_docx_query_representation(query)
        return query

    @staticmethod
    def _scope_revision_ids(request: ChatRequest) -> list[str] | None:
        scope = request.knowledge_scope
        if scope is None:
            if request.answer_mode == "revalidate":
                raise KnowledgeScopeError("revalidation requires the latest published knowledge scope")
            raise KnowledgeScopeError("knowledge retrieval requires a complete immutable scope")
        if scope.snapshot_id != request.knowledge_snapshot_id or not scope.revision_ids:
            raise KnowledgeScopeError("Backend lifecycle knowledge scope is unavailable")
        return scope.revision_ids

    def _validate_embedding_model(self, request: ChatRequest) -> None:
        requested_model = request.response_overrides.embedding_model if request.response_overrides else None
        if (
            self._embedding_adapter.requires_config_model_match
            and requested_model != self._embedding_adapter.model_name
        ):
            raise RetrievalExecutionError(
                "retrieval config embedding model does not match the active Runtime embedding provider"
            )

    def _validate_retrieval_strategy(self, request: ChatRequest) -> None:
        if self._active_retrieval_strategy is None:
            return
        requested_strategy = request.response_overrides.retrieval_strategy if request.response_overrides else None
        if requested_strategy != self._active_retrieval_strategy:
            raise RetrievalExecutionError(
                "retrieval config strategy does not match the active Runtime retrieval profile"
            )

    def _evidence_min_score(self, request: ChatRequest) -> float:
        configured = request.response_overrides.evidence_min_score if request.response_overrides else None
        if self._active_retrieval_strategy == "native_dense_v1" and configured is None:
            raise RetrievalExecutionError(
                "native dense retrieval requires evidence_min_score from the immutable retrieval config"
            )
        return self._min_score if configured is None else configured

    @staticmethod
    def _validate_scope_matches(matches: list[SearchMatch], revision_ids: list[str]) -> None:
        allowed_revisions = set(revision_ids)
        for match in matches:
            chunk = match.chunk
            if chunk.revision_id not in allowed_revisions:
                raise KnowledgeScopeError("vector store returned a chunk outside the requested revision scope")
            if not chunk.document_id or not chunk.chunk_content_sha256:
                raise KnowledgeScopeError("vector store returned scoped content without immutable provenance")

    @staticmethod
    def _lexical_rerank(query_text: str, matches: list[SearchMatch]) -> list[SearchMatch]:
        """A deterministic K7 rerank stage; K8 may replace it with a provider-backed reranker."""
        terms = {term for term in re.findall(r"[a-z0-9]+", query_text.lower()) if len(term) > 1}
        if not terms:
            return matches
        return sorted(
            matches,
            key=lambda match: (
                sum(term in match.chunk.text.lower() for term in terms),
                match.score,
            ),
            reverse=True,
        )

    def _ensure_index(self) -> None:
        if self._index_ready:
            return

        for document in self._corpus_documents:
            parser = self._registry.get(document.source_format)
            parsed = parser.parse(
                IngestionRunRequest(
                    request_id=f"bootstrap-{document.file_id}",
                    task_id=f"task-{document.file_id}",
                    file_id=document.file_id,
                    filename=document.filename,
                    mime_type=document.mime_type,
                    source_format=document.source_format,
                    object_uri=document.object_uri,
                    document_id=document.document_id or None,
                    revision_id=document.revision_id or None,
                    checksum_sha256=document.checksum_sha256 or None,
                    delivery_id=document.delivery_id or None,
                    parse_version="parse_v1",
                    index_version="sample_index_v1",
                    requested_at=datetime.now(timezone.utc).isoformat(),
                )
            )
            parsed = replace(
                parsed,
                document_id=document.document_id or parsed.document_id,
                revision_id=document.revision_id or parsed.revision_id,
                checksum_sha256=document.checksum_sha256 or parsed.checksum_sha256,
            )
            for chunk in chunk_document(
                parsed,
                "sample_index_v1",
                embedding_model=self._embedding_adapter.model_name,
            ):
                self._vector_store.upsert(chunk, self._embedding_adapter.embed(chunk.text))

        self._index_ready = True
        if self._corpus_documents:
            logger.info("Runtime test fixture indexed %d explicit corpus documents", len(self._corpus_documents))

    @staticmethod
    def _build_context_text(citations: list[RetrievedSource]) -> str:
        return "\n\n".join(
            f"[{citation.rank}] file={citation.filename}\nlocator={citation.citation_label}\ntext={citation.snippet}"
            for citation in citations
        )

    @staticmethod
    def _to_retrieved_source(
        rank: int,
        match: SearchMatch,
        knowledge_snapshot_id: str | None,
    ) -> RetrievedSource:
        locator_payload = match.chunk.source_locator.to_dict()
        locator = SourceLocator(**locator_payload)
        return RetrievedSource(
            chunk_id=match.chunk.chunk_id,
            file_id=match.chunk.file_id,
            filename=match.chunk.filename,
            document_id=match.chunk.document_id,
            revision_id=match.chunk.revision_id,
            chunk_content_sha256=match.chunk.chunk_content_sha256,
            knowledge_snapshot_id=knowledge_snapshot_id,
            rank=rank,
            score=round(match.score, 4),
            citation_label=_format_citation_label(locator),
            source_locator=locator,
            snippet=_build_snippet(match.chunk.text),
        )


def build_default_corpus_documents() -> tuple[CorpusDocument, ...]:
    return (
        CorpusDocument(
            file_id="sample_employee_handbook_txt",
            filename="employee-handbook.txt",
            source_format="txt",
            mime_type="text/plain",
            object_uri=str(_SAMPLE_DOCS_ROOT / "employee-handbook.txt"),
            document_id="doc_test_fixture",
            revision_id="rev_test_fixture",
            checksum_sha256="sha256:test-fixture",
            delivery_id="delivery_test_fixture",
        ),
        CorpusDocument(
            file_id="sample_employee_handbook_md",
            filename="employee-handbook.md",
            source_format="md",
            mime_type="text/markdown",
            object_uri=str(_SAMPLE_DOCS_ROOT / "employee-handbook.md"),
            document_id="doc_test_fixture",
            revision_id="rev_test_fixture",
            checksum_sha256="sha256:test-fixture",
            delivery_id="delivery_test_fixture",
        ),
        CorpusDocument(
            file_id="sample_policy_catalog_json",
            filename="policy-catalog.json",
            source_format="json",
            mime_type="application/json",
            object_uri=str(_SAMPLE_DOCS_ROOT / "policy-catalog.json"),
            document_id="doc_test_fixture",
            revision_id="rev_test_fixture",
            checksum_sha256="sha256:test-fixture",
            delivery_id="delivery_test_fixture",
        ),
        CorpusDocument(
            file_id="sample_policy_pdf",
            filename="sample-policy.pdf",
            source_format="pdf",
            mime_type="application/pdf",
            object_uri=str(_SAMPLE_DOCS_ROOT / "sample-policy.pdf"),
            document_id="doc_test_fixture",
            revision_id="rev_test_fixture",
            checksum_sha256="sha256:test-fixture",
            delivery_id="delivery_test_fixture",
        ),
        CorpusDocument(
            file_id="sample_handbook_docx",
            filename="sample-handbook.docx",
            source_format="docx",
            mime_type="application/vnd.openxmlformats-officedocument.wordprocessingml.document",
            object_uri=str(_SAMPLE_DOCS_ROOT / "sample-handbook.docx"),
            document_id="doc_test_fixture",
            revision_id="rev_test_fixture",
            checksum_sha256="sha256:test-fixture",
            delivery_id="delivery_test_fixture",
        ),
        CorpusDocument(
            file_id="sample_budget_xlsx",
            filename="sample-budget.xlsx",
            source_format="xlsx",
            mime_type="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
            object_uri=str(_SAMPLE_DOCS_ROOT / "sample-budget.xlsx"),
            document_id="doc_test_fixture",
            revision_id="rev_test_fixture",
            checksum_sha256="sha256:test-fixture",
            delivery_id="delivery_test_fixture",
        ),
        CorpusDocument(
            file_id="sample_training_deck_pptx",
            filename="sample-training-deck.pptx",
            source_format="pptx",
            mime_type="application/vnd.openxmlformats-officedocument.presentationml.presentation",
            object_uri=str(_SAMPLE_DOCS_ROOT / "sample-training-deck.pptx"),
            document_id="doc_test_fixture",
            revision_id="rev_test_fixture",
            checksum_sha256="sha256:test-fixture",
            delivery_id="delivery_test_fixture",
        ),
    )


def _format_citation_label(locator: SourceLocator) -> str:
    if locator.page_no is not None:
        return f"Page {locator.page_no}"
    if locator.sheet_name and locator.cell_range:
        return f"{locator.sheet_name} · {locator.cell_range}"
    if locator.slide_no is not None:
        if locator.slide_title:
            return f"Slide {locator.slide_no} · {locator.slide_title}"
        return f"Slide {locator.slide_no}"
    if locator.heading_path:
        return " / ".join(locator.heading_path)
    if locator.json_path:
        return locator.json_path
    if locator.block_label:
        return locator.block_label
    return locator.source_format


def _build_snippet(text: str, limit: int = 220) -> str:
    normalized = " ".join(text.split())
    if len(normalized) <= limit:
        return normalized
    return normalized[: limit - 1].rstrip() + "…"
