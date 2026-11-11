from __future__ import annotations

from dataclasses import dataclass
import re

from app.api.schemas import CitationAnchor, RetrievedSource


_MARKER_PREFIX = "[[cite:"
_MARKER_RE = re.compile(r"^\[\[cite:(\d+)\]\]")
_SENTENCE_BOUNDARIES = frozenset("。！？.!?\n")


@dataclass(frozen=True)
class CitationAnchorParseResult:
    content: str
    anchors: list[CitationAnchor]


class CitationAnchorStreamParser:
    """Removes internal cite markers while preserving user-visible stream chunks."""

    def __init__(self, citations: list[RetrievedSource]) -> None:
        self._available_ranks = {citation.rank for citation in citations if citation.rank > 0}
        self._buffer = ""
        self._visible_parts: list[str] = []
        self._anchors: list[CitationAnchor] = []
        self._last_anchor_range: tuple[int, int] | None = None

    @property
    def content(self) -> str:
        return "".join(self._visible_parts)

    @property
    def anchors(self) -> list[CitationAnchor]:
        return self._anchors.copy()

    def push(self, delta: str) -> str:
        self._buffer += delta
        return self._drain(final=False)

    def finalize(self) -> str:
        return self._drain(final=True)

    def _drain(self, *, final: bool) -> str:
        emitted: list[str] = []
        while self._buffer:
            marker_start = self._buffer.find(_MARKER_PREFIX)
            if marker_start < 0:
                safe_length = len(self._buffer) if final else len(self._buffer) - _trailing_marker_prefix_length(self._buffer)
                if safe_length <= 0:
                    break
                emitted.append(self._append_visible(self._buffer[:safe_length]))
                self._buffer = self._buffer[safe_length:]
                continue

            if marker_start > 0:
                emitted.append(self._append_visible(self._buffer[:marker_start]))
                self._buffer = self._buffer[marker_start:]
                continue

            marker = _MARKER_RE.match(self._buffer)
            if marker is not None:
                self._buffer = self._buffer[marker.end():]
                rank = int(marker.group(1))
                if rank in self._available_ranks:
                    self._attach_rank(rank)
                continue

            if not final:
                break
            emitted.append(self._append_visible(self._buffer))
            self._buffer = ""

        return "".join(emitted)

    def _append_visible(self, value: str) -> str:
        if value:
            self._visible_parts.append(value)
            self._last_anchor_range = None
        return value

    def _attach_rank(self, rank: int) -> None:
        runes = list(self.content)
        end_rune = len(runes)
        while end_rune > 0 and runes[end_rune - 1].isspace():
            end_rune -= 1
        if end_rune == 0:
            return

        search_index = end_rune - 1
        if runes[search_index] in _SENTENCE_BOUNDARIES:
            search_index -= 1
        while search_index >= 0 and runes[search_index] not in _SENTENCE_BOUNDARIES:
            search_index -= 1
        start_rune = search_index + 1
        while start_rune < end_rune and runes[start_rune].isspace():
            start_rune += 1
        if start_rune >= end_rune:
            return

        anchor_range = (start_rune, end_rune)
        if self._last_anchor_range == anchor_range and self._anchors:
            last = self._anchors[-1]
            if rank not in last.citation_ranks:
                self._anchors[-1] = CitationAnchor(
                    start_rune=last.start_rune,
                    end_rune=last.end_rune,
                    citation_ranks=[*last.citation_ranks, rank],
                )
            return

        self._anchors.append(
            CitationAnchor(start_rune=start_rune, end_rune=end_rune, citation_ranks=[rank]),
        )
        self._last_anchor_range = anchor_range


def parse_citation_anchors(content: str, citations: list[RetrievedSource]) -> CitationAnchorParseResult:
    parser = CitationAnchorStreamParser(citations)
    parser.push(content)
    parser.finalize()
    return CitationAnchorParseResult(content=parser.content, anchors=parser.anchors)


def _trailing_marker_prefix_length(value: str) -> int:
    max_length = min(len(value), len(_MARKER_PREFIX) - 1)
    for length in range(max_length, 0, -1):
        if _MARKER_PREFIX.startswith(value[-length:]):
            return length
    return 0
