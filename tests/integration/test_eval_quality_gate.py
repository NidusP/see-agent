from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

from scripts.eval import run_baseline


def quality_gate() -> dict[str, object]:
    return run_baseline.validate_quality_gate(
        {
            "gate_id": "synthetic-runtime-gate-v1",
            "mode": "runtime-local",
            "model_id": "llama3.2:latest",
            "generation_config": {
                "system_prompt_version": "prompt_v1",
                "temperature": 0.2,
                "max_output_tokens": 1024,
            },
            "minimum_passed_count": 26,
            "minimum_metrics": {
                "retrieval_recall_at_k": 0.95,
                "citation_accuracy": 0.95,
                "refusal_correctness": 1.0,
                "grounded_correctness": 0.55,
            },
            "maximum_failure_type_counts": {
                "unexpected_runtime_error": 0,
            },
        }
    )


def synthetic_report() -> dict[str, object]:
    return {
        "run_id": "runtime-local-synthetic",
        "mode": "runtime-local",
        "dataset_id": "v1-seed",
        "model_id": "llama3.2:latest",
        "generation_config": {
            "system_prompt_version": "prompt_v1",
            "temperature": 0.2,
            "max_output_tokens": 1024,
        },
        "metrics": {
            "sample_count": 40,
            "must_answer_count": 35,
            "must_refuse_count": 5,
            "passed_count": 26,
            "failed_count": 14,
            "retrieval_recall_at_k": 0.95,
            "expected_file_hit_rate": 0.95,
            "grounded_correctness": 0.55,
            "citation_accuracy": 0.95,
            "refusal_correctness": 1.0,
            "average_latency_ms": 1000.0,
            "format_family_counts": {"json": 40},
        },
        "failures": [],
        "failure_type_counts": {"unexpected_runtime_error": 0},
    }


def check_by_name(result: dict[str, object], name: str) -> dict[str, object]:
    return next(check for check in result["checks"] if check["name"] == name)


def test_quality_gate_passes_matching_synthetic_report() -> None:
    result = run_baseline.evaluate_quality_gate(quality_gate(), synthetic_report())

    assert result["passed"] is True
    assert all(check["passed"] for check in result["checks"])


def test_quality_gate_rejects_metric_below_minimum() -> None:
    report = synthetic_report()
    report["metrics"]["grounded_correctness"] = 0.54

    result = run_baseline.evaluate_quality_gate(quality_gate(), report)

    assert result["passed"] is False
    check = check_by_name(result, "metrics.grounded_correctness")
    assert check["passed"] is False
    assert check["reason"] == "0.54 is below minimum 0.55"


def test_quality_gate_rejects_model_and_generation_mismatch() -> None:
    report = synthetic_report()
    report["model_id"] = "other-model"
    report["generation_config"] = {
        "system_prompt_version": "prompt_v2",
        "temperature": 0.4,
        "max_output_tokens": 512,
    }

    result = run_baseline.evaluate_quality_gate(quality_gate(), report)

    assert result["passed"] is False
    assert check_by_name(result, "model_id")["passed"] is False
    assert check_by_name(result, "generation_config")["passed"] is False


def test_quality_gate_result_renders_and_writes_report(tmp_path: Path) -> None:
    report = synthetic_report()
    report["quality_gate"] = run_baseline.evaluate_quality_gate(quality_gate(), report)

    run_baseline.write_reports(report, tmp_path)

    written_report = json.loads((tmp_path / "latest.json").read_text(encoding="utf-8"))
    markdown = (tmp_path / "latest.md").read_text(encoding="utf-8")
    assert written_report["quality_gate"]["passed"] is True
    assert json.loads((tmp_path / f"{report['run_id']}.json").read_text(encoding="utf-8"))["run_id"] == report["run_id"]
    assert (tmp_path / f"{report['run_id']}.md").read_text(encoding="utf-8") == markdown
    assert "## Quality Gate" in markdown
    assert "- Gate: `synthetic-runtime-gate-v1`" in markdown


def test_report_archive_preserves_prior_run_when_latest_moves(tmp_path: Path) -> None:
    first = synthetic_report()
    first["run_id"] = "runtime-local-first"
    second = synthetic_report()
    second["run_id"] = "runtime-local-second"
    second["metrics"] = {**second["metrics"], "passed_count": 27}

    run_baseline.write_reports(first, tmp_path)
    first_archive = (tmp_path / "runtime-local-first.json").read_text(encoding="utf-8")
    run_baseline.write_reports(second, tmp_path)

    assert (tmp_path / "runtime-local-first.json").read_text(encoding="utf-8") == first_archive
    assert json.loads((tmp_path / "runtime-local-second.json").read_text(encoding="utf-8"))["metrics"]["passed_count"] == 27
    assert json.loads((tmp_path / "latest.json").read_text(encoding="utf-8"))["run_id"] == "runtime-local-second"


def test_main_writes_report_before_returning_quality_gate_failure(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    gate = quality_gate()
    gate["minimum_passed_count"] = 27
    gate_path = tmp_path / "gate.json"
    gate_path.write_text(json.dumps(gate), encoding="utf-8")
    report_dir = tmp_path / "report"

    monkeypatch.setattr(
        sys,
        "argv",
        [
            "run_baseline.py",
            "--mode",
            "runtime-local",
            "--quality-gate",
            str(gate_path),
            "--report-dir",
            str(report_dir),
        ],
    )
    monkeypatch.setattr(run_baseline, "load_dataset", lambda _: [])
    monkeypatch.setattr(run_baseline, "build_report", lambda *_args, **_kwargs: synthetic_report())

    assert run_baseline.main() == 1

    written_report = json.loads((report_dir / "latest.json").read_text(encoding="utf-8"))
    assert written_report["quality_gate"]["passed"] is False
    assert check_by_name(written_report["quality_gate"], "passed_count")["passed"] is False


def test_quality_gate_rejects_unknown_fields() -> None:
    gate = quality_gate()
    gate["extra_threshold"] = 1

    with pytest.raises(run_baseline.QualityGateError, match="unknown fields"):
        run_baseline.validate_quality_gate(gate)
