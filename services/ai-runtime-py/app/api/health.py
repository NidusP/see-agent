from __future__ import annotations

from datetime import datetime, timezone
from collections.abc import Callable

from fastapi import APIRouter
from fastapi.responses import JSONResponse

from app.api.schemas import HealthResponse


ReadinessCheck = Callable[[], tuple[bool, str]]


def build_health_router(readiness_check: ReadinessCheck | None = None) -> APIRouter:
    router = APIRouter()

    @router.get("/healthz", response_model=HealthResponse)
    def healthcheck() -> HealthResponse:
        return HealthResponse(
            status="ok",
            service="ai-runtime",
            timestamp=datetime.now(timezone.utc).isoformat(),
        )

    @router.get("/readyz", response_model=None)
    def readycheck() -> HealthResponse | JSONResponse:
        ready, reason = readiness_check() if readiness_check is not None else (True, "")
        if not ready:
            return JSONResponse(status_code=503, content={"status": "not_ready", "service": "ai-runtime", "reason": reason})
        return HealthResponse(status="ready", service="ai-runtime", timestamp=datetime.now(timezone.utc).isoformat())

    return router


router = build_health_router()
