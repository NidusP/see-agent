from __future__ import annotations

import pytest

from app import main as runtime_main
from app.adapters.embeddings import OpenAICompatibleEmbeddingAdapter
from app.retrieval_strategies import NATIVE_DENSE_V1, resolve_active_strategy


def test_native_dense_mode_requires_complete_fail_closed_configuration(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("RUNTIME_RETRIEVAL_STRATEGY", NATIVE_DENSE_V1)
    monkeypatch.delenv("RUNTIME_RETRIEVAL_MODE", raising=False)
    monkeypatch.delenv("QDRANT_URL", raising=False)
    monkeypatch.delenv("EMBEDDING_BASE_URL", raising=False)
    monkeypatch.delenv("EMBEDDING_MODEL", raising=False)
    monkeypatch.delenv("EMBEDDING_DIMENSIONS", raising=False)

    with pytest.raises(ValueError, match="requires QDRANT_URL"):
        runtime_main.build_default_retrieval_dependencies()


def test_native_dense_mode_builds_a_dedicated_dense_qdrant_store(monkeypatch: pytest.MonkeyPatch) -> None:
    created: dict[str, object] = {}

    class StubStore:
        def __init__(self, url: str, **kwargs: object) -> None:
            created["url"] = url
            created.update(kwargs)

    monkeypatch.setenv("RUNTIME_RETRIEVAL_STRATEGY", NATIVE_DENSE_V1)
    monkeypatch.delenv("RUNTIME_RETRIEVAL_MODE", raising=False)
    monkeypatch.setenv("QDRANT_URL", "http://qdrant.test")
    monkeypatch.setenv("EMBEDDING_BASE_URL", "http://embedding.test/v1")
    monkeypatch.setenv("EMBEDDING_MODEL", "embeddinggemma")
    monkeypatch.setenv("EMBEDDING_DIMENSIONS", "768")
    monkeypatch.delenv("QDRANT_COLLECTION", raising=False)
    monkeypatch.setattr(runtime_main, "QdrantVectorStore", StubStore)

    embedding_adapter, vector_store = runtime_main.build_default_retrieval_dependencies()

    assert isinstance(embedding_adapter, OpenAICompatibleEmbeddingAdapter)
    assert embedding_adapter.model_name == "embeddinggemma"
    assert isinstance(vector_store, StubStore)
    assert created == {
        "url": "http://qdrant.test",
            "collection_name": "see_agent_dense_qwen_chunks_v1",
        "timeout_seconds": 5.0,
        "native_dense": True,
        "vector_size": 768,
    }


def test_strategy_registry_accepts_only_native_dense_and_rejects_retired_legacy_mode() -> None:
    assert resolve_active_strategy({"RUNTIME_RETRIEVAL_STRATEGY": NATIVE_DENSE_V1}) == NATIVE_DENSE_V1
    assert resolve_active_strategy({}) == NATIVE_DENSE_V1

    with pytest.raises(ValueError, match="must be native_dense_v1"):
        resolve_active_strategy({"RUNTIME_RETRIEVAL_STRATEGY": "untrusted_python_plugin"})
    with pytest.raises(ValueError, match="is retired"):
        resolve_active_strategy({"RUNTIME_RETRIEVAL_MODE": "lexical"})
