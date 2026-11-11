from __future__ import annotations

from pathlib import Path

from app.adapters.embeddings import LexicalEmbeddingAdapter
from app.adapters.vector_store import InMemoryVectorStore
from app.api.schemas import IngestionOptions, IngestionRunRequest
from app.domain.models import NormalizedBlock, NormalizedDocument, SourceLocatorData
from app.parsers.pdf_parser import PdfTextParser
from app.parsers.registry import ParserRegistry, build_default_registry
from app.pipelines.chunking import chunk_document, chunk_manifest_sha256
from app.pipelines.ingestion_pipeline import IngestionPipeline


def test_revision_retry_is_idempotent_and_does_not_delete_other_revisions(tmp_path: Path) -> None:
    source = tmp_path / "deployment.txt"
    source.write_text("Deployment schedule\n\nThe approved rollout window is Monday at 09:00.", encoding="utf-8")
    store = InMemoryVectorStore()
    pipeline = _pipeline(store)

    revision_a = _request(source, revision_id="rev_deployment_a", delivery_id="delivery_a")
    revision_b = _request(source, revision_id="rev_deployment_b", delivery_id="delivery_b")
    first = pipeline.execute(revision_a)
    second = pipeline.execute(revision_a)
    other = pipeline.execute(revision_b)
    retry = pipeline.execute(revision_a)

    assert first.status == second.status == other.status == retry.status == "succeeded"
    assert first.chunk_manifest_sha256 == second.chunk_manifest_sha256 == retry.chunk_manifest_sha256
    assert first.embedding_model == "baseline-lexical-embedder"
    assert first.document_id == "doc_deployment"
    assert first.revision_id == "rev_deployment_a"
    assert first.delivery_id == "delivery_a"

    revision_ids = {item.chunk.revision_id for item in store._items_by_chunk_id.values()}
    assert revision_ids == {"rev_deployment_a", "rev_deployment_b"}
    assert store.delete_by_revision_id("rev_deployment_a") == 2
    assert {item.chunk.revision_id for item in store._items_by_chunk_id.values()} == {"rev_deployment_b"}


def test_revision_chunk_ids_and_manifest_change_only_with_immutable_projection_inputs(tmp_path: Path) -> None:
    source = tmp_path / "policy.txt"
    source.write_text("Policy\n\nThe reimbursement limit is 900.", encoding="utf-8")
    store = InMemoryVectorStore()
    pipeline = _pipeline(store)

    original = pipeline.execute(_request(source, revision_id="rev_policy_1", delivery_id="delivery_1"))
    same_projection = pipeline.execute(_request(source, revision_id="rev_policy_1", delivery_id="delivery_2"))
    changed_index = pipeline.execute(
        _request(source, revision_id="rev_policy_1", delivery_id="delivery_3", index_version="index_v3")
    )

    assert original.chunk_manifest_sha256 == same_projection.chunk_manifest_sha256
    assert changed_index.chunk_manifest_sha256 != original.chunk_manifest_sha256
    chunks = list(store._items_by_chunk_id.values())
    assert {item.chunk.revision_id for item in chunks} == {"rev_policy_1"}
    assert all(item.chunk.chunk_id.startswith("revision:rev_policy_1:") for item in chunks)
    assert all(item.chunk.chunk_content_sha256 and item.chunk.chunk_content_sha256.startswith("sha256:") for item in chunks)
    assert all(item.chunk.visibility == "revision" for item in chunks)


def test_revision_chunk_manifest_identity_includes_embedding_model() -> None:
    document = NormalizedDocument(
        document_id="doc_deployment",
        file_id="file_deployment",
        filename="deployment.txt",
        source_format="txt",
        title="Deployment",
        metadata={},
        blocks=[
            NormalizedBlock(
                block_id="block-1",
                document_id="doc_deployment",
                block_type="paragraph",
                text="The approved rollout window is Monday at 09:00.",
                order_index=0,
                source_locator=SourceLocatorData(source_format="txt", block_label="paragraph-1"),
            )
        ],
        revision_id="rev_deployment_a",
        checksum_sha256="sha256:deployment-content",
        parse_version="parse_v2",
        index_version="index_v2",
    )

    lexical = chunk_document(document, "index_v2", embedding_model="baseline-lexical-embedder")
    upgraded = chunk_document(document, "index_v2", embedding_model="bge-m3-v1")

    assert lexical[0].chunk_id != upgraded[0].chunk_id
    assert chunk_manifest_sha256(lexical) != chunk_manifest_sha256(upgraded)


def test_revision_projection_failure_returns_retryable_identity_without_manifest(tmp_path: Path) -> None:
    source = tmp_path / "failure.txt"
    source.write_text("Failure test\n\nThe vector store is unavailable.", encoding="utf-8")
    pipeline = _pipeline(_FailingRevisionStore())

    response = pipeline.execute(_request(source, revision_id="rev_failure", delivery_id="delivery_failure"))

    assert response.status == "failed"
    assert response.document_id == "doc_deployment"
    assert response.revision_id == "rev_failure"
    assert response.delivery_id == "delivery_failure"
    assert response.chunk_manifest_sha256 is None
    assert response.embedding_model is None
    assert response.error is not None
    assert response.error.code == "index_projection_failure"
    assert response.error.retryable is True


def test_zero_chunk_revision_fails_without_vector_projection(tmp_path: Path) -> None:
    source = tmp_path / "empty.txt"
    source.write_text("\n\t  \n", encoding="utf-8")
    store = InMemoryVectorStore()

    response = _pipeline(store).execute(_request(source, revision_id="rev_empty", delivery_id="delivery_empty"))

    assert response.status == "failed"
    assert response.chunks_created == 0
    assert response.error is not None
    assert response.error.code == "no_indexable_content"
    assert response.error.retryable is False
    assert store._items_by_chunk_id == {}


def test_textless_pdf_with_requested_ocr_fails_explicitly(tmp_path: Path) -> None:
    source = tmp_path / "scan.pdf"
    source.write_bytes(b"not-used-by-test-parser")
    store = InMemoryVectorStore()
    registry = ParserRegistry()
    registry.register(_TextlessPdfParser())
    pipeline = IngestionPipeline(registry, LexicalEmbeddingAdapter(), store)
    request = IngestionRunRequest(
        request_id="request_pdf_ocr",
        task_id="task_pdf_ocr",
        file_id="file_pdf_ocr",
        document_id="doc_pdf_ocr",
        revision_id="rev_pdf_ocr",
        delivery_id="delivery_pdf_ocr",
        attempt=1,
        filename=source.name,
        mime_type="application/pdf",
        source_format="pdf",
        object_uri=str(source),
        checksum_sha256="sha256:pdf-ocr",
        parse_version="parse_v2",
        index_version="index_v2",
        requested_at="2026-07-14T00:00:00Z",
        ingestion_options=IngestionOptions(enable_ocr_fallback=True),
    )

    response = pipeline.execute(request)

    assert response.status == "failed"
    assert response.error is not None
    assert response.error.code == "ocr_unavailable"
    assert response.error.retryable is False
    assert store._items_by_chunk_id == {}


class _FailingRevisionStore(InMemoryVectorStore):
    def replace_revision(self, revision_id, indexed_chunks) -> None:
        raise RuntimeError("Qdrant revision cleanup failed")


class _TextlessPdfParser(PdfTextParser):
    def parse(self, request: IngestionRunRequest) -> NormalizedDocument:
        return NormalizedDocument(
            document_id="doc_pdf_ocr",
            file_id=request.file_id,
            filename=request.filename,
            source_format="pdf",
            title=request.filename,
            metadata={"page_count": 1},
            blocks=[],
        )


def _pipeline(store: InMemoryVectorStore) -> IngestionPipeline:
    return IngestionPipeline(build_default_registry(), LexicalEmbeddingAdapter(), store)


def _request(
    source: Path,
    *,
    revision_id: str,
    delivery_id: str,
    index_version: str = "index_v2",
) -> IngestionRunRequest:
    return IngestionRunRequest(
        request_id=f"request_{delivery_id}",
        task_id=f"task_{delivery_id}",
        file_id="file_deployment",
        document_id="doc_deployment",
        revision_id=revision_id,
        delivery_id=delivery_id,
        attempt=1,
        filename=source.name,
        mime_type="text/plain",
        source_format="txt",
        object_uri=str(source),
        checksum_sha256="sha256:deployment-content",
        parse_version="parse_v2",
        index_version=index_version,
        requested_at="2026-07-14T00:00:00Z",
    )
