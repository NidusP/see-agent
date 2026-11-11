from __future__ import annotations

import json

from app.api.schemas import IngestionPreviewRequest, IngestionRunRequest
from app.domain.models import NormalizedBlock, NormalizedDocument, PreviewDescriptor, SourceLocatorData
from app.parsers.base import DocumentParser, require_local_path, resolve_object_uri
from app.parsers.common import flatten_json, make_document_id


class JsonStructureParser(DocumentParser):
    source_format = "json"
    parser_strategy = "json_flattener"
    locator_fields = ("json_path",)

    def preview(self, request: IngestionPreviewRequest) -> PreviewDescriptor:
        resolved = resolve_object_uri(request.object_uri)
        metadata = {}
        blocks = None
        if resolved.path is not None:
            payload = json.loads(resolved.path.read_text(encoding="utf-8"))
            entries = flatten_json(payload)
            metadata = {
                "top_level_keys": list(payload.keys()) if isinstance(payload, dict) else [],
                "entry_count": len(entries),
            }
            blocks = len(entries)

        return PreviewDescriptor(
            file_id=request.file_id,
            source_format=request.source_format,
            parser_strategy=self.parser_strategy,
            supported=True,
            locator_fields=list(self.locator_fields),
            preview_metadata=metadata,
            estimated_blocks=blocks,
            estimated_chunks=blocks,
            warnings=resolved.warnings,
        )

    def parse(self, request: IngestionRunRequest) -> NormalizedDocument:
        path = require_local_path(request.object_uri)
        payload = json.loads(path.read_text(encoding="utf-8"))
        entries = flatten_json(payload)
        document_id = make_document_id(request.file_id, request.source_format)

        blocks = [
            NormalizedBlock(
                block_id=f"block-{index}",
                document_id=document_id,
                block_type="json_entry",
                text=f"{json_path}: {value}",
                order_index=index,
                source_locator=SourceLocatorData(
                    source_format=request.source_format,
                    json_path=json_path,
                ),
                block_metadata={"json_path": json_path},
            )
            for index, (json_path, value) in enumerate(entries, start=1)
        ]

        title = request.filename
        if isinstance(payload, dict):
            title = next((str(payload[key]) for key in ("title", "name", "id") if key in payload), request.filename)

        return NormalizedDocument(
            document_id=document_id,
            file_id=request.file_id,
            filename=request.filename,
            source_format=request.source_format,
            title=title,
            metadata={"entry_count": len(entries)},
            blocks=blocks,
        )
