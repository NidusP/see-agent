from __future__ import annotations

from xml.etree import ElementTree
from zipfile import ZipFile

from app.api.schemas import IngestionPreviewRequest, IngestionRunRequest
from app.domain.models import NormalizedBlock, NormalizedDocument, PreviewDescriptor, SourceLocatorData
from app.parsers.base import DocumentParser, require_local_path, resolve_object_uri
from app.parsers.common import iter_text_nodes, local_name, make_document_id, normalize_whitespace


class DocxStructureParser(DocumentParser):
    source_format = "docx"
    parser_strategy = "docx_structure_parser"
    locator_fields = ("heading_path", "paragraph_index")

    def preview(self, request: IngestionPreviewRequest) -> PreviewDescriptor:
        resolved = resolve_object_uri(request.object_uri)
        metadata = {}
        blocks = None
        if resolved.path is not None:
            document = self._parse_document(resolved.path)
            metadata = {
                "title": document["title"],
                "heading_count": document["heading_count"],
                "paragraph_count": document["paragraph_count"],
                "table_count": document["table_count"],
            }
            blocks = len(document["entries"])

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
        parsed = self._parse_document(path)

        blocks: list[NormalizedBlock] = []
        section_paragraph_index = 0
        for order_index, entry in enumerate(parsed["entries"], start=1):
            if entry["block_type"] == "heading":
                section_paragraph_index = 0
            else:
                section_paragraph_index += 1

            blocks.append(
                NormalizedBlock(
                    block_id=f"block-{order_index}",
                    document_id=document_id,
                    block_type=entry["block_type"],
                    text=entry["text"],
                    order_index=order_index,
                    heading_path=entry["heading_path"],
                    source_locator=SourceLocatorData(
                        source_format=request.source_format,
                        heading_path=entry["heading_path"] or None,
                        paragraph_index=None if entry["block_type"] == "heading" else section_paragraph_index,
                        block_label=entry.get("block_label"),
                    ),
                    block_metadata=entry.get("metadata", {}),
                )
            )

        return NormalizedDocument(
            document_id=document_id,
            file_id=request.file_id,
            filename=request.filename,
            source_format=request.source_format,
            title=parsed["title"],
            metadata={
                "heading_count": parsed["heading_count"],
                "paragraph_count": parsed["paragraph_count"],
                "table_count": parsed["table_count"],
            },
            blocks=blocks,
        )

    def _parse_document(self, path) -> dict[str, object]:
        with ZipFile(path) as archive:
            root = ElementTree.fromstring(archive.read("word/document.xml"))

        body = next(node for node in root if local_name(node.tag) == "body")
        entries: list[dict[str, object]] = []
        heading_path: list[str] = []
        title = path.stem
        heading_count = 0
        paragraph_count = 0
        table_count = 0

        for child in body:
            tag = local_name(child.tag)
            if tag == "p":
                text = normalize_whitespace(" ".join(iter_text_nodes(child, "t")))
                if not text:
                    continue
                paragraph_count += 1
                is_heading = self._is_heading_paragraph(child, text)
                is_list_item = self._is_list_paragraph(child)
                block_type = "paragraph"
                block_label = None
                if is_heading:
                    block_type = "heading"
                    heading_count += 1
                    heading_path = [text]
                    title = text if heading_count == 1 else title
                elif is_list_item:
                    block_type = "list_item"
                entries.append(
                    {
                        "block_type": block_type,
                        "text": text,
                        "heading_path": heading_path.copy() if block_type != "heading" else heading_path.copy(),
                        "block_label": block_label,
                    }
                )
            elif tag == "tbl":
                rows: list[str] = []
                for row in child:
                    if local_name(row.tag) != "tr":
                        continue
                    cells = []
                    for cell in row:
                        if local_name(cell.tag) != "tc":
                            continue
                        cells.append(normalize_whitespace(" ".join(iter_text_nodes(cell, "t"))))
                    row_text = " | ".join(cell for cell in cells if cell)
                    if row_text:
                        rows.append(row_text)
                if rows:
                    table_count += 1
                    entries.append(
                        {
                            "block_type": "table",
                            "text": "\n".join(rows),
                            "heading_path": heading_path.copy(),
                            "block_label": f"table-{table_count}",
                            "metadata": {"table_rows": len(rows)},
                        }
                    )

        return {
            "title": title,
            "heading_count": heading_count,
            "paragraph_count": paragraph_count,
            "table_count": table_count,
            "entries": entries,
        }

    @staticmethod
    def _is_list_paragraph(paragraph: ElementTree.Element) -> bool:
        for node in paragraph.iter():
            if local_name(node.tag) == "pStyle" and node.attrib.get("{http://schemas.openxmlformats.org/wordprocessingml/2006/main}val", "").startswith("List"):
                return True
        return False

    @staticmethod
    def _is_heading_paragraph(paragraph: ElementTree.Element, text: str) -> bool:
        for node in paragraph.iter():
            if local_name(node.tag) == "pStyle" and node.attrib.get("{http://schemas.openxmlformats.org/wordprocessingml/2006/main}val", "").startswith("Heading"):
                return True
        has_bold = any(local_name(node.tag) == "b" for node in paragraph.iter())
        return has_bold and len(text) <= 120
