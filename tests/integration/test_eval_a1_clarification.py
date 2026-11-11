from __future__ import annotations

import copy
from pathlib import Path

import pytest

from scripts.eval import run_baseline


DATASET_PATH = Path("data/eval-datasets/clarification/v1-dev.jsonl")


def load_a1_records() -> list[dict[str, object]]:
    return run_baseline.load_dataset(DATASET_PATH)


def test_a1_development_dataset_has_frozen_32_case_composition() -> None:
    records = load_a1_records()

    assert len(records) == 32
    assert [record["question_id"] for record in records] == [f"a1_dev_{index:03d}" for index in range(1, 33)]
    assert {record["expected_disposition"] for record in records} == {
        "grounded_answer",
        "clarification_required",
        "insufficient_evidence",
        "partial_answer",
    }
    assert {
        disposition: sum(record["expected_disposition"] == disposition for record in records)
        for disposition in (
            "grounded_answer",
            "clarification_required",
            "insufficient_evidence",
            "partial_answer",
        )
    } == {
        "grounded_answer": 10,
        "clarification_required": 10,
        "insufficient_evidence": 6,
        "partial_answer": 6,
    }
    assert run_baseline.dataset_sha256(DATASET_PATH) == run_baseline.dataset_sha256(DATASET_PATH)


def test_a1_mock_report_records_evaluation_and_ineligible_provider_evidence() -> None:
    report = run_baseline.build_report(
        load_a1_records(),
        DATASET_PATH,
        mode="mock",
        model_id="qwen3:14b",
        config_version="cfg_a1_dev_v1",
        retrieval_config_version="rcfg_a1_dev_v1",
        knowledge_snapshot_id="snapshot_a1_dev",
    )

    assert report["metrics"]["passed_count"] == 32
    assert report["metrics"]["answer_disposition_counts"] == {
        "clarification_required": 10,
        "grounded_answer": 10,
        "insufficient_evidence": 6,
        "partial_answer": 6,
    }
    assert report["metrics"]["answer_disposition_accuracy"] == 1.0
    assert report["metrics"]["coverage_honesty"] == 1.0
    assert report["metrics"]["clarification_resolution_accuracy"] == 1.0
    assert report["provider_evidence"] == {
        "observation_count": 32,
        "complete_observation_count": 0,
        "fallback_count": 0,
        "generation_providers": [],
        "generation_model_ids": [],
        "policy_providers": [],
        "policy_model_ids": [],
        "comparison_eligible": False,
        "quality_eligible": False,
    }
    assert all(sample["provider_evidence"]["quality_eligible"] is False for sample in report["samples"])
    assert "## A1 Answer Disposition" in run_baseline.render_markdown(report)
    assert "- Quality eligible: `False`" in run_baseline.render_markdown(report)


def test_a1_validation_rejects_missing_schema_and_invalid_partial_coverage() -> None:
    record = copy.deepcopy(load_a1_records()[10])
    record.pop("expected_missing_dimensions")

    with pytest.raises(run_baseline.DatasetError, match="expected_missing_dimensions"):
        run_baseline.validate_record(record, 1)

    partial_record = copy.deepcopy(load_a1_records()[26])
    partial_record["expected_coverage"]["supported_count"] = partial_record["expected_coverage"]["requested_count"]

    with pytest.raises(run_baseline.DatasetError, match="positive partial result"):
        run_baseline.validate_record(partial_record, 1)


def test_a1_scoring_reports_one_primary_failure_without_losing_observed_forbidden_fact() -> None:
    record = copy.deepcopy(load_a1_records()[10])
    sample = run_baseline.evaluate_runtime_response(
        record,
        {
            "assistant_message": {"role": "assistant", "content": "直属负责人审批。"},
            "citations": [],
            "refusal": False,
            "finish_reason": "stop",
            "answer_disposition": "clarification_required",
            "clarification": {"missing_dimensions": record["expected_missing_dimensions"]},
            "used_citation_ranks": [],
        },
        8.0,
    )

    assert sample["passed"] is False
    assert sample["a1_evaluation"]["forbidden_answer_point_hits"] == ["直属负责人审批"]
    assert sample["a1_evaluation"]["primary_failure_type"] == "unsupported_business_claim"
    assert run_baseline.build_sample_failures(record, sample) == [
        {
            "question_id": "a1_dev_011",
            "failure_type": "unsupported_business_claim",
            "observed_answer": "直属负责人审批。",
            "observed_citations": [],
            "notes": "answer contained forbidden business facts: ['直属负责人审批']",
        }
    ]


def test_a1_runtime_evidence_needs_an_observed_field_and_only_remote_can_be_eligible() -> None:
    record = copy.deepcopy(load_a1_records()[0])
    payload = {
        "assistant_message": {"role": "assistant", "content": "差旅行程结束后的10个自然日内 / 以系统成功提交时间为准"},
        "citations": [{"filename": "差旅与费用管理制度.md", "rank": 1, "source_locator": {"heading_path": ["报销提交时限"]}}],
        "refusal": False,
        "finish_reason": "stop",
        "answer_disposition": "grounded_answer",
        "used_citation_ranks": [1],
        "execution": {
            "fallback_used": False,
            "generation": {"status": "performed", "provider": "ollama_native", "model_id": "qwen3:14b"},
            "retrieval": {"status": "performed", "embedding_model": "qwen3-embedding:0.6b"},
        },
    }
    sample = run_baseline.evaluate_runtime_response(record, payload, 9.0)

    assert sample["passed"] is True
    assert sample["a1_evaluation"]["observed_used_citation_ranks"] == [1]
    assert sample["provider_evidence"]["observation_complete"] is True
    assert run_baseline.summarize_provider_evidence([sample], "runtime-local")["quality_eligible"] is False
    assert run_baseline.summarize_provider_evidence([sample], "runtime-remote", "baseline")["comparison_eligible"] is True
    assert run_baseline.summarize_provider_evidence([sample], "runtime-remote", "baseline")["quality_eligible"] is False
    sample["provider_evidence"]["candidate_observation_valid"] = True
    assert run_baseline.summarize_provider_evidence([sample], "runtime-remote", "candidate")["quality_eligible"] is True

    payload.pop("used_citation_ranks")
    invalid_sample = run_baseline.evaluate_runtime_response(record, payload, 9.0)

    assert invalid_sample["passed"] is False
    assert invalid_sample["a1_evaluation"]["primary_failure_type"] == "citation_mismatch"
