from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Protocol
from urllib.parse import urlparse

from app.api.schemas import IngestionPreviewRequest, IngestionRunRequest
from app.domain.models import NormalizedDocument, PreviewDescriptor


class ParserError(RuntimeError):
    """Raised when a parser cannot complete a preview or parse task."""


@dataclass
class ResolvedObject:
    uri: str
    path: Path | None
    warnings: list[str]


class DocumentParser(Protocol):
    source_format: str
    parser_strategy: str
    locator_fields: tuple[str, ...]

    def preview(self, request: IngestionPreviewRequest) -> PreviewDescriptor:
        ...

    def parse(self, request: IngestionRunRequest) -> NormalizedDocument:
        ...


def resolve_object_uri(uri: str) -> ResolvedObject:
    parsed = urlparse(uri)
    warnings: list[str] = []

    if parsed.scheme in {"http", "https", "minio", "s3"}:
        warnings.append(
            "P2 baseline can preview remote objects but only parses locally readable files."
        )
        return ResolvedObject(uri=uri, path=None, warnings=warnings)

    if parsed.scheme == "file":
        candidate = Path(parsed.path)
    else:
        candidate = Path(uri).expanduser()

    if candidate.exists():
        return ResolvedObject(uri=uri, path=candidate.resolve(), warnings=warnings)

    warnings.append("object_uri is not a locally readable file in the P2 baseline.")
    return ResolvedObject(uri=uri, path=None, warnings=warnings)


def require_local_path(uri: str) -> Path:
    resolved = resolve_object_uri(uri)
    if resolved.path is None:
        raise ParserError("; ".join(resolved.warnings))
    return resolved.path
