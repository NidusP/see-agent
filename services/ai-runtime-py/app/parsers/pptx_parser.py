from __future__ import annotations

from app.api.schemas import IngestionPreviewRequest, IngestionRunRequest
from app.domain.models import NormalizedBlock, NormalizedDocument, PreviewDescriptor, SourceLocatorData
from app.parsers.base import DocumentParser, require_local_path, resolve_object_uri
from app.parsers.common import make_document_id, parse_pptx_slides


class SlideContentParser(DocumentParser):
    source_format = "pptx"
    parser_strategy = "slide_content_parser"
    locator_fields = ("slide_no", "slide_title", "block_label")

    def preview(self, request: IngestionPreviewRequest) -> PreviewDescriptor:
        resolved = resolve_object_uri(request.object_uri)
        metadata = {}
        blocks = None
        if resolved.path is not None:
            slides = parse_pptx_slides(resolved.path, include_notes=False)
            metadata = {
                "slide_count": len(slides),
                "table_count": sum(len(slide["tables"]) for slide in slides),
                "slide_titles": [slide["title"] for slide in slides],
                "notes_preview_enabled": False,
            }
            blocks = sum(1 + len(slide["tables"]) for slide in slides)

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
        include_notes = False
        if request.ingestion_options is not None:
            include_notes = request.ingestion_options.include_pptx_notes
        slides = parse_pptx_slides(path, include_notes=include_notes)

        blocks: list[NormalizedBlock] = []
        order_index = 0
        for slide in slides:
            order_index += 1
            body_text = "\n".join(text for text in slide["text_shapes"][1:] if text)
            if body_text:
                blocks.append(
                    NormalizedBlock(
                        block_id=f"block-{order_index}",
                        document_id=document_id,
                        block_type="slide_body",
                        text=body_text,
                        order_index=order_index,
                        source_locator=SourceLocatorData(
                            source_format=request.source_format,
                            slide_no=slide["slide_no"],
                            slide_title=slide["title"],
                            block_label=f"slide-body-{slide['slide_no']}",
                        ),
                    )
                )

            for table_index, table_text in enumerate(slide["tables"], start=1):
                order_index += 1
                blocks.append(
                    NormalizedBlock(
                        block_id=f"block-{order_index}",
                        document_id=document_id,
                        block_type="table",
                        text=table_text,
                        order_index=order_index,
                        source_locator=SourceLocatorData(
                            source_format=request.source_format,
                            slide_no=slide["slide_no"],
                            slide_title=slide["title"],
                            block_label=f"table-{table_index}",
                        ),
                    )
                )

            if include_notes and slide["notes"]:
                order_index += 1
                blocks.append(
                    NormalizedBlock(
                        block_id=f"block-{order_index}",
                        document_id=document_id,
                        block_type="paragraph",
                        text=slide["notes"],
                        order_index=order_index,
                        source_locator=SourceLocatorData(
                            source_format=request.source_format,
                            slide_no=slide["slide_no"],
                            slide_title=slide["title"],
                            block_label=f"notes-{slide['slide_no']}",
                        ),
                    )
                )

        title = slides[0]["title"] if slides else request.filename
        return NormalizedDocument(
            document_id=document_id,
            file_id=request.file_id,
            filename=request.filename,
            source_format=request.source_format,
            title=title,
            metadata={
                "slide_count": len(slides),
                "table_count": sum(len(slide["tables"]) for slide in slides),
            },
            blocks=blocks,
        )
