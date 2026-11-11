from __future__ import annotations

from app.parsers.base import DocumentParser, ParserError
from app.parsers.docx_parser import DocxStructureParser
from app.parsers.json_parser import JsonStructureParser
from app.parsers.md_parser import MarkdownParser
from app.parsers.pdf_parser import PdfTextParser
from app.parsers.pptx_parser import SlideContentParser
from app.parsers.txt_parser import PlainTextParser
from app.parsers.xlsx_parser import WorkbookRegionParser


class ParserRegistry:
    def __init__(self) -> None:
        self._parsers: dict[str, DocumentParser] = {}

    def register(self, parser: DocumentParser) -> None:
        if parser.source_format in self._parsers:
            raise ParserError(f"Duplicate parser registration for source_format={parser.source_format}")
        self._parsers[parser.source_format] = parser

    def get(self, source_format: str) -> DocumentParser:
        try:
            return self._parsers[source_format]
        except KeyError as exc:
            raise ParserError(f"No parser registered for source_format={source_format}") from exc

    def supported_formats(self) -> tuple[str, ...]:
        return tuple(self._parsers.keys())


def build_default_registry() -> ParserRegistry:
    registry = ParserRegistry()
    for parser in (
        PlainTextParser(),
        MarkdownParser(),
        JsonStructureParser(),
        PdfTextParser(),
        DocxStructureParser(),
        WorkbookRegionParser(),
        SlideContentParser(),
    ):
        registry.register(parser)
    return registry
