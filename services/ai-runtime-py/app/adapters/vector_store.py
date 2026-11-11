from __future__ import annotations

from dataclasses import dataclass
from threading import RLock
from typing import Any, Protocol
from uuid import NAMESPACE_URL, uuid5

import httpx

from app.adapters.embeddings import EmbeddingVector, cosine_similarity, tokenize_text
from app.domain.models import ChunkRecord, SourceLocatorData


@dataclass(frozen=True)
class SearchMatch:
    chunk: ChunkRecord
    score: float


@dataclass(frozen=True)
class IndexedChunk:
    chunk: ChunkRecord
    embedding: EmbeddingVector
    tokens: frozenset[str]
    normalized_text: str


class VectorStore(Protocol):
    def upsert(self, chunk: ChunkRecord, embedding: EmbeddingVector) -> None:
        pass

    def delete_by_file_id(self, file_id: str) -> int:
        pass

    def delete_by_revision_id(self, revision_id: str) -> int:
        pass

    def replace_revision(
        self,
        revision_id: str,
        indexed_chunks: list[tuple[ChunkRecord, EmbeddingVector]],
    ) -> None:
        pass

    def search(
        self,
        query_text: str,
        query_embedding: EmbeddingVector,
        *,
        top_k: int,
        file_ids: list[str] | None = None,
        source_formats: list[str] | None = None,
        revision_ids: list[str] | None = None,
    ) -> list[SearchMatch]:
        pass


class ScopedRetrievalError(RuntimeError):
    """A revision-scoped query could not be executed safely."""


class InMemoryVectorStore(VectorStore):
    collection_name = "memory_test"

    def __init__(self) -> None:
        self._items_by_chunk_id: dict[str, IndexedChunk] = {}
        self._lock = RLock()

    def upsert(self, chunk: ChunkRecord, embedding: EmbeddingVector) -> None:
        with self._lock:
            self._items_by_chunk_id[chunk.chunk_id] = IndexedChunk(
                chunk=chunk,
                embedding=embedding,
                tokens=frozenset(embedding.keys()) if isinstance(embedding, dict) else frozenset(),
                normalized_text=chunk.text.lower(),
            )

    def delete_by_file_id(self, file_id: str) -> int:
        with self._lock:
            chunk_ids = [
                chunk_id
                for chunk_id, item in self._items_by_chunk_id.items()
                if item.chunk.file_id == file_id
            ]
            for chunk_id in chunk_ids:
                del self._items_by_chunk_id[chunk_id]
            return len(chunk_ids)

    def delete_by_revision_id(self, revision_id: str) -> int:
        with self._lock:
            chunk_ids = [
                chunk_id
                for chunk_id, item in self._items_by_chunk_id.items()
                if item.chunk.revision_id == revision_id
            ]
            for chunk_id in chunk_ids:
                del self._items_by_chunk_id[chunk_id]
            return len(chunk_ids)

    def replace_revision(
        self,
        revision_id: str,
        indexed_chunks: list[tuple[ChunkRecord, EmbeddingVector]],
    ) -> None:
        _validate_revision_entries(revision_id, indexed_chunks)
        replacements = {
            chunk.chunk_id: IndexedChunk(
                chunk=chunk,
                embedding=embedding,
                tokens=frozenset(embedding.keys()) if isinstance(embedding, dict) else frozenset(),
                normalized_text=chunk.text.lower(),
            )
            for chunk, embedding in indexed_chunks
        }
        with self._lock:
            stale_chunk_ids = [
                chunk_id
                for chunk_id, item in self._items_by_chunk_id.items()
                if item.chunk.revision_id == revision_id
            ]
            for chunk_id in stale_chunk_ids:
                del self._items_by_chunk_id[chunk_id]
            self._items_by_chunk_id.update(replacements)

    def search(
        self,
        query_text: str,
        query_embedding: EmbeddingVector,
        *,
        top_k: int,
        file_ids: list[str] | None = None,
        source_formats: list[str] | None = None,
        revision_ids: list[str] | None = None,
    ) -> list[SearchMatch]:
        _validate_revision_scope(revision_ids)
        query_tokens = set(tokenize_text(query_text))
        results: list[SearchMatch] = []

        with self._lock:
            indexed_items = list(self._items_by_chunk_id.values())

        for item in indexed_items:
            if file_ids and item.chunk.file_id not in file_ids:
                continue
            if source_formats and item.chunk.source_format not in source_formats:
                continue
            if revision_ids and item.chunk.revision_id not in revision_ids:
                continue

            cosine = cosine_similarity(query_embedding, item.embedding)
            score = cosine
            if isinstance(query_embedding, dict) and isinstance(item.embedding, dict):
                overlap = len(query_tokens & item.tokens)
                exact_hits = sum(
                    1
                    for token in query_tokens
                    if len(token) >= 2 and token.lower() in item.normalized_text
                )
                score += (0.03 * overlap) + (0.02 * exact_hits)
            if item.chunk.block_type == "heading":
                score -= 0.02

            if score <= 0:
                continue

            results.append(SearchMatch(chunk=item.chunk, score=score))

        results.sort(key=lambda item: item.score, reverse=True)
        return results[:top_k]


class QdrantVectorStore(VectorStore):
    def __init__(
        self,
        base_url: str,
        *,
        collection_name: str = "see_agent_chunks",
        timeout_seconds: float = 5.0,
        native_dense: bool = False,
        vector_size: int | None = None,
        client: httpx.Client | None = None,
    ) -> None:
        if native_dense and (vector_size is None or vector_size < 1):
            raise ValueError("native dense Qdrant retrieval requires a positive vector size")
        self._collection_name = collection_name
        self._native_dense = native_dense
        self._vector_size = vector_size if native_dense else 1
        self._client = client or httpx.Client(
            base_url=base_url.rstrip("/"),
            timeout=timeout_seconds,
            trust_env=False,
        )
        self._ensure_collection()

    @property
    def collection_name(self) -> str:
        return self._collection_name

    def upsert(self, chunk: ChunkRecord, embedding: EmbeddingVector) -> None:
        self._upsert_many([(chunk, embedding)])

    def delete_by_file_id(self, file_id: str) -> int:
        return self._delete_by_filter("file_id", file_id)

    def delete_by_revision_id(self, revision_id: str) -> int:
        return self._delete_by_filter("revision_id", revision_id)

    def replace_revision(
        self,
        revision_id: str,
        indexed_chunks: list[tuple[ChunkRecord, EmbeddingVector]],
    ) -> None:
        _validate_revision_entries(revision_id, indexed_chunks)
        # A retry only removes this immutable revision. Other revisions may be
        # indexed concurrently and are deliberately outside this filter.
        self.delete_by_revision_id(revision_id)
        if indexed_chunks:
            self._upsert_many(indexed_chunks)

    def search(
        self,
        query_text: str,
        query_embedding: EmbeddingVector,
        *,
        top_k: int,
        file_ids: list[str] | None = None,
        source_formats: list[str] | None = None,
        revision_ids: list[str] | None = None,
    ) -> list[SearchMatch]:
        _validate_revision_scope(revision_ids)
        if self._native_dense:
            return self._native_dense_search(
                query_embedding,
                top_k=top_k,
                file_ids=file_ids,
                source_formats=source_formats,
                revision_ids=revision_ids,
            )
        query_tokens = set(tokenize_text(query_text))
        results: list[SearchMatch] = []

        try:
            payloads = self._scroll_payloads(file_ids=file_ids, revision_ids=revision_ids)
        except (httpx.HTTPError, KeyError, TypeError, ValueError) as exc:
            if revision_ids:
                raise ScopedRetrievalError("Qdrant revision filter execution failed") from exc
            raise

        for payload in payloads:
            if revision_ids:
                _validate_scoped_payload(payload, revision_ids)
            try:
                indexed = _indexed_chunk_from_payload(payload)
            except (KeyError, TypeError, ValueError) as exc:
                if revision_ids:
                    raise ScopedRetrievalError("Qdrant returned an invalid scoped payload") from exc
                raise
            if source_formats and indexed.chunk.source_format not in source_formats:
                continue

            cosine = cosine_similarity(query_embedding, indexed.embedding)
            overlap = len(query_tokens & indexed.tokens)
            exact_hits = sum(
                1
                for token in query_tokens
                if len(token) >= 2 and token.lower() in indexed.normalized_text
            )

            score = cosine + (0.03 * overlap) + (0.02 * exact_hits)
            if indexed.chunk.block_type == "heading":
                score -= 0.02
            if score <= 0:
                continue

            results.append(SearchMatch(chunk=indexed.chunk, score=score))

        results.sort(key=lambda item: item.score, reverse=True)
        return results[:top_k]

    def _native_dense_search(
        self,
        query_embedding: EmbeddingVector,
        *,
        top_k: int,
        file_ids: list[str] | None,
        source_formats: list[str] | None,
        revision_ids: list[str] | None,
    ) -> list[SearchMatch]:
        if not isinstance(query_embedding, list) or len(query_embedding) != self._vector_size:
            raise ValueError("native dense Qdrant query received an incompatible embedding vector")
        body: dict[str, Any] = {
            "query": query_embedding,
            "limit": top_k,
            "with_payload": True,
            "with_vector": False,
        }
        query_filter = _search_filter(
            file_ids=file_ids,
            source_formats=source_formats,
            revision_ids=revision_ids,
        )
        if query_filter is not None:
            body["filter"] = query_filter
        try:
            response = self._client.post(f"/collections/{self._collection_name}/points/query", json=body)
            response.raise_for_status()
            result = response.json().get("result", {})
            points = result.get("points", result if isinstance(result, list) else [])
        except (httpx.HTTPError, AttributeError, KeyError, TypeError, ValueError) as exc:
            if revision_ids:
                raise ScopedRetrievalError("Qdrant native revision query execution failed") from exc
            raise

        matches: list[SearchMatch] = []
        for point in points:
            payload = point.get("payload") or {}
            if revision_ids:
                _validate_scoped_payload(payload, revision_ids)
            try:
                chunk = _chunk_from_payload(payload)
                score = float(point["score"])
            except (KeyError, TypeError, ValueError) as exc:
                if revision_ids:
                    raise ScopedRetrievalError("Qdrant native query returned an invalid scoped payload") from exc
                raise
            if file_ids and chunk.file_id not in file_ids:
                continue
            if source_formats and chunk.source_format not in source_formats:
                continue
            matches.append(SearchMatch(chunk=chunk, score=score))
        return matches

    def _ensure_collection(self) -> None:
        existing = self._client.get(f"/collections/{self._collection_name}")
        if existing.status_code == 404:
            response = self._client.put(
                f"/collections/{self._collection_name}",
                json={
                    "vectors": {
                        "size": self._vector_size,
                        "distance": "Cosine",
                    }
                },
            )
            response.raise_for_status()
        elif existing.status_code != 200:
            existing.raise_for_status()
        elif self._native_dense:
            self._validate_native_collection(existing.json())

        for field_name in ("document_id", "revision_id", "visibility", "file_id", "source_format", "embedding_model"):
            response = self._client.put(
                f"/collections/{self._collection_name}/index",
                json={"field_name": field_name, "field_schema": "keyword"},
            )
            response.raise_for_status()

    def _upsert_many(self, indexed_chunks: list[tuple[ChunkRecord, EmbeddingVector]]) -> None:
        response = self._client.put(
            f"/collections/{self._collection_name}/points",
            params={"wait": "true"},
            json={
                "points": [
                    {
                        "id": _point_id(chunk.chunk_id),
                        "vector": embedding if self._native_dense else [1.0],
                        "payload": _chunk_payload(chunk, embedding),
                    }
                    for chunk, embedding in indexed_chunks
                ]
            },
        )
        response.raise_for_status()

    def _delete_by_filter(self, field_name: str, value: str) -> int:
        if self._native_dense:
            return self._native_dense_delete_by_filter(field_name, value)
        existing = self._scroll_payloads(**{f"{field_name}s": [value]})
        response = self._client.post(
            f"/collections/{self._collection_name}/points/delete",
            params={"wait": "true"},
            json={"filter": _keyword_filter(field_name, value)},
        )
        response.raise_for_status()
        return len(existing)

    def _native_dense_delete_by_filter(self, field_name: str, value: str) -> int:
        query_filter = _keyword_filter(field_name, value)
        count_response = self._client.post(
            f"/collections/{self._collection_name}/points/count",
            json={"filter": query_filter, "exact": True},
        )
        count_response.raise_for_status()
        try:
            count = int(count_response.json()["result"]["count"])
        except (KeyError, TypeError, ValueError):
            raise ValueError("Qdrant native dense delete count response was invalid") from None
        response = self._client.post(
            f"/collections/{self._collection_name}/points/delete",
            params={"wait": "true"},
            json={"filter": query_filter},
        )
        response.raise_for_status()
        return count

    def _scroll_payloads(
        self,
        *,
        file_ids: list[str] | None = None,
        revision_ids: list[str] | None = None,
    ) -> list[dict[str, Any]]:
        payloads: list[dict[str, Any]] = []
        offset: str | int | None = None

        while True:
            body: dict[str, Any] = {
                "limit": 256,
                "with_payload": True,
                "with_vector": False,
            }
            if offset is not None:
                body["offset"] = offset
            query_filter = _search_filter(file_ids=file_ids, revision_ids=revision_ids)
            if query_filter is not None:
                body["filter"] = query_filter

            response = self._client.post(f"/collections/{self._collection_name}/points/scroll", json=body)
            response.raise_for_status()
            result = response.json().get("result", {})
            for point in result.get("points", []):
                point_payload = point.get("payload") or {}
                if file_ids and point_payload.get("file_id") not in file_ids:
                    continue
                if revision_ids:
                    _validate_scoped_payload(point_payload, revision_ids)
                payloads.append(point_payload)

            offset = result.get("next_page_offset")
            if offset is None:
                return payloads

    def _validate_native_collection(self, payload: dict[str, Any]) -> None:
        try:
            vectors = payload["result"]["config"]["params"]["vectors"]
            size = vectors["size"] if isinstance(vectors, dict) else None
            distance = vectors["distance"] if isinstance(vectors, dict) else None
        except (KeyError, TypeError):
            raise ValueError("existing Qdrant collection configuration could not be inspected") from None
        if size != self._vector_size or str(distance).lower() != "cosine":
            raise ValueError(
                "existing Qdrant collection is incompatible with native dense retrieval; "
                "configure a new collection and reindex immutable revisions"
            )


def _point_id(chunk_id: str) -> str:
    return str(uuid5(NAMESPACE_URL, f"see-agent:{chunk_id}"))


def _keyword_filter(field_name: str, value: str) -> dict[str, Any]:
    return {"must": [{"key": field_name, "match": {"value": value}}]}


def _search_filter(
    *,
    file_ids: list[str] | None,
    source_formats: list[str] | None = None,
    revision_ids: list[str] | None = None,
) -> dict[str, Any] | None:
    must: list[dict[str, Any]] = []
    if file_ids:
        must.append(
            _keyword_filter("file_id", file_ids[0])["must"][0]
            if len(file_ids) == 1
            else _keyword_any_condition("file_id", file_ids)
        )
    if revision_ids:
        must.append(_keyword_any_condition("revision_id", revision_ids))
    if source_formats:
        must.append(
            _keyword_filter("source_format", source_formats[0])["must"][0]
            if len(source_formats) == 1
            else _keyword_any_condition("source_format", source_formats)
        )
    return {"must": must} if must else None


def _keyword_any_condition(field_name: str, values: list[str]) -> dict[str, Any]:
    return {"key": field_name, "match": {"any": values}}


def _validate_revision_scope(revision_ids: list[str] | None) -> None:
    if revision_ids is not None and not revision_ids:
        raise ScopedRetrievalError("revision-scoped retrieval requires a nonempty revision allow list")


def _validate_scoped_payload(payload: dict[str, Any], revision_ids: list[str]) -> None:
    revision_id = payload.get("revision_id")
    if not isinstance(revision_id, str) or not revision_id:
        raise ScopedRetrievalError("Qdrant scoped payload is missing revision_id")
    if revision_id not in revision_ids:
        raise ScopedRetrievalError("Qdrant returned a point outside the requested revision scope")
    for field_name in ("document_id", "chunk_content_sha256"):
        value = payload.get(field_name)
        if not isinstance(value, str) or not value:
            raise ScopedRetrievalError(f"Qdrant scoped payload is missing {field_name}")


def _validate_revision_entries(
    revision_id: str,
    indexed_chunks: list[tuple[ChunkRecord, EmbeddingVector]],
) -> None:
    invalid = [chunk.chunk_id for chunk, _ in indexed_chunks if chunk.revision_id != revision_id]
    if invalid:
        raise ValueError(f"revision replace received chunks outside revision {revision_id}: {invalid}")


def _chunk_payload(chunk: ChunkRecord, embedding: EmbeddingVector) -> dict[str, Any]:
    payload: dict[str, Any] = {
        "chunk_id": chunk.chunk_id,
        "file_id": chunk.file_id,
        "document_id": chunk.document_id,
        "revision_id": chunk.revision_id,
        "checksum_sha256": chunk.checksum_sha256,
        "chunk_content_sha256": chunk.chunk_content_sha256,
        "visibility": chunk.visibility,
        "filename": chunk.filename,
        "source_format": chunk.source_format,
        "block_type": chunk.block_type,
        "text": chunk.text,
        "token_count": chunk.token_count,
        "heading_path": chunk.heading_path,
        "source_locator": chunk.source_locator.to_dict(),
        "embedding_model": chunk.embedding_model,
        "parse_version": chunk.parse_version,
        "index_version": chunk.index_version,
        "normalized_text": chunk.text.lower(),
    }
    if isinstance(embedding, dict):
        payload["embedding"] = embedding
        payload["tokens"] = list(embedding.keys())
    return payload


def _chunk_from_payload(payload: dict[str, Any]) -> ChunkRecord:
    source_locator_payload = payload.get("source_locator") or {"source_format": payload.get("source_format", "txt")}
    source_locator = SourceLocatorData(**source_locator_payload)
    return ChunkRecord(
        chunk_id=str(payload["chunk_id"]),
        file_id=str(payload["file_id"]),
        document_id=str(payload["document_id"]),
        filename=str(payload["filename"]),
        source_format=str(payload["source_format"]),
        block_type=str(payload["block_type"]),
        text=str(payload["text"]),
        token_count=int(payload["token_count"]),
        heading_path=list(payload.get("heading_path") or []),
        source_locator=source_locator,
        embedding_model=str(payload["embedding_model"]),
        index_version=str(payload["index_version"]),
        revision_id=_optional_string(payload.get("revision_id")),
        checksum_sha256=_optional_string(payload.get("checksum_sha256")),
        parse_version=_optional_string(payload.get("parse_version")),
        chunk_content_sha256=_optional_string(payload.get("chunk_content_sha256")),
        visibility=str(payload.get("visibility") or "legacy"),
    )


def _indexed_chunk_from_payload(payload: dict[str, Any]) -> IndexedChunk:
    embedding = {
        str(token): float(weight)
        for token, weight in (payload.get("embedding") or {}).items()
    }
    chunk = _chunk_from_payload(payload)
    return IndexedChunk(
        chunk=chunk,
        embedding=embedding,
        tokens=frozenset(payload.get("tokens") or embedding.keys()),
        normalized_text=str(payload.get("normalized_text") or chunk.text.lower()),
    )


def _optional_string(value: object) -> str | None:
    return str(value) if value is not None else None
