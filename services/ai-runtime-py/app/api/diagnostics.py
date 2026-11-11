from __future__ import annotations

from fastapi import APIRouter

from app.adapters.model_client import ModelClient
from app.model_readiness import GenerationReadiness
from app.pipelines.retrieval_pipeline import RetrievalPipeline


def build_diagnostics_router(
    retrieval_pipeline: RetrievalPipeline,
    model_client: ModelClient,
    generation_readiness: GenerationReadiness | None = None,
) -> APIRouter:
    """Private configuration facts for Backend/operations; never response evidence."""
    router = APIRouter()

    @router.get("/v1/diagnostics/capabilities")
    def capabilities() -> dict[str, object]:
        strategy, embedding_model, collection = retrieval_pipeline.execution_profile()
        warmup = generation_readiness.status().state if generation_readiness is not None else "unknown"
        return {
            "service": "ai-runtime",
            "retrieval": {
                "strategy": strategy,
                "embedding_model": embedding_model,
                "collection": collection,
            },
            "generation": {"provider": getattr(model_client, "provider_name", "unknown"), "warmup": warmup},
        }

    return router
