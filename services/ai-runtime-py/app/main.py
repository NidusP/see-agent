from __future__ import annotations

import hmac
import logging
import os
import time
from collections.abc import Callable
from contextlib import asynccontextmanager

import httpx

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from app.adapters.embeddings import EmbeddingAdapter
from app.adapters.model_client import ModelClient, build_default_model_client
from app.adapters.object_store import build_default_object_resolver
from app.adapters.vector_store import InMemoryVectorStore, QdrantVectorStore, VectorStore
from app.api.chat import build_chat_router
from app.api.diagnostics import build_diagnostics_router
from app.api.health import build_health_router
from app.api.ingestion import build_ingestion_router
from app.model_readiness import GenerationReadiness, warm_up_generation_model
from app.parsers.registry import build_default_registry
from app.pipelines.answer_pipeline import AnswerPipeline
from app.pipelines.chat_pipeline import ChatPipeline
from app.pipelines.ingestion_pipeline import IngestionPipeline
from app.pipelines.preview_pipeline import PreviewPipeline
from app.pipelines.retrieval_pipeline import CorpusDocument, RetrievalPipeline
from app.retrieval_strategies import RetrievalStrategyProfile, build_default_retrieval_strategy_profile
from app.startup_diagnostics import log_runtime_startup_diagnostics


_RUNTIME_SERVICE_TOKEN_PLACEHOLDER = "replace-with-local-dev-secret"
_RUNTIME_SERVICE_TOKEN_MIN_LENGTH = 32


def _validate_runtime_service_token(name: str, token: str) -> None:
    if not token:
        raise ValueError(f"{name} must be configured with a non-empty secret")
    if token == _RUNTIME_SERVICE_TOKEN_PLACEHOLDER:
        raise ValueError(f"{name} must not use the repository placeholder")
    if len(token) < _RUNTIME_SERVICE_TOKEN_MIN_LENGTH:
        raise ValueError(f"{name} must be at least {_RUNTIME_SERVICE_TOKEN_MIN_LENGTH} characters")


def validate_runtime_service_token_policy() -> None:
    current = os.getenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN", "").strip()
    _validate_runtime_service_token("SEE_AGENT_RUNTIME_SERVICE_TOKEN", current)

    previous = os.getenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS", "").strip()
    if not previous:
        return
    _validate_runtime_service_token("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS", previous)
    if hmac.compare_digest(previous, current):
        raise ValueError("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS must differ from SEE_AGENT_RUNTIME_SERVICE_TOKEN")


def validate_runtime_profile() -> None:
    profile = os.getenv("SEE_AGENT_PROFILE", "").strip()
    if profile in {"", "test", "smoke"}:
        return
    if profile not in {"fullstack_dev", "production"}:
        raise ValueError("SEE_AGENT_PROFILE must be test, smoke, fullstack_dev, or production")
    validate_runtime_service_token_policy()
    required = (
        "SEE_AGENT_RUNTIME_SERVICE_TOKEN",
        "QDRANT_URL",
        "QDRANT_COLLECTION",
        "RUNTIME_RETRIEVAL_STRATEGY",
        "LOCAL_LLM_BASE_URL",
        "LOCAL_LLM_DEFAULT_MODEL_ID",
        "OBJECT_STORE_ENDPOINT",
        "OBJECT_STORE_BUCKET",
        "OBJECT_STORE_ACCESS_KEY",
        "OBJECT_STORE_SECRET_KEY",
    )
    missing = [key for key in required if not os.getenv(key, "").strip()]
    if missing:
        raise ValueError(f"{profile} requires {', '.join(missing)}")


def build_default_retrieval_dependencies() -> tuple[EmbeddingAdapter, VectorStore]:
    profile = build_default_retrieval_strategy_profile(
        qdrant_store_factory=QdrantVectorStore,
    )
    return profile.embedding_adapter, profile.vector_store


def build_default_retrieval_profile() -> RetrievalStrategyProfile:
    return build_default_retrieval_strategy_profile(
        qdrant_store_factory=QdrantVectorStore,
    )


def build_runtime_readiness_check(generation_readiness: GenerationReadiness | None = None) -> Callable[[], tuple[bool, str]]:
    """Probe owned dependencies with a short cache to avoid health-check fanout."""
    cache: tuple[float, tuple[bool, str]] | None = None

    def check() -> tuple[bool, str]:
        nonlocal cache
        now = time.monotonic()
        if cache is not None and now - cache[0] < 2.0:
            return cache[1]
        profile = os.getenv("SEE_AGENT_PROFILE", "").strip()
        if profile not in {"fullstack_dev", "production"}:
            cache = (now, (True, ""))
            return cache[1]
        probes = (
            ("qdrant", os.getenv("QDRANT_URL", "").rstrip("/") + "/healthz"),
            ("model_provider", os.getenv("LOCAL_LLM_BASE_URL", "").rstrip("/") + "/models"),
            ("object_store", os.getenv("OBJECT_STORE_ENDPOINT", "").rstrip("/") + "/minio/health/live"),
        )
        for dependency, url in probes:
            if not url or url.startswith("/"):
                cache = (now, (False, f"{dependency} configuration is missing"))
                return cache[1]
            try:
                response = httpx.get(url, timeout=1.5, trust_env=False)
                response.raise_for_status()
            except httpx.HTTPError:
                cache = (now, (False, f"{dependency} is unavailable"))
                return cache[1]
        if generation_readiness is not None and not generation_readiness.is_ready():
            status = generation_readiness.status()
            cache = (now, (False, f"generation warm-up is {status.state}"))
            return cache[1]
        cache = (now, (True, ""))
        return cache[1]

    return check


def create_app(
    *, model_client_override: ModelClient | None = None, corpus_documents_override: tuple[CorpusDocument, ...] | None = None
) -> FastAPI:
    validate_runtime_profile()
    registry = build_default_registry()
    preview_pipeline = PreviewPipeline(registry)
    retrieval_profile = build_default_retrieval_profile()
    embedding_adapter, vector_store = retrieval_profile.embedding_adapter, retrieval_profile.vector_store
    object_resolver = build_default_object_resolver()
    ingestion_pipeline = IngestionPipeline(registry, embedding_adapter, vector_store, object_resolver)
    retrieval_pipeline = RetrievalPipeline(
        registry,
        embedding_adapter,
        vector_store,
        active_retrieval_strategy=retrieval_profile.strategy_id,
        corpus_documents=corpus_documents_override,
    )
    model_client = model_client_override or build_default_model_client()
    generation_readiness = GenerationReadiness(
        getattr(model_client, "provider_name", "unknown"),
        os.getenv("LOCAL_LLM_DEFAULT_MODEL_ID", "").strip() or "unset",
    )

    @asynccontextmanager
    async def lifespan(_: FastAPI):
        profile = os.getenv("SEE_AGENT_PROFILE", "").strip()
        if profile not in {"fullstack_dev", "production"}:
            generation_readiness.mark_not_required()
        else:
            warm_up_generation_model(model_client, generation_readiness)
        yield

    runtime_app = FastAPI(title="see-agent ai runtime", version="0.3.0", lifespan=lifespan)

    @runtime_app.middleware("http")
    async def require_backend_service_token(request: Request, call_next):
        if not request.url.path.startswith(("/v1/chat/", "/v1/ingestion/", "/v1/diagnostics/")):
            return await call_next(request)
        current = os.getenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN", "").strip()
        previous = os.getenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS", "").strip()
        if not current and not previous:
            return await call_next(request)
        authorization = request.headers.get("authorization", "")
        token = authorization.removeprefix("Bearer ") if authorization.startswith("Bearer ") else ""
        if not any(hmac.compare_digest(token, candidate) for candidate in (current, previous) if candidate):
            return JSONResponse(status_code=401, content={"error": {"code": "runtime_service_authentication_failed", "message": "Runtime service authentication failed", "retryable": False}})
        return await call_next(request)
    answer_pipeline = AnswerPipeline(retrieval_pipeline, model_client)
    chat_pipeline = ChatPipeline(answer_pipeline)

    runtime_app.include_router(build_health_router(build_runtime_readiness_check(generation_readiness)))
    runtime_app.include_router(build_diagnostics_router(retrieval_pipeline, model_client, generation_readiness))
    runtime_app.include_router(build_ingestion_router(preview_pipeline, ingestion_pipeline))
    runtime_app.include_router(build_chat_router(chat_pipeline))

    return runtime_app


app = create_app()
log_runtime_startup_diagnostics(logging.getLogger("uvicorn.error"))
