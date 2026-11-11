from __future__ import annotations

import json
from collections.abc import Iterator

from fastapi import APIRouter
from fastapi.encoders import jsonable_encoder
from fastapi.responses import JSONResponse, StreamingResponse

from app.api.schemas import ChatRequest, ChatResponse
from app.adapters.model_client import ModelClientError
from app.pipelines.chat_pipeline import ChatPipeline
from app.pipelines.retrieval_pipeline import KnowledgeScopeError, RetrievalExecutionError


def build_chat_router(chat_pipeline: ChatPipeline) -> APIRouter:
    router = APIRouter()

    @router.post("/v1/chat/run", response_model=ChatResponse)
    def run_chat(payload: ChatRequest) -> ChatResponse | JSONResponse:
        try:
            return chat_pipeline.execute(payload)
        except (KnowledgeScopeError, RetrievalExecutionError) as exc:
            return _scope_error_response(exc)
        except ModelClientError as exc:
            return _model_error_response(exc)

    @router.post("/v1/chat/stream")
    def stream_chat(payload: ChatRequest) -> StreamingResponse:
        return StreamingResponse(
            _stream_events(chat_pipeline, payload),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache, no-transform"},
        )

    return router


def _stream_events(chat_pipeline: ChatPipeline, payload: ChatRequest) -> Iterator[str]:
    try:
        for item in chat_pipeline.stream(payload):
            yield _encode_sse(item.event, item.payload)
    except (KnowledgeScopeError, RetrievalExecutionError, ModelClientError) as exc:
        yield _encode_sse(
            "message.error",
            {
                "request_id": payload.request_id,
                "message_id": payload.message_id,
                "error": {
                    "code": getattr(exc, "code", "model_stream_failed"),
                    "message": str(exc),
                    "retryable": getattr(exc, "retryable", True),
                },
            },
        )


def _encode_sse(event: str, payload: object) -> str:
    return f"event: {event}\ndata: {json.dumps(jsonable_encoder(payload), ensure_ascii=False)}\n\n"


def _scope_error_response(exc: KnowledgeScopeError) -> JSONResponse:
    return JSONResponse(
        status_code=503,
        content={
            "error": {
                "code": exc.code,
                "message": str(exc),
                "retryable": exc.retryable,
            }
        },
    )


def _model_error_response(exc: ModelClientError) -> JSONResponse:
    return JSONResponse(
        status_code=503,
        content={
            "error": {
                "code": exc.code,
                "message": str(exc),
                "retryable": exc.retryable,
            }
        },
    )
