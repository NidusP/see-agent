from __future__ import annotations

from collections.abc import Iterator

from app.api.schemas import ChatRequest, ChatResponse
from app.pipelines.answer_pipeline import AnswerPipeline, ChatStreamEvent


class ChatPipeline:
    def __init__(self, answer_pipeline: AnswerPipeline) -> None:
        self._answer_pipeline = answer_pipeline

    def execute(self, request: ChatRequest) -> ChatResponse:
        return self._answer_pipeline.execute(request)

    def stream(self, request: ChatRequest) -> Iterator[ChatStreamEvent]:
        return self._answer_pipeline.stream(request)
