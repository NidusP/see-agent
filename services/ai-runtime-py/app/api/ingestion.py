from __future__ import annotations

from fastapi import APIRouter

from app.api.schemas import (
    IngestionPreviewRequest,
    IngestionPreviewResponse,
    IngestionRunRequest,
    IngestionRunResponse,
)
from app.pipelines.ingestion_pipeline import IngestionPipeline
from app.pipelines.preview_pipeline import PreviewPipeline


def build_ingestion_router(
    preview_pipeline: PreviewPipeline,
    ingestion_pipeline: IngestionPipeline,
) -> APIRouter:
    router = APIRouter()

    @router.post("/v1/ingestion/preview", response_model=IngestionPreviewResponse)
    def preview_ingestion(payload: IngestionPreviewRequest) -> IngestionPreviewResponse:
        return preview_pipeline.execute(payload)

    @router.post("/v1/ingestion/run", response_model=IngestionRunResponse)
    def run_ingestion(payload: IngestionRunRequest) -> IngestionRunResponse:
        return ingestion_pipeline.execute(payload)

    return router
