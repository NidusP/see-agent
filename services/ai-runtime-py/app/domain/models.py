from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


SUPPORTED_SOURCE_FORMATS: tuple[str, ...] = (
    "txt",
    "md",
    "json",
    "pdf",
    "docx",
    "xlsx",
    "pptx",
)


@dataclass
class SourceLocatorData:
    source_format: str
    page_no: int | None = None
    heading_path: list[str] | None = None
    paragraph_index: int | None = None
    sheet_name: str | None = None
    cell_range: str | None = None
    slide_no: int | None = None
    slide_title: str | None = None
    block_label: str | None = None
    json_path: str | None = None

    def to_dict(self) -> dict[str, Any]:
        return {
            key: value
            for key, value in {
                "source_format": self.source_format,
                "page_no": self.page_no,
                "heading_path": self.heading_path,
                "paragraph_index": self.paragraph_index,
                "sheet_name": self.sheet_name,
                "cell_range": self.cell_range,
                "slide_no": self.slide_no,
                "slide_title": self.slide_title,
                "block_label": self.block_label,
                "json_path": self.json_path,
            }.items()
            if value is not None
        }


@dataclass
class NormalizedBlock:
    block_id: str
    document_id: str
    block_type: str
    text: str
    order_index: int
    heading_path: list[str] = field(default_factory=list)
    source_locator: SourceLocatorData = field(default_factory=lambda: SourceLocatorData(source_format="txt"))
    block_metadata: dict[str, Any] = field(default_factory=dict)


@dataclass
class NormalizedDocument:
    document_id: str
    file_id: str
    filename: str
    source_format: str
    title: str | None
    metadata: dict[str, Any]
    blocks: list[NormalizedBlock]
    revision_id: str | None = None
    checksum_sha256: str | None = None
    parse_version: str | None = None
    index_version: str | None = None


@dataclass
class PreviewDescriptor:
    file_id: str
    source_format: str
    parser_strategy: str
    supported: bool
    locator_fields: list[str]
    preview_metadata: dict[str, Any]
    estimated_blocks: int | None = None
    estimated_chunks: int | None = None
    warnings: list[str] = field(default_factory=list)


@dataclass
class ChunkRecord:
    chunk_id: str
    file_id: str
    document_id: str
    filename: str
    source_format: str
    block_type: str
    text: str
    token_count: int
    heading_path: list[str]
    source_locator: SourceLocatorData
    embedding_model: str
    index_version: str
    revision_id: str | None = None
    checksum_sha256: str | None = None
    parse_version: str | None = None
    chunk_content_sha256: str | None = None
    visibility: str = "legacy"
