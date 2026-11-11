from __future__ import annotations

import re

from app.api.schemas import IngestionPreviewRequest, IngestionRunRequest
from app.domain.models import NormalizedBlock, NormalizedDocument, PreviewDescriptor, SourceLocatorData
from app.parsers.base import DocumentParser, require_local_path, resolve_object_uri
from app.parsers.common import make_document_id, read_text_file, strip_inline_markdown


class MarkdownParser(DocumentParser):
    source_format = "md"
    parser_strategy = "markdown_parser"
    locator_fields = ("heading_path", "block_label")

    def preview(self, request: IngestionPreviewRequest) -> PreviewDescriptor:
        resolved = resolve_object_uri(request.object_uri)
        metadata = {}
        blocks = None
        if resolved.path is not None:
            text = read_text_file(resolved.path)
            headings = len(re.findall(r"^\s*#{1,6}\s+", text, re.M))
            list_items = len(re.findall(r"^\s*(?:[-*+]|\d+\.)\s+", text, re.M))
            paragraphs = len([part for part in re.split(r"\n\s*\n", text) if part.strip()])
            metadata = {
                "title": strip_inline_markdown(next((line.lstrip("# ").strip() for line in text.splitlines() if line.startswith("#")), "")),
                "heading_count": headings,
                "list_item_count": list_items,
                "paragraph_group_count": paragraphs,
            }
            blocks = max(headings + list_items, paragraphs)

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
        text = read_text_file(path)
        lines = text.splitlines()

        blocks: list[NormalizedBlock] = []
        heading_stack: list[str] = []
        paragraph_buffer: list[str] = []
        order_index = 0
        title = request.filename

        def flush_paragraph() -> None:
            nonlocal order_index
            if not paragraph_buffer:
                return
            order_index += 1
            paragraph_text = strip_inline_markdown(" ".join(paragraph_buffer).strip())
            if paragraph_text:
                blocks.append(
                    NormalizedBlock(
                        block_id=f"block-{order_index}",
                        document_id=document_id,
                        block_type="paragraph",
                        text=paragraph_text,
                        order_index=order_index,
                        heading_path=heading_stack.copy(),
                        source_locator=SourceLocatorData(
                            source_format=request.source_format,
                            heading_path=heading_stack.copy() or None,
                            block_label=f"paragraph-{order_index}",
                        ),
                    )
                )
            paragraph_buffer.clear()

        for line in lines:
            stripped = line.strip()
            if not stripped:
                flush_paragraph()
                continue

            heading_match = re.match(r"^(#{1,6})\s+(.*)$", stripped)
            if heading_match:
                flush_paragraph()
                level = len(heading_match.group(1))
                heading_text = strip_inline_markdown(heading_match.group(2))
                heading_stack = heading_stack[: level - 1]
                heading_stack.append(heading_text)
                title = heading_text if level == 1 else title
                order_index += 1
                blocks.append(
                    NormalizedBlock(
                        block_id=f"block-{order_index}",
                        document_id=document_id,
                        block_type="heading",
                        text=heading_text,
                        order_index=order_index,
                        heading_path=heading_stack.copy(),
                        source_locator=SourceLocatorData(
                            source_format=request.source_format,
                            heading_path=heading_stack.copy(),
                            block_label=f"heading-{order_index}",
                        ),
                    )
                )
                continue

            list_match = re.match(r"^(?:[-*+]|\d+\.)\s+(.*)$", stripped)
            if list_match:
                flush_paragraph()
                order_index += 1
                item_text = strip_inline_markdown(list_match.group(1))
                blocks.append(
                    NormalizedBlock(
                        block_id=f"block-{order_index}",
                        document_id=document_id,
                        block_type="list_item",
                        text=item_text,
                        order_index=order_index,
                        heading_path=heading_stack.copy(),
                        source_locator=SourceLocatorData(
                            source_format=request.source_format,
                            heading_path=heading_stack.copy() or None,
                            block_label=f"list-item-{order_index}",
                        ),
                    )
                )
                continue

            paragraph_buffer.append(stripped)

        flush_paragraph()

        return NormalizedDocument(
            document_id=document_id,
            file_id=request.file_id,
            filename=request.filename,
            source_format=request.source_format,
            title=title,
            metadata={"block_count": len(blocks)},
            blocks=blocks,
        )
