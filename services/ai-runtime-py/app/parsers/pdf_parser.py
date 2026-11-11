from __future__ import annotations

from app.api.schemas import IngestionPreviewRequest, IngestionRunRequest
from app.domain.models import NormalizedBlock, NormalizedDocument, PreviewDescriptor, SourceLocatorData
from app.parsers.base import DocumentParser, require_local_path, resolve_object_uri
from app.parsers.common import extract_pdf_pages, make_document_id


class PdfTextParser(DocumentParser):
    source_format = "pdf"
    parser_strategy = "pdf_text_layer_only"
    locator_fields = ("page_no", "block_label")

    def preview(self, request: IngestionPreviewRequest) -> PreviewDescriptor:
        resolved = resolve_object_uri(request.object_uri)
        metadata = {}
        warnings = list(resolved.warnings)
        blocks = None
        if resolved.path is not None:
            pages = extract_pdf_pages(resolved.path)
            line_count = sum(len(page) for page in pages)
            if line_count == 0:
                warnings.append("No PDF text layer was extracted in the P2 baseline.")
            metadata = {
                "page_count": len(pages),
                "extracted_line_count": line_count,
                "ocr_fallback_available": False,
            }
            blocks = line_count

        return PreviewDescriptor(
            file_id=request.file_id,
            source_format=request.source_format,
            parser_strategy=self.parser_strategy,
            supported=True,
            locator_fields=list(self.locator_fields),
            preview_metadata=metadata,
            estimated_blocks=blocks,
            estimated_chunks=blocks,
            warnings=warnings,
        )

    def parse(self, request: IngestionRunRequest) -> NormalizedDocument:
        path = require_local_path(request.object_uri)
        document_id = make_document_id(request.file_id, request.source_format)
        pages = extract_pdf_pages(path)

        blocks: list[NormalizedBlock] = []
        title = request.filename
        order_index = 0
        for page_no, lines in enumerate(pages, start=1):
            for line in lines:
                order_index += 1
                is_heading = len(line) <= 60 and not line.endswith((".", "。"))
                if order_index == 1:
                    title = line
                blocks.append(
                    NormalizedBlock(
                        block_id=f"block-{order_index}",
                        document_id=document_id,
                        block_type="heading" if is_heading else "paragraph",
                        text=line,
                        order_index=order_index,
                        source_locator=SourceLocatorData(
                            source_format=request.source_format,
                            page_no=page_no,
                            block_label=f"page-{page_no}-block-{order_index}",
                        ),
                        block_metadata={"page_no": page_no},
                    )
                )

        return NormalizedDocument(
            document_id=document_id,
            file_id=request.file_id,
            filename=request.filename,
            source_format=request.source_format,
            title=title,
            metadata={"page_count": len(pages)},
            blocks=blocks,
        )
