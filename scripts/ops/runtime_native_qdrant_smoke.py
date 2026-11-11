from __future__ import annotations

import os
import sys
import uuid
from pathlib import Path

import httpx


ROOT = Path(__file__).resolve().parents[2]
RUNTIME_ROOT = ROOT / "services" / "ai-runtime-py"
sys.path.insert(0, str(RUNTIME_ROOT))

from app.adapters.vector_store import QdrantVectorStore
from app.domain.models import ChunkRecord, SourceLocatorData


def main() -> int:
    qdrant_url = os.environ.get("QDRANT_URL", "http://127.0.0.1:16333").rstrip("/")
    collection_name = os.environ.get("QDRANT_COLLECTION") or f"see_agent_native_qdrant_smoke_{uuid.uuid4().hex}"
    cleanup_collection(qdrant_url, collection_name)
    try:
        store = QdrantVectorStore(
            qdrant_url,
            collection_name=collection_name,
            native_dense=True,
            vector_size=3,
        )
        allowed = _chunk("rev_native_allowed", "The approved native dense restart code is 48152.")
        excluded = _chunk("rev_native_excluded", "The retired native dense restart code is 99999.")
        store.replace_revision(allowed.revision_id or "", [(allowed, [1.0, 0.0, 0.0])])
        store.replace_revision(excluded.revision_id or "", [(excluded, [0.0, 1.0, 0.0])])

        matches = store.search(
            "native dense restart code",
            [1.0, 0.0, 0.0],
            top_k=3,
            revision_ids=[allowed.revision_id or ""],
        )
        assert [(match.chunk.revision_id, match.chunk.text) for match in matches] == [
            ("rev_native_allowed", "The approved native dense restart code is 48152.")
        ]
        assert store.delete_by_revision_id("rev_native_excluded") == 1
        print(f"runtime native Qdrant smoke ok: collection={collection_name}")
        return 0
    finally:
        cleanup_collection(qdrant_url, collection_name)


def _chunk(revision_id: str, text: str) -> ChunkRecord:
    return ChunkRecord(
        chunk_id=f"{revision_id}:chunk-1",
        file_id=f"file_{revision_id}",
        document_id=f"doc_{revision_id}",
        filename=f"{revision_id}.txt",
        source_format="txt",
        block_type="paragraph",
        text=text,
        token_count=len(text.split()),
        heading_path=[],
        source_locator=SourceLocatorData(source_format="txt", block_label="paragraph-1"),
        embedding_model="static-native-smoke",
        index_version="native_smoke_v1",
        revision_id=revision_id,
        checksum_sha256=f"sha256:{revision_id}",
        parse_version="parse_v1",
        chunk_content_sha256=f"sha256:{revision_id}:chunk-1",
        visibility="revision",
    )


def cleanup_collection(qdrant_url: str, collection_name: str) -> None:
    try:
        httpx.delete(f"{qdrant_url}/collections/{collection_name}", timeout=5.0, trust_env=False)
    except httpx.HTTPError:
        pass


if __name__ == "__main__":
    raise SystemExit(main())
