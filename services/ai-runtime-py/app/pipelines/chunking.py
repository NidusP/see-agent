from __future__ import annotations

import hashlib
import json

from app.domain.models import ChunkRecord, NormalizedDocument
from app.parsers.common import count_tokens


def chunk_document(
    document: NormalizedDocument,
    index_version: str,
    *,
    embedding_model: str,
) -> list[ChunkRecord]:
    chunks: list[ChunkRecord] = []
    for block in document.blocks:
        if not block.text.strip():
            continue
        chunk_content_sha256 = _sha256(block.text)
        chunk_id = _chunk_id(
            document,
            block.block_id,
            block.source_locator.to_dict(),
            chunk_content_sha256,
            embedding_model,
        )
        chunks.append(
            ChunkRecord(
                chunk_id=chunk_id,
                file_id=document.file_id,
                document_id=document.document_id,
                filename=document.filename,
                source_format=document.source_format,
                block_type=block.block_type,
                text=block.text,
                token_count=count_tokens(block.text),
                heading_path=block.heading_path.copy(),
                source_locator=block.source_locator,
                embedding_model=embedding_model,
                index_version=index_version,
                revision_id=document.revision_id,
                checksum_sha256=document.checksum_sha256,
                parse_version=document.parse_version,
                chunk_content_sha256=chunk_content_sha256,
                visibility="revision" if document.revision_id else "legacy",
            )
        )
    return chunks


def chunk_manifest_sha256(chunks: list[ChunkRecord]) -> str:
    """Return the stable manifest Backend uses to verify a revision projection."""
    entries = [
        {
            "chunk_id": chunk.chunk_id,
            "chunk_content_sha256": chunk.chunk_content_sha256,
            "checksum_sha256": chunk.checksum_sha256,
            "document_id": chunk.document_id,
            "embedding_model": chunk.embedding_model,
            "index_version": chunk.index_version,
            "parse_version": chunk.parse_version,
            "revision_id": chunk.revision_id,
            "source_locator": chunk.source_locator.to_dict(),
        }
        for chunk in chunks
    ]
    canonical = json.dumps(sorted(entries, key=lambda entry: entry["chunk_id"]), sort_keys=True, separators=(",", ":"))
    return f"sha256:{_sha256(canonical)[7:]}"


def _chunk_id(
    document: NormalizedDocument,
    block_id: str,
    source_locator: dict[str, object],
    chunk_content_sha256: str,
    embedding_model: str,
) -> str:
    if not document.revision_id:
        return f"{document.file_id}:{block_id}"

    canonical = json.dumps(
        {
            "block_id": block_id,
            "checksum_sha256": document.checksum_sha256,
            "chunk_content_sha256": chunk_content_sha256,
            "document_id": document.document_id,
            "embedding_model": embedding_model,
            "index_version": document.index_version,
            "parse_version": document.parse_version,
            "revision_id": document.revision_id,
            "source_locator": source_locator,
        },
        sort_keys=True,
        separators=(",", ":"),
    )
    return f"revision:{document.revision_id}:{_sha256(canonical)[7:]}"


def _sha256(value: str) -> str:
    return f"sha256:{hashlib.sha256(value.encode('utf-8')).hexdigest()}"
