from __future__ import annotations

import argparse
import copy
import json
import sys
from pathlib import Path

import pytest

from scripts.eval import run_baseline


def conditions(schema_version: str = "a1_q1_remote_conditions_v1") -> dict[str, object]:
    candidate = {
        "config_version": "cfg_candidate_q1",
        "model_id": "qwen3:14b",
        "system_prompt_version": "prompt_v8",
        "answer_policy_version": "answer_policy_v1",
        "response_style": "concise",
        "temperature": 0.0,
        "top_p": 1.0,
        "max_output_tokens": 1024,
    }
    candidate_gate_id = "a1-q1-candidate-v1"
    if schema_version == "a1_q1_remote_conditions_v2":
        candidate.update(
            {
                "config_version": "cfg_candidate_q1_v2",
                "system_prompt_version": "prompt_v9",
                "answer_policy_version": "answer_policy_v2",
            }
        )
        candidate_gate_id = "a1-q1-candidate-v2"
    elif schema_version == "a1_q1_remote_conditions_v3":
        candidate.update(
            {
                "config_version": "cfg_candidate_q1_v3",
                "system_prompt_version": "prompt_v10",
                "answer_policy_version": "answer_policy_v3",
            }
        )
        candidate_gate_id = "a1-q1-candidate-v3"
    elif schema_version == "a1_q1_remote_conditions_v4":
        candidate.update(
            {
                "config_version": "cfg_candidate_q1_v4",
                "system_prompt_version": "prompt_v11",
                "answer_policy_version": "answer_policy_v4",
            }
        )
        candidate_gate_id = "a1-q1-candidate-v4"
    elif schema_version == "a1_q1_remote_conditions_v5":
        candidate.update(
            {
                "config_version": "cfg_candidate_q1_v5",
                "system_prompt_version": "prompt_v12",
                "answer_policy_version": "answer_policy_v5",
            }
        )
        candidate_gate_id = "a1-q1-candidate-v5"
    return {
        "schema_version": schema_version,
        "dataset": {
            "id": "v1-q1",
            "sha256": "sha256:5aef680a679e79afe087f969cdd1bc02814231064d70ce98317aa97ceae11373",
        },
        "scope": {
            "snapshot_id": "snap_q1",
            "manifest_hash": "sha256:" + "1" * 64,
            "revision_ids": ["rev_q1"],
            "resolved_at": "2026-08-04T00:00:00Z",
        },
        "retrieval": {
            "config_version": "rcfg_q1",
            "strategy": "native_dense_v1",
            "embedding_model": "qwen3-embedding:0.6b",
            "top_k": 3,
            "rerank_enabled": False,
            "evidence_min_score": 0.51,
        },
        "baseline": {
            "config_version": "cfg_baseline_q1",
            "model_id": "qwen3:14b",
            "system_prompt_version": "prompt_v7",
            "answer_policy_version": None,
            "response_style": "concise",
            "temperature": 0.0,
            "top_p": 1.0,
            "max_output_tokens": 1024,
        },
        "candidate": candidate,
        "router_policy_version": "conversation_router_v1",
        "candidate_gate_id": candidate_gate_id,
    }


def candidate_clarification_response() -> dict[str, object]:
    return {
        "knowledge_snapshot_id": "snap_q1",
        "knowledge_snapshot_manifest_hash": "sha256:" + "1" * 64,
        "answer_policy_version": "answer_policy_v1",
        "answer_disposition": "clarification_required",
        "refusal": False,
        "knowledge_used": False,
        "citations": [],
        "citation_anchors": [],
        "used_citation_ranks": [],
        "execution": {
            "policy": "performed",
            "policy_provider": "ollama_native",
            "policy_model_id": "qwen3:14b",
            "policy_call_count": 1,
            "retrieval": {"status": "not_run"},
            "generation": {"status": "not_run"},
            "fallback_used": False,
        },
    }


def parse_preflight_probe_args(
    monkeypatch: pytest.MonkeyPatch,
    conditions_path: Path | None,
    *,
    mode: str = "runtime-remote",
    role: str = "candidate",
    question_ids: list[str] | None = None,
    extra: list[str] | None = None,
) -> argparse.Namespace:
    argv = ["run_baseline.py", "--mode", mode, "--evaluation-role", role]
    if conditions_path is not None:
        argv.extend(["--a1-q1-conditions", str(conditions_path)])
    for question_id in question_ids or ["a1_q1_001", "a1_q1_002"]:
        argv.extend(["--probe-question-id", question_id])
    argv.extend(extra or [])
    monkeypatch.setattr(sys, "argv", argv)
    return run_baseline.parse_args()


def test_conditions_are_canonical_and_enforce_candidate_policy() -> None:
    first = run_baseline.validate_a1_q1_remote_conditions(conditions())
    second = copy.deepcopy(first)
    second["scope"] = dict(reversed(list(second["scope"].items())))

    assert run_baseline.canonical_json_sha256(first) == run_baseline.canonical_json_sha256(second)
    broken = copy.deepcopy(first)
    broken["candidate"]["answer_policy_version"] = None
    with pytest.raises(run_baseline.DatasetError, match="prompt_v8"):
        run_baseline.validate_a1_q1_remote_conditions(broken)

    wrong_baseline = copy.deepcopy(first)
    wrong_baseline["baseline"]["system_prompt_version"] = "prompt_v6"
    with pytest.raises(run_baseline.DatasetError, match="baseline must freeze prompt_v7"):
        run_baseline.validate_a1_q1_remote_conditions(wrong_baseline)

    shared_config = copy.deepcopy(first)
    shared_config["candidate"]["config_version"] = shared_config["baseline"]["config_version"]
    with pytest.raises(run_baseline.DatasetError, match="must differ"):
        run_baseline.validate_a1_q1_remote_conditions(shared_config)


def test_v2_conditions_accept_only_prompt_v9_and_answer_policy_v2(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    frozen_v2 = run_baseline.validate_a1_q1_remote_conditions(conditions("a1_q1_remote_conditions_v2"))
    assert frozen_v2["schema_version"] == "a1_q1_remote_conditions_v2"
    assert frozen_v2["candidate"]["system_prompt_version"] == "prompt_v9"
    assert frozen_v2["candidate"]["answer_policy_version"] == "answer_policy_v2"
    assert frozen_v2["candidate_gate_id"] == "a1-q1-candidate-v2"

    conditions_path = tmp_path / "conditions-v2.json"
    conditions_path.write_text(json.dumps(conditions("a1_q1_remote_conditions_v2")), encoding="utf-8")
    args = parse_preflight_probe_args(monkeypatch, conditions_path)
    role, policy_version = run_baseline.apply_a1_q1_conditions(args, frozen_v2)
    assert role == "candidate"
    assert policy_version == "answer_policy_v2"
    assert args.system_prompt_version == "prompt_v9"
    assert args.config_version == "cfg_candidate_q1_v2"

    v1_with_v2_pair = conditions()
    v1_with_v2_pair["candidate"] = conditions("a1_q1_remote_conditions_v2")["candidate"]
    with pytest.raises(run_baseline.DatasetError, match="a1_q1_remote_conditions_v1 candidate must freeze prompt_v8"):
        run_baseline.validate_a1_q1_remote_conditions(v1_with_v2_pair)

    v2_with_v1_pair = conditions("a1_q1_remote_conditions_v2")
    v2_with_v1_pair["candidate"] = conditions()["candidate"]
    with pytest.raises(run_baseline.DatasetError, match="a1_q1_remote_conditions_v2 candidate must freeze prompt_v9"):
        run_baseline.validate_a1_q1_remote_conditions(v2_with_v1_pair)

    v2_with_crossed_pair = conditions("a1_q1_remote_conditions_v2")
    v2_with_crossed_pair["candidate"]["answer_policy_version"] = "answer_policy_v1"  # type: ignore[index]
    with pytest.raises(run_baseline.DatasetError, match="a1_q1_remote_conditions_v2 candidate must freeze prompt_v9"):
        run_baseline.validate_a1_q1_remote_conditions(v2_with_crossed_pair)

    v2_with_v1_gate = conditions("a1_q1_remote_conditions_v2")
    v2_with_v1_gate["candidate_gate_id"] = "a1-q1-candidate-v1"
    with pytest.raises(run_baseline.DatasetError, match="candidate_gate_id must be a1-q1-candidate-v2"):
        run_baseline.validate_a1_q1_remote_conditions(v2_with_v1_gate)


def test_v2_conditions_cannot_use_the_v1_candidate_gate() -> None:
    frozen_v2 = run_baseline.validate_a1_q1_remote_conditions(conditions("a1_q1_remote_conditions_v2"))
    v1_gate = run_baseline.load_quality_gate(Path("data/eval-datasets/quality-gates/a1-q1-candidate-v1.json"))

    with pytest.raises(run_baseline.DatasetError, match="gate ID does not match"):
        run_baseline.validate_a1_q1_candidate_gate(v1_gate, frozen_v2)

    report = {
        "mode": "runtime-remote",
        "model_id": "qwen3:14b",
        "generation_config": run_baseline._generation_config_from_conditions(frozen_v2["candidate"]),
        "evaluation_role": "candidate",
        "quality_eligible": True,
        "conditions_sha256": run_baseline.canonical_json_sha256(frozen_v2),
        "request_conditions": run_baseline.build_a1_q1_request_conditions(frozen_v2, "candidate"),
        "metrics": {},
        "failure_type_counts": {},
    }
    gate_result = run_baseline.evaluate_quality_gate(v1_gate, report)
    assert gate_result["passed"] is False
    assert next(check for check in gate_result["checks"] if check["name"] == "generation_config")["passed"] is False


def test_v3_conditions_accept_only_prompt_v10_and_answer_policy_v3(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    schema_version = "a1_q1_remote_conditions_v3"
    frozen_v3 = run_baseline.validate_a1_q1_remote_conditions(conditions(schema_version))
    assert frozen_v3["schema_version"] == schema_version
    assert frozen_v3["candidate"]["system_prompt_version"] == "prompt_v10"
    assert frozen_v3["candidate"]["answer_policy_version"] == "answer_policy_v3"
    assert frozen_v3["candidate_gate_id"] == "a1-q1-candidate-v3"

    conditions_path = tmp_path / "conditions-v3.json"
    conditions_path.write_text(json.dumps(conditions(schema_version)), encoding="utf-8")
    args = parse_preflight_probe_args(monkeypatch, conditions_path)
    assert run_baseline.validate_a1_q1_preflight_probe_args(args) == ["a1_q1_001", "a1_q1_002"]
    role, policy_version = run_baseline.apply_a1_q1_conditions(args, frozen_v3)
    assert role == "candidate"
    assert policy_version == "answer_policy_v3"
    assert args.system_prompt_version == "prompt_v10"
    assert args.config_version == "cfg_candidate_q1_v3"

    gate_args = parse_preflight_probe_args(
        monkeypatch,
        conditions_path,
        extra=["--quality-gate", "data/eval-datasets/quality-gates/a1-q1-candidate-v1.json"],
    )
    with pytest.raises(run_baseline.DatasetError, match="must not use --quality-gate"):
        run_baseline.validate_a1_q1_preflight_probe_args(gate_args)

    registration_args = parse_preflight_probe_args(
        monkeypatch,
        conditions_path,
        extra=["--quality-experiment-id", "qexp_v3_probe"],
    )
    with pytest.raises(run_baseline.DatasetError, match="must not register"):
        run_baseline.validate_a1_q1_preflight_probe_args(registration_args)


def test_v4_conditions_accept_only_prompt_v11_and_answer_policy_v4(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    schema_version = "a1_q1_remote_conditions_v4"
    frozen_v4 = run_baseline.validate_a1_q1_remote_conditions(conditions(schema_version))
    assert frozen_v4["schema_version"] == schema_version
    assert frozen_v4["candidate"]["system_prompt_version"] == "prompt_v11"
    assert frozen_v4["candidate"]["answer_policy_version"] == "answer_policy_v4"
    assert frozen_v4["candidate_gate_id"] == "a1-q1-candidate-v4"

    conditions_path = tmp_path / "conditions-v4.json"
    conditions_path.write_text(json.dumps(conditions(schema_version)), encoding="utf-8")
    args = parse_preflight_probe_args(monkeypatch, conditions_path)
    role, policy_version = run_baseline.apply_a1_q1_conditions(args, frozen_v4)
    assert role == "candidate"
    assert policy_version == "answer_policy_v4"
    assert args.system_prompt_version == "prompt_v11"
    assert args.config_version == "cfg_candidate_q1_v4"


def test_v5_conditions_accept_only_prompt_v12_and_answer_policy_v5(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    schema_version = "a1_q1_remote_conditions_v5"
    frozen_v5 = run_baseline.validate_a1_q1_remote_conditions(conditions(schema_version))
    assert frozen_v5["candidate"]["system_prompt_version"] == "prompt_v12"
    assert frozen_v5["candidate"]["answer_policy_version"] == "answer_policy_v5"
    assert frozen_v5["candidate_gate_id"] == "a1-q1-candidate-v5"

    conditions_path = tmp_path / "conditions-v5.json"
    conditions_path.write_text(json.dumps(conditions(schema_version)), encoding="utf-8")
    args = parse_preflight_probe_args(monkeypatch, conditions_path)
    role, policy_version = run_baseline.apply_a1_q1_conditions(args, frozen_v5)
    assert role == "candidate"
    assert policy_version == "answer_policy_v5"
    assert args.system_prompt_version == "prompt_v12"
    assert args.config_version == "cfg_candidate_q1_v5"


@pytest.mark.parametrize(
    ("schema_version", "foreign_schema_version", "expected_prompt"),
    [
        ("a1_q1_remote_conditions_v1", "a1_q1_remote_conditions_v3", "prompt_v8"),
        ("a1_q1_remote_conditions_v2", "a1_q1_remote_conditions_v3", "prompt_v9"),
        ("a1_q1_remote_conditions_v3", "a1_q1_remote_conditions_v1", "prompt_v10"),
        ("a1_q1_remote_conditions_v3", "a1_q1_remote_conditions_v2", "prompt_v10"),
        ("a1_q1_remote_conditions_v4", "a1_q1_remote_conditions_v3", "prompt_v11"),
        ("a1_q1_remote_conditions_v4", "a1_q1_remote_conditions_v2", "prompt_v11"),
        ("a1_q1_remote_conditions_v5", "a1_q1_remote_conditions_v4", "prompt_v12"),
        ("a1_q1_remote_conditions_v5", "a1_q1_remote_conditions_v3", "prompt_v12"),
    ],
)
def test_a1_q1_conditions_reject_cross_version_candidate_pairs_and_gates(
    schema_version: str, foreign_schema_version: str, expected_prompt: str
) -> None:
    crossed_pair = conditions(schema_version)
    crossed_pair["candidate"] = conditions(foreign_schema_version)["candidate"]
    with pytest.raises(run_baseline.DatasetError, match=f"candidate must freeze {expected_prompt}"):
        run_baseline.validate_a1_q1_remote_conditions(crossed_pair)

    crossed_gate = conditions(schema_version)
    crossed_gate["candidate_gate_id"] = conditions(foreign_schema_version)["candidate_gate_id"]
    with pytest.raises(run_baseline.DatasetError, match="candidate_gate_id must be"):
        run_baseline.validate_a1_q1_remote_conditions(crossed_gate)


def test_v3_conditions_cannot_use_v1_or_v2_candidate_gates() -> None:
    frozen_v3 = run_baseline.validate_a1_q1_remote_conditions(conditions("a1_q1_remote_conditions_v3"))
    v1_gate = run_baseline.load_quality_gate(Path("data/eval-datasets/quality-gates/a1-q1-candidate-v1.json"))
    v2_gate = copy.deepcopy(v1_gate)
    v2_gate["gate_id"] = "a1-q1-candidate-v2"

    for gate in (v1_gate, v2_gate):
        with pytest.raises(run_baseline.DatasetError, match="gate ID does not match"):
            run_baseline.validate_a1_q1_candidate_gate(gate, frozen_v3)


def test_prompt_v9_is_registered_for_runner_cli(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(sys, "argv", ["run_baseline.py", "--system-prompt-version", "prompt_v9"])

    assert run_baseline.parse_args().system_prompt_version == "prompt_v9"


def test_prompt_v10_is_registered_for_runner_cli(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(sys, "argv", ["run_baseline.py", "--system-prompt-version", "prompt_v10"])

    assert run_baseline.parse_args().system_prompt_version == "prompt_v10"


def test_prompt_v11_is_registered_for_runner_cli(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(sys, "argv", ["run_baseline.py", "--system-prompt-version", "prompt_v11"])

    assert run_baseline.parse_args().system_prompt_version == "prompt_v11"


def test_prompt_v12_is_registered_for_runner_cli(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(sys, "argv", ["run_baseline.py", "--system-prompt-version", "prompt_v12"])

    assert run_baseline.parse_args().system_prompt_version == "prompt_v12"


def test_q1_cli_derives_conditions_and_rejects_free_drift(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    conditions_path = tmp_path / "conditions.json"
    conditions_path.write_text(json.dumps(conditions()), encoding="utf-8")
    gate_path = Path("data/eval-datasets/quality-gates/a1-q1-candidate-v1.json")
    monkeypatch.setattr(
        sys,
        "argv",
        [
            "run_baseline.py",
            "--a1-q1-conditions",
            str(conditions_path),
            "--mode",
            "runtime-remote",
            "--evaluation-role",
            "candidate",
            "--quality-gate",
            str(gate_path),
        ],
    )
    args = run_baseline.parse_args()
    role, policy = run_baseline.apply_a1_q1_conditions(args, run_baseline.load_a1_q1_remote_conditions(conditions_path))

    assert role == "candidate"
    assert policy == "answer_policy_v1"
    assert args.system_prompt_version == "prompt_v8"
    assert args.dataset == Path("data/eval-datasets/clarification/v1-q1.jsonl")

    monkeypatch.setattr(sys, "argv", ["run_baseline.py", "--system-prompt-version", "prompt_v7"])
    conflicting = run_baseline.parse_args()
    conflicting.mode = "runtime-remote"
    conflicting.evaluation_role = "candidate"
    conflicting.quality_gate = gate_path
    with pytest.raises(run_baseline.DatasetError, match="system-prompt-version conflicts"):
        run_baseline.apply_a1_q1_conditions(conflicting, run_baseline.validate_a1_q1_remote_conditions(conditions()))


def test_candidate_remote_validation_allows_pre_retrieval_clarification_and_rejects_legacy_evidence() -> None:
    frozen = run_baseline.validate_a1_q1_remote_conditions(conditions())
    record = {"task_type": "answer_disposition", "expected_disposition": "clarification_required"}

    run_baseline.validate_remote_execution_observation(
        candidate_clarification_response(),
        model_id="qwen3:14b",
        record=record,
        evaluation_role="candidate",
        conditions=frozen,
    )

    legacy = candidate_clarification_response()
    legacy["execution"]["policy_provider"] = "test_injected"  # type: ignore[index]
    with pytest.raises(run_baseline.RemoteObservationError, match="eligible policy_provider"):
        run_baseline.validate_remote_execution_observation(
            legacy,
            model_id="qwen3:14b",
            record=record,
            evaluation_role="candidate",
            conditions=frozen,
        )

    wrong_policy_model = candidate_clarification_response()
    wrong_policy_model["execution"]["policy_model_id"] = "different-model"  # type: ignore[index]
    with pytest.raises(run_baseline.RemoteObservationError, match="policy model identity"):
        run_baseline.validate_remote_execution_observation(
            wrong_policy_model,
            model_id="qwen3:14b",
            record=record,
            evaluation_role="candidate",
            conditions=frozen,
        )

    wrong_knowledge_used = candidate_clarification_response()
    wrong_knowledge_used["knowledge_used"] = True
    with pytest.raises(run_baseline.RemoteObservationError, match="knowledge_used=false"):
        run_baseline.validate_remote_execution_observation(
            wrong_knowledge_used,
            model_id="qwen3:14b",
            record=record,
            evaluation_role="candidate",
            conditions=frozen,
        )


def test_candidate_remote_validation_requires_knowledge_used_for_grounded_answers() -> None:
    frozen = run_baseline.validate_a1_q1_remote_conditions(conditions())
    record = {"task_type": "answer_disposition", "expected_disposition": "grounded_answer"}
    response = candidate_clarification_response()
    response.update(
        {
            "answer_disposition": "grounded_answer",
            "knowledge_used": True,
            "assistant_message": {"role": "assistant", "content": "Supported answer."},
            "citations": [{"rank": 1}],
            "citation_anchors": [{"start_rune": 0, "end_rune": 17, "citation_ranks": [1]}],
            "used_citation_ranks": [1],
        }
    )
    response["execution"]["retrieval"] = {  # type: ignore[index]
        "status": "performed",
        "strategy": "native_dense_v1",
        "embedding_model": "qwen3-embedding:0.6b",
        "collection": "q1",
        "candidate_count": 1,
        "top_k": 3,
        "rerank_enabled": False,
        "evidence_min_score": 0.51,
    }
    response["execution"]["generation"] = {  # type: ignore[index]
        "status": "performed",
        "provider": "ollama_native",
        "model_id": "qwen3:14b",
    }

    run_baseline.validate_remote_execution_observation(
        response,
        model_id="qwen3:14b",
        record=record,
        evaluation_role="candidate",
        conditions=frozen,
    )
    response["knowledge_used"] = False
    with pytest.raises(run_baseline.RemoteObservationError, match="knowledge_used=true"):
        run_baseline.validate_remote_execution_observation(
            response,
            model_id="qwen3:14b",
            record=record,
            evaluation_role="candidate",
            conditions=frozen,
        )


def test_candidate_request_and_report_are_role_bound_without_remote_execution(monkeypatch: pytest.MonkeyPatch) -> None:
    frozen = run_baseline.validate_a1_q1_remote_conditions(conditions())
    payload = run_baseline.build_runtime_chat_payload(
        {"question_id": "q1", "question": "test", "task_type": "answer_disposition", "must_answer": False},
        model_id="qwen3:14b",
        config_version="cfg_candidate_q1",
        retrieval_config_version="rcfg_q1",
        knowledge_snapshot_id="snap_q1",
        generation_config=run_baseline.build_generation_config(0.0, 1024, "prompt_v8", "concise", 1.0),
        answer_policy_version="answer_policy_v1",
    )
    assert payload["answer_policy_version"] == "answer_policy_v1"

    sample = {
        "question_id": "q1",
        "task_type": "answer_disposition",
        "must_answer": False,
        "provider_evidence": {"candidate_observation_valid": True},
        "passed": True,
    }
    monkeypatch.setattr(
        run_baseline,
        "run_remote_runtime_eval",
        lambda *_args, **_kwargs: ({"sample_count": 1}, [], [sample]),
    )
    report = run_baseline.build_remote_report(
        [],
        Path("data/eval-datasets/clarification/v1-q1.jsonl"),
        runtime_base_url="https://runtime.test",
        runtime_service_token="not-used",
        model_id="qwen3:14b",
        config_version="cfg_candidate_q1",
        retrieval_config_version="rcfg_q1",
        knowledge_snapshot_id="snap_q1",
        generation_config=run_baseline.build_generation_config(0.0, 1024, "prompt_v8", "concise", 1.0),
        embedding_model="qwen3-embedding:0.6b",
        retrieval_strategy="native_dense_v1",
        knowledge_scope=frozen["scope"],
        top_k=3,
        rerank_enabled=False,
        evidence_min_score=0.51,
        quality_experiment=None,
        router_policy_version="conversation_router_v1",
        answer_policy_version="answer_policy_v1",
        evaluation_role="candidate",
        conditions=frozen,
        conditions_sha256=run_baseline.canonical_json_sha256(frozen),
    )

    assert report["evaluation_role"] == "candidate"
    assert report["conditions_sha256"] == run_baseline.canonical_json_sha256(frozen)
    assert report["request_conditions"]["generation"]["answer_policy_version"] == "answer_policy_v1"
    assert report["quality_eligible"] is True
    assert "evaluation_purpose" not in report


def test_candidate_gate_checks_a1_maximum_metrics_and_role_bound_identity() -> None:
    gate = run_baseline.load_quality_gate(Path("data/eval-datasets/quality-gates/a1-q1-candidate-v1.json"))
    frozen = run_baseline.validate_a1_q1_remote_conditions(conditions())
    report = {
        "mode": "runtime-remote",
        "model_id": "qwen3:14b",
        "config_version": "cfg_candidate_q1",
        "retrieval_config_version": "rcfg_q1",
        "retrieval_strategy": "native_dense_v1",
        "generation_config": frozen["candidate"],
        "dataset_id": "v1-q1",
        "dataset_sha256": frozen["dataset"]["sha256"],
        "evaluation_role": "candidate",
        "conditions_sha256": run_baseline.canonical_json_sha256(frozen),
        "request_conditions": run_baseline.build_a1_q1_request_conditions(frozen, "candidate"),
        "quality_eligible": True,
        "metrics": {
            "passed_count": 80,
            "answer_disposition_accuracy": 0.96,
            "clarification_dimension_accuracy": 0.91,
            "clarification_resolution_accuracy": 0.91,
            "resolved_query_retrieval_hit_rate": 0.96,
            "citation_anchor_fidelity": 0.99,
            "answer_point_recall": 0.91,
            "coverage_honesty": 1.0,
            "unnecessary_clarification_rate": 0.05,
            "forbidden_answer_point_violation_count": 0,
        },
        "failure_type_counts": {},
    }
    report["generation_config"] = run_baseline._generation_config_from_conditions(frozen["candidate"])

    assert run_baseline.evaluate_quality_gate(gate, report)["passed"] is True
    report["metrics"]["passed_count"] = 71
    result = run_baseline.evaluate_quality_gate(gate, report)
    assert result["passed"] is False
    assert next(check for check in result["checks"] if check["name"] == "passed_count")["passed"] is False
    report["metrics"]["passed_count"] = 72
    assert run_baseline.evaluate_quality_gate(gate, report)["passed"] is True
    report["metrics"]["unnecessary_clarification_rate"] = 0.06
    result = run_baseline.evaluate_quality_gate(gate, report)
    assert result["passed"] is False
    assert next(check for check in result["checks"] if check["name"] == "metrics.unnecessary_clarification_rate")["passed"] is False
    report["metrics"]["unnecessary_clarification_rate"] = 0.05
    report["metrics"]["forbidden_answer_point_violation_count"] = 1
    result = run_baseline.evaluate_quality_gate(gate, report)
    assert result["passed"] is False
    assert next(check for check in result["checks"] if check["name"] == "metrics.forbidden_answer_point_violation_count")["passed"] is False


def test_candidate_gate_must_match_frozen_conditions_before_remote_execution() -> None:
    frozen = run_baseline.validate_a1_q1_remote_conditions(conditions())
    gate = run_baseline.load_quality_gate(Path("data/eval-datasets/quality-gates/a1-q1-candidate-v1.json"))

    run_baseline.validate_a1_q1_candidate_gate(gate, frozen)
    invalid = copy.deepcopy(gate)
    invalid["require_a1_q1_candidate"] = False
    with pytest.raises(run_baseline.DatasetError, match="must require"):
        run_baseline.validate_a1_q1_candidate_gate(invalid, frozen)
    invalid = copy.deepcopy(gate)
    invalid["generation_config"]["top_p"] = 0.9
    with pytest.raises(run_baseline.DatasetError, match="generation_config"):
        run_baseline.validate_a1_q1_candidate_gate(invalid, frozen)


def test_a1_q1_preflight_probe_selects_two_frozen_records_and_marks_report_non_evidence(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    conditions_path = tmp_path / "conditions.json"
    conditions_path.write_text(json.dumps(conditions()), encoding="utf-8")
    args = parse_preflight_probe_args(monkeypatch, conditions_path)

    probe_question_ids = run_baseline.validate_a1_q1_preflight_probe_args(args)
    frozen = run_baseline.load_a1_q1_remote_conditions(conditions_path)
    role, _ = run_baseline.apply_a1_q1_conditions(args, frozen)
    records = run_baseline.load_dataset(args.dataset)
    assert run_baseline.dataset_sha256(args.dataset) == frozen["dataset"]["sha256"]
    selected = run_baseline.select_a1_q1_preflight_probe_records(records, probe_question_ids)
    assert [record["question_id"] for record in selected] == probe_question_ids
    assert role == "candidate"

    sample = {
        "question_id": "a1_q1_001",
        "task_type": "answer_disposition",
        "must_answer": True,
        "provider_evidence": {"candidate_observation_valid": True, "quality_eligible": True},
        "passed": True,
    }
    monkeypatch.setattr(
        run_baseline,
        "run_remote_runtime_eval",
        lambda *_args, **_kwargs: ({"sample_count": 2}, [], [sample, sample]),
    )
    report = run_baseline.build_remote_report(
        selected,
        args.dataset,
        runtime_base_url="https://runtime.test",
        runtime_service_token="not-used",
        model_id="qwen3:14b",
        config_version="cfg_candidate_q1",
        retrieval_config_version="rcfg_q1",
        knowledge_snapshot_id="snap_q1",
        generation_config=run_baseline.build_generation_config(0.0, 1024, "prompt_v8", "concise", 1.0),
        embedding_model="qwen3-embedding:0.6b",
        retrieval_strategy="native_dense_v1",
        knowledge_scope=frozen["scope"],
        top_k=3,
        rerank_enabled=False,
        evidence_min_score=0.51,
        quality_experiment=None,
        router_policy_version="conversation_router_v1",
        answer_policy_version="answer_policy_v1",
        evaluation_role="candidate",
        conditions=frozen,
        conditions_sha256=run_baseline.canonical_json_sha256(frozen),
        evaluation_purpose="pre_q1_probe",
    )

    assert report["evaluation_purpose"] == "pre_q1_probe"
    assert report["comparison_eligible"] is False
    assert report["quality_eligible"] is False
    assert report["provider_evidence"]["comparison_eligible"] is False
    assert report["provider_evidence"]["quality_eligible"] is False


def test_pre_q1_probe_preserves_a_sanitized_strict_failure_observation(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    frozen = run_baseline.validate_a1_q1_remote_conditions(conditions())
    record = next(
        item
        for item in run_baseline.load_dataset(Path("data/eval-datasets/clarification/v1-q1.jsonl"))
        if item["question_id"] == "a1_q1_069"
    )
    response = candidate_clarification_response()
    response["assistant_message"] = {"role": "assistant", "content": "private assistant body must never enter the report"}
    monkeypatch.setattr(run_baseline, "post_runtime_json", lambda *_args, **_kwargs: response)

    report = run_baseline.build_remote_report(
        [record],
        Path("data/eval-datasets/clarification/v1-q1.jsonl"),
        runtime_base_url="https://runtime.test",
        runtime_service_token="not-used",
        model_id="qwen3:14b",
        config_version="cfg_candidate_q1",
        retrieval_config_version="rcfg_q1",
        knowledge_snapshot_id="snap_q1",
        generation_config=run_baseline.build_generation_config(0.0, 1024, "prompt_v8", "concise", 1.0),
        embedding_model="qwen3-embedding:0.6b",
        retrieval_strategy="native_dense_v1",
        knowledge_scope=frozen["scope"],
        top_k=3,
        rerank_enabled=False,
        evidence_min_score=0.51,
        quality_experiment=None,
        router_policy_version="conversation_router_v1",
        answer_policy_version="answer_policy_v1",
        evaluation_role="candidate",
        conditions=frozen,
        conditions_sha256=run_baseline.canonical_json_sha256(frozen),
        evaluation_purpose="pre_q1_probe",
    )

    sample = report["samples"][0]
    assert sample["observed_answer"] == ""
    assert "runtime_observation" not in sample
    assert sample["strict_observation_summary"] == {
        "disposition": "clarification_required",
        "refusal": False,
        "execution": {"policy": "performed", "retrieval": "not_run", "generation": "not_run"},
        "reason": "candidate Runtime answer_disposition did not match the frozen sample",
    }
    assert "private assistant body" not in json.dumps(report)
    assert report["evaluation_purpose"] == "pre_q1_probe"
    assert report["comparison_eligible"] is False
    assert report["quality_eligible"] is False
    assert report["provider_evidence"]["comparison_eligible"] is False
    assert report["provider_evidence"]["quality_eligible"] is False


@pytest.mark.parametrize(
    ("mode", "role", "conditions_present", "question_ids", "extra", "error"),
    [
        ("mock", "candidate", True, ["a1_q1_001", "a1_q1_002"], [], "--mode runtime-remote"),
        ("runtime-remote", "baseline", True, ["a1_q1_001", "a1_q1_002"], [], "--evaluation-role candidate"),
        ("runtime-remote", "candidate", False, ["a1_q1_001", "a1_q1_002"], [], "--a1-q1-conditions"),
        ("runtime-remote", "candidate", True, ["a1_q1_001"], [], "exactly two"),
        ("runtime-remote", "candidate", True, ["a1_q1_001", "a1_q1_002", "a1_q1_003"], [], "exactly two"),
        ("runtime-remote", "candidate", True, ["", "a1_q1_002"], [], "non-empty"),
        ("runtime-remote", "candidate", True, ["a1_q1_001", "a1_q1_001"], [], "unique"),
        (
            "runtime-remote",
            "candidate",
            True,
            ["a1_q1_001", "a1_q1_002"],
            ["--quality-gate", "data/eval-datasets/quality-gates/a1-q1-candidate-v1.json"],
            "must not use --quality-gate",
        ),
        (
            "runtime-remote",
            "candidate",
            True,
            ["a1_q1_001", "a1_q1_002"],
            ["--quality-experiment-id", "qexp_probe"],
            "must not register",
        ),
    ],
)
def test_a1_q1_preflight_probe_rejects_invalid_cli_boundaries(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    mode: str,
    role: str,
    conditions_present: bool,
    question_ids: list[str],
    extra: list[str],
    error: str,
) -> None:
    conditions_path = tmp_path / "conditions.json"
    conditions_path.write_text(json.dumps(conditions()), encoding="utf-8")
    args = parse_preflight_probe_args(
        monkeypatch,
        conditions_path if conditions_present else None,
        mode=mode,
        role=role,
        question_ids=question_ids,
        extra=extra,
    )

    with pytest.raises(run_baseline.DatasetError, match=error):
        run_baseline.validate_a1_q1_preflight_probe_args(args)


def test_a1_q1_preflight_probe_rejects_unknown_frozen_dataset_ids() -> None:
    records = run_baseline.load_dataset(Path("data/eval-datasets/clarification/v1-q1.jsonl"))

    with pytest.raises(run_baseline.DatasetError, match="not in the frozen dataset: a1_q1_missing"):
        run_baseline.select_a1_q1_preflight_probe_records(records, ["a1_q1_001", "a1_q1_missing"])


def test_a1_q1_preflight_probe_cannot_register_even_if_called_directly() -> None:
    with pytest.raises(run_baseline.ReportRegistrationError, match="cannot be registered"):
        run_baseline.register_quality_experiment_report(
            {"evaluation_purpose": "pre_q1_probe"},
            argparse.Namespace(),
            {"experiment_id": "qexp_probe", "role": "candidate", "conditions_sha256": "sha256:" + "1" * 64},
        )
