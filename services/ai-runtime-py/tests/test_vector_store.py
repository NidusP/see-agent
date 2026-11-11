from __future__ import annotations

import json
from typing import Any

import httpx
import pytest

from app.adapters.embeddings import LexicalEmbeddingAdapter
from app.adapters.vector_store import QdrantVectorStore
from app.domain.models import ChunkRecord, SourceLocatorData


def test_qdrant_vector_store_upsert_search_and_delete() -> None:
    points: dict[str, dict[str, Any]] = {}
    collection_exists = False
    payload_indexes: list[str] = []

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal collection_exists

        if request.method == "GET" and request.url.path == "/collections/test_chunks":
            return httpx.Response(200 if collection_exists else 404, json={"result": {}})

        if request.method == "PUT" and request.url.path == "/collections/test_chunks":
            collection_exists = True
            return httpx.Response(200, json={"result": True})

        if request.method == "PUT" and request.url.path == "/collections/test_chunks/index":
            body = json.loads(request.content)
            payload_indexes.append(body["field_name"])
            assert body["field_schema"] == "keyword"
            return httpx.Response(200, json={"result": True})

        if request.method == "PUT" and request.url.path == "/collections/test_chunks/points":
            body = json.loads(request.content)
            for point in body["points"]:
                points[str(point["id"])] = point["payload"]
            return httpx.Response(200, json={"result": {"operation_id": 1}})

        if request.method == "POST" and request.url.path == "/collections/test_chunks/points/scroll":
            body = json.loads(request.content)
            filter_key, filter_value = _keyword_filter_value(body.get("filter"))
            payloads = [
                {"id": point_id, "payload": payload}
                for point_id, payload in points.items()
                if filter_key is None or payload.get(filter_key) == filter_value
            ]
            return httpx.Response(200, json={"result": {"points": payloads, "next_page_offset": None}})

        if request.method == "POST" and request.url.path == "/collections/test_chunks/points/delete":
            body = json.loads(request.content)
            filter_key, filter_value = _keyword_filter_value(body.get("filter"))
            for point_id, payload in list(points.items()):
                if payload.get(filter_key) == filter_value:
                    del points[point_id]
            return httpx.Response(200, json={"result": {"operation_id": 2}})

        return httpx.Response(500, json={"error": f"unhandled {request.method} {request.url.path}"})

    client = httpx.Client(transport=httpx.MockTransport(handler), base_url="http://qdrant.test")
    store = QdrantVectorStore("http://qdrant.test", collection_name="test_chunks", client=client)
    embedder = LexicalEmbeddingAdapter()
    assert payload_indexes == ["document_id", "revision_id", "visibility", "file_id", "source_format", "embedding_model"]
    chunk = ChunkRecord(
        chunk_id="file_zeta:block_1",
        file_id="file_zeta",
        document_id="doc_zeta",
        filename="zeta-vault.txt",
        source_format="txt",
        block_type="paragraph",
        text="The Zeta vault emergency drill passcode is 73491.",
        token_count=8,
        heading_path=[],
        source_locator=SourceLocatorData(source_format="txt", block_label="paragraph-1"),
        embedding_model=embedder.model_name,
        index_version="index_v1",
    )

    store.upsert(chunk, embedder.embed(chunk.text))
    matches = store.search(
        "What is the Zeta vault emergency drill passcode?",
        embedder.embed("What is the Zeta vault emergency drill passcode?"),
        top_k=3,
        file_ids=["file_zeta"],
    )

    assert len(matches) == 1
    assert matches[0].chunk.chunk_id == chunk.chunk_id
    assert matches[0].score > 0
    assert store.delete_by_file_id("file_zeta") == 1
    assert store.search("Zeta passcode", embedder.embed("Zeta passcode"), top_k=3) == []


def test_qdrant_revision_replace_is_isolated_and_carries_projection_payload() -> None:
    points: dict[str, dict[str, Any]] = {}
    deleted_filters: list[dict[str, Any]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        if request.method == "GET" and request.url.path == "/collections/test_revisions":
            return httpx.Response(200, json={"result": {}})
        if request.method == "PUT" and request.url.path == "/collections/test_revisions/index":
            return httpx.Response(200, json={"result": True})
        if request.method == "PUT" and request.url.path == "/collections/test_revisions/points":
            for point in json.loads(request.content)["points"]:
                points[str(point["id"])] = point["payload"]
            return httpx.Response(200, json={"result": {"operation_id": 1}})
        if request.method == "POST" and request.url.path == "/collections/test_revisions/points/scroll":
            filter_key, filter_value = _keyword_filter_value(json.loads(request.content).get("filter"))
            return httpx.Response(
                200,
                json={
                    "result": {
                        "points": [
                            {"id": point_id, "payload": payload}
                            for point_id, payload in points.items()
                            if filter_key is None or payload.get(filter_key) == filter_value
                        ],
                        "next_page_offset": None,
                    }
                },
            )
        if request.method == "POST" and request.url.path == "/collections/test_revisions/points/delete":
            filter_payload = json.loads(request.content)["filter"]
            deleted_filters.append(filter_payload)
            filter_key, filter_value = _keyword_filter_value(filter_payload)
            for point_id, payload in list(points.items()):
                if payload.get(filter_key) == filter_value:
                    del points[point_id]
            return httpx.Response(200, json={"result": {"operation_id": 2}})
        return httpx.Response(500, json={"error": f"unhandled {request.method} {request.url.path}"})

    client = httpx.Client(transport=httpx.MockTransport(handler), base_url="http://qdrant.test")
    store = QdrantVectorStore("http://qdrant.test", collection_name="test_revisions", client=client)
    embedder = LexicalEmbeddingAdapter()
    revision_a = _revision_chunk("rev_alpha", "Alpha deployment window is Monday.")
    revision_b = _revision_chunk("rev_beta", "Beta deployment window is Tuesday.")

    store.replace_revision("rev_alpha", [(revision_a, embedder.embed(revision_a.text))])
    first_point_ids = set(points)
    store.replace_revision("rev_beta", [(revision_b, embedder.embed(revision_b.text))])
    store.replace_revision("rev_alpha", [(revision_a, embedder.embed(revision_a.text))])

    assert set(points) == first_point_ids | {
        point_id for point_id, payload in points.items() if payload["revision_id"] == "rev_beta"
    }
    assert {payload["revision_id"] for payload in points.values()} == {"rev_alpha", "rev_beta"}
    alpha_payload = next(payload for payload in points.values() if payload["revision_id"] == "rev_alpha")
    assert alpha_payload["document_id"] == "doc_deployment"
    assert alpha_payload["checksum_sha256"] == "sha256:alpha-content"
    assert alpha_payload["chunk_content_sha256"].startswith("sha256:")
    assert alpha_payload["parse_version"] == "parse_v2"
    assert alpha_payload["index_version"] == "index_v2"
    assert alpha_payload["visibility"] == "revision"
    assert all(
        filter_payload == {"must": [{"key": "revision_id", "match": {"value": "rev_alpha"}}]}
        for filter_payload in deleted_filters[::2]
    )


def test_qdrant_native_dense_search_uses_query_endpoint_with_server_side_scope_filters() -> None:
    created_collection: dict[str, Any] = {}
    indexed_fields: list[str] = []
    upserted_points: list[dict[str, Any]] = []
    query_calls = 0

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal query_calls
        if request.method == "GET" and request.url.path == "/collections/native_dense":
            return httpx.Response(404, json={"result": {}})
        if request.method == "PUT" and request.url.path == "/collections/native_dense":
            created_collection.update(json.loads(request.content))
            return httpx.Response(200, json={"result": True})
        if request.method == "PUT" and request.url.path == "/collections/native_dense/index":
            indexed_fields.append(json.loads(request.content)["field_name"])
            return httpx.Response(200, json={"result": True})
        if request.method == "PUT" and request.url.path == "/collections/native_dense/points":
            upserted_points.extend(json.loads(request.content)["points"])
            return httpx.Response(200, json={"result": {"operation_id": 1}})
        if request.method == "POST" and request.url.path == "/collections/native_dense/points/query":
            query_calls += 1
            body = json.loads(request.content)
            assert body["query"] == [0.1, 0.2, 0.3]
            assert body["limit"] == 2
            assert {condition["key"] for condition in body["filter"]["must"]} == {
                "file_id",
                "revision_id",
                "source_format",
            }
            return httpx.Response(
                200,
                json={
                    "result": {
                        "points": [
                            {"id": "point-1", "score": 0.91, "payload": upserted_points[0]["payload"]}
                        ]
                    }
                },
            )
        if request.method == "POST" and request.url.path.endswith("/points/scroll"):
            raise AssertionError("native dense query must not scroll payloads")
        return httpx.Response(500, json={"error": f"unhandled {request.method} {request.url.path}"})

    client = httpx.Client(transport=httpx.MockTransport(handler), base_url="http://qdrant.test")
    store = QdrantVectorStore(
        "http://qdrant.test",
        collection_name="native_dense",
        native_dense=True,
        vector_size=3,
        client=client,
    )
    chunk = _revision_chunk("rev_dense", "Dense retrieval keeps revision scoped evidence.")
    store.upsert(chunk, [0.2, 0.3, 0.4])

    matches = store.search(
        "query",
        [0.1, 0.2, 0.3],
        top_k=2,
        file_ids=[chunk.file_id],
        source_formats=["txt"],
        revision_ids=["rev_dense"],
    )

    assert created_collection == {"vectors": {"size": 3, "distance": "Cosine"}}
    assert indexed_fields == ["document_id", "revision_id", "visibility", "file_id", "source_format", "embedding_model"]
    assert upserted_points[0]["vector"] == [0.2, 0.3, 0.4]
    assert "embedding" not in upserted_points[0]["payload"]
    assert query_calls == 1
    assert [(match.chunk.revision_id, match.score) for match in matches] == [("rev_dense", 0.91)]


def test_qdrant_native_dense_rejects_an_incompatible_existing_collection() -> None:
    client = httpx.Client(
        transport=httpx.MockTransport(
            lambda request: httpx.Response(
                200,
                json={"result": {"config": {"params": {"vectors": {"size": 1, "distance": "Cosine"}}}}},
            )
            if request.method == "GET"
            else httpx.Response(500, json={"error": "unexpected mutation"})
        ),
        base_url="http://qdrant.test",
    )

    with pytest.raises(ValueError, match="incompatible"):
        QdrantVectorStore(
            "http://qdrant.test",
            collection_name="legacy_lexical",
            native_dense=True,
            vector_size=768,
            client=client,
        )


def test_qdrant_native_dense_revision_delete_uses_filtered_count_not_scroll() -> None:
    requests: list[tuple[str, str, dict[str, Any]]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        body = json.loads(request.content) if request.content else {}
        requests.append((request.method, request.url.path, body))
        if request.method == "GET" and request.url.path == "/collections/native_dense_delete":
            return httpx.Response(404, json={"result": {}})
        if request.method == "PUT" and request.url.path in {
            "/collections/native_dense_delete",
            "/collections/native_dense_delete/index",
        }:
            return httpx.Response(200, json={"result": True})
        if request.method == "POST" and request.url.path == "/collections/native_dense_delete/points/count":
            assert body == {
                "filter": {"must": [{"key": "revision_id", "match": {"value": "rev_dense"}}]},
                "exact": True,
            }
            return httpx.Response(200, json={"result": {"count": 2}})
        if request.method == "POST" and request.url.path == "/collections/native_dense_delete/points/delete":
            return httpx.Response(200, json={"result": {"operation_id": 1}})
        if request.url.path.endswith("/points/scroll"):
            raise AssertionError("native dense revision delete must not scroll payloads")
        return httpx.Response(500, json={"error": f"unhandled {request.method} {request.url.path}"})

    client = httpx.Client(transport=httpx.MockTransport(handler), base_url="http://qdrant.test")
    store = QdrantVectorStore(
        "http://qdrant.test",
        collection_name="native_dense_delete",
        native_dense=True,
        vector_size=3,
        client=client,
    )

    assert store.delete_by_revision_id("rev_dense") == 2
    assert ("POST", "/collections/native_dense_delete/points/count") == requests[-2][:2]
    assert ("POST", "/collections/native_dense_delete/points/delete") == requests[-1][:2]


def _revision_chunk(revision_id: str, text: str) -> ChunkRecord:
    return ChunkRecord(
        chunk_id=f"revision:{revision_id}:chunk-1",
        file_id="file_deployment",
        document_id="doc_deployment",
        filename="deployment.txt",
        source_format="txt",
        block_type="paragraph",
        text=text,
        token_count=len(text.split()),
        heading_path=[],
        source_locator=SourceLocatorData(source_format="txt", block_label="paragraph-1"),
        embedding_model="baseline-lexical-embedder",
        index_version="index_v2",
        revision_id=revision_id,
        checksum_sha256=f"sha256:{revision_id.removeprefix('rev_')}-content",
        parse_version="parse_v2",
        chunk_content_sha256="sha256:chunk-content",
        visibility="revision",
    )


def _keyword_filter_value(filter_payload: dict[str, Any] | None) -> tuple[str | None, Any | None]:
    if not filter_payload:
        return None, None
    for item in filter_payload.get("must", []):
        if item.get("key"):
            return item["key"], item.get("match", {}).get("value")
    return None, None
