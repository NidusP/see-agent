from __future__ import annotations

from app.api.schemas import IngestionPreviewRequest, IngestionPreviewResponse
from app.parsers.registry import ParserRegistry


class PreviewPipeline:
    def __init__(self, registry: ParserRegistry) -> None:
        self._registry = registry

    def execute(self, request: IngestionPreviewRequest) -> IngestionPreviewResponse:
        parser = self._registry.get(request.source_format)
        descriptor = parser.preview(request)
        return IngestionPreviewResponse(**descriptor.__dict__)
