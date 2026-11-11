from __future__ import annotations

import copy
from collections import Counter, defaultdict
from pathlib import Path

import pytest

from scripts.eval import run_baseline


DATASET_PATH = Path("data/eval-datasets/clarification/v1-q1.jsonl")
DATASET_SHA256 = "sha256:5aef680a679e79afe087f969cdd1bc02814231064d70ce98317aa97ceae11373"
EXPECTED_BOUNDARY_MINIMUMS = {
    "threshold_exact": 8,
    "threshold_adjacent": 8,
    "precondition": 18,
    "exception": 12,
    "negative_condition": 8,
    "time_clock": 8,
    "time_business_day": 8,
    "role_separation": 8,
    "scope_missing": 8,
    "scope_paired_resolution": 8,
    "evidence_absent": 8,
    "count_partial": 8,
    "revision_scope": 1,
    "prompt_injection": 1,
    "topic_abandonment": 1,
    "needs_more": 1,
}


def load_release_records() -> list[dict[str, object]]:
    return run_baseline.load_dataset(DATASET_PATH)


def test_a1_q1_release_dataset_has_frozen_composition_and_boundary_coverage() -> None:
    records = load_release_records()

    assert len(records) == 80
    assert [record["question_id"] for record in records] == [f"a1_q1_{index:03d}" for index in range(1, 81)]
    assert run_baseline.dataset_sha256(DATASET_PATH) == DATASET_SHA256
    assert Counter(record["expected_disposition"] for record in records) == {
        "grounded_answer": 42,
        "clarification_required": 16,
        "insufficient_evidence": 12,
        "partial_answer": 10,
    }
    assert sum(record["expected_disposition"] == "grounded_answer" for record in records) >= 40

    boundary_counts = Counter(tag for record in records for tag in record["boundary_tags"])
    assert all(boundary_counts[tag] >= minimum for tag, minimum in EXPECTED_BOUNDARY_MINIMUMS.items())
    assert set(boundary_counts).issubset(run_baseline.A1_BOUNDARY_TAGS)
    boundary_coverage_text = "\n".join(
        " ".join([record["question"], *record["expected_answer_points"]]) for record in records
    )
    for boundary_value in ("2000", "10000", "800", "3个工作日", "5个工作日", "1个工作日", "2小时", "30分钟"):
        assert boundary_value in boundary_coverage_text

    answer_sources = {
        source_file
        for record in records
        if record["must_answer"]
        for source_file in record["expected_source_files"]
    }
    assert answer_sources == {
        "差旅与费用管理制度.md",
        "请假与远程办公制度.md",
        "信息安全与访问管理规范.md",
    }


def test_a1_q1_release_pairs_are_complete_and_preserve_resolution_boundaries() -> None:
    paired_records: dict[str, list[dict[str, object]]] = defaultdict(list)
    for record in load_release_records():
        if pair_id := record.get("evaluation_pair_id"):
            paired_records[pair_id].append(record)

    assert len(paired_records) == 8
    assert all(len(records) == 2 for records in paired_records.values())
    assert sum(len(records) for records in paired_records.values()) >= 10
    assert {
        record.get("expected_clarification_resolution")
        for records in paired_records.values()
        for record in records
        if record.get("expected_clarification_resolution")
    } == {"resolved", "abandoned", "needs_more"}

    for records in paired_records.values():
        for record in records:
            if record.get("expected_clarification_resolution") == "resolved":
                assert record["expected_resolved_query"] != record["question"]


def test_a1_q1_release_mock_report_is_schema_complete_but_not_quality_eligible() -> None:
    report = run_baseline.build_report(
        load_release_records(),
        DATASET_PATH,
        mode="mock",
        model_id="qwen3:14b",
        config_version="cfg_a1_q1_v1",
        retrieval_config_version="rcfg_a1_q1_v1",
        knowledge_snapshot_id="snapshot_a1_q1",
    )

    assert report["metrics"]["passed_count"] == 80
    assert report["metrics"]["failed_count"] == 0
    assert report["metrics"]["boundary_tag_counts"]["prompt_injection"] == 1
    assert report["metrics"]["evaluation_pair_sample_count"] == 16
    assert report["provider_evidence"]["quality_eligible"] is False
    assert "- Boundary tags:" in run_baseline.render_markdown(report)
    assert "- Paired samples: `16`" in run_baseline.render_markdown(report)


def test_a1_boundary_tags_reject_unknown_or_duplicate_values() -> None:
    unknown_tag_record = copy.deepcopy(load_release_records()[0])
    unknown_tag_record["boundary_tags"] = ["not_a_boundary"]

    with pytest.raises(run_baseline.DatasetError, match="unsupported boundary_tags"):
        run_baseline.validate_record(unknown_tag_record, 1)

    duplicate_tag_record = copy.deepcopy(load_release_records()[0])
    duplicate_tag_record["boundary_tags"] = ["threshold_exact", "threshold_exact"]

    with pytest.raises(run_baseline.DatasetError, match="boundary_tags must be unique"):
        run_baseline.validate_record(duplicate_tag_record, 1)
