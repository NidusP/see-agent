from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import re
import sys
import time
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlparse
from urllib.request import ProxyHandler, Request, build_opener, urlopen


REQUIRED_FIELDS = {
    "question_id": str,
    "question": str,
    "task_type": str,
    "expected_source_files": list,
    "expected_source_locators": list,
    "must_answer": bool,
    "scoring_notes": str,
}

OPTIONAL_FIELDS = {
    "expected_answer_points": list,
    "format_family": str,
    "difficulty": str,
    "runtime_intent": str,
    "conversation_turns": list,
    "expected_context_resolution": dict,
    "expected_disposition": str,
    "expected_router_policy_version": str,
    "required_scope_dimensions": list,
    "expected_missing_dimensions": list,
    "forbidden_answer_points": list,
    "expected_used_citation_ranks": list,
    "expected_coverage": dict,
    "expected_clarification_resolution": str,
    "expected_resolved_query": str,
    "boundary_tags": list,
    "evaluation_pair_id": str,
}

VALID_TASK_TYPES = {
    "single_source_fact_lookup",
    "same_file_multi_block",
    "cross_file_synthesis",
    "policy_flow_answer",
    "locator_check",
    "table_lookup",
    "insufficient_evidence",
    "router_context_resolution",
    "answer_disposition",
}

FORMAT_FAMILIES = {"txt", "md", "json", "pdf", "docx", "xlsx", "pptx", "must_refuse", "cross_format"}
RUNTIME_SOURCE_FORMATS = {"txt", "md", "json", "pdf", "docx", "xlsx", "pptx"}
REPO_ROOT = Path(__file__).resolve().parents[2]
RUNTIME_ROOT = REPO_ROOT / "services" / "ai-runtime-py"
DEFAULT_TEMPERATURE = 0.2
DEFAULT_MAX_OUTPUT_TOKENS = 1024
DEFAULT_SYSTEM_PROMPT_VERSION = "prompt_v1"
K8_R2_EXPERIMENT_SNAPSHOT_ID = "snapshot_k8_r2_fixture_v1"
REGISTERED_SYSTEM_PROMPT_VERSIONS = {
    "prompt_v1",
    "prompt_v2",
    "prompt_v3",
    "prompt_v4",
    "prompt_v5",
    "prompt_v6",
    "prompt_v7",
    "prompt_v8",
    "prompt_v9",
    "prompt_v10",
    "prompt_v11",
    "prompt_v12",
}
QUALITY_GATE_REQUIRED_FIELDS = {
    "gate_id",
    "mode",
    "model_id",
    "generation_config",
    "minimum_passed_count",
    "minimum_metrics",
    "maximum_failure_type_counts",
}
QUALITY_GATE_OPTIONAL_FIELDS = {"maximum_metrics", "require_a1_q1_candidate"}
QUALITY_GATE_METRICS = {
    "retrieval_recall_at_k",
    "expected_file_hit_rate",
    "grounded_correctness",
    "citation_accuracy",
    "refusal_correctness",
    "router_resolution_accuracy",
    "answer_disposition_accuracy",
    "clarification_dimension_accuracy",
    "clarification_resolution_accuracy",
    "resolved_query_retrieval_hit_rate",
    "citation_anchor_fidelity",
    "answer_point_recall",
    "unnecessary_clarification_rate",
    "coverage_honesty",
    "forbidden_answer_point_violation_count",
}
QUALITY_GATE_FAILURE_TYPES = {
    "unexpected_runtime_error",
    "retrieval_miss",
    "citation_mismatch",
    "answer_point_miss",
    "refusal_policy_failure",
    "router_quality_mismatch",
    "runtime_contract_violation",
    "answer_policy_protocol_failure",
    "repeated_clarification",
    "internal_marker_leak",
    "unsupported_business_claim",
}
QUALITY_EXPERIMENT_ROLES = {"baseline", "candidate"}
QUALITY_EXPERIMENT_ENDPOINT_TOKEN = "{experiment_id}"
RUNTIME_EVAL_MODES = {"runtime-local", "runtime-remote"}
CONVERSATION_ROUTER_POLICY_VERSION = "conversation_router_v1"
ROUTER_TASK_TYPES = {"router_context_resolution"}
A1_TASK_TYPES = {"answer_disposition"}
A1_ANSWER_DISPOSITIONS = {
    "grounded_answer",
    "clarification_required",
    "insufficient_evidence",
    "partial_answer",
}
A1_CLARIFICATION_RESOLUTIONS = {"resolved", "abandoned", "needs_more"}
A1_BOUNDARY_TAGS = {
    "threshold_exact",
    "threshold_adjacent",
    "precondition",
    "exception",
    "negative_condition",
    "sequence",
    "time_clock",
    "time_business_day",
    "role_separation",
    "scope_missing",
    "scope_paired_resolution",
    "topic_abandonment",
    "needs_more",
    "evidence_absent",
    "count_partial",
    "revision_scope",
    "prompt_injection",
    "ambiguity",
}
A1_Q1_CONDITIONS_SCHEMA_VERSION_V1 = "a1_q1_remote_conditions_v1"
A1_Q1_CONDITIONS_SCHEMA_VERSION_V2 = "a1_q1_remote_conditions_v2"
A1_Q1_CONDITIONS_SCHEMA_VERSION_V3 = "a1_q1_remote_conditions_v3"
A1_Q1_CONDITIONS_SCHEMA_VERSION_V4 = "a1_q1_remote_conditions_v4"
A1_Q1_CONDITIONS_SCHEMA_VERSION_V5 = "a1_q1_remote_conditions_v5"
A1_Q1_CONDITIONS_SCHEMA_VERSIONS = {
    A1_Q1_CONDITIONS_SCHEMA_VERSION_V1,
    A1_Q1_CONDITIONS_SCHEMA_VERSION_V2,
    A1_Q1_CONDITIONS_SCHEMA_VERSION_V3,
    A1_Q1_CONDITIONS_SCHEMA_VERSION_V4,
    A1_Q1_CONDITIONS_SCHEMA_VERSION_V5,
}
A1_Q1_CANDIDATE_PAIR_BY_SCHEMA = {
    A1_Q1_CONDITIONS_SCHEMA_VERSION_V1: ("prompt_v8", "answer_policy_v1", "a1-q1-candidate-v1"),
    A1_Q1_CONDITIONS_SCHEMA_VERSION_V2: ("prompt_v9", "answer_policy_v2", "a1-q1-candidate-v2"),
    A1_Q1_CONDITIONS_SCHEMA_VERSION_V3: ("prompt_v10", "answer_policy_v3", "a1-q1-candidate-v3"),
    A1_Q1_CONDITIONS_SCHEMA_VERSION_V4: ("prompt_v11", "answer_policy_v4", "a1-q1-candidate-v4"),
    A1_Q1_CONDITIONS_SCHEMA_VERSION_V5: ("prompt_v12", "answer_policy_v5", "a1-q1-candidate-v5"),
}
A1_Q1_CONDITIONS_FIELDS = {
    "schema_version",
    "dataset",
    "scope",
    "retrieval",
    "baseline",
    "candidate",
    "router_policy_version",
    "candidate_gate_id",
}
A1_Q1_RETRIEVAL_FIELDS = {
    "config_version",
    "strategy",
    "embedding_model",
    "top_k",
    "rerank_enabled",
    "evidence_min_score",
}
A1_Q1_ROLE_FIELDS = {
    "config_version",
    "model_id",
    "system_prompt_version",
    "answer_policy_version",
    "response_style",
    "temperature",
    "top_p",
    "max_output_tokens",
}


class DatasetError(ValueError):
    pass


class QualityGateError(ValueError):
    pass


class ReportRegistrationError(RuntimeError):
    pass


class RemoteObservationError(DatasetError):
    def __init__(self, message: str, failure_type: str = "runtime_contract_violation") -> None:
        super().__init__(message)
        self.failure_type = failure_type


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def canonical_json_sha256(value: object) -> str:
    """Hash a JSON value independent of whitespace and object member order."""
    try:
        canonical = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    except (TypeError, ValueError) as exc:
        raise DatasetError(f"conditions cannot be canonicalized: {exc}") from exc
    return "sha256:" + hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def load_a1_q1_remote_conditions(path: Path) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except OSError as exc:
        raise DatasetError(f"cannot read A1-Q1 conditions {path}: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise DatasetError(f"invalid A1-Q1 conditions JSON {path}: {exc}") from exc
    return validate_a1_q1_remote_conditions(payload)


def validate_a1_q1_remote_conditions(payload: object) -> dict[str, Any]:
    if not isinstance(payload, dict):
        raise DatasetError("A1-Q1 conditions must be a JSON object")
    if set(payload) != A1_Q1_CONDITIONS_FIELDS:
        missing = sorted(A1_Q1_CONDITIONS_FIELDS - set(payload))
        unknown = sorted(set(payload) - A1_Q1_CONDITIONS_FIELDS)
        details = []
        if missing:
            details.append(f"missing fields: {', '.join(missing)}")
        if unknown:
            details.append(f"unknown fields: {', '.join(unknown)}")
        raise DatasetError(f"invalid A1-Q1 conditions fields ({'; '.join(details)})")
    schema_version = payload["schema_version"]
    if not isinstance(schema_version, str) or schema_version not in A1_Q1_CONDITIONS_SCHEMA_VERSIONS:
        expected = ", ".join(sorted(A1_Q1_CONDITIONS_SCHEMA_VERSIONS))
        raise DatasetError(f"A1-Q1 conditions schema_version must be one of: {expected}")

    dataset = payload["dataset"]
    if not isinstance(dataset, dict) or set(dataset) != {"id", "sha256"}:
        raise DatasetError("A1-Q1 conditions dataset must contain only id and sha256")
    dataset_id = _required_string(dataset["id"], "dataset.id")
    dataset_hash = _validate_sha256(dataset["sha256"], "dataset.sha256")

    scope = validate_knowledge_scope_value(payload["scope"], "A1-Q1 conditions scope")
    retrieval = _validate_a1_q1_retrieval(payload["retrieval"])
    baseline = _validate_a1_q1_role_config(payload["baseline"], "baseline")
    candidate = _validate_a1_q1_role_config(payload["candidate"], "candidate")
    _validate_a1_q1_role_pair(baseline, candidate, retrieval, schema_version)
    router_policy_version = _required_string(payload["router_policy_version"], "router_policy_version")
    if router_policy_version != CONVERSATION_ROUTER_POLICY_VERSION:
        raise DatasetError(f"router_policy_version must be {CONVERSATION_ROUTER_POLICY_VERSION}")
    candidate_gate_id = _required_string(payload["candidate_gate_id"], "candidate_gate_id")
    expected_gate_id = A1_Q1_CANDIDATE_PAIR_BY_SCHEMA[schema_version][2]
    if candidate_gate_id != expected_gate_id:
        raise DatasetError(f"A1-Q1 {schema_version} candidate_gate_id must be {expected_gate_id}")
    return {
        "schema_version": schema_version,
        "dataset": {"id": dataset_id, "sha256": dataset_hash},
        "scope": scope,
        "retrieval": retrieval,
        "baseline": baseline,
        "candidate": candidate,
        "router_policy_version": router_policy_version,
        "candidate_gate_id": candidate_gate_id,
    }


def _required_string(value: object, field_name: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise DatasetError(f"{field_name} must be a non-empty string")
    return value.strip()


def _validate_sha256(value: object, field_name: str) -> str:
    if not isinstance(value, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", value):
        raise DatasetError(f"{field_name} must be sha256:<64 lowercase hex characters>")
    return value


def validate_knowledge_scope_value(value: object, label: str = "knowledge scope") -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != {"snapshot_id", "manifest_hash", "revision_ids", "resolved_at"}:
        raise DatasetError(f"{label} must contain snapshot_id, manifest_hash, revision_ids and resolved_at")
    snapshot_id = _required_string(value["snapshot_id"], f"{label}.snapshot_id")
    manifest_hash = _validate_sha256(value["manifest_hash"], f"{label}.manifest_hash")
    revision_ids = value["revision_ids"]
    if not isinstance(revision_ids, list) or not revision_ids or not all(isinstance(item, str) and item.strip() for item in revision_ids):
        raise DatasetError(f"{label}.revision_ids must be a non-empty list of strings")
    if len(set(revision_ids)) != len(revision_ids):
        raise DatasetError(f"{label}.revision_ids must be unique")
    resolved_at = _required_string(value["resolved_at"], f"{label}.resolved_at")
    return {
        "snapshot_id": snapshot_id,
        "manifest_hash": manifest_hash,
        "revision_ids": list(revision_ids),
        "resolved_at": resolved_at,
    }


def _validate_a1_q1_retrieval(value: object) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != A1_Q1_RETRIEVAL_FIELDS:
        raise DatasetError("A1-Q1 retrieval must contain the frozen config, strategy, model and boundary fields")
    config_version = _required_string(value["config_version"], "retrieval.config_version")
    strategy = _required_string(value["strategy"], "retrieval.strategy")
    if strategy not in {"lexical_compat_v1", "native_dense_v1"}:
        raise DatasetError("retrieval.strategy must be lexical_compat_v1 or native_dense_v1")
    embedding_model = _required_string(value["embedding_model"], "retrieval.embedding_model")
    top_k = value["top_k"]
    if isinstance(top_k, bool) or not isinstance(top_k, int) or top_k < 1:
        raise DatasetError("retrieval.top_k must be a positive integer")
    if not isinstance(value["rerank_enabled"], bool):
        raise DatasetError("retrieval.rerank_enabled must be boolean")
    try:
        evidence_min_score = parse_evidence_min_score(str(value["evidence_min_score"]))
    except argparse.ArgumentTypeError as exc:
        raise DatasetError(str(exc)) from exc
    return {
        "config_version": config_version,
        "strategy": strategy,
        "embedding_model": embedding_model,
        "top_k": top_k,
        "rerank_enabled": value["rerank_enabled"],
        "evidence_min_score": evidence_min_score,
    }


def _validate_a1_q1_role_config(value: object, role: str) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != A1_Q1_ROLE_FIELDS:
        raise DatasetError(f"A1-Q1 {role} must contain every frozen generation and policy field")
    answer_policy_version = value["answer_policy_version"]
    if answer_policy_version is not None:
        answer_policy_version = _required_string(answer_policy_version, f"{role}.answer_policy_version")
    try:
        generation_config = build_generation_config(
            value["temperature"],
            value["max_output_tokens"],
            value["system_prompt_version"],
            value["response_style"],
            value["top_p"],
        )
    except ValueError as exc:
        raise DatasetError(f"invalid {role} generation config: {exc}") from exc
    return {
        "config_version": _required_string(value["config_version"], f"{role}.config_version"),
        "model_id": _required_string(value["model_id"], f"{role}.model_id"),
        **generation_config,
        "answer_policy_version": answer_policy_version,
    }


def _validate_a1_q1_role_pair(
    baseline: dict[str, Any],
    candidate: dict[str, Any],
    retrieval: dict[str, Any],
    schema_version: str,
) -> None:
    if baseline["system_prompt_version"] != "prompt_v7":
        raise DatasetError("A1-Q1 baseline must freeze prompt_v7")
    expected_prompt_version, expected_policy_version, _ = A1_Q1_CANDIDATE_PAIR_BY_SCHEMA[schema_version]
    if (
        candidate["system_prompt_version"] != expected_prompt_version
        or candidate["answer_policy_version"] != expected_policy_version
    ):
        raise DatasetError(
            f"A1-Q1 {schema_version} candidate must freeze {expected_prompt_version} + {expected_policy_version}"
        )
    if baseline["answer_policy_version"] is not None:
        raise DatasetError("A1-Q1 baseline answer_policy_version must be null")
    if baseline["config_version"] == candidate["config_version"]:
        raise DatasetError("A1-Q1 candidate config_version must differ from baseline")
    for field_name in ("model_id", "temperature", "max_output_tokens", "response_style", "top_p"):
        if baseline[field_name] != candidate[field_name]:
            raise DatasetError(f"A1-Q1 baseline and candidate must match {field_name}")
    if not retrieval["config_version"]:
        raise DatasetError("A1-Q1 retrieval config_version must not be empty")


def build_a1_q1_request_conditions(conditions: dict[str, Any], evaluation_role: str | None) -> dict[str, Any]:
    if evaluation_role not in QUALITY_EXPERIMENT_ROLES:
        raise DatasetError("A1-Q1 report requires evaluation_role baseline or candidate")
    return {
        "dataset": conditions["dataset"],
        "scope": conditions["scope"],
        "retrieval": conditions["retrieval"],
        "generation": conditions[evaluation_role],
        "router_policy_version": conditions["router_policy_version"],
        "candidate_gate_id": conditions["candidate_gate_id"],
    }


def apply_a1_q1_conditions(args: argparse.Namespace, conditions: dict[str, Any]) -> tuple[str, str | None]:
    """Reject drift, then derive every report-relevant Q1 request value from conditions."""
    if args.mode != "runtime-remote":
        raise DatasetError("A1-Q1 conditions require --mode runtime-remote")
    role = args.evaluation_role or args.quality_experiment_role
    if role not in QUALITY_EXPERIMENT_ROLES:
        raise DatasetError("A1-Q1 conditions require --evaluation-role baseline or candidate")
    if args.evaluation_role and args.quality_experiment_role and args.evaluation_role != args.quality_experiment_role:
        raise DatasetError("evaluation role conflicts with quality experiment role")
    if args.quality_experiment_conditions_sha256 and args.quality_experiment_conditions_sha256 != canonical_json_sha256(conditions):
        raise DatasetError("quality experiment conditions SHA-256 conflicts with A1-Q1 conditions")

    frozen_role = conditions[role]
    frozen_retrieval = conditions["retrieval"]
    frozen_dataset_path = Path("data/eval-datasets/clarification") / f"{conditions['dataset']['id']}.jsonl"
    if _cli_flag_supplied("--dataset") and args.dataset != frozen_dataset_path:
        raise DatasetError("--dataset conflicts with frozen A1-Q1 conditions")
    conflicts = {
        "model_id": (args.model_id, frozen_role["model_id"]),
        "config_version": (args.config_version, frozen_role["config_version"]),
        "retrieval_config_version": (args.retrieval_config_version, frozen_retrieval["config_version"]),
        "knowledge_snapshot_id": (args.knowledge_snapshot_id, conditions["scope"]["snapshot_id"]),
        "retrieval_strategy": (args.retrieval_strategy, frozen_retrieval["strategy"]),
        "embedding_model": (args.embedding_model, frozen_retrieval["embedding_model"]),
        "top_k": (args.top_k, frozen_retrieval["top_k"]),
        "rerank_enabled": (args.rerank_enabled, frozen_retrieval["rerank_enabled"]),
        "evidence_min_score": (args.evidence_min_score, frozen_retrieval["evidence_min_score"]),
        "system_prompt_version": (args.system_prompt_version, frozen_role["system_prompt_version"]),
        "response_style": (args.response_style, frozen_role["response_style"]),
        "top_p": (args.top_p, frozen_role["top_p"]),
        "temperature": (args.temperature, frozen_role["temperature"]),
        "max_output_tokens": (args.max_output_tokens, frozen_role["max_output_tokens"]),
        "router_policy_version": (args.router_policy_version, conditions["router_policy_version"]),
    }
    # Defaults are legacy runner values. They are overwritten by conditions; only
    # explicitly set, contradictory values must block an immutable Q1 run.
    for field_name, (actual, expected) in conflicts.items():
        flag = "--" + field_name.replace("_", "-")
        if _cli_flag_supplied(flag) and actual != expected:
            raise DatasetError(f"{flag} conflicts with frozen A1-Q1 conditions")
    if role == "candidate" and args.quality_gate is None and not args.probe_question_ids:
        raise DatasetError("candidate A1-Q1 conditions require the frozen candidate quality gate")
    if args.quality_gate is not None:
        gate = load_quality_gate(args.quality_gate)
        if role != "candidate" or gate["gate_id"] != conditions["candidate_gate_id"]:
            raise DatasetError("quality gate conflicts with frozen A1-Q1 role or candidate gate")
        validate_a1_q1_candidate_gate(gate, conditions)

    args.dataset = frozen_dataset_path
    args.model_id = frozen_role["model_id"]
    args.config_version = frozen_role["config_version"]
    args.retrieval_config_version = frozen_retrieval["config_version"]
    args.knowledge_snapshot_id = conditions["scope"]["snapshot_id"]
    args.retrieval_strategy = frozen_retrieval["strategy"]
    args.embedding_model = frozen_retrieval["embedding_model"]
    args.top_k = frozen_retrieval["top_k"]
    args.rerank_enabled = frozen_retrieval["rerank_enabled"]
    args.evidence_min_score = frozen_retrieval["evidence_min_score"]
    args.system_prompt_version = frozen_role["system_prompt_version"]
    args.response_style = frozen_role["response_style"]
    args.top_p = frozen_role["top_p"]
    args.temperature = frozen_role["temperature"]
    args.max_output_tokens = frozen_role["max_output_tokens"]
    args.router_policy_version = conditions["router_policy_version"]
    args.knowledge_scope_file = None
    return role, frozen_role["answer_policy_version"]


def validate_a1_q1_preflight_probe_args(args: argparse.Namespace) -> list[str]:
    """Keep the two-question Runtime probe outside the Q1 evidence write path."""
    probe_question_ids = [question_id.strip() for question_id in args.probe_question_ids]
    if not probe_question_ids:
        return []
    if args.mode != "runtime-remote":
        raise DatasetError("A1-Q1 preflight probe requires --mode runtime-remote")
    if args.a1_q1_conditions is None:
        raise DatasetError("A1-Q1 preflight probe requires --a1-q1-conditions")
    if args.evaluation_role != "candidate":
        raise DatasetError("A1-Q1 preflight probe requires --evaluation-role candidate")
    if len(probe_question_ids) != 2:
        raise DatasetError("A1-Q1 preflight probe requires exactly two --probe-question-id values")
    if not all(probe_question_ids):
        raise DatasetError("A1-Q1 preflight probe question IDs must be non-empty")
    if len(set(probe_question_ids)) != len(probe_question_ids):
        raise DatasetError("A1-Q1 preflight probe question IDs must be unique")
    if args.quality_gate is not None:
        raise DatasetError("A1-Q1 preflight probe must not use --quality-gate")
    registration_options = {
        "--quality-experiment-id": args.quality_experiment_id,
        "--quality-experiment-role": args.quality_experiment_role,
        "--quality-experiment-conditions-sha256": args.quality_experiment_conditions_sha256,
        "--backend-eval-report-endpoint": args.backend_eval_report_endpoint,
        "--quality-experiment-report-endpoint": args.quality_experiment_report_endpoint,
    }
    supplied = [flag for flag, value in registration_options.items() if value is not None]
    if supplied:
        raise DatasetError(
            "A1-Q1 preflight probe must not register a quality experiment report: " + ", ".join(supplied)
        )
    return probe_question_ids


def select_a1_q1_preflight_probe_records(
    records: list[dict[str, Any]], probe_question_ids: list[str]
) -> list[dict[str, Any]]:
    records_by_id = {record["question_id"]: record for record in records}
    missing = [question_id for question_id in probe_question_ids if question_id not in records_by_id]
    if missing:
        raise DatasetError(
            "A1-Q1 preflight probe question IDs are not in the frozen dataset: " + ", ".join(missing)
        )
    return [records_by_id[question_id] for question_id in probe_question_ids]


def validate_a1_q1_candidate_gate(gate: dict[str, Any], conditions: dict[str, Any]) -> None:
    candidate = conditions["candidate"]
    expected_generation_config = _generation_config_from_conditions(candidate)
    if gate.get("gate_id") != conditions["candidate_gate_id"]:
        raise DatasetError("A1-Q1 candidate gate ID does not match frozen conditions")
    if gate.get("require_a1_q1_candidate") is not True:
        raise DatasetError("A1-Q1 candidate gate must require A1-Q1 candidate evidence")
    if gate.get("mode") != "runtime-remote":
        raise DatasetError("A1-Q1 candidate gate must use runtime-remote mode")
    if gate.get("model_id") != candidate["model_id"]:
        raise DatasetError("A1-Q1 candidate gate model_id does not match frozen candidate conditions")
    if gate.get("generation_config") != expected_generation_config:
        raise DatasetError("A1-Q1 candidate gate generation_config does not match frozen candidate conditions")


def _cli_flag_supplied(flag: str) -> bool:
    return any(
        argument == flag
        or argument.startswith(flag + "=")
        or argument == "--no-" + flag.removeprefix("--")
        for argument in sys.argv[1:]
    )


def load_dataset(path: Path) -> list[dict[str, Any]]:
    if not path.exists():
        raise DatasetError(f"dataset does not exist: {path}")

    records: list[dict[str, Any]] = []
    seen_ids: set[str] = set()
    with path.open("r", encoding="utf-8") as handle:
        for line_number, raw_line in enumerate(handle, start=1):
            line = raw_line.strip()
            if not line:
                continue
            try:
                record = json.loads(line)
            except json.JSONDecodeError as exc:
                raise DatasetError(f"line {line_number}: invalid JSON: {exc}") from exc

            validate_record(record, line_number)
            question_id = record["question_id"]
            if question_id in seen_ids:
                raise DatasetError(f"line {line_number}: duplicate question_id {question_id}")
            seen_ids.add(question_id)
            records.append(record)

    if not records:
        raise DatasetError("dataset is empty")
    return records


def build_experiment_knowledge_scope(source_identities: list[dict[str, str]]) -> dict[str, Any]:
    """Build a model-independent scope for the K8-R2 fixture corpus."""
    if not source_identities:
        raise DatasetError("K8-R2 experiment scope requires at least one source identity")
    required_fields = {"document_id", "revision_id", "checksum_sha256", "file_id", "source_format"}
    normalized: list[dict[str, str]] = []
    for identity in source_identities:
        if set(identity) != required_fields or not all(identity.values()):
            raise DatasetError("K8-R2 experiment source identity is incomplete")
        normalized.append(dict(identity))
    normalized.sort(key=lambda item: item["revision_id"])
    revision_ids = [item["revision_id"] for item in normalized]
    if len(set(revision_ids)) != len(revision_ids):
        raise DatasetError("K8-R2 experiment revisions must be unique")
    canonical = json.dumps(normalized, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return {
        "snapshot_id": K8_R2_EXPERIMENT_SNAPSHOT_ID,
        "manifest_hash": f"sha256:{hashlib.sha256(canonical.encode('utf-8')).hexdigest()}",
        "revision_ids": revision_ids,
        "resolved_at": "2026-07-17T00:00:00Z",
    }


def materialize_k8_r2_experiment_scope() -> dict[str, Any]:
    """Project the sample corpus as immutable experiment revisions in the active collection."""
    runtime_main = load_runtime_main()
    from app.api.schemas import IngestionRunRequest
    from app.pipelines.retrieval_pipeline import build_default_corpus_documents

    requests: list[Any] = []
    identities: list[dict[str, str]] = []
    for document in sorted(build_default_corpus_documents(), key=lambda item: item.file_id):
        source_path = Path(document.object_uri)
        if not source_path.is_file():
            raise DatasetError(f"K8-R2 fixture is unavailable: {source_path}")
        checksum_sha256 = f"sha256:{hashlib.sha256(source_path.read_bytes()).hexdigest()}"
        document_id = f"eval-r2-document-{document.file_id}"
        revision_id = f"eval-r2-revision-{document.file_id}-v1"
        identities.append(
            {
                "document_id": document_id,
                "revision_id": revision_id,
                "checksum_sha256": checksum_sha256,
                "file_id": document.file_id,
                "source_format": document.source_format,
            }
        )
        requests.append(
            IngestionRunRequest(
                request_id=f"eval-r2-ingest-{document.file_id}",
                task_id=f"eval-r2-task-{document.file_id}",
                file_id=document.file_id,
                document_id=document_id,
                revision_id=revision_id,
                delivery_id=f"eval-r2-delivery-{document.file_id}",
                checksum_sha256=checksum_sha256,
                attempt=1,
                filename=document.filename,
                mime_type=document.mime_type,
                source_format=document.source_format,
                object_uri=document.object_uri,
                parse_version="parse_v1",
                index_version="eval_k8_r2_index_v1",
                requested_at="2026-07-17T00:00:00Z",
            )
        )

    scope = build_experiment_knowledge_scope(identities)
    for request in requests:
        response = runtime_main.ingestion_pipeline.execute(request)
        if response.status != "succeeded" or not response.chunk_manifest_sha256:
            detail = response.error.message if response.error else "missing revision manifest"
            raise DatasetError(f"K8-R2 fixture projection failed for {request.file_id}: {detail}")
    return scope


def validate_record(record: dict[str, Any], line_number: int) -> None:
    for field_name, expected_type in REQUIRED_FIELDS.items():
        if field_name not in record:
            raise DatasetError(f"line {line_number}: missing required field {field_name}")
        if not isinstance(record[field_name], expected_type):
            raise DatasetError(f"line {line_number}: {field_name} must be {expected_type.__name__}")

    for field_name, expected_type in OPTIONAL_FIELDS.items():
        if field_name in record and not isinstance(record[field_name], expected_type):
            raise DatasetError(f"line {line_number}: {field_name} must be {expected_type.__name__}")

    if not record["question"].strip():
        raise DatasetError(f"line {line_number}: question must not be empty")
    if record["task_type"] not in VALID_TASK_TYPES:
        raise DatasetError(f"line {line_number}: unsupported task_type {record['task_type']}")

    format_family = record.get("format_family")
    if format_family is not None and format_family not in FORMAT_FAMILIES:
        raise DatasetError(f"line {line_number}: unsupported format_family {format_family}")

    if record["task_type"] in ROUTER_TASK_TYPES:
        validate_router_record(record, line_number)
    elif record["task_type"] in A1_TASK_TYPES:
        validate_a1_record(record, line_number)
    elif record["must_answer"]:
        if not record["expected_source_files"]:
            raise DatasetError(f"line {line_number}: must-answer samples require expected_source_files")
        if not record["expected_source_locators"]:
            raise DatasetError(f"line {line_number}: must-answer samples require expected_source_locators")
    else:
        if record["expected_source_files"] or record["expected_source_locators"]:
            raise DatasetError(f"line {line_number}: refusal samples must not declare expected sources")


def validate_a1_record(record: dict[str, Any], line_number: int) -> None:
    for field_name in (
        "required_scope_dimensions",
        "expected_missing_dimensions",
        "forbidden_answer_points",
        "expected_used_citation_ranks",
    ):
        if field_name not in record:
            raise DatasetError(f"line {line_number}: answer_disposition requires {field_name}")
    expected_disposition = record.get("expected_disposition")
    if expected_disposition not in A1_ANSWER_DISPOSITIONS:
        allowed = ", ".join(sorted(A1_ANSWER_DISPOSITIONS))
        raise DatasetError(f"line {line_number}: answer_disposition requires expected_disposition ({allowed})")

    required_scope_dimensions = validate_nonempty_string_list(
        record.get("required_scope_dimensions"),
        "required_scope_dimensions",
        line_number,
    )
    del required_scope_dimensions
    expected_missing_dimensions = validate_string_list(
        record.get("expected_missing_dimensions", []),
        "expected_missing_dimensions",
        line_number,
    )
    validate_string_list(record.get("forbidden_answer_points", []), "forbidden_answer_points", line_number)
    expected_used_citation_ranks = validate_citation_ranks(
        record.get("expected_used_citation_ranks"),
        line_number,
    )
    validate_conversation_turns(record.get("conversation_turns", []), line_number)
    boundary_tags = record.get("boundary_tags")
    if boundary_tags is not None:
        normalized_boundary_tags = validate_nonempty_string_list(boundary_tags, "boundary_tags", line_number)
        if len(set(normalized_boundary_tags)) != len(normalized_boundary_tags):
            raise DatasetError(f"line {line_number}: boundary_tags must be unique")
        unknown_boundary_tags = sorted(set(normalized_boundary_tags) - A1_BOUNDARY_TAGS)
        if unknown_boundary_tags:
            raise DatasetError(f"line {line_number}: unsupported boundary_tags: {', '.join(unknown_boundary_tags)}")
    evaluation_pair_id = record.get("evaluation_pair_id")
    if evaluation_pair_id is not None and not evaluation_pair_id.strip():
        raise DatasetError(f"line {line_number}: evaluation_pair_id must not be empty")

    answers_with_evidence = {"grounded_answer", "partial_answer"}
    if record["must_answer"] != (expected_disposition in answers_with_evidence):
        raise DatasetError(f"line {line_number}: must_answer conflicts with expected_disposition")
    if record["must_answer"]:
        if not record["expected_source_files"] or not record["expected_source_locators"]:
            raise DatasetError(f"line {line_number}: evidence answers require expected source files and locators")
        if not expected_used_citation_ranks:
            raise DatasetError(f"line {line_number}: evidence answers require expected_used_citation_ranks")
    elif record["expected_source_files"] or record["expected_source_locators"] or expected_used_citation_ranks:
        raise DatasetError(f"line {line_number}: clarification and insufficient-evidence samples cannot declare citations")

    expected_coverage = record.get("expected_coverage")
    if expected_disposition == "partial_answer":
        validate_expected_coverage(expected_coverage, line_number)
    elif expected_coverage is not None:
        raise DatasetError(f"line {line_number}: expected_coverage is only valid for partial_answer")

    if expected_disposition == "clarification_required" and not expected_missing_dimensions:
        raise DatasetError(f"line {line_number}: clarification_required requires expected_missing_dimensions")
    if expected_disposition == "insufficient_evidence" and expected_missing_dimensions:
        raise DatasetError(f"line {line_number}: insufficient_evidence cannot declare missing scope dimensions")

    clarification_resolution = record.get("expected_clarification_resolution")
    resolved_query = record.get("expected_resolved_query")
    if clarification_resolution is None:
        if resolved_query is not None:
            raise DatasetError(f"line {line_number}: expected_resolved_query requires expected_clarification_resolution=resolved")
        return
    if clarification_resolution not in A1_CLARIFICATION_RESOLUTIONS:
        allowed = ", ".join(sorted(A1_CLARIFICATION_RESOLUTIONS))
        raise DatasetError(f"line {line_number}: expected_clarification_resolution must be one of {allowed}")
    if not record.get("conversation_turns"):
        raise DatasetError(f"line {line_number}: clarification resolution requires conversation_turns")
    if clarification_resolution == "resolved":
        if not isinstance(resolved_query, str) or not resolved_query.strip():
            raise DatasetError(f"line {line_number}: resolved clarification requires expected_resolved_query")
    elif resolved_query is not None:
        raise DatasetError(f"line {line_number}: only resolved clarification may declare expected_resolved_query")


def validate_nonempty_string_list(value: object, field_name: str, line_number: int) -> list[str]:
    values = validate_string_list(value, field_name, line_number)
    if not values:
        raise DatasetError(f"line {line_number}: {field_name} must not be empty")
    return values


def validate_string_list(value: object, field_name: str, line_number: int) -> list[str]:
    if not isinstance(value, list) or not all(isinstance(item, str) and item.strip() for item in value):
        raise DatasetError(f"line {line_number}: {field_name} must be a string list")
    return value


def validate_citation_ranks(value: object, line_number: int) -> list[int]:
    if not isinstance(value, list) or any(isinstance(rank, bool) or not isinstance(rank, int) or rank < 1 for rank in value):
        raise DatasetError(f"line {line_number}: expected_used_citation_ranks must be positive integers")
    if len(set(value)) != len(value):
        raise DatasetError(f"line {line_number}: expected_used_citation_ranks must be unique")
    return value


def validate_expected_coverage(value: object, line_number: int) -> None:
    if not isinstance(value, dict) or set(value) != {"requested_count", "supported_count", "uncovered_description"}:
        raise DatasetError(
            f"line {line_number}: expected_coverage must contain requested_count, supported_count and uncovered_description"
        )
    requested_count = value["requested_count"]
    supported_count = value["supported_count"]
    uncovered_description = value["uncovered_description"]
    if (
        isinstance(requested_count, bool)
        or isinstance(supported_count, bool)
        or not isinstance(requested_count, int)
        or not isinstance(supported_count, int)
        or not 0 < supported_count < requested_count
        or not isinstance(uncovered_description, str)
        or not uncovered_description.strip()
    ):
        raise DatasetError(f"line {line_number}: expected_coverage must describe a positive partial result")


def validate_conversation_turns(turns: object, line_number: int) -> None:
    if not isinstance(turns, list):
        raise DatasetError(f"line {line_number}: conversation_turns must be a list")
    for index, turn in enumerate(turns, start=1):
        if not isinstance(turn, dict) or not all(
            isinstance(turn.get(field), str) and turn[field].strip() for field in ("message_id", "role", "content")
        ):
            raise DatasetError(f"line {line_number}: conversation_turns[{index}] must include message_id, role and content")
        if turn["role"] not in {"user", "assistant"}:
            raise DatasetError(f"line {line_number}: conversation_turns[{index}].role must be user or assistant")


def validate_router_record(record: dict[str, Any], line_number: int) -> None:
    if record.get("runtime_intent") != "context_routing_required":
        raise DatasetError(f"line {line_number}: router_context_resolution requires runtime_intent=context_routing_required")
    expected = record.get("expected_context_resolution")
    if not isinstance(expected, dict):
        raise DatasetError(f"line {line_number}: router_context_resolution requires expected_context_resolution")
    required = {"intent", "route", "kind"}
    missing = sorted(field for field in required if not isinstance(expected.get(field), str) or not expected[field].strip())
    if missing:
        raise DatasetError(f"line {line_number}: expected_context_resolution missing {', '.join(missing)}")
    if expected["route"] not in {"rag", "controlled_reply", "clarification"}:
        raise DatasetError(f"line {line_number}: expected_context_resolution.route is invalid")
    if expected["kind"] not in {"follow_up", "standalone", "topic_shift", "clarification_required"}:
        raise DatasetError(f"line {line_number}: expected_context_resolution.kind is invalid")
    for field_name in ("basis_message_ids", "resolved_query_contains"):
        if field_name in expected and (not isinstance(expected[field_name], list) or not all(isinstance(value, str) and value.strip() for value in expected[field_name])):
            raise DatasetError(f"line {line_number}: expected_context_resolution.{field_name} must be a non-empty string list")
    turns = record.get("conversation_turns", [])
    if not isinstance(turns, list):
        raise DatasetError(f"line {line_number}: conversation_turns must be a list")
    for index, turn in enumerate(turns, start=1):
        if not isinstance(turn, dict) or not all(isinstance(turn.get(field), str) and turn[field].strip() for field in ("message_id", "role", "content")):
            raise DatasetError(f"line {line_number}: conversation_turns[{index}] must include message_id, role and content")
        if turn["role"] not in {"user", "assistant"}:
            raise DatasetError(f"line {line_number}: conversation_turns[{index}].role must be user or assistant")
    expected_policy = record.get("expected_router_policy_version")
    if expected_policy is not None and expected_policy != CONVERSATION_ROUTER_POLICY_VERSION:
        raise DatasetError(f"line {line_number}: expected_router_policy_version must be {CONVERSATION_ROUTER_POLICY_VERSION}")


def run_mock_eval(records: list[dict[str, Any]]) -> tuple[dict[str, Any], list[dict[str, Any]], list[dict[str, Any]]]:
    samples: list[dict[str, Any]] = []
    failures: list[dict[str, Any]] = []

    for record in records:
        started = time.perf_counter()
        if record["task_type"] in A1_TASK_TYPES:
            samples.append(build_mock_a1_sample(record, round((time.perf_counter() - started) * 1000, 3)))
            continue
        if record["must_answer"]:
            answer_points = record.get("expected_answer_points") or [record["scoring_notes"]]
            observed_answer = " / ".join(str(point) for point in answer_points)
            observed_citations = [
                {
                    "file": source_file,
                    "locator": record["expected_source_locators"][min(index, len(record["expected_source_locators"]) - 1)],
                }
                for index, source_file in enumerate(record["expected_source_files"])
            ]
            refusal = False
            expected_file_hit = True
            citation_locator_hit = True
            answer_point_hit_rate = 1.0
        else:
            observed_answer = "I do not have enough evidence in the provided knowledge base to answer this question."
            observed_citations = []
            refusal = True
            expected_file_hit = None
            citation_locator_hit = None
            answer_point_hit_rate = None

        elapsed_ms = round((time.perf_counter() - started) * 1000, 3)
        samples.append(
            {
                "question_id": record["question_id"],
                "task_type": record["task_type"],
                "format_family": record.get("format_family", "unknown"),
                "must_answer": record["must_answer"],
                "observed_answer": observed_answer,
                "observed_citations": observed_citations,
                "top_score": None,
                "evidence_min_score": None,
                "threshold_decision": "refused" if refusal else "answered",
                "refusal": refusal,
                "latency_ms": elapsed_ms,
                "expected_file_hit": expected_file_hit,
                "citation_locator_hit": citation_locator_hit,
                "answer_point_hit_rate": answer_point_hit_rate,
                "passed": True,
            }
        )

    metrics = build_metrics(records, samples)
    return metrics, failures, samples


def build_mock_a1_sample(record: dict[str, Any], latency_ms: float) -> dict[str, Any]:
    """Produce an explicit synthetic A1 response without pretending it came from Runtime."""
    disposition = record["expected_disposition"]
    if disposition in {"grounded_answer", "partial_answer"}:
        answer = " / ".join(str(point) for point in record.get("expected_answer_points") or [record["scoring_notes"]])
        if disposition == "partial_answer":
            answer += " / " + record["expected_coverage"]["uncovered_description"]
        citations = [
            {
                "filename": source_file,
                "rank": rank,
                "source_locator": {"heading_path": [record["expected_source_locators"][0]]},
            }
            for source_file, rank in zip(record["expected_source_files"], record["expected_used_citation_ranks"], strict=False)
        ]
        payload: dict[str, Any] = {
            "assistant_message": {"role": "assistant", "content": answer},
            "citations": citations,
            "citation_anchors": [{"start_rune": 0, "end_rune": len(answer), "citation_ranks": record["expected_used_citation_ranks"]}],
            "refusal": False,
            "finish_reason": "stop",
            "answer_disposition": disposition,
            "used_citation_ranks": record["expected_used_citation_ranks"],
        }
        if disposition == "partial_answer":
            payload["coverage"] = record["expected_coverage"]
    elif disposition == "clarification_required":
        payload = {
            "assistant_message": {"role": "assistant", "content": "请补充必要范围后，我才能依据示例制度回答。"},
            "citations": [],
            "refusal": False,
            "finish_reason": "stop",
            "answer_disposition": disposition,
            "clarification": {"missing_dimensions": record["expected_missing_dimensions"]},
            "used_citation_ranks": [],
        }
    else:
        payload = {
            "assistant_message": {"role": "assistant", "content": "当前示例制度没有足够证据回答这个问题。"},
            "citations": [],
            "refusal": True,
            "finish_reason": "stop",
            "answer_disposition": disposition,
            "used_citation_ranks": [],
        }

    clarification_resolution = record.get("expected_clarification_resolution")
    if clarification_resolution is not None:
        payload["clarification_resolution"] = clarification_resolution
    if record.get("expected_resolved_query"):
        payload["resolved_query"] = record["expected_resolved_query"]
    sample = evaluate_runtime_response(record, payload, latency_ms)
    sample["provider_evidence"] = {
        "observation_kind": "mock_fixture",
        "fallback_used": None,
        "generation_status": "synthetic",
        "generation_provider": None,
        "generation_model_id": None,
        "retrieval_status": "synthetic",
        "retrieval_provider": None,
        "retrieval_model_id": None,
        "observation_complete": False,
        "quality_eligible": False,
    }
    return sample


def run_runtime_eval(
    records: list[dict[str, Any]],
    *,
    model_id: str,
    config_version: str,
    retrieval_config_version: str,
    knowledge_snapshot_id: str,
    generation_config: dict[str, str | float | int],
    embedding_model: str | None = None,
    retrieval_strategy: str | None = None,
    knowledge_scope: dict[str, Any] | None = None,
    top_k: int | None = None,
    rerank_enabled: bool | None = None,
    evidence_min_score: float | None = None,
    answer_policy_version: str | None = None,
) -> tuple[dict[str, Any], list[dict[str, Any]], list[dict[str, Any]]]:
    client = build_runtime_test_client()
    samples: list[dict[str, Any]] = []
    failures: list[dict[str, Any]] = []

    for record in records:
        started = time.perf_counter()
        sample: dict[str, Any]
        payload = build_runtime_chat_payload(
            record,
            model_id=model_id,
            config_version=config_version,
            retrieval_config_version=retrieval_config_version,
            knowledge_snapshot_id=knowledge_snapshot_id,
            generation_config=generation_config,
            embedding_model=embedding_model,
            retrieval_strategy=retrieval_strategy,
            knowledge_scope=knowledge_scope,
            top_k=top_k,
            rerank_enabled=rerank_enabled,
            evidence_min_score=evidence_min_score,
            answer_policy_version=answer_policy_version,
        )

        try:
            response = client.post("/v1/chat/run", json=payload)
            elapsed_ms = round((time.perf_counter() - started) * 1000, 3)
            if response.status_code != 200:
                sample = build_error_sample(record, elapsed_ms, f"runtime status {response.status_code}: {response.text}")
                failures.append(build_failure(record, sample, "unexpected_runtime_error", "Runtime returned a non-200 response."))
            else:
                sample = evaluate_runtime_response(record, response.json(), elapsed_ms)
                sample["evidence_min_score"] = evidence_min_score
                sample["threshold_decision"] = "refused" if sample["refusal"] else "answered"
                failures.extend(build_sample_failures(record, sample))
        except Exception as exc:  # pragma: no cover - defensive runner guard
            elapsed_ms = round((time.perf_counter() - started) * 1000, 3)
            sample = build_error_sample(record, elapsed_ms, str(exc))
            failures.append(build_failure(record, sample, "unexpected_runtime_error", "Runtime eval raised an exception."))

        samples.append(sample)

    metrics = build_metrics(records, samples)
    return metrics, failures, samples


def run_remote_runtime_eval(
    records: list[dict[str, Any]],
    *,
    runtime_base_url: str,
    runtime_service_token: str,
    model_id: str,
    config_version: str,
    retrieval_config_version: str,
    knowledge_snapshot_id: str,
    generation_config: dict[str, str | float | int],
    embedding_model: str | None = None,
    retrieval_strategy: str | None = None,
    knowledge_scope: dict[str, Any],
    top_k: int | None = None,
    rerank_enabled: bool | None = None,
    evidence_min_score: float | None = None,
    answer_policy_version: str | None = None,
    evaluation_role: str | None = None,
    conditions: dict[str, Any] | None = None,
    evaluation_purpose: str | None = None,
) -> tuple[dict[str, Any], list[dict[str, Any]], list[dict[str, Any]]]:
    """Evaluate the already-running Runtime without creating fixtures or fallbacks."""
    samples: list[dict[str, Any]] = []
    failures: list[dict[str, Any]] = []
    endpoint = runtime_base_url.rstrip("/") + "/v1/chat/run"

    for record in records:
        started = time.perf_counter()
        response: dict[str, Any] | None = None
        payload = build_runtime_chat_payload(
            record,
            model_id=model_id,
            config_version=config_version,
            retrieval_config_version=retrieval_config_version,
            knowledge_snapshot_id=knowledge_snapshot_id,
            generation_config=generation_config,
            embedding_model=embedding_model,
            retrieval_strategy=retrieval_strategy,
            knowledge_scope=knowledge_scope,
            top_k=top_k,
            rerank_enabled=rerank_enabled,
            evidence_min_score=evidence_min_score,
            answer_policy_version=answer_policy_version,
        )
        try:
            response = post_runtime_json(endpoint, payload, runtime_service_token)
            elapsed_ms = round((time.perf_counter() - started) * 1000, 3)
            validate_remote_execution_observation(
                response,
                model_id=model_id,
                record=record,
                evaluation_role=evaluation_role,
                conditions=conditions,
            )
            sample = evaluate_runtime_response(record, response, elapsed_ms)
            sample["runtime_observation"] = response
            if evaluation_role == "candidate":
                # This is set only after the disposition-aware validator accepts
                # the observed Runtime facts; it is never inferred from text.
                sample["provider_evidence"]["candidate_observation_valid"] = True
            sample["evidence_min_score"] = evidence_min_score
            sample["threshold_decision"] = "refused" if sample["refusal"] else "answered"
            failures.extend(build_sample_failures(record, sample))
        except (DatasetError, ReportRegistrationError) as exc:
            elapsed_ms = round((time.perf_counter() - started) * 1000, 3)
            sample = build_error_sample(record, elapsed_ms, str(exc))
            if (
                evaluation_purpose == "pre_q1_probe"
                and isinstance(exc, RemoteObservationError)
                and response is not None
            ):
                sample["strict_observation_summary"] = build_safe_strict_observation_summary(response, str(exc))
            failure_type = exc.failure_type if isinstance(exc, RemoteObservationError) else "unexpected_runtime_error"
            failures.append(build_failure(record, sample, failure_type, "Remote Runtime response was not a real strict execution."))
        samples.append(sample)

    return build_metrics(records, samples), failures, samples


def build_safe_strict_observation_summary(payload: dict[str, Any], reason: str) -> dict[str, Any]:
    """Keep pre-Q1 strict failures reviewable without persisting response content or citations."""
    execution = payload.get("execution")
    retrieval = execution.get("retrieval") if isinstance(execution, dict) else None
    generation = execution.get("generation") if isinstance(execution, dict) else None
    disposition = payload.get("answer_disposition")
    refusal = payload.get("refusal")
    return {
        "disposition": disposition if isinstance(disposition, str) else None,
        "refusal": refusal if isinstance(refusal, bool) else None,
        "execution": {
            "policy": execution.get("policy") if isinstance(execution, dict) and isinstance(execution.get("policy"), str) else None,
            "retrieval": retrieval.get("status") if isinstance(retrieval, dict) and isinstance(retrieval.get("status"), str) else None,
            "generation": generation.get("status") if isinstance(generation, dict) and isinstance(generation.get("status"), str) else None,
        },
        "reason": reason,
    }


def post_runtime_json(endpoint: str, payload: dict[str, Any], service_token: str) -> dict[str, Any]:
    request = Request(
        endpoint,
        data=json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8"),
        headers={"Content-Type": "application/json", "Accept": "application/json", "Authorization": f"Bearer {service_token}"},
        method="POST",
    )
    try:
        with open_http_request(request, timeout=120) as response:
            body = response.read().decode("utf-8")
    except HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise ReportRegistrationError(f"POST {endpoint} returned HTTP {exc.code}: {body}") from exc
    except (URLError, OSError) as exc:
        raise ReportRegistrationError(f"POST {endpoint} failed: {exc}") from exc
    try:
        decoded = json.loads(body)
    except json.JSONDecodeError as exc:
        raise DatasetError("Runtime returned invalid JSON") from exc
    if not isinstance(decoded, dict):
        raise DatasetError("Runtime response must be a JSON object")
    return decoded


def validate_remote_execution_observation(
    payload: dict[str, Any],
    *,
    model_id: str,
    require_generation: bool = True,
    record: dict[str, Any] | None = None,
    evaluation_role: str | None = None,
    conditions: dict[str, Any] | None = None,
) -> None:
    """Validate observed Runtime facts; candidate observations are disposition-aware."""
    execution = payload.get("execution")
    if not isinstance(execution, dict) or execution.get("fallback_used") is not False:
        raise RemoteObservationError("remote Runtime response is missing strict execution observation")
    generation = execution.get("generation")
    retrieval = execution.get("retrieval")
    if not isinstance(generation, dict) or not isinstance(retrieval, dict):
        raise RemoteObservationError("remote Runtime response has incomplete execution observation")

    if evaluation_role is None:
        if generation.get("status") == "performed":
            _validate_real_generation(generation, model_id)
        elif require_generation:
            raise RemoteObservationError("remote Runtime response did not perform generation")
        _validate_performed_retrieval(retrieval, conditions=None)
        return
    if evaluation_role not in QUALITY_EXPERIMENT_ROLES:
        raise RemoteObservationError("evaluation_role must be baseline or candidate")
    if conditions is None:
        raise RemoteObservationError("role-bound remote validation requires frozen conditions")
    _validate_remote_scope(payload, conditions["scope"])
    if evaluation_role == "baseline":
        _validate_real_generation(generation, model_id)
        _validate_performed_retrieval(retrieval, conditions["retrieval"])
        return

    if record is None or record.get("task_type") not in A1_TASK_TYPES:
        raise RemoteObservationError("candidate Q1 observation requires an answer_disposition record")
    _validate_candidate_policy(payload, execution, conditions["candidate"])
    disposition = record["expected_disposition"]
    if payload.get("answer_disposition") != disposition:
        raise RemoteObservationError("candidate Runtime answer_disposition did not match the frozen sample", "answer_policy_protocol_failure")
    if disposition in {"grounded_answer", "partial_answer"}:
        if payload.get("knowledge_used") is not True:
            raise RemoteObservationError("grounded and partial answers must record knowledge_used=true", "answer_policy_protocol_failure")
        _validate_real_generation(generation, model_id)
        _validate_performed_retrieval(retrieval, conditions["retrieval"])
        _validate_visible_candidate_evidence(payload)
    elif disposition == "clarification_required":
        if payload.get("knowledge_used") is not False:
            raise RemoteObservationError("clarification_required must record knowledge_used=false", "answer_policy_protocol_failure")
        if generation.get("status") != "not_run":
            raise RemoteObservationError("clarification_required must not execute answer generation", "answer_policy_protocol_failure")
        if retrieval.get("status") == "performed":
            _validate_performed_retrieval(retrieval, conditions["retrieval"])
        elif retrieval.get("status") != "not_run":
            raise RemoteObservationError("clarification_required has an invalid retrieval status", "answer_policy_protocol_failure")
        _validate_empty_user_evidence(payload)
    elif disposition == "insufficient_evidence":
        if payload.get("knowledge_used") is not False:
            raise RemoteObservationError("insufficient_evidence must record knowledge_used=false", "answer_policy_protocol_failure")
        if generation.get("status") != "not_run" or payload.get("refusal") is not True:
            raise RemoteObservationError("insufficient_evidence must refuse without answer generation", "answer_policy_protocol_failure")
        _validate_performed_retrieval(retrieval, conditions["retrieval"])
        _validate_empty_user_evidence(payload)


def _validate_real_generation(generation: dict[str, Any], model_id: str) -> None:
    if generation.get("status") != "performed":
        raise RemoteObservationError("remote Runtime response did not perform generation")
    provider = generation.get("provider")
    observed_model = generation.get("model_id")
    if not isinstance(provider, str) or not provider.strip() or provider.startswith("test_"):
        raise RemoteObservationError("remote Runtime response has no eligible generation provider")
    if observed_model != model_id:
        raise RemoteObservationError("remote Runtime response model identity does not match the frozen condition")


def _validate_performed_retrieval(retrieval: dict[str, Any], conditions: dict[str, Any] | None) -> None:
    if retrieval.get("status") != "performed":
        raise RemoteObservationError("remote Runtime response did not perform scoped retrieval")
    if not all(isinstance(retrieval.get(field), str) and retrieval[field].strip() for field in ("strategy", "embedding_model", "collection")):
        raise RemoteObservationError("remote Runtime response has incomplete retrieval observation")
    if not isinstance(retrieval.get("candidate_count"), int) or retrieval["candidate_count"] < 0:
        raise RemoteObservationError("remote Runtime response has invalid retrieval candidate count")
    if conditions is not None:
        for field_name in ("strategy", "embedding_model", "top_k", "rerank_enabled", "evidence_min_score"):
            if retrieval.get(field_name) != conditions[field_name]:
                raise RemoteObservationError(f"remote Runtime retrieval.{field_name} did not match frozen conditions")


def _validate_remote_scope(payload: dict[str, Any], scope: dict[str, Any]) -> None:
    observed_snapshot_id = payload.get("knowledge_snapshot_id")
    observed_manifest_hash = payload.get("knowledge_snapshot_manifest_hash")
    if observed_snapshot_id != scope["snapshot_id"] or observed_manifest_hash != scope["manifest_hash"]:
        raise RemoteObservationError("remote Runtime scope did not match frozen conditions")


def _validate_candidate_policy(payload: dict[str, Any], execution: dict[str, Any], candidate: dict[str, Any]) -> None:
    if payload.get("answer_policy_version") != candidate["answer_policy_version"] or execution.get("policy") != "performed":
        raise RemoteObservationError("candidate Runtime response is missing answer policy execution", "answer_policy_protocol_failure")
    for field_name in ("policy_provider", "policy_model_id"):
        value = execution.get(field_name)
        if not isinstance(value, str) or not value.strip() or value.startswith("test_"):
            raise RemoteObservationError(f"candidate Runtime response has no eligible {field_name}", "answer_policy_protocol_failure")
    if not isinstance(execution.get("policy_call_count"), int) or execution["policy_call_count"] < 1:
        raise RemoteObservationError("candidate Runtime response has no policy call count", "answer_policy_protocol_failure")
    if execution["policy_model_id"] != candidate["model_id"]:
        raise RemoteObservationError("candidate Runtime policy model identity does not match frozen conditions", "answer_policy_protocol_failure")


def _validate_empty_user_evidence(payload: dict[str, Any]) -> None:
    if payload.get("citations") or payload.get("citation_anchors") or payload.get("used_citation_ranks"):
        raise RemoteObservationError("non-evidence disposition returned user-visible evidence", "answer_policy_protocol_failure")


def _validate_visible_candidate_evidence(payload: dict[str, Any]) -> None:
    citations = payload.get("citations")
    anchors = payload.get("citation_anchors")
    ranks = parse_observed_citation_ranks(payload.get("used_citation_ranks"))
    if not isinstance(citations, list) or not citations or not isinstance(anchors, list) or not anchors or not ranks:
        raise RemoteObservationError("candidate evidence answer is missing citations, anchors or used ranks", "answer_policy_protocol_failure")
    if not citation_anchors_are_valid(((payload.get("assistant_message") or {}).get("content") or ""), anchors, ranks):
        raise RemoteObservationError("candidate citation anchors are invalid", "answer_policy_protocol_failure")


def build_runtime_test_client() -> Any:
    from fastapi.testclient import TestClient

    return TestClient(load_runtime_main().app)


def load_runtime_main() -> Any:
    runtime_root = str(RUNTIME_ROOT)
    if runtime_root not in sys.path:
        sys.path.insert(0, runtime_root)

    from app import main as runtime_main

    return runtime_main


def build_runtime_chat_payload(
    record: dict[str, Any],
    *,
    model_id: str,
    config_version: str,
    retrieval_config_version: str,
    knowledge_snapshot_id: str,
    generation_config: dict[str, str | float | int],
    embedding_model: str | None = None,
    retrieval_strategy: str | None = None,
    knowledge_scope: dict[str, Any] | None = None,
    top_k: int | None = None,
    rerank_enabled: bool | None = None,
    evidence_min_score: float | None = None,
    answer_policy_version: str | None = None,
) -> dict[str, Any]:
    actual_generation_config = normalize_generation_config(generation_config)
    response_overrides = dict(actual_generation_config)
    response_overrides["retrieval_strategy"] = resolve_retrieval_strategy(retrieval_strategy)
    if embedding_model:
        response_overrides["embedding_model"] = embedding_model
    if top_k is not None:
        response_overrides["top_k"] = top_k
    if rerank_enabled is not None:
        response_overrides["rerank_enabled"] = rerank_enabled
    if evidence_min_score is not None:
        response_overrides["evidence_min_score"] = evidence_min_score
    payload: dict[str, Any] = {
        "request_id": f"eval_{record['question_id']}",
        "conversation_id": f"eval_conv_{record['question_id']}",
        "message_id": f"eval_msg_{record['question_id']}",
        "user_query": record["question"],
        "model_id": model_id,
        "config_version": config_version,
        "retrieval_config_version": retrieval_config_version,
        "knowledge_snapshot_id": knowledge_snapshot_id,
        "stream": False,
        "messages": list(record.get("conversation_turns", [])),
        "response_overrides": response_overrides,
    }
    if record.get("runtime_intent"):
        payload["intent"] = record["runtime_intent"]
    if answer_policy_version is not None:
        payload["answer_policy_version"] = answer_policy_version
    if knowledge_scope is not None:
        if knowledge_scope.get("snapshot_id") != knowledge_snapshot_id:
            raise DatasetError("knowledge_scope.snapshot_id must match knowledge_snapshot_id")
        payload["knowledge_scope"] = knowledge_scope
    format_family = record.get("format_family")
    if record["must_answer"] and format_family in RUNTIME_SOURCE_FORMATS:
        payload["source_filters"] = {"source_formats": [format_family]}
    return payload


def evaluate_runtime_response(record: dict[str, Any], payload: dict[str, Any], latency_ms: float) -> dict[str, Any]:
    citations = payload.get("citations") or []
    answer = ((payload.get("assistant_message") or {}).get("content") or "").strip()
    refusal = bool(payload.get("refusal"))
    router_evaluation = evaluate_router_expectations(record, payload)
    a1_evaluation = evaluate_a1_expectations(record, payload, answer, citations, refusal)
    top_score = 0.0
    if citations and isinstance(citations[0], dict):
        try:
            top_score = float(citations[0].get("score", 0.0))
        except (TypeError, ValueError):
            top_score = 0.0

    if record["task_type"] in ROUTER_TASK_TYPES:
        expected_file_hit = has_expected_file_hit(record, citations) if record["expected_source_files"] else None
        citation_locator_hit = has_expected_locator_hit(record, citations) if record["expected_source_locators"] else None
        answer_point_hit_rate = calculate_answer_point_hit_rate(record, answer) if record.get("expected_answer_points") else None
        passed = router_evaluation["passed"]
    elif record["task_type"] in A1_TASK_TYPES:
        expected_file_hit = has_expected_file_hit(record, citations) if record["expected_source_files"] else None
        citation_locator_hit = has_expected_locator_hit(record, citations) if record["expected_source_locators"] else None
        answer_point_hit_rate = calculate_answer_point_hit_rate(record, answer) if record.get("expected_answer_points") else None
        passed = a1_evaluation["passed"]
    elif record["must_answer"]:
        expected_file_hit = has_expected_file_hit(record, citations)
        citation_locator_hit = has_expected_locator_hit(record, citations)
        answer_point_hit_rate = calculate_answer_point_hit_rate(record, answer)
        passed = (not refusal) and expected_file_hit and answer_point_hit_rate > 0
    else:
        expected_file_hit = None
        citation_locator_hit = None
        answer_point_hit_rate = None
        passed = refusal

    return {
        "question_id": record["question_id"],
        "task_type": record["task_type"],
        "format_family": record.get("format_family", "unknown"),
        "must_answer": record["must_answer"],
        "observed_answer": answer,
        "observed_citations": [format_observed_citation(item) for item in citations],
        "top_score": top_score,
        "refusal": refusal,
        "finish_reason": payload.get("finish_reason"),
        "latency_ms": latency_ms,
        "expected_file_hit": expected_file_hit,
        "citation_locator_hit": citation_locator_hit,
        "answer_point_hit_rate": answer_point_hit_rate,
        "answer_disposition": payload.get("answer_disposition"),
        "observed_context_resolution": format_observed_context_resolution(payload.get("context_resolution")),
        "router_evaluation": router_evaluation if record["task_type"] in ROUTER_TASK_TYPES else None,
        "a1_evaluation": a1_evaluation if record["task_type"] in A1_TASK_TYPES else None,
        "provider_evidence": extract_provider_evidence(payload),
        "passed": passed,
    }


def evaluate_a1_expectations(
    record: dict[str, Any],
    payload: dict[str, Any],
    answer: str,
    citations: list[dict[str, Any]],
    refusal: bool,
) -> dict[str, Any]:
    if record["task_type"] not in A1_TASK_TYPES:
        return {"passed": True, "mismatches": [], "primary_failure_type": None}

    expected_disposition = record["expected_disposition"]
    observed_disposition = payload.get("answer_disposition")
    observed_ranks = parse_observed_citation_ranks(payload.get("used_citation_ranks"))
    expected_ranks = record["expected_used_citation_ranks"]
    observed_anchors = payload.get("citation_anchors")
    clarification = payload.get("clarification")
    observed_missing_dimensions = clarification.get("missing_dimensions") if isinstance(clarification, dict) else None
    observed_coverage = payload.get("coverage")
    observed_resolution = payload.get("clarification_resolution")
    observed_resolved_query = payload.get("resolved_query")
    mismatches: list[dict[str, str]] = []

    def add_mismatch(failure_type: str, message: str) -> None:
        mismatches.append({"failure_type": failure_type, "message": message})

    if observed_disposition != expected_disposition:
        add_mismatch(
            "clarification_policy_failure",
            f"answer_disposition: expected {expected_disposition!r}, got {observed_disposition!r}",
        )

    expected_resolution = record.get("expected_clarification_resolution")
    if expected_disposition == "clarification_required":
        if observed_missing_dimensions != record["expected_missing_dimensions"]:
            add_mismatch("clarification_resolution_failure", "clarification missing_dimensions did not match the minimal required dimensions")
    if expected_resolution is not None:
        if observed_resolution != expected_resolution:
            add_mismatch(
                "clarification_resolution_failure",
                f"clarification_resolution: expected {expected_resolution!r}, got {observed_resolution!r}",
            )
        if expected_resolution == "resolved" and observed_resolved_query != record.get("expected_resolved_query"):
            add_mismatch("clarification_resolution_failure", "resolved_query did not match the expected resolved query")

    forbidden_hits = [point for point in record["forbidden_answer_points"] if answer_contains_forbidden_point(answer, point)]
    if forbidden_hits:
        add_mismatch("unsupported_business_claim", f"answer contained forbidden business facts: {forbidden_hits!r}")

    if expected_disposition == "partial_answer":
        expected_coverage = record["expected_coverage"]
        if observed_coverage != expected_coverage or expected_coverage["uncovered_description"] not in answer:
            add_mismatch("coverage_honesty_failure", "coverage did not state the supported and uncovered portions exactly")

    evidence_required = expected_disposition in {"grounded_answer", "partial_answer"}
    if evidence_required:
        if observed_ranks != expected_ranks:
            add_mismatch("citation_mismatch", "used_citation_ranks did not match the explicit runtime response field")
        if not has_expected_file_hit(record, citations) or not has_expected_locator_hit(record, citations):
            add_mismatch("citation_mismatch", "citations did not match the expected source file and locator")
        if observed_anchors is not None and not citation_anchors_are_valid(answer, observed_anchors, observed_ranks or []):
            add_mismatch("citation_mismatch", "citation_anchors did not faithfully reference the visible evidence")
    elif citations or observed_ranks:
        add_mismatch("citation_mismatch", "non-evidence disposition returned user-visible citations or used citation ranks")

    if expected_disposition == "insufficient_evidence":
        if not refusal:
            add_mismatch("refusal_policy_failure", "insufficient_evidence must refuse without making a business assertion")
    elif refusal:
        add_mismatch("refusal_policy_failure", "grounded, clarification, and partial dispositions must not be marked as refusal")

    if evidence_required and calculate_answer_point_hit_rate(record, answer) < 1.0:
        add_mismatch("answer_point_miss", "observed answer missed one or more expected answer points")

    primary_failure_type = mismatches[0]["failure_type"] if mismatches else None
    return {
        "passed": not mismatches,
        "expected_disposition": expected_disposition,
        "observed_disposition": observed_disposition,
        "expected_missing_dimensions": record["expected_missing_dimensions"],
        "observed_missing_dimensions": observed_missing_dimensions,
        "expected_used_citation_ranks": expected_ranks,
        "observed_used_citation_ranks": observed_ranks,
        "citation_anchor_fidelity": citation_anchors_are_valid(answer, observed_anchors, observed_ranks or []) if evidence_required else observed_anchors == [],
        "expected_coverage": record.get("expected_coverage"),
        "observed_coverage": observed_coverage,
        "expected_clarification_resolution": expected_resolution,
        "observed_clarification_resolution": observed_resolution,
        "expected_resolved_query": record.get("expected_resolved_query"),
        "observed_resolved_query": observed_resolved_query,
        "forbidden_answer_point_hits": forbidden_hits,
        "mismatches": mismatches,
        "primary_failure_type": primary_failure_type,
    }


def citation_anchors_are_valid(answer: str, anchors: object, used_ranks: list[int]) -> bool:
    if not isinstance(anchors, list) or not anchors or not used_ranks:
        return False
    used_rank_set = set(used_ranks)
    for anchor in anchors:
        if not isinstance(anchor, dict):
            return False
        start_rune = anchor.get("start_rune")
        end_rune = anchor.get("end_rune")
        ranks = anchor.get("citation_ranks")
        if (
            isinstance(start_rune, bool)
            or not isinstance(start_rune, int)
            or isinstance(end_rune, bool)
            or not isinstance(end_rune, int)
            or start_rune < 0
            or end_rune <= start_rune
            or end_rune > len(answer)
            or not isinstance(ranks, list)
            or not ranks
            or any(isinstance(rank, bool) or not isinstance(rank, int) or rank not in used_rank_set for rank in ranks)
        ):
            return False
    return True


def parse_observed_citation_ranks(value: object) -> list[int] | None:
    if not isinstance(value, list) or any(isinstance(rank, bool) or not isinstance(rank, int) or rank < 1 for rank in value):
        return None
    if len(set(value)) != len(value):
        return None
    return value


def extract_provider_evidence(payload: dict[str, Any]) -> dict[str, Any]:
    execution = payload.get("execution")
    if not isinstance(execution, dict):
        return {
            "observation_kind": "runtime_response",
            "fallback_used": None,
            "generation_status": None,
            "generation_provider": None,
            "generation_model_id": None,
            "policy_status": None,
            "policy_provider": None,
            "policy_model_id": None,
            "policy_call_count": None,
            "retrieval_status": None,
            "retrieval_provider": None,
            "retrieval_model_id": None,
            "observation_complete": False,
            "quality_eligible": False,
        }
    generation = execution.get("generation")
    retrieval = execution.get("retrieval")
    policy_provider = execution.get("policy_provider")
    policy_model_id = execution.get("policy_model_id")
    generation_provider = generation.get("provider") if isinstance(generation, dict) else None
    generation_model_id = generation.get("model_id") if isinstance(generation, dict) else None
    retrieval_provider = retrieval.get("provider") if isinstance(retrieval, dict) else None
    retrieval_model_id = retrieval.get("embedding_model") if isinstance(retrieval, dict) else None
    fallback_used = execution.get("fallback_used")
    complete = (
        fallback_used is False
        and isinstance(generation, dict)
        and isinstance(retrieval, dict)
        and generation.get("status") == "performed"
        and retrieval.get("status") == "performed"
        and isinstance(generation_provider, str)
        and bool(generation_provider.strip())
        and isinstance(generation_model_id, str)
        and bool(generation_model_id.strip())
        and isinstance(retrieval_model_id, str)
        and bool(retrieval_model_id.strip())
    )
    return {
        "observation_kind": "runtime_execution",
        "fallback_used": fallback_used if isinstance(fallback_used, bool) else None,
        "generation_status": generation.get("status") if isinstance(generation, dict) else None,
        "generation_provider": generation_provider if isinstance(generation_provider, str) else None,
        "generation_model_id": generation_model_id if isinstance(generation_model_id, str) else None,
        "policy_status": execution.get("policy"),
        "policy_provider": policy_provider if isinstance(policy_provider, str) else None,
        "policy_model_id": policy_model_id if isinstance(policy_model_id, str) else None,
        "policy_call_count": execution.get("policy_call_count"),
        "retrieval_status": retrieval.get("status") if isinstance(retrieval, dict) else None,
        "retrieval_provider": retrieval_provider if isinstance(retrieval_provider, str) else None,
        "retrieval_model_id": retrieval_model_id if isinstance(retrieval_model_id, str) else None,
        "observation_complete": complete,
        "quality_eligible": complete,
    }


def evaluate_router_expectations(record: dict[str, Any], payload: dict[str, Any]) -> dict[str, Any]:
    if record["task_type"] not in ROUTER_TASK_TYPES:
        return {"passed": True, "mismatches": []}
    expected = record["expected_context_resolution"]
    observed = payload.get("context_resolution")
    mismatches: list[str] = []
    if not isinstance(observed, dict):
        return {"passed": False, "mismatches": ["missing context_resolution"]}
    for field_name in ("intent", "route", "kind"):
        if observed.get(field_name) != expected.get(field_name):
            mismatches.append(f"{field_name}: expected {expected.get(field_name)!r}, got {observed.get(field_name)!r}")
    if "basis_message_ids" in expected and observed.get("basis_message_ids") != expected["basis_message_ids"]:
        mismatches.append(f"basis_message_ids: expected {expected['basis_message_ids']!r}, got {observed.get('basis_message_ids')!r}")
    resolved_query = str(observed.get("resolved_query") or "")
    for needle in expected.get("resolved_query_contains", []):
        if needle not in resolved_query:
            mismatches.append(f"resolved_query missing {needle!r}")
    expected_disposition = record.get("expected_disposition")
    if expected_disposition is not None and payload.get("answer_disposition") != expected_disposition:
        mismatches.append(f"answer_disposition: expected {expected_disposition!r}, got {payload.get('answer_disposition')!r}")
    return {"passed": not mismatches, "mismatches": mismatches}


def format_observed_context_resolution(value: object) -> dict[str, Any] | None:
    if not isinstance(value, dict):
        return None
    return {
        "intent": value.get("intent"),
        "route": value.get("route"),
        "kind": value.get("kind"),
        "resolved_query": value.get("resolved_query"),
        "basis_message_ids": value.get("basis_message_ids") or [],
        "resolver_provider": value.get("resolver_provider"),
        "resolver_model_id": value.get("resolver_model_id"),
        "confidence": value.get("confidence"),
    }


def build_error_sample(record: dict[str, Any], latency_ms: float, message: str) -> dict[str, Any]:
    return {
        "question_id": record["question_id"],
        "task_type": record["task_type"],
        "format_family": record.get("format_family", "unknown"),
        "must_answer": record["must_answer"],
        "observed_answer": "",
        "observed_citations": [],
        "top_score": None,
        "evidence_min_score": None,
        "threshold_decision": "error",
        "refusal": False,
        "finish_reason": "error",
        "latency_ms": latency_ms,
        "expected_file_hit": False if record["must_answer"] else None,
        "citation_locator_hit": False if record["must_answer"] else None,
        "answer_point_hit_rate": 0.0 if record["must_answer"] else None,
        "passed": False,
        "error": message,
    }


def build_sample_failures(record: dict[str, Any], sample: dict[str, Any]) -> list[dict[str, Any]]:
    failures: list[dict[str, Any]] = []

    if record["task_type"] in ROUTER_TASK_TYPES:
        router_evaluation = sample.get("router_evaluation") or {"mismatches": ["router evaluation did not run"]}
        if not sample["passed"]:
            failures.append(build_failure(record, sample, "router_quality_mismatch", "; ".join(router_evaluation.get("mismatches", []))))
    elif record["task_type"] in A1_TASK_TYPES:
        a1_evaluation = sample.get("a1_evaluation") or {}
        failure_type = a1_evaluation.get("primary_failure_type") or "answer_policy_protocol_failure"
        mismatches = a1_evaluation.get("mismatches") or [{"message": sample.get("error", "A1 evaluation did not run")}]
        if not sample["passed"]:
            failures.append(build_failure(record, sample, failure_type, str(mismatches[0].get("message", "A1 evaluation failed"))))
    elif record["must_answer"]:
        if sample["refusal"]:
            failures.append(build_failure(record, sample, "retrieval_miss", "Runtime refused a must-answer sample."))
        elif not sample["observed_citations"]:
            failures.append(build_failure(record, sample, "retrieval_miss", "Runtime returned no citations for a must-answer sample."))
        if sample["expected_file_hit"] is False:
            failures.append(build_failure(record, sample, "citation_mismatch", "No citation matched expected source files."))
        if sample["citation_locator_hit"] is False:
            failures.append(build_failure(record, sample, "citation_mismatch", "No citation matched expected locator hints."))
        if sample["answer_point_hit_rate"] == 0:
            failures.append(build_failure(record, sample, "answer_point_miss", "Observed answer missed all expected answer points."))
    elif not sample["refusal"]:
        failures.append(build_failure(record, sample, "refusal_policy_failure", "Runtime answered a must-refuse sample."))

    return failures


def build_failure(record: dict[str, Any], sample: dict[str, Any], failure_type: str, notes: str) -> dict[str, Any]:
    return {
        "question_id": record["question_id"],
        "failure_type": failure_type,
        "observed_answer": sample.get("observed_answer", ""),
        "observed_citations": sample.get("observed_citations", []),
        "notes": notes,
    }


def has_expected_file_hit(record: dict[str, Any], citations: list[dict[str, Any]]) -> bool:
    expected = {normalize_text(value) for value in record["expected_source_files"]}
    observed = {normalize_text(str(item.get("filename", ""))) for item in citations}
    return bool(expected & observed)


def has_expected_locator_hit(record: dict[str, Any], citations: list[dict[str, Any]]) -> bool:
    expected_locators = record["expected_source_locators"]
    if not expected_locators:
        return True

    expected_filenames = {filename for filename in record["expected_source_files"] if isinstance(filename, str)}
    expected_file_citations = [
        citation for citation in citations if citation.get("filename") in expected_filenames
    ]
    if not expected_file_citations:
        return False

    if record.get("format_family") == "xlsx":
        return any(
            xlsx_locator_matches(expected_locator, citation.get("source_locator"))
            for expected_locator in expected_locators
            for citation in expected_file_citations
        )

    if record.get("format_family") == "json":
        return any(
            json_selector_locator_matches(
                expected_locator,
                citation.get("filename"),
                citation,
                load_json_sample_document(citation.get("filename")),
            )
            for expected_locator in expected_locators
            for citation in expected_file_citations
        )

    citation_text = normalize_text(
        " ".join(
            json.dumps(
                {
                    "citation_label": item.get("citation_label"),
                    "source_locator": item.get("source_locator"),
                    "snippet": item.get("snippet"),
                },
                ensure_ascii=False,
            )
            for item in expected_file_citations
        )
    )
    return any(normalize_text(str(locator)) in citation_text for locator in expected_locators)


def xlsx_locator_matches(expected_locator: object, source_locator: object) -> bool:
    if not isinstance(expected_locator, str) or not isinstance(source_locator, dict):
        return False

    sheet_name, separator, expected_range_value = expected_locator.strip().partition("!")
    expected_range = parse_xlsx_cell_range(expected_range_value) if separator and sheet_name else None
    citation_range = parse_xlsx_cell_range(source_locator.get("cell_range"))
    if expected_range is None or citation_range is None:
        return False
    if sheet_name != source_locator.get("sheet_name"):
        return False

    expected_first_column, expected_first_row, expected_last_column, expected_last_row = expected_range
    citation_first_column, citation_first_row, citation_last_column, citation_last_row = citation_range
    return (
        citation_first_column <= expected_first_column
        and citation_first_row <= expected_first_row
        and expected_last_column <= citation_last_column
        and expected_last_row <= citation_last_row
    )


def parse_xlsx_cell_range(value: object) -> tuple[int, int, int, int] | None:
    if not isinstance(value, str):
        return None

    first_cell, separator, last_cell = value.strip().partition(":")
    if not separator:
        last_cell = first_cell
    elif ":" in last_cell:
        return None

    first = parse_xlsx_cell_reference(first_cell)
    last = parse_xlsx_cell_reference(last_cell)
    if first is None or last is None:
        return None

    first_column, first_row = first
    last_column, last_row = last
    if first_column > last_column or first_row > last_row:
        return None
    return first_column, first_row, last_column, last_row


def parse_xlsx_cell_reference(value: str) -> tuple[int, int] | None:
    match = re.fullmatch(r"\$?([A-Za-z]+)\$?([1-9]\d*)", value.strip())
    if match is None:
        return None

    column = 0
    for character in match.group(1).upper():
        column = column * 26 + ord(character) - ord("A") + 1
    return column, int(match.group(2))


def json_selector_locator_matches(
    expected_locator: object,
    expected_filename: object,
    citation: object,
    document: object,
) -> bool:
    if not isinstance(expected_locator, str) or not isinstance(expected_filename, str) or not isinstance(citation, dict):
        return False
    if citation.get("filename") != expected_filename:
        return False

    source_locator = citation.get("source_locator")
    citation_path = source_locator.get("json_path") if isinstance(source_locator, dict) else None
    resolved_path = resolve_json_selector_path(expected_locator, document)
    if not isinstance(citation_path, str) or resolved_path is None:
        return False
    return citation_path == resolved_path or citation_path.startswith(f"{resolved_path}.") or citation_path.startswith(
        f"{resolved_path}["
    )


def load_json_sample_document(filename: object) -> object | None:
    if not isinstance(filename, str) or Path(filename).name != filename:
        return None

    path = REPO_ROOT / "data" / "sample-docs" / filename
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None


def resolve_json_selector_path(expected_locator: str, document: object) -> str | None:
    match = re.fullmatch(
        r"(?P<array_path>\$(?:\.[A-Za-z_][A-Za-z0-9_]*|\[\d+\])*)\[\?id=(?P<identifier>[A-Za-z0-9_.-]+)\](?:\.[A-Za-z_][A-Za-z0-9_]*|\[\d+\])*",
        expected_locator.strip(),
    )
    if match is None:
        return None

    array_path = match.group("array_path")
    array_value = resolve_json_path(document, array_path)
    if not isinstance(array_value, list):
        return None

    matching_indices = [
        index
        for index, item in enumerate(array_value)
        if isinstance(item, dict) and item.get("id") == match.group("identifier")
    ]
    if len(matching_indices) != 1:
        return None
    return f"{array_path}[{matching_indices[0]}]"


def resolve_json_path(document: object, path: str) -> object | None:
    value = document
    for match in re.finditer(r"\.([A-Za-z_][A-Za-z0-9_]*)|\[(\d+)\]", path[1:]):
        key, index = match.groups()
        if key is not None:
            if not isinstance(value, dict) or key not in value:
                return None
            value = value[key]
        else:
            if not isinstance(value, list):
                return None
            item_index = int(index)
            if item_index >= len(value):
                return None
            value = value[item_index]
    return value


def calculate_answer_point_hit_rate(record: dict[str, Any], answer: str) -> float:
    expected_points = record.get("expected_answer_points") or []
    if not expected_points:
        return 1.0
    hits = sum(1 for point in expected_points if answer_contains_point(answer, str(point)))
    return hits / len(expected_points)


def answer_contains_point(answer: str, point: str) -> bool:
    normalized_answer = normalize_compact(answer)
    normalized_point = normalize_compact(point)
    if normalized_point and normalized_point in normalized_answer:
        return True

    point_numbers = re.findall(r"\d+(?:\.\d+)?", point)
    if point_numbers and not all(number in answer for number in point_numbers):
        return False

    point_tokens = meaningful_tokens(point)
    if not point_tokens:
        return False
    answer_tokens = set(meaningful_tokens(answer))
    required_hits = max(1, min(len(point_tokens), round(len(point_tokens) * 0.6)))
    return sum(1 for token in point_tokens if token in answer_tokens) >= required_hits


def answer_contains_forbidden_point(answer: str, point: str) -> bool:
    normalized_answer = normalize_compact(answer)
    normalized_point = normalize_compact(point)
    return bool(normalized_point and normalized_point in normalized_answer)


def meaningful_tokens(text: str) -> list[str]:
    ascii_tokens = re.findall(r"[a-z0-9]+", text.lower())
    cjk_sequences = re.findall(r"[\u4e00-\u9fff]{2,}", text)
    cjk_bigrams: list[str] = []
    for sequence in cjk_sequences:
        cjk_bigrams.extend(sequence[index : index + 2] for index in range(len(sequence) - 1))
    return [token for token in ascii_tokens + cjk_sequences + cjk_bigrams if len(token) >= 2]


def normalize_text(value: str) -> str:
    return " ".join(value.casefold().split())


def normalize_compact(value: str) -> str:
    return re.sub(r"[\s，。,.!?:;；：、`'\"（）()\[\]\-_/]+", "", value.casefold())


def format_observed_citation(item: dict[str, Any]) -> dict[str, Any]:
    return {
        "chunk_id": item.get("chunk_id"),
        "file_id": item.get("file_id"),
        "filename": item.get("filename"),
        "rank": item.get("rank"),
        "score": item.get("score"),
        "citation_label": item.get("citation_label"),
        "source_locator": item.get("source_locator"),
        "snippet": item.get("snippet"),
    }


def build_metrics(records: list[dict[str, Any]], samples: list[dict[str, Any]]) -> dict[str, Any]:
    total = len(records)
    router_count = sum(1 for record in records if record["task_type"] in ROUTER_TASK_TYPES)
    must_answer = sum(1 for record in records if record["must_answer"] and record["task_type"] not in ROUTER_TASK_TYPES)
    must_refuse = sum(1 for record in records if not record["must_answer"] and record["task_type"] not in ROUTER_TASK_TYPES)
    format_counts = Counter(record.get("format_family", "unknown") for record in records)
    task_type_counts = Counter(record["task_type"] for record in records)
    answer_samples = [sample for sample in samples if sample["must_answer"] and sample["task_type"] not in ROUTER_TASK_TYPES]
    refusal_samples = [sample for sample in samples if not sample["must_answer"] and sample["task_type"] not in ROUTER_TASK_TYPES]
    router_samples = [sample for sample in samples if sample["task_type"] in ROUTER_TASK_TYPES]
    average_latency = sum(sample["latency_ms"] for sample in samples) / len(samples)
    expected_file_hits = sum(1 for sample in answer_samples if sample.get("expected_file_hit") is True)
    citation_hits = sum(1 for sample in answer_samples if sample.get("citation_locator_hit") is True)
    answer_point_rate = (
        sum(float(sample.get("answer_point_hit_rate") or 0.0) for sample in answer_samples) / len(answer_samples)
        if answer_samples
        else None
    )
    refusal_hits = sum(1 for sample in refusal_samples if sample.get("refusal") is True)
    a1_records = [record for record in records if record["task_type"] in A1_TASK_TYPES]
    a1_samples = [sample for sample in samples if sample["task_type"] in A1_TASK_TYPES]
    a1_evaluations = [sample.get("a1_evaluation") or {} for sample in a1_samples]
    a1_disposition_counts = Counter(record["expected_disposition"] for record in a1_records)
    a1_boundary_tag_counts = Counter(
        tag for record in a1_records for tag in record.get("boundary_tags", [])
    )
    a1_disposition_hits = sum(
        1
        for evaluation in a1_evaluations
        if evaluation.get("expected_disposition") == evaluation.get("observed_disposition")
    )
    clarification_evaluations = [
        evaluation for evaluation in a1_evaluations if evaluation.get("expected_disposition") == "clarification_required"
    ]
    partial_evaluations = [
        evaluation for evaluation in a1_evaluations if evaluation.get("expected_disposition") == "partial_answer"
    ]
    resolution_evaluations = [
        evaluation for evaluation in a1_evaluations if evaluation.get("expected_clarification_resolution") is not None
    ]
    resolved_retrieval_samples = [
        sample
        for sample in a1_samples
        if (sample.get("a1_evaluation") or {}).get("expected_clarification_resolution") == "resolved"
    ]
    evidence_evaluations = [
        evaluation
        for evaluation in a1_evaluations
        if evaluation.get("expected_disposition") in {"grounded_answer", "partial_answer"}
    ]

    return {
        "sample_count": total,
        "must_answer_count": must_answer,
        "must_refuse_count": must_refuse,
        "router_sample_count": router_count,
        "format_family_counts": dict(sorted(format_counts.items())),
        "task_type_counts": dict(sorted(task_type_counts.items())),
        "passed_count": sum(1 for sample in samples if sample["passed"]),
        "failed_count": sum(1 for sample in samples if not sample["passed"]),
        "retrieval_recall_at_k": round(expected_file_hits / must_answer, 4) if must_answer else None,
        "expected_file_hit_rate": round(expected_file_hits / must_answer, 4) if must_answer else None,
        "grounded_correctness": round(answer_point_rate, 4) if answer_point_rate is not None else None,
        "citation_accuracy": round(citation_hits / must_answer, 4) if must_answer else None,
        "refusal_correctness": round(refusal_hits / must_refuse, 4) if must_refuse else None,
        "router_resolution_accuracy": round(sum(1 for sample in router_samples if sample["passed"]) / router_count, 4) if router_count else None,
        "answer_disposition_sample_count": len(a1_samples),
        "answer_disposition_counts": dict(sorted(a1_disposition_counts.items())),
        "boundary_tag_counts": dict(sorted(a1_boundary_tag_counts.items())),
        "evaluation_pair_sample_count": sum(1 for record in a1_records if record.get("evaluation_pair_id")),
        "answer_disposition_accuracy": round(a1_disposition_hits / len(a1_samples), 4) if a1_samples else None,
        "clarification_dimension_accuracy": round(
            sum(1 for evaluation in clarification_evaluations if evaluation.get("expected_missing_dimensions") == evaluation.get("observed_missing_dimensions"))
            / len(clarification_evaluations),
            4,
        )
        if clarification_evaluations
        else None,
        "unnecessary_clarification_rate": round(
            sum(
                1
                for evaluation in a1_evaluations
                if evaluation.get("expected_disposition") in {"grounded_answer", "partial_answer"}
                and evaluation.get("observed_disposition") == "clarification_required"
            )
            / len(evidence_evaluations),
            4,
        )
        if evidence_evaluations
        else None,
        "coverage_honesty": round(
            sum(1 for evaluation in partial_evaluations if evaluation.get("expected_coverage") == evaluation.get("observed_coverage"))
            / len(partial_evaluations),
            4,
        )
        if partial_evaluations
        else None,
        "clarification_resolution_accuracy": round(
            sum(
                1
                for evaluation in resolution_evaluations
                if evaluation.get("expected_clarification_resolution") == evaluation.get("observed_clarification_resolution")
                and (
                    evaluation.get("expected_clarification_resolution") != "resolved"
                    or evaluation.get("expected_resolved_query") == evaluation.get("observed_resolved_query")
                )
            )
            / len(resolution_evaluations),
            4,
        )
        if resolution_evaluations
        else None,
        "used_citation_rank_accuracy": round(
            sum(1 for evaluation in evidence_evaluations if evaluation.get("expected_used_citation_ranks") == evaluation.get("observed_used_citation_ranks"))
            / len(evidence_evaluations),
            4,
        )
        if evidence_evaluations
        else None,
        "citation_anchor_fidelity": round(
            sum(1 for evaluation in evidence_evaluations if evaluation.get("citation_anchor_fidelity") is True) / len(evidence_evaluations),
            4,
        )
        if evidence_evaluations
        else None,
        "resolved_query_retrieval_hit_rate": round(
            sum(1 for sample in resolved_retrieval_samples if sample.get("expected_file_hit") is True) / len(resolved_retrieval_samples),
            4,
        )
        if resolved_retrieval_samples
        else None,
        "answer_point_recall": round(answer_point_rate, 4) if answer_point_rate is not None else None,
        "forbidden_answer_point_violation_count": sum(
            len(evaluation.get("forbidden_answer_point_hits") or []) for evaluation in a1_evaluations
        ),
        "average_latency_ms": round(average_latency, 3),
    }


def summarize_provider_evidence(
    samples: list[dict[str, Any]],
    mode: str,
    evaluation_role: str | None = None,
) -> dict[str, Any]:
    observations = [sample.get("provider_evidence") for sample in samples if isinstance(sample.get("provider_evidence"), dict)]
    generation_providers = sorted(
        {
            provider
            for observation in observations
            if isinstance((provider := observation.get("generation_provider")), str) and provider.strip()
        }
    )
    generation_models = sorted(
        {
            model_id
            for observation in observations
            if isinstance((model_id := observation.get("generation_model_id")), str) and model_id.strip()
        }
    )
    policy_providers = sorted(
        {
            provider
            for observation in observations
            if isinstance((provider := observation.get("policy_provider")), str) and provider.strip()
        }
    )
    policy_models = sorted(
        {
            model_id
            for observation in observations
            if isinstance((model_id := observation.get("policy_model_id")), str) and model_id.strip()
        }
    )
    complete_count = sum(1 for observation in observations if observation.get("observation_complete") is True)
    fallback_count = sum(1 for observation in observations if observation.get("fallback_used") is True)
    remote_complete = (
        mode == "runtime-remote"
        and len(observations) == len(samples)
        and bool(observations)
        and all(observation.get("quality_eligible") is True for observation in observations)
    )
    comparison_eligible = remote_complete and evaluation_role == "baseline"
    quality_eligible = (
        mode == "runtime-remote"
        and evaluation_role == "candidate"
        and len(observations) == len(samples)
        and bool(observations)
        and all(observation.get("candidate_observation_valid") is True for observation in observations)
    )
    return {
        "observation_count": len(observations),
        "complete_observation_count": complete_count,
        "fallback_count": fallback_count,
        "generation_providers": generation_providers,
        "generation_model_ids": generation_models,
        "policy_providers": policy_providers,
        "policy_model_ids": policy_models,
        "comparison_eligible": comparison_eligible,
        "quality_eligible": quality_eligible,
    }


def write_reports(report: dict[str, Any], report_dir: Path) -> None:
    report_dir.mkdir(parents=True, exist_ok=True)
    run_id = str(report.get("run_id", "")).strip()
    if not re.fullmatch(r"[A-Za-z0-9_-]+", run_id):
        raise DatasetError("report run_id must contain only letters, numbers, underscores, or hyphens")

    content_json = json.dumps(report, ensure_ascii=False, indent=2) + "\n"
    content_markdown = render_markdown(report)
    # `latest` is a convenience pointer. Calibration and release evidence must
    # retain every evaluated condition, so each immutable run is stored beside it.
    for path, content in (
        (report_dir / f"{run_id}.json", content_json),
        (report_dir / f"{run_id}.md", content_markdown),
        (report_dir / "latest.json", content_json),
        (report_dir / "latest.md", content_markdown),
    ):
        path.write_text(content, encoding="utf-8")


def render_markdown(report: dict[str, Any]) -> str:
    metrics = report["metrics"]
    generation_config = report["generation_config"]
    lines = [
        "# Eval Baseline Report",
        "",
        f"- Run ID: `{report['run_id']}`",
        f"- Dataset: `{report['dataset_id']}`",
        f"- Model: `{report['model_id']}`",
        f"- Router policy: `{report.get('router_policy_version') or 'not set'}`",
        f"- Evaluation role: `{report.get('evaluation_role') or 'not set'}`",
        f"- Samples: `{metrics['sample_count']}`",
        f"- Must answer: `{metrics['must_answer_count']}`",
        f"- Must refuse: `{metrics['must_refuse_count']}`",
        f"- Router samples: `{metrics.get('router_sample_count', 0)}`",
        f"- Passed: `{metrics['passed_count']}`",
        f"- Failed: `{metrics['failed_count']}`",
        "",
        "## Generation Config",
        "",
        f"- System prompt version: `{generation_config['system_prompt_version']}`",
        f"- Temperature: `{generation_config['temperature']}`",
        f"- Max output tokens: `{generation_config['max_output_tokens']}`",
        f"- Response style: `{generation_config.get('response_style', 'not set')}`",
        f"- Top p: `{generation_config.get('top_p', 'not set')}`",
        "",
    ]
    if report.get("evaluation_purpose"):
        lines.insert(7, f"- Evaluation purpose: `{report['evaluation_purpose']}`")

    retrieval = report.get("retrieval")
    if isinstance(retrieval, dict):
        lines.extend(
            [
                "## Retrieval Config",
                "",
                f"- Mode: `{retrieval['mode']}`",
                f"- Strategy: `{retrieval['strategy']}`",
                f"- Embedding model: `{retrieval['embedding_model'] or 'not applicable'}`",
                f"- Embedding dimensions: `{retrieval['embedding_dimensions'] or 'not applicable'}`",
                f"- Qdrant collection: `{retrieval['qdrant_collection'] or 'not applicable'}`",
                f"- Top k: `{retrieval['top_k'] if retrieval['top_k'] is not None else 'not set'}`",
                f"- Rerank enabled: `{retrieval['rerank_enabled'] if retrieval['rerank_enabled'] is not None else 'not set'}`",
                f"- Evidence minimum score: `{retrieval['evidence_min_score'] if retrieval['evidence_min_score'] is not None else 'not set'}`",
                "",
            ]
        )

    knowledge_scope = report.get("knowledge_scope")
    if isinstance(knowledge_scope, dict):
        lines.extend(
            [
                "## Knowledge Scope",
                "",
                f"- Snapshot: `{knowledge_scope['snapshot_id']}`",
                f"- Manifest hash: `{knowledge_scope['manifest_hash']}`",
                f"- Revisions: `{', '.join(knowledge_scope['revision_ids'])}`",
                "",
            ]
        )

    quality_experiment = report.get("quality_experiment")
    if isinstance(quality_experiment, dict):
        lines.extend(
            [
                "## Quality Experiment",
                "",
                f"- Experiment: `{quality_experiment['experiment_id']}`",
                f"- Role: `{quality_experiment['role']}`",
                f"- Frozen conditions SHA-256: `{quality_experiment['conditions_sha256']}`",
                "",
            ]
        )

    if report.get("conditions_sha256"):
        lines.extend(
            [
                "## Frozen Conditions",
                "",
                f"- Conditions SHA-256: `{report['conditions_sha256']}`",
                f"- Comparison eligible: `{report.get('comparison_eligible', False)}`",
                f"- Candidate quality eligible: `{report.get('quality_eligible', False)}`",
                "",
            ]
        )

    quality_gate = report.get("quality_gate")
    if quality_gate is not None:
        gate_lines = [
            "## Quality Gate",
            "",
            f"- Gate: `{quality_gate['gate_id']}`",
            f"- Passed: `{quality_gate['passed']}`",
        ]
        for check in quality_gate["checks"]:
            status = "passed" if check["passed"] else "failed"
            gate_lines.append(f"- `{check['name']}`: `{status}` - {check['reason']}")
        gate_lines.append("")
        lines.extend(gate_lines)

    provider_evidence = report.get("provider_evidence")
    if isinstance(provider_evidence, dict):
        lines.extend(
            [
                "## Provider Evidence",
                "",
                f"- Observations: `{provider_evidence['observation_count']}`",
                f"- Complete observations: `{provider_evidence['complete_observation_count']}`",
                f"- Generation providers: `{', '.join(provider_evidence['generation_providers']) or 'not observed'}`",
                f"- Generation model IDs: `{', '.join(provider_evidence['generation_model_ids']) or 'not observed'}`",
                f"- Policy providers: `{', '.join(provider_evidence.get('policy_providers', [])) or 'not observed'}`",
                f"- Policy model IDs: `{', '.join(provider_evidence.get('policy_model_ids', [])) or 'not observed'}`",
                f"- Fallback count: `{provider_evidence['fallback_count']}`",
                f"- Comparison eligible: `{provider_evidence.get('comparison_eligible', False)}`",
                f"- Quality eligible: `{provider_evidence['quality_eligible']}`",
                "",
            ]
        )

    if metrics.get("answer_disposition_sample_count", 0):
        lines.extend(
            [
                "## A1 Answer Disposition",
                "",
                f"- Samples: `{metrics['answer_disposition_sample_count']}`",
                f"- Counts: `{metrics['answer_disposition_counts']}`",
                f"- Boundary tags: `{metrics['boundary_tag_counts']}`",
                f"- Paired samples: `{metrics['evaluation_pair_sample_count']}`",
                f"- Disposition accuracy: `{metrics['answer_disposition_accuracy']}`",
                f"- Clarification dimension accuracy: `{metrics['clarification_dimension_accuracy']}`",
                f"- Unnecessary clarification rate: `{metrics['unnecessary_clarification_rate']}`",
                f"- Coverage honesty: `{metrics['coverage_honesty']}`",
                f"- Clarification resolution accuracy: `{metrics['clarification_resolution_accuracy']}`",
                f"- Used citation rank accuracy: `{metrics['used_citation_rank_accuracy']}`",
                f"- Citation anchor fidelity: `{metrics.get('citation_anchor_fidelity')}`",
                f"- Resolved-query retrieval hit rate: `{metrics.get('resolved_query_retrieval_hit_rate')}`",
                f"- Answer-point recall: `{metrics.get('answer_point_recall')}`",
                f"- Forbidden fact violations: `{metrics['forbidden_answer_point_violation_count']}`",
                "",
            ]
        )

    lines.extend(
        [
            "## Metrics",
            "",
            f"- Retrieval recall@k: `{metrics['retrieval_recall_at_k']}`",
            f"- Expected file hit rate: `{metrics['expected_file_hit_rate']}`",
            f"- Grounded correctness: `{metrics['grounded_correctness']}`",
            f"- Citation accuracy: `{metrics['citation_accuracy']}`",
            f"- Refusal correctness: `{metrics['refusal_correctness']}`",
            f"- Router resolution accuracy: `{metrics.get('router_resolution_accuracy')}`",
            f"- Average latency ms: `{metrics['average_latency_ms']}`",
            "",
            "## Format Coverage",
            "",
        ]
    )

    for name, count in metrics["format_family_counts"].items():
        lines.append(f"- `{name}`: `{count}`")

    lines.extend(["", "## Failures", ""])
    failure_type_counts = report.get("failure_type_counts") or {}
    if failure_type_counts:
        lines.append("### Failure Type Counts")
        lines.append("")
        for name, count in failure_type_counts.items():
            lines.append(f"- `{name}`: `{count}`")
        lines.extend(["", "### Failure Items", ""])

    if report["failures"]:
        for failure in report["failures"]:
            lines.append(f"- `{failure['question_id']}`: {failure['failure_type']}")
    else:
        lines.append("- None.")

    return "\n".join(lines) + "\n"


def parse_temperature(value: str) -> float:
    try:
        return validate_temperature(float(value))
    except (TypeError, ValueError) as exc:
        raise argparse.ArgumentTypeError(str(exc)) from exc


def parse_max_output_tokens(value: str) -> int:
    try:
        return validate_max_output_tokens(int(value))
    except (TypeError, ValueError) as exc:
        raise argparse.ArgumentTypeError(str(exc)) from exc


def parse_top_k(value: str) -> int:
    try:
        top_k = int(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("top_k must be a positive integer") from exc
    if top_k < 1:
        raise argparse.ArgumentTypeError("top_k must be a positive integer")
    return top_k


def parse_evidence_min_score(value: str) -> float:
    try:
        score = float(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("evidence_min_score must be a number in (0,1]") from exc
    if not math.isfinite(score) or score <= 0 or score > 1:
        raise argparse.ArgumentTypeError("evidence_min_score must be a number in (0,1]")
    return score


def validate_temperature(value: object) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError("temperature must be a number")
    temperature = float(value)
    if not math.isfinite(temperature) or temperature < 0:
        raise ValueError("temperature must be a finite value greater than or equal to 0")
    return temperature


def validate_max_output_tokens(value: object) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 1:
        raise ValueError("max_output_tokens must be an integer greater than or equal to 1")
    return value


def validate_system_prompt_version(value: object) -> str:
    if not isinstance(value, str) or value not in REGISTERED_SYSTEM_PROMPT_VERSIONS:
        registered = ", ".join(sorted(REGISTERED_SYSTEM_PROMPT_VERSIONS))
        raise ValueError(f"system_prompt_version must be one of: {registered}")
    return value


def validate_response_style(value: object) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError("response_style must be a non-empty string")
    return value.strip()


def validate_top_p(value: object) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError("top_p must be a number")
    top_p = float(value)
    if not math.isfinite(top_p) or top_p < 0 or top_p > 1:
        raise ValueError("top_p must be a finite value between 0 and 1")
    return top_p


def parse_top_p(value: str) -> float:
    try:
        return validate_top_p(float(value))
    except ValueError as exc:
        raise argparse.ArgumentTypeError(str(exc)) from exc


def build_generation_config(
    temperature: object,
    max_output_tokens: object,
    system_prompt_version: object = DEFAULT_SYSTEM_PROMPT_VERSION,
    response_style: object | None = None,
    top_p: object | None = None,
) -> dict[str, str | float | int]:
    config: dict[str, str | float | int] = {
        "system_prompt_version": validate_system_prompt_version(system_prompt_version),
        "temperature": validate_temperature(temperature),
        "max_output_tokens": validate_max_output_tokens(max_output_tokens),
    }
    if response_style is not None:
        config["response_style"] = validate_response_style(response_style)
    if top_p is not None:
        config["top_p"] = validate_top_p(top_p)
    return config


def normalize_generation_config(
    generation_config: dict[str, str | float | int] | None,
) -> dict[str, str | float | int]:
    if generation_config is None:
        return build_generation_config(DEFAULT_TEMPERATURE, DEFAULT_MAX_OUTPUT_TOKENS)
    return build_generation_config(
        generation_config.get("temperature"),
        generation_config.get("max_output_tokens"),
        generation_config.get("system_prompt_version", DEFAULT_SYSTEM_PROMPT_VERSION),
        generation_config.get("response_style"),
        generation_config.get("top_p"),
    )


def load_quality_gate(path: Path) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except OSError as exc:
        raise QualityGateError(f"cannot read quality gate {path}: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise QualityGateError(f"invalid quality gate JSON {path}: {exc}") from exc
    return validate_quality_gate(payload)


def validate_quality_gate(payload: object) -> dict[str, Any]:
    if not isinstance(payload, dict):
        raise QualityGateError("quality gate must be a JSON object")

    fields = set(payload)
    missing_fields = sorted(QUALITY_GATE_REQUIRED_FIELDS - fields)
    unknown_fields = sorted(fields - (QUALITY_GATE_REQUIRED_FIELDS | QUALITY_GATE_OPTIONAL_FIELDS))
    if missing_fields or unknown_fields:
        details: list[str] = []
        if missing_fields:
            details.append(f"missing fields: {', '.join(missing_fields)}")
        if unknown_fields:
            details.append(f"unknown fields: {', '.join(unknown_fields)}")
        raise QualityGateError(f"invalid quality gate fields ({'; '.join(details)})")

    gate_id = validate_quality_gate_string(payload["gate_id"], "gate_id")
    mode = validate_quality_gate_string(payload["mode"], "mode")
    if mode not in RUNTIME_EVAL_MODES:
        raise QualityGateError("quality gate mode must be runtime-local or runtime-remote")
    model_id = validate_quality_gate_string(payload["model_id"], "model_id")
    generation_config = validate_quality_gate_generation_config(payload["generation_config"])

    minimum_passed_count = payload["minimum_passed_count"]
    if isinstance(minimum_passed_count, bool) or not isinstance(minimum_passed_count, int) or minimum_passed_count < 0:
        raise QualityGateError("minimum_passed_count must be an integer greater than or equal to 0")

    minimum_metrics = validate_quality_gate_thresholds(
        payload["minimum_metrics"],
        allowed_names=QUALITY_GATE_METRICS,
        field_name="minimum_metrics",
    )
    maximum_failure_type_counts = validate_quality_gate_thresholds(
        payload["maximum_failure_type_counts"],
        allowed_names=QUALITY_GATE_FAILURE_TYPES,
        field_name="maximum_failure_type_counts",
        integer_values=True,
    )
    maximum_metrics = validate_quality_gate_thresholds(
        payload.get("maximum_metrics", {}),
        allowed_names=QUALITY_GATE_METRICS,
        field_name="maximum_metrics",
    )
    require_a1_q1_candidate = payload.get("require_a1_q1_candidate", False)
    if not isinstance(require_a1_q1_candidate, bool):
        raise QualityGateError("require_a1_q1_candidate must be boolean")
    return {
        "gate_id": gate_id,
        "mode": mode,
        "model_id": model_id,
        "generation_config": generation_config,
        "minimum_passed_count": minimum_passed_count,
        "minimum_metrics": minimum_metrics,
        "maximum_failure_type_counts": maximum_failure_type_counts,
        "maximum_metrics": maximum_metrics,
        "require_a1_q1_candidate": require_a1_q1_candidate,
    }


def validate_quality_gate_string(value: object, field_name: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise QualityGateError(f"{field_name} must be a non-empty string")
    return value


def validate_quality_gate_generation_config(value: object) -> dict[str, str | float | int]:
    required_fields = {"system_prompt_version", "temperature", "max_output_tokens"}
    optional_fields = {"response_style", "top_p"}
    if not isinstance(value, dict) or not required_fields.issubset(value) or not set(value).issubset(required_fields | optional_fields):
        raise QualityGateError(
            "generation_config must contain system_prompt_version, temperature and max_output_tokens, with optional response_style/top_p"
        )
    try:
        return build_generation_config(
            value["temperature"],
            value["max_output_tokens"],
            value["system_prompt_version"],
            value.get("response_style"),
            value.get("top_p"),
        )
    except ValueError as exc:
        raise QualityGateError(str(exc)) from exc


def validate_quality_gate_thresholds(
    value: object,
    *,
    allowed_names: set[str],
    field_name: str,
    integer_values: bool = False,
) -> dict[str, float | int]:
    if not isinstance(value, dict):
        raise QualityGateError(f"{field_name} must be an object")

    thresholds: dict[str, float | int] = {}
    for name, threshold in value.items():
        if name not in allowed_names:
            raise QualityGateError(f"{field_name} contains unsupported check {name}")
        if isinstance(threshold, bool):
            raise QualityGateError(f"{field_name}.{name} must be numeric")
        if integer_values:
            if not isinstance(threshold, int) or threshold < 0:
                raise QualityGateError(f"{field_name}.{name} must be an integer greater than or equal to 0")
            thresholds[name] = threshold
            continue
        if not isinstance(threshold, (int, float)) or not math.isfinite(float(threshold)) or threshold < 0:
            raise QualityGateError(f"{field_name}.{name} must be a finite number greater than or equal to 0")
        thresholds[name] = float(threshold)
    return thresholds


def evaluate_quality_gate(gate: dict[str, Any], report: dict[str, Any]) -> dict[str, Any]:
    checks = [
        build_quality_gate_check("mode", gate["mode"], report.get("mode")),
        build_quality_gate_check("model_id", gate["model_id"], report.get("model_id")),
        build_quality_gate_check(
            "generation_config",
            gate["generation_config"],
            report.get("generation_config"),
        ),
    ]
    if gate.get("require_a1_q1_candidate"):
        checks.extend(_build_a1_q1_candidate_identity_checks(report))

    metrics = report.get("metrics") or {}
    passed_count = metrics.get("passed_count")
    checks.append(
        build_quality_gate_threshold_check(
            "passed_count",
            passed_count,
            gate["minimum_passed_count"],
            comparison="minimum",
        )
    )
    for name, threshold in gate["minimum_metrics"].items():
        checks.append(
            build_quality_gate_threshold_check(
                f"metrics.{name}",
                metrics.get(name),
                threshold,
                comparison="minimum",
            )
        )
    for name, threshold in gate.get("maximum_metrics", {}).items():
        checks.append(
            build_quality_gate_threshold_check(
                f"metrics.{name}",
                metrics.get(name),
                threshold,
                comparison="maximum",
            )
        )

    failure_type_counts = report.get("failure_type_counts") or {}
    for name, threshold in gate["maximum_failure_type_counts"].items():
        checks.append(
            build_quality_gate_threshold_check(
                f"failure_type_counts.{name}",
                failure_type_counts.get(name, 0),
                threshold,
                comparison="maximum",
            )
        )

    return {
        "gate_id": gate["gate_id"],
        "passed": all(check["passed"] for check in checks),
        "checks": checks,
    }


def _build_a1_q1_candidate_identity_checks(report: dict[str, Any]) -> list[dict[str, Any]]:
    checks = [
        build_quality_gate_check("evaluation_role", "candidate", report.get("evaluation_role")),
        build_quality_gate_check("quality_eligible", True, report.get("quality_eligible")),
    ]
    conditions_sha256 = report.get("conditions_sha256")
    valid_conditions_hash = isinstance(conditions_sha256, str) and re.fullmatch(r"sha256:[0-9a-f]{64}", conditions_sha256)
    checks.append(
        {
            "name": "conditions_sha256",
            "passed": bool(valid_conditions_hash),
            "expected": "sha256:<64 lowercase hex characters>",
            "actual": conditions_sha256,
            "reason": "valid frozen conditions hash" if valid_conditions_hash else "missing or invalid frozen conditions hash",
        }
    )
    request_conditions = report.get("request_conditions")
    if not isinstance(request_conditions, dict):
        checks.append(build_quality_gate_check("request_conditions", "present", None))
        return checks
    generation = request_conditions.get("generation")
    retrieval = request_conditions.get("retrieval")
    dataset = request_conditions.get("dataset")
    checks.extend(
        [
            build_quality_gate_check("dataset_id", dataset.get("id") if isinstance(dataset, dict) else None, report.get("dataset_id")),
            build_quality_gate_check("dataset_sha256", dataset.get("sha256") if isinstance(dataset, dict) else None, report.get("dataset_sha256")),
            build_quality_gate_check("config_version", generation.get("config_version") if isinstance(generation, dict) else None, report.get("config_version")),
            build_quality_gate_check("generation_config", _generation_config_from_conditions(generation), report.get("generation_config")),
            build_quality_gate_check("retrieval_config_version", retrieval.get("config_version") if isinstance(retrieval, dict) else None, report.get("retrieval_config_version")),
            build_quality_gate_check("retrieval_strategy", retrieval.get("strategy") if isinstance(retrieval, dict) else None, report.get("retrieval_strategy")),
        ]
    )
    return checks


def _generation_config_from_conditions(value: object) -> dict[str, Any] | None:
    if not isinstance(value, dict):
        return None
    return {name: value.get(name) for name in ("system_prompt_version", "temperature", "max_output_tokens", "response_style", "top_p")}


def build_quality_gate_check(name: str, expected: object, actual: object) -> dict[str, Any]:
    passed = actual == expected
    reason = "matched expected value" if passed else f"expected {expected!r}, got {actual!r}"
    return {
        "name": name,
        "passed": passed,
        "expected": expected,
        "actual": actual,
        "reason": reason,
    }


def build_quality_gate_threshold_check(
    name: str,
    actual: object,
    threshold: float | int,
    *,
    comparison: str,
) -> dict[str, Any]:
    if not isinstance(actual, (int, float)) or isinstance(actual, bool):
        passed = False
        reason = f"actual value {actual!r} is not numeric"
    elif comparison == "minimum":
        passed = actual >= threshold
        reason = f"{actual} meets minimum {threshold}" if passed else f"{actual} is below minimum {threshold}"
    else:
        passed = actual <= threshold
        reason = f"{actual} is within maximum {threshold}" if passed else f"{actual} exceeds maximum {threshold}"
    return {
        "name": name,
        "passed": passed,
        "expected": {comparison: threshold},
        "actual": actual,
        "reason": reason,
    }


def quality_gate_failure_reasons(quality_gate_result: dict[str, Any]) -> str:
    return "; ".join(check["reason"] for check in quality_gate_result["checks"] if not check["passed"])


def build_report(
    records: list[dict[str, Any]],
    dataset_path: Path,
    *,
    mode: str,
    model_id: str,
    config_version: str,
    retrieval_config_version: str,
    knowledge_snapshot_id: str,
    generation_config: dict[str, str | float | int] | None = None,
    embedding_model: str | None = None,
    retrieval_strategy: str | None = None,
    knowledge_scope: dict[str, Any] | None = None,
    top_k: int | None = None,
    rerank_enabled: bool | None = None,
    evidence_min_score: float | None = None,
    quality_experiment: dict[str, str] | None = None,
    router_policy_version: str | None = None,
) -> dict[str, Any]:
    actual_generation_config = normalize_generation_config(generation_config)
    actual_retrieval_strategy = resolve_retrieval_strategy(retrieval_strategy)
    started_at = utc_now()
    if mode == "mock":
        metrics, failures, samples = run_mock_eval(records)
        report_model_id = "mock-deterministic-v1"
        report_config_version = "eval_mock_config_v1"
        report_retrieval_config_version = "eval_mock_retrieval_v1"
    elif mode == "runtime-local":
        metrics, failures, samples = run_runtime_eval(
            records,
            model_id=model_id,
            config_version=config_version,
            retrieval_config_version=retrieval_config_version,
            knowledge_snapshot_id=knowledge_snapshot_id,
            generation_config=actual_generation_config,
            embedding_model=embedding_model,
            retrieval_strategy=actual_retrieval_strategy,
            knowledge_scope=knowledge_scope,
            top_k=top_k,
            rerank_enabled=rerank_enabled,
            evidence_min_score=evidence_min_score,
        )
        report_model_id = model_id
        report_config_version = config_version
        report_retrieval_config_version = retrieval_config_version
    else:  # pragma: no cover - argparse enforces choices
        raise DatasetError(f"unsupported eval mode: {mode}")
    completed_at = utc_now()
    report = {
        "run_id": f"{mode}-{datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')}",
        "mode": mode,
        "dataset_id": dataset_path.stem,
        "dataset_sha256": dataset_sha256(dataset_path),
        "model_id": report_model_id,
        "config_version": report_config_version,
        "retrieval_config_version": report_retrieval_config_version,
        "retrieval_strategy": actual_retrieval_strategy,
        "router_policy_version": router_policy_version,
        "knowledge_snapshot_id": knowledge_snapshot_id if mode in RUNTIME_EVAL_MODES else None,
        "knowledge_scope": knowledge_scope if mode in RUNTIME_EVAL_MODES else None,
        "generation_config": actual_generation_config,
        "retrieval": build_retrieval_metadata(
            embedding_model,
            actual_retrieval_strategy,
            top_k=top_k,
            rerank_enabled=rerank_enabled,
            evidence_min_score=evidence_min_score,
        ),
        "started_at": started_at,
        "completed_at": completed_at,
        "metrics": metrics,
        "samples": samples,
        "failures": failures,
        "failure_type_counts": dict(sorted(Counter(failure["failure_type"] for failure in failures).items())),
        "provider_evidence": summarize_provider_evidence(samples, mode),
    }
    if quality_experiment is not None:
        report["quality_experiment"] = quality_experiment
    return report


def build_remote_report(
    records: list[dict[str, Any]],
    dataset_path: Path,
    *,
    runtime_base_url: str,
    runtime_service_token: str,
    model_id: str,
    config_version: str,
    retrieval_config_version: str,
    knowledge_snapshot_id: str,
    generation_config: dict[str, str | float | int],
    embedding_model: str | None,
    retrieval_strategy: str | None,
    knowledge_scope: dict[str, Any] | None,
    top_k: int | None,
    rerank_enabled: bool | None,
    evidence_min_score: float | None,
    quality_experiment: dict[str, str] | None,
    router_policy_version: str | None,
    answer_policy_version: str | None = None,
    evaluation_role: str | None = None,
    conditions: dict[str, Any] | None = None,
    conditions_sha256: str | None = None,
    evaluation_purpose: str | None = None,
) -> dict[str, Any]:
    if knowledge_scope is None:
        raise DatasetError("runtime-remote requires a knowledge scope")
    actual_generation_config = normalize_generation_config(generation_config)
    strategy = resolve_retrieval_strategy(retrieval_strategy)
    started_at = utc_now()
    metrics, failures, samples = run_remote_runtime_eval(
        records,
        runtime_base_url=runtime_base_url,
        runtime_service_token=runtime_service_token,
        model_id=model_id,
        config_version=config_version,
        retrieval_config_version=retrieval_config_version,
        knowledge_snapshot_id=knowledge_snapshot_id,
        generation_config=actual_generation_config,
        embedding_model=embedding_model,
        retrieval_strategy=strategy,
        knowledge_scope=knowledge_scope,
        top_k=top_k,
        rerank_enabled=rerank_enabled,
        evidence_min_score=evidence_min_score,
        answer_policy_version=answer_policy_version,
        evaluation_role=evaluation_role,
        conditions=conditions,
        evaluation_purpose=evaluation_purpose,
    )
    provider_evidence = summarize_provider_evidence(samples, "runtime-remote", evaluation_role)
    if evaluation_purpose == "pre_q1_probe":
        provider_evidence["comparison_eligible"] = False
        provider_evidence["quality_eligible"] = False
    report: dict[str, Any] = {
        "run_id": f"runtime-remote-{datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')}",
        "mode": "runtime-remote",
        "dataset_id": dataset_path.stem,
        "dataset_sha256": dataset_sha256(dataset_path),
        "model_id": model_id,
        "config_version": config_version,
        "retrieval_config_version": retrieval_config_version,
        "retrieval_strategy": strategy,
        "router_policy_version": router_policy_version,
        "knowledge_snapshot_id": knowledge_snapshot_id,
        "knowledge_scope": knowledge_scope,
        "generation_config": actual_generation_config,
        "retrieval": build_retrieval_metadata(
            embedding_model,
            strategy,
            top_k=top_k,
            rerank_enabled=rerank_enabled,
            evidence_min_score=evidence_min_score,
        ),
        "started_at": started_at,
        "completed_at": utc_now(),
        "metrics": metrics,
        "samples": samples,
        "failures": failures,
        "failure_type_counts": dict(sorted(Counter(failure["failure_type"] for failure in failures).items())),
        "execution_evidence": "remote_strict_observation_per_sample",
        "provider_evidence": provider_evidence,
    }
    if evaluation_role is not None:
        report["evaluation_role"] = evaluation_role
        report["comparison_eligible"] = report["provider_evidence"]["comparison_eligible"]
        report["quality_eligible"] = report["provider_evidence"]["quality_eligible"]
    if evaluation_purpose is not None:
        report["evaluation_purpose"] = evaluation_purpose
    if conditions is not None and conditions_sha256 is not None:
        report["conditions_sha256"] = conditions_sha256
        report["request_conditions"] = build_a1_q1_request_conditions(conditions, evaluation_role)
    if quality_experiment is not None:
        report["quality_experiment"] = quality_experiment
    return report


def dataset_sha256(dataset_path: Path) -> str:
    # `build_report` is also a pure unit-test seam where a synthetic Path is
    # deliberately supplied. Real CLI runs load the dataset before this point,
    # so they always hash the exact dataset bytes used for scoring.
    source = dataset_path.read_bytes() if dataset_path.is_file() else dataset_path.as_posix().encode("utf-8")
    return "sha256:" + hashlib.sha256(source).hexdigest()


def resolve_retrieval_strategy(value: str | None = None) -> str:
    configured = (value or os.getenv("EVAL_RETRIEVAL_STRATEGY", "") or os.getenv("RUNTIME_RETRIEVAL_STRATEGY", "")).strip()
    if configured:
        if configured not in {"lexical_compat_v1", "native_dense_v1"}:
            raise DatasetError("retrieval strategy must be lexical_compat_v1 or native_dense_v1")
        return configured
    legacy_mode = os.getenv("RUNTIME_RETRIEVAL_MODE", "lexical").strip().lower() or "lexical"
    strategy_by_mode = {
        "lexical": "lexical_compat_v1",
        "native_dense": "native_dense_v1",
    }
    try:
        return strategy_by_mode[legacy_mode]
    except KeyError as exc:
        raise DatasetError("RUNTIME_RETRIEVAL_MODE must be lexical or native_dense") from exc


def build_retrieval_metadata(
    embedding_model: str | None = None,
    retrieval_strategy: str | None = None,
    *,
    top_k: int | None = None,
    rerank_enabled: bool | None = None,
    evidence_min_score: float | None = None,
) -> dict[str, str | int | float | bool | None]:
    resolved_strategy = resolve_retrieval_strategy(retrieval_strategy)
    retrieval_mode = "native_dense" if resolved_strategy == "native_dense_v1" else "lexical"
    configured_model = (embedding_model or os.getenv("EMBEDDING_MODEL", "")).strip() or None
    return {
        "mode": retrieval_mode,
        "strategy": resolved_strategy,
        "embedding_base_url": os.getenv("EMBEDDING_BASE_URL", "").strip() or None,
        "embedding_model": configured_model,
        "embedding_dimensions": os.getenv("EMBEDDING_DIMENSIONS", "").strip() or None,
        "qdrant_collection": os.getenv("QDRANT_COLLECTION", "").strip() or None,
        "top_k": top_k,
        "rerank_enabled": rerank_enabled,
        "evidence_min_score": evidence_min_score,
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run the eval baseline.")
    parser.add_argument("--dataset", default="data/eval-datasets/baseline/v1-seed.jsonl", type=Path)
    parser.add_argument("--report-dir", default="data/eval-datasets/reports", type=Path)
    parser.add_argument("--mode", choices=("mock", "runtime-local", "runtime-remote"), default="mock")
    parser.add_argument(
        "--a1-q1-conditions",
        type=Path,
        help="Frozen a1_q1_remote_conditions_v1 JSON. It derives Q1 request conditions and rejects conflicting CLI values.",
    )
    parser.add_argument(
        "--evaluation-role",
        choices=tuple(sorted(QUALITY_EXPERIMENT_ROLES)),
        default=os.getenv("EVAL_EVALUATION_ROLE", "").strip() or None,
        help="Frozen A1-Q1 role. Required with --a1-q1-conditions.",
    )
    parser.add_argument(
        "--probe-question-id",
        dest="probe_question_ids",
        action="append",
        default=[],
        help=(
            "Repeat exactly twice for a non-evidence A1-Q1 candidate preflight probe. "
            "Requires runtime-remote frozen conditions and rejects gates and report registration."
        ),
    )
    parser.add_argument(
        "--runtime-base-url",
        default=os.getenv("EVAL_RUNTIME_BASE_URL", "").strip() or None,
        help="Required for runtime-remote: already-running Runtime base URL. The runner never starts or seeds it.",
    )
    parser.add_argument(
        "--runtime-service-token",
        default=os.getenv("EVAL_RUNTIME_SERVICE_TOKEN", "").strip() or None,
        help="Required for runtime-remote: Runtime service bearer token; never written into reports.",
    )
    parser.add_argument(
        "--knowledge-scope-file",
        type=Path,
        help="Required for runtime-remote: frozen JSON scope from the authoritative published snapshot.",
    )
    parser.add_argument(
        "--model-id",
        default=os.getenv("LOCAL_LLM_DEFAULT_MODEL_ID", "llama3.2:latest").strip() or "llama3.2:latest",
    )
    parser.add_argument("--config-version", default="cfg_eval_runtime_v1")
    parser.add_argument("--retrieval-config-version", default="rcfg_eval_runtime_v1")
    parser.add_argument(
        "--retrieval-strategy",
        choices=("lexical_compat_v1", "native_dense_v1"),
        default=os.getenv("EVAL_RETRIEVAL_STRATEGY", "").strip() or None,
        help="Registered Runtime retrieval strategy recorded in the report and sent as the request override.",
    )
    parser.add_argument(
        "--embedding-model",
        default=os.getenv("EVAL_EMBEDDING_MODEL", "").strip() or None,
        help=(
            "Embedding model recorded in the report and passed as the Runtime retrieval-config override. "
            "Required when evaluating native_dense Runtime mode."
        ),
    )
    parser.add_argument("--knowledge-snapshot-id", default="snapshot_active")
    parser.add_argument(
        "--top-k",
        type=parse_top_k,
        default=None,
        help="Explicit retrieval top_k recorded in the Runtime request and report.",
    )
    parser.add_argument(
        "--rerank-enabled",
        action=argparse.BooleanOptionalAction,
        default=None,
        help="Explicit retrieval rerank switch recorded in the Runtime request and report.",
    )
    parser.add_argument(
        "--evidence-min-score",
        type=parse_evidence_min_score,
        default=None,
        help="Explicit immutable evidence boundary: Runtime refuses when the best retrieved score is below this value.",
    )
    parser.add_argument(
        "--materialize-k8-r2-experiment-scope",
        action="store_true",
        help=(
            "Index the immutable seven-format fixture corpus into the active model-specific collection "
            "and send the resulting experiment knowledge scope with every Runtime request."
        ),
    )
    parser.add_argument(
        "--system-prompt-version",
        choices=tuple(sorted(REGISTERED_SYSTEM_PROMPT_VERSIONS)),
        default=os.getenv("EVAL_SYSTEM_PROMPT_VERSION", DEFAULT_SYSTEM_PROMPT_VERSION),
        help=(
            "Bundled system prompt version for runtime-local eval. CLI overrides "
            f"EVAL_SYSTEM_PROMPT_VERSION; otherwise default: {DEFAULT_SYSTEM_PROMPT_VERSION}."
        ),
    )
    parser.add_argument("--response-style", default=os.getenv("EVAL_RESPONSE_STYLE", "").strip() or None)
    parser.add_argument("--top-p", type=parse_top_p, default=None)
    parser.add_argument(
        "--quality-gate",
        type=Path,
        help="Versioned runtime-local quality gate JSON. Gate failures return a non-zero exit code after writing the report.",
    )
    parser.add_argument(
        "--router-policy-version",
        choices=(CONVERSATION_ROUTER_POLICY_VERSION,),
        default=os.getenv("EVAL_ROUTER_POLICY_VERSION", "").strip() or None,
        help="Code-owned conversation router policy version recorded as a frozen report condition for C3/Q1 runs.",
    )
    parser.add_argument(
        "--quality-experiment-id",
        default=os.getenv("EVAL_QUALITY_EXPERIMENT_ID", "").strip() or None,
        help="Frozen Q1 experiment identifier recorded with this report. Requires all quality experiment registration options.",
    )
    parser.add_argument(
        "--quality-experiment-role",
        choices=tuple(sorted(QUALITY_EXPERIMENT_ROLES)),
        default=os.getenv("EVAL_QUALITY_EXPERIMENT_ROLE", "").strip() or None,
        help="Whether this frozen Q1 report is the baseline or candidate result.",
    )
    parser.add_argument(
        "--quality-experiment-conditions-sha256",
        default=os.getenv("EVAL_QUALITY_EXPERIMENT_CONDITIONS_SHA256", "").strip() or None,
        help="Frozen Q1 experiment conditions SHA-256 recorded in report metadata for Backend validation.",
    )
    parser.add_argument(
        "--backend-eval-report-endpoint",
        default=os.getenv("EVAL_BACKEND_REPORT_ENDPOINT", "").strip() or None,
        help="Explicit POST /api/eval/reports endpoint. No Backend write occurs unless this and all Q1 options are supplied.",
    )
    parser.add_argument(
        "--quality-experiment-report-endpoint",
        default=os.getenv("EVAL_QUALITY_EXPERIMENT_REPORT_ENDPOINT", "").strip() or None,
        help=(
            "Explicit Q1 POST endpoint template containing {experiment_id}, for example "
            "https://backend/api/admin/quality/experiments/{experiment_id}/reports."
        ),
    )
    parser.add_argument(
        "--backend-report-bearer-token",
        default=os.getenv("EVAL_BACKEND_REPORT_BEARER_TOKEN", "").strip() or None,
        help="Optional bearer token used only for the two explicit Backend report registrations.",
    )
    parser.add_argument(
        "--backend-report-cookie-file",
        type=Path,
        default=Path(os.environ["EVAL_BACKEND_REPORT_COOKIE_FILE"]) if os.getenv("EVAL_BACKEND_REPORT_COOKIE_FILE") else None,
        help="Optional Netscape/curl cookie jar for the two explicit system-admin Backend report registrations.",
    )
    parser.add_argument(
        "--backend-report-timeout-seconds",
        type=parse_backend_report_timeout,
        default=os.getenv("EVAL_BACKEND_REPORT_TIMEOUT_SECONDS", "10"),
        help="Timeout for each opt-in Backend report registration request (default: 10).",
    )
    parser.add_argument(
        "--temperature",
        type=parse_temperature,
        default=os.getenv("EVAL_TEMPERATURE", str(DEFAULT_TEMPERATURE)),
        help=(
            "Generation temperature for runtime-local eval. CLI overrides EVAL_TEMPERATURE; "
            f"otherwise default: {DEFAULT_TEMPERATURE}."
        ),
    )
    parser.add_argument(
        "--max-output-tokens",
        type=parse_max_output_tokens,
        default=os.getenv("EVAL_MAX_OUTPUT_TOKENS", str(DEFAULT_MAX_OUTPUT_TOKENS)),
        help=(
            "Maximum generation tokens for runtime-local eval. CLI overrides EVAL_MAX_OUTPUT_TOKENS; "
            f"otherwise default: {DEFAULT_MAX_OUTPUT_TOKENS}."
        ),
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    a1_q1_conditions: dict[str, Any] | None = None
    a1_q1_conditions_sha256: str | None = None
    evaluation_role: str | None = None
    answer_policy_version: str | None = None
    try:
        probe_question_ids = validate_a1_q1_preflight_probe_args(args)
    except DatasetError as exc:
        print(f"A1-Q1 preflight probe setup error: {exc}", file=sys.stderr)
        return 2
    if args.a1_q1_conditions is not None:
        try:
            a1_q1_conditions = load_a1_q1_remote_conditions(args.a1_q1_conditions)
            a1_q1_conditions_sha256 = canonical_json_sha256(a1_q1_conditions)
            evaluation_role, answer_policy_version = apply_a1_q1_conditions(args, a1_q1_conditions)
            if args.quality_experiment_id:
                args.quality_experiment_role = evaluation_role
                args.quality_experiment_conditions_sha256 = a1_q1_conditions_sha256
        except (DatasetError, QualityGateError) as exc:
            print(f"A1-Q1 conditions setup error: {exc}", file=sys.stderr)
            return 2
    try:
        quality_experiment = build_quality_experiment_metadata(args)
    except DatasetError as exc:
        print(f"quality experiment registration setup error: {exc}", file=sys.stderr)
        return 2
    quality_gate_config: dict[str, Any] | None = None
    if args.quality_gate is not None:
        try:
            quality_gate_config = load_quality_gate(args.quality_gate)
        except QualityGateError as exc:
            print(f"eval quality gate error: {exc}", file=sys.stderr)
            return 2

    records = load_dataset(args.dataset)
    if a1_q1_conditions is not None:
        if args.dataset.stem != a1_q1_conditions["dataset"]["id"] or dataset_sha256(args.dataset) != a1_q1_conditions["dataset"]["sha256"]:
            print("A1-Q1 conditions setup error: frozen dataset identity does not match the loaded dataset", file=sys.stderr)
            return 2
    if probe_question_ids:
        try:
            records = select_a1_q1_preflight_probe_records(records, probe_question_ids)
        except DatasetError as exc:
            print(f"A1-Q1 preflight probe setup error: {exc}", file=sys.stderr)
            return 2
    knowledge_scope: dict[str, Any] | None = None
    if args.materialize_k8_r2_experiment_scope:
        try:
            _validate_k8_r2_experiment_args(args)
            knowledge_scope = materialize_k8_r2_experiment_scope()
            args.knowledge_snapshot_id = knowledge_scope["snapshot_id"]
        except DatasetError as exc:
            print(f"K8-R2 experiment setup error: {exc}", file=sys.stderr)
            return 2
    if args.mode == "runtime-remote":
        try:
            _validate_remote_runtime_args(args)
            knowledge_scope = a1_q1_conditions["scope"] if a1_q1_conditions is not None else load_knowledge_scope(args.knowledge_scope_file)
            args.knowledge_snapshot_id = knowledge_scope["snapshot_id"]
        except DatasetError as exc:
            print(f"remote Runtime eval setup error: {exc}", file=sys.stderr)
            return 2
    generation_config = build_generation_config(
        args.temperature,
        args.max_output_tokens,
        args.system_prompt_version,
        args.response_style,
        args.top_p,
    )
    if args.mode == "runtime-remote":
        report = build_remote_report(
            records,
            args.dataset,
            runtime_base_url=args.runtime_base_url,
            runtime_service_token=args.runtime_service_token,
            model_id=args.model_id,
            config_version=args.config_version,
            retrieval_config_version=args.retrieval_config_version,
            knowledge_snapshot_id=args.knowledge_snapshot_id,
            generation_config=generation_config,
            embedding_model=args.embedding_model,
            retrieval_strategy=args.retrieval_strategy,
            knowledge_scope=knowledge_scope,
            top_k=args.top_k,
            rerank_enabled=args.rerank_enabled,
            evidence_min_score=args.evidence_min_score,
            quality_experiment=quality_experiment,
            router_policy_version=args.router_policy_version,
            answer_policy_version=answer_policy_version,
            evaluation_role=evaluation_role,
            conditions=a1_q1_conditions,
            conditions_sha256=a1_q1_conditions_sha256,
            evaluation_purpose="pre_q1_probe" if probe_question_ids else None,
        )
    else:
        report = build_report(
            records,
            args.dataset,
            mode=args.mode,
            model_id=args.model_id,
            config_version=args.config_version,
            retrieval_config_version=args.retrieval_config_version,
            knowledge_snapshot_id=args.knowledge_snapshot_id,
            generation_config=generation_config,
            embedding_model=args.embedding_model,
            retrieval_strategy=args.retrieval_strategy,
            knowledge_scope=knowledge_scope,
            top_k=args.top_k,
            rerank_enabled=args.rerank_enabled,
            evidence_min_score=args.evidence_min_score,
            quality_experiment=quality_experiment,
            router_policy_version=args.router_policy_version,
        )
    quality_gate_result: dict[str, Any] | None = None
    if quality_gate_config is not None:
        quality_gate_result = evaluate_quality_gate(quality_gate_config, report)
        report["quality_gate"] = quality_gate_result
    write_reports(report, args.report_dir)

    if quality_experiment is not None:
        if report.get("evaluation_purpose") == "pre_q1_probe":
            print("quality experiment report registration failed: pre-Q1 probes cannot be registered", file=sys.stderr)
            return 2
        try:
            registration = register_quality_experiment_report(report, args, quality_experiment)
        except ReportRegistrationError as exc:
            report["backend_registration"] = {"status": "failed", "error": str(exc)}
            write_reports(report, args.report_dir)
            print(f"quality experiment report registration failed: {exc}", file=sys.stderr)
            return 2
        report["backend_registration"] = registration
        write_reports(report, args.report_dir)

    if quality_gate_result is not None and not quality_gate_result["passed"]:
        print(f"eval quality gate failed: {quality_gate_failure_reasons(quality_gate_result)}", file=sys.stderr)
        return 1

    metrics = report["metrics"]
    print(
        "eval baseline ok: "
        f"mode={report['mode']} "
        f"samples={metrics['sample_count']} "
        f"must_answer={metrics['must_answer_count']} "
        f"must_refuse={metrics['must_refuse_count']} "
        f"failed={metrics['failed_count']} "
        f"report_dir={args.report_dir}"
    )
    return 0


def _validate_k8_r2_experiment_args(args: argparse.Namespace) -> None:
    if args.mode != "runtime-local":
        raise DatasetError("K8-R2 experiment scope requires --mode runtime-local")
    if resolve_retrieval_strategy(args.retrieval_strategy) != "native_dense_v1":
        raise DatasetError("K8-R2 experiment scope requires native_dense_v1")
    if not args.embedding_model:
        raise DatasetError("K8-R2 experiment scope requires --embedding-model")
    if args.top_k != 3:
        raise DatasetError("K8-R2 experiment scope requires --top-k 3")
    if args.rerank_enabled is not False:
        raise DatasetError("K8-R2 experiment scope requires --no-rerank-enabled")
    if args.evidence_min_score is None:
        raise DatasetError("K8-R2 experiment scope requires --evidence-min-score")


def _validate_remote_runtime_args(args: argparse.Namespace) -> None:
    if not args.runtime_base_url or not args.runtime_base_url.startswith(("http://", "https://")):
        raise DatasetError("runtime-remote requires --runtime-base-url using http:// or https://")
    if not args.runtime_service_token:
        raise DatasetError("runtime-remote requires --runtime-service-token")
    if args.knowledge_scope_file is None and args.a1_q1_conditions is None:
        raise DatasetError("runtime-remote requires --knowledge-scope-file")


def load_knowledge_scope(path: Path | None) -> dict[str, Any]:
    if path is None:
        raise DatasetError("knowledge scope file is required")
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except OSError as exc:
        raise DatasetError(f"cannot read knowledge scope file: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise DatasetError(f"knowledge scope file is invalid JSON: {exc}") from exc
    if not isinstance(raw, dict):
        raise DatasetError("knowledge scope must be a JSON object")
    required = ("snapshot_id", "manifest_hash", "revision_ids", "resolved_at")
    if any(not raw.get(field) for field in required) or not isinstance(raw.get("revision_ids"), list):
        raise DatasetError("knowledge scope requires snapshot_id, manifest_hash, revision_ids and resolved_at")
    if not all(isinstance(value, str) and value.strip() for value in raw["revision_ids"]):
        raise DatasetError("knowledge scope revision_ids must contain non-empty strings")
    if not isinstance(raw["resolved_at"], str) or not raw["resolved_at"].strip():
        raise DatasetError("knowledge scope resolved_at must be a non-empty string")
    return raw


def parse_backend_report_timeout(value: str) -> float:
    try:
        timeout = float(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("backend report timeout must be a positive number") from exc
    if not math.isfinite(timeout) or timeout <= 0:
        raise argparse.ArgumentTypeError("backend report timeout must be a positive number")
    return timeout


def build_quality_experiment_metadata(args: argparse.Namespace) -> dict[str, str] | None:
    values = {
        "experiment_id": args.quality_experiment_id,
        "role": args.quality_experiment_role,
        "conditions_sha256": args.quality_experiment_conditions_sha256,
        "eval_report_endpoint": args.backend_eval_report_endpoint,
        "experiment_report_endpoint": args.quality_experiment_report_endpoint,
    }
    supplied = {name: value for name, value in values.items() if value is not None}
    if not supplied:
        return None
    missing = sorted(name for name, value in values.items() if value is None)
    if missing:
        raise DatasetError(f"quality experiment registration requires: {', '.join(missing)}")

    experiment_id = str(values["experiment_id"]).strip()
    if not experiment_id:
        raise DatasetError("quality experiment experiment_id must not be empty")
    role = str(values["role"]).strip()
    if role not in QUALITY_EXPERIMENT_ROLES:
        raise DatasetError("quality experiment role must be baseline or candidate")
    conditions_sha256 = str(values["conditions_sha256"])
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", conditions_sha256):
        raise DatasetError("quality experiment conditions_sha256 must be sha256:<64 lowercase hex characters>")
    experiment_endpoint = str(values["experiment_report_endpoint"])
    if QUALITY_EXPERIMENT_ENDPOINT_TOKEN not in experiment_endpoint:
        raise DatasetError("quality experiment report endpoint must contain {experiment_id}")
    for name in ("eval_report_endpoint", "experiment_report_endpoint"):
        endpoint = str(values[name])
        if not endpoint.startswith(("http://", "https://")):
            raise DatasetError(f"{name} must use http:// or https://")
    return {
        "experiment_id": experiment_id,
        "role": role,
        "conditions_sha256": conditions_sha256,
    }


def register_quality_experiment_report(
    report: dict[str, Any],
    args: argparse.Namespace,
    quality_experiment: dict[str, str],
) -> dict[str, str]:
    if report.get("evaluation_purpose") == "pre_q1_probe":
        raise ReportRegistrationError("pre-Q1 probes cannot be registered as quality experiment reports")
    report_id = str(report["run_id"])
    registration = dict(report)
    registration["report_id"] = report_id
    raw_response = post_backend_json(
        str(args.backend_eval_report_endpoint),
        registration,
        idempotency_key=f"eval-runner-report-{report_id}",
        bearer_token=args.backend_report_bearer_token,
        cookie_file=args.backend_report_cookie_file,
        timeout=float(args.backend_report_timeout_seconds),
    )
    registered_report_id = raw_response.get("report", {}).get("report_id")
    if registered_report_id != report_id:
        raise ReportRegistrationError("Backend eval report registration did not return the submitted report_id")

    experiment_endpoint = str(args.quality_experiment_report_endpoint).replace(
        QUALITY_EXPERIMENT_ENDPOINT_TOKEN,
        quote(quality_experiment["experiment_id"], safe=""),
    )
    post_backend_json(
        experiment_endpoint,
        {"report_id": report_id, "role": quality_experiment["role"]},
        idempotency_key=f"eval-runner-experiment-{report_id}-{quality_experiment['role']}",
        bearer_token=args.backend_report_bearer_token,
        cookie_file=args.backend_report_cookie_file,
        timeout=float(args.backend_report_timeout_seconds),
    )
    return {
        "status": "registered",
        "report_id": report_id,
        "eval_report_endpoint": str(args.backend_eval_report_endpoint),
        "experiment_report_endpoint": experiment_endpoint,
    }


def post_backend_json(
    endpoint: str,
    payload: dict[str, Any],
    *,
    idempotency_key: str,
    bearer_token: str | None,
    cookie_file: Path | None,
    timeout: float,
) -> dict[str, Any]:
    headers = {
        "Content-Type": "application/json",
        "Accept": "application/json",
        "Idempotency-Key": idempotency_key,
    }
    if bearer_token:
        headers["Authorization"] = f"Bearer {bearer_token}"
    if cookie_file is not None:
        headers["Cookie"] = load_cookie_header(cookie_file)
    request = Request(
        endpoint,
        data=json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8"),
        headers=headers,
        method="POST",
    )
    try:
        with open_http_request(request, timeout=timeout) as response:
            status = response.status
            body = response.read().decode("utf-8")
    except HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise ReportRegistrationError(f"POST {endpoint} returned HTTP {exc.code}: {body}") from exc


    except URLError as exc:
        raise ReportRegistrationError(f"POST {endpoint} failed: {exc.reason}") from exc
    except OSError as exc:
        raise ReportRegistrationError(f"POST {endpoint} failed: {exc}") from exc
    if status < 200 or status >= 300:
        raise ReportRegistrationError(f"POST {endpoint} returned unexpected HTTP {status}: {body}")
    try:
        decoded = json.loads(body)
    except json.JSONDecodeError as exc:
        raise ReportRegistrationError(f"POST {endpoint} returned invalid JSON") from exc
    if not isinstance(decoded, dict):
        raise ReportRegistrationError(f"POST {endpoint} returned a non-object JSON response")
    return decoded


def load_cookie_header(path: Path) -> str:
    """Read the small, local curl cookie jar without ever copying it into reports."""
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        raise ReportRegistrationError(f"cannot read Backend cookie file: {exc}") from exc
    cookies: list[str] = []
    for line in lines:
        if not line or line.startswith("#") and not line.startswith("#HttpOnly_"):
            continue
        columns = line.removeprefix("#HttpOnly_").split("\t")
        if len(columns) == 7 and columns[5] and columns[6]:
            cookies.append(f"{columns[5]}={columns[6]}")
    if not cookies:
        raise ReportRegistrationError("Backend cookie file did not contain a session cookie")
    return "; ".join(cookies)


def open_http_request(request: Request, *, timeout: float) -> Any:
    """Never send a local fullstack verification through ambient HTTP proxies."""
    host = (urlparse(request.full_url).hostname or "").lower()
    if host in {"127.0.0.1", "localhost", "::1"}:
        return build_opener(ProxyHandler({})).open(request, timeout=timeout)
    return urlopen(request, timeout=timeout)


if __name__ == "__main__":
    raise SystemExit(main())
