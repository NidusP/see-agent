from __future__ import annotations

from app.api.schemas import IngestionPreviewRequest, IngestionRunRequest
from app.domain.models import NormalizedBlock, NormalizedDocument, PreviewDescriptor, SourceLocatorData
from app.parsers.base import DocumentParser, require_local_path, resolve_object_uri
from app.parsers.common import make_document_id, parse_xlsx_workbook


class WorkbookRegionParser(DocumentParser):
    source_format = "xlsx"
    parser_strategy = "workbook_sheet_table_parser"
    locator_fields = ("sheet_name", "cell_range")

    def preview(self, request: IngestionPreviewRequest) -> PreviewDescriptor:
        resolved = resolve_object_uri(request.object_uri)
        metadata = {}
        blocks = None
        if resolved.path is not None:
            workbook = parse_xlsx_workbook(resolved.path)
            metadata = {
                "sheet_count": len(workbook),
                "sheet_names": [sheet["name"] for sheet in workbook],
                "region_count": sum(len(sheet["regions"]) for sheet in workbook),
            }
            blocks = metadata["region_count"]

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
        workbook = parse_xlsx_workbook(path)

        blocks: list[NormalizedBlock] = []
        order_index = 0
        for sheet in workbook:
            for region_index, region in enumerate(sheet["regions"], start=1):
                order_index += 1
                rows = [" | ".join(cell for cell in row if cell) for row in region["rows"]]
                rows = [row for row in rows if row]
                blocks.append(
                    NormalizedBlock(
                        block_id=f"block-{order_index}",
                        document_id=document_id,
                        block_type="sheet_region",
                        text="\n".join(rows),
                        order_index=order_index,
                        source_locator=SourceLocatorData(
                            source_format=request.source_format,
                            sheet_name=sheet["name"],
                            cell_range=region["cell_range"],
                        ),
                        block_metadata={"region_index": region_index},
                    )
                )

        title = workbook[0]["name"] if workbook else request.filename
        return NormalizedDocument(
            document_id=document_id,
            file_id=request.file_id,
            filename=request.filename,
            source_format=request.source_format,
            title=title,
            metadata={
                "sheet_count": len(workbook),
                "region_count": len(blocks),
            },
            blocks=blocks,
        )
