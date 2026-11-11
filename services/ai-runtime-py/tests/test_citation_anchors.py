from app.api.schemas import RetrievedSource, SourceLocator
from app.pipelines.citation_anchors import CitationAnchorStreamParser, parse_citation_anchors


def test_stream_parser_hides_split_markers_and_merges_supporting_ranks() -> None:
    parser = CitationAnchorStreamParser([_citation(1), _citation(2)])
    emitted = [
        parser.push("差旅报销需要在十个自然日内提交。[[ci"),
        parser.push("te:1]][[cite:2]]"),
        parser.finalize(),
    ]

    assert "".join(emitted) == "差旅报销需要在十个自然日内提交。"
    assert "[[cite:" not in parser.content
    assert [anchor.model_dump() for anchor in parser.anchors] == [
        {
            "start_rune": 0,
            "end_rune": 16,
            "citation_ranks": [1, 2],
        },
    ]


def test_parser_uses_unicode_runes_and_removes_unknown_rank_markers() -> None:
    result = parse_citation_anchors("第一句。第二条依据🙂。[[cite:2]][[cite:99]]", [_citation(2)])

    assert result.content == "第一句。第二条依据🙂。"
    assert len(result.anchors) == 1
    anchor = result.anchors[0]
    assert "".join(list(result.content)[anchor.start_rune : anchor.end_rune]) == "第二条依据🙂。"
    assert anchor.citation_ranks == [2]


def test_parser_keeps_visible_text_when_no_marker_is_present() -> None:
    result = parse_citation_anchors("依据不足时不应猜测。", [_citation(1)])

    assert result.content == "依据不足时不应猜测。"
    assert result.anchors == []


def _citation(rank: int) -> RetrievedSource:
    return RetrievedSource(
        chunk_id=f"chunk_{rank}",
        file_id="file_policy",
        filename="policy.md",
        rank=rank,
        score=0.9,
        citation_label="policy",
        source_locator=SourceLocator(source_format="md", block_label="policy"),
        snippet="政策片段",
    )
