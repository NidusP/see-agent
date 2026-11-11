from __future__ import annotations

import os
from collections.abc import Callable, Mapping
from dataclasses import dataclass

from app.adapters.embeddings import EmbeddingAdapter, LexicalEmbeddingAdapter, OpenAICompatibleEmbeddingAdapter
from app.adapters.vector_store import InMemoryVectorStore, QdrantVectorStore, VectorStore

NATIVE_DENSE_V1 = "native_dense_v1"


@dataclass(frozen=True)
class RetrievalStrategyProfile:
    strategy_id: str
    embedding_adapter: EmbeddingAdapter
    vector_store: VectorStore


def registered_strategy_ids() -> tuple[str, ...]:
    return (NATIVE_DENSE_V1,)


def resolve_active_strategy(environment: Mapping[str, str] | None = None) -> str:
    environment = os.environ if environment is None else environment
    configured = environment.get("RUNTIME_RETRIEVAL_STRATEGY", "").strip()
    legacy_mode = environment.get("RUNTIME_RETRIEVAL_MODE", "").strip()
    if legacy_mode:
        raise ValueError("RUNTIME_RETRIEVAL_MODE is retired; set RUNTIME_RETRIEVAL_STRATEGY=native_dense_v1")

    if configured:
        if configured not in registered_strategy_ids():
            raise ValueError(
                "RUNTIME_RETRIEVAL_STRATEGY must be native_dense_v1"
            )
        return configured

    return NATIVE_DENSE_V1


def build_default_retrieval_strategy_profile(
    *,
    environment: Mapping[str, str] | None = None,
    qdrant_store_factory: Callable[..., VectorStore] = QdrantVectorStore,
) -> RetrievalStrategyProfile:
    environment = os.environ if environment is None else environment
    strategy_id = resolve_active_strategy(environment)
    qdrant_url = environment.get("QDRANT_URL", "").strip()
    qdrant_timeout_seconds = float(environment.get("QDRANT_TIMEOUT_SECONDS", "5"))

    embedding_base_url = environment.get("EMBEDDING_BASE_URL", "").strip()
    embedding_model = environment.get("EMBEDDING_MODEL", "").strip()
    dimensions_raw = environment.get("EMBEDDING_DIMENSIONS", "").strip()
    if not qdrant_url or not embedding_base_url or not embedding_model or not dimensions_raw:
        profile = environment.get("SEE_AGENT_PROFILE", "").strip()
        if profile in {"test", "smoke"} and not environment.get("RUNTIME_RETRIEVAL_STRATEGY", "").strip():
            # Test-only fixture: product profiles cannot select it and it is
            # always reported as native_dense_v1, never as a lexical strategy.
            return RetrievalStrategyProfile(strategy_id, LexicalEmbeddingAdapter(), InMemoryVectorStore())
        raise ValueError(
            "native_dense retrieval requires QDRANT_URL, EMBEDDING_BASE_URL, EMBEDDING_MODEL, and EMBEDDING_DIMENSIONS"
        )
    try:
        dimensions = int(dimensions_raw)
    except ValueError as exc:
        raise ValueError("EMBEDDING_DIMENSIONS must be a positive integer") from exc
    if dimensions < 1:
        raise ValueError("EMBEDDING_DIMENSIONS must be a positive integer")

    collection_name = environment.get("QDRANT_COLLECTION", "see_agent_dense_qwen_chunks_v1").strip() or "see_agent_dense_qwen_chunks_v1"
    embedding_adapter = OpenAICompatibleEmbeddingAdapter(
        embedding_base_url,
        model_name=embedding_model,
        dimensions=dimensions,
        api_key=environment.get("EMBEDDING_API_KEY", "").strip() or None,
        timeout_seconds=float(environment.get("EMBEDDING_TIMEOUT_SECONDS", "15")),
    )
    return RetrievalStrategyProfile(
        strategy_id,
        embedding_adapter,
        qdrant_store_factory(
            qdrant_url,
            collection_name=collection_name,
            timeout_seconds=qdrant_timeout_seconds,
            native_dense=True,
            vector_size=dimensions,
        ),
    )
