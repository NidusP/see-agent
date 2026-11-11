from __future__ import annotations

from dataclasses import replace
from datetime import datetime, timezone

from app.adapters.embeddings import EmbeddingAdapter, EmbeddingProviderError
from app.adapters.object_store import ObjectResolver
from app.adapters.vector_store import VectorStore
from app.api.schemas import ErrorSummary, IngestionRunRequest, IngestionRunResponse
from app.parsers.base import ParserError
from app.parsers.registry import ParserRegistry
from app.pipelines.chunking import chunk_document, chunk_manifest_sha256


class IngestionPipeline:
    def __init__(
        self,
        registry: ParserRegistry,
        embedding_adapter: EmbeddingAdapter,
        vector_store: VectorStore,
        object_resolver: ObjectResolver | None = None,
    ) -> None:
        self._registry = registry
        self._embedding_adapter = embedding_adapter
        self._vector_store = vector_store
        self._object_resolver = object_resolver or ObjectResolver()

    def execute(self, request: IngestionRunRequest) -> IngestionRunResponse:
        started_at = datetime.now(timezone.utc).isoformat()
        parser = self._registry.get(request.source_format)
        warnings: list[str] = []

        try:
            with self._object_resolver.local_path(request.object_uri, request.filename) as local_path:
                parse_request = request.model_copy(update={"object_uri": str(local_path)})
                document = parser.parse(parse_request)
            document = _with_ingestion_identity(document, request)
            if request.source_format == "pdf" and not document.blocks and _ocr_fallback_requested(request):
                return _failed_response(
                    request,
                    started_at,
                    warnings,
                    code="ocr_unavailable",
                    message="PDF has no extractable text layer and no OCR provider is configured",
                    retryable=False,
                )
            chunks = chunk_document(
                document,
                request.index_version,
                embedding_model=self._embedding_adapter.model_name,
            )
        except ParserError as exc:
            return _failed_response(
                request,
                started_at,
                warnings,
                code="parse_failure",
                message=str(exc),
                retryable=False,
            )
        except Exception as exc:  # pragma: no cover - exercised through integration failure cases
            return _failed_response(
                request,
                started_at,
                warnings,
                code="unexpected_parse_failure",
                message=str(exc),
                retryable=False,
            )

        try:
            indexed_chunks = [
                (chunk, self._embedding_adapter.embed(chunk.text))
                for chunk in chunks
            ]
            if not indexed_chunks:
                return _failed_response(
                    request,
                    started_at,
                    warnings,
                    code="no_indexable_content",
                    message="parser completed but produced no indexable content",
                    retryable=False,
                )
            if request.revision_id:
                self._vector_store.replace_revision(request.revision_id, indexed_chunks)
            else:
                self._vector_store.delete_by_file_id(request.file_id)
                for chunk, embedding in indexed_chunks:
                    self._vector_store.upsert(chunk, embedding)
        except EmbeddingProviderError as exc:
            return _failed_response(
                request,
                started_at,
                warnings,
                code="embedding_provider_failure",
                message=str(exc),
                retryable=True,
            )
        except Exception as exc:
            return _failed_response(
                request,
                started_at,
                warnings,
                code="index_projection_failure",
                message=str(exc),
                retryable=True,
            )

        metadata = document.metadata
        completed_at = datetime.now(timezone.utc).isoformat()
        return IngestionRunResponse(
            request_id=request.request_id,
            task_id=request.task_id,
            file_id=request.file_id,
            document_id=request.document_id,
            revision_id=request.revision_id,
            delivery_id=request.delivery_id,
            status="succeeded",
            updated_at=completed_at,
            started_at=started_at,
            completed_at=completed_at,
            chunks_created=len(chunks),
            pages_processed=metadata.get("page_count") or metadata.get("slide_count"),
            chunk_manifest_sha256=chunk_manifest_sha256(chunks) if request.revision_id else None,
            embedding_model=self._embedding_adapter.model_name,
            warning_messages=warnings or None,
            error=None,
        )


def _with_ingestion_identity(document, request: IngestionRunRequest):
    """Keep parser output compatible while giving revision runs immutable identity."""
    document_id = request.document_id or document.document_id
    blocks = [replace(block, document_id=document_id) for block in document.blocks]
    metadata = {
        **document.metadata,
        "checksum_sha256": request.checksum_sha256,
        "index_version": request.index_version,
        "parse_version": request.parse_version,
    }
    if request.revision_id:
        metadata["revision_id"] = request.revision_id
    return replace(
        document,
        document_id=document_id,
        blocks=blocks,
        metadata=metadata,
        revision_id=request.revision_id,
        checksum_sha256=request.checksum_sha256,
        parse_version=request.parse_version,
        index_version=request.index_version,
    )


def _ocr_fallback_requested(request: IngestionRunRequest) -> bool:
    """OCR is not implemented in the product pipeline; never silently ignore an OCR request."""
    return request.ingestion_options is None or request.ingestion_options.enable_ocr_fallback


def _failed_response(
    request: IngestionRunRequest,
    started_at: str,
    warnings: list[str],
    *,
    code: str,
    message: str,
    retryable: bool,
) -> IngestionRunResponse:
    completed_at = datetime.now(timezone.utc).isoformat()
    return IngestionRunResponse(
        request_id=request.request_id,
        task_id=request.task_id,
        file_id=request.file_id,
        document_id=request.document_id,
        revision_id=request.revision_id,
        delivery_id=request.delivery_id,
        status="failed",
        updated_at=completed_at,
        started_at=started_at,
        completed_at=completed_at,
        chunks_created=0,
        pages_processed=None,
        chunk_manifest_sha256=None,
        embedding_model=None,
        warning_messages=warnings or None,
        error=ErrorSummary(code=code, message=message, retryable=retryable),
    )
