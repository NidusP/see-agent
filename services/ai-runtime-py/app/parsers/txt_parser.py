from __future__ import annotations

from app.api.schemas import IngestionPreviewRequest, IngestionRunRequest
from app.domain.models import NormalizedBlock, NormalizedDocument, PreviewDescriptor, SourceLocatorData
from app.parsers.base import DocumentParser, require_local_path, resolve_object_uri
from app.parsers.common import make_document_id, read_text_file, split_paragraphs


class PlainTextParser(DocumentParser):
    source_format = "txt"
    parser_strategy = "plain_text_parser"
    locator_fields = ("block_label",)

    def preview(self, request: IngestionPreviewRequest) -> PreviewDescriptor:
        resolved = resolve_object_uri(request.object_uri)
        metadata = {}
        blocks = None
        if resolved.path is not None:
            paragraphs = split_paragraphs(read_text_file(resolved.path))
            metadata = {
                "title": paragraphs[0][:80] if paragraphs else None,
                "paragraph_count": len(paragraphs),
                "file_size_bytes": resolved.path.stat().st_size,
            }
            blocks = len(paragraphs)

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
        document_id = make_document_id(request.file_id, request.source_format)
        paragraphs = split_paragraphs(read_text_file(path))

        blocks: list[NormalizedBlock] = []
        current_heading: list[str] = []
        for index, paragraph in enumerate(paragraphs, start=1):
            is_heading = "\n" not in paragraph and len(paragraph) <= 40 and not paragraph.endswith(("。", "."))
            block_type = "heading" if is_heading else "paragraph"
            if is_heading:
                current_heading = [paragraph]
            blocks.append(
                NormalizedBlock(
                    block_id=f"block-{index}",
                    document_id=document_id,
                    block_type=block_type,
                    text=paragraph,
                    order_index=index,
                    heading_path=[] if is_heading else current_heading.copy(),
                    source_locator=SourceLocatorData(
                        source_format=request.source_format,
                        block_label=f"{block_type}-{index}",
                    ),
                )
            )

        return NormalizedDocument(
            document_id=document_id,
            file_id=request.file_id,
            filename=request.filename,
            source_format=request.source_format,
            title=paragraphs[0] if paragraphs else request.filename,
            metadata={"paragraph_count": len(paragraphs)},
            blocks=blocks,
        )
