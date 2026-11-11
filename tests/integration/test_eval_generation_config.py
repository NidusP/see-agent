from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

from scripts.eval import run_baseline


MOCK_REFUSAL_RECORD = {
    "question_id": "q_refuse_test",
    "question": "No answer is available.",
    "task_type": "insufficient_evidence",
    "expected_source_files": [],
    "expected_source_locators": [],
    "must_answer": False,
    "scoring_notes": "The mock should refuse.",
}

RUNTIME_RECORD = {
    "question_id": "q_runtime_test",
    "question": "What is the configured generation budget?",
    "task_type": "single_source_fact_lookup",
    "expected_source_files": ["employee-handbook.md"],
    "expected_source_locators": ["Travel Policy"],
    "must_answer": True,
    "format_family": "md",
    "scoring_notes": "Inspect the payload only.",
}

ROUTER_RECORD = {
    "question_id": "c3_router_test",
    "question": "真的吗？",
    "task_type": "router_context_resolution",
    "expected_source_files": [],
    "expected_source_locators": [],
    "must_answer": False,
    "runtime_intent": "context_routing_required",
    "conversation_turns": [
        {
            "message_id": "msg_assistant_capability",
            "role": "assistant",
            "content": "我只依据已发布知识库的 RAG 检索结果回答。",
        }
    ],
    "expected_context_resolution": {
        "intent": "capability_help",
        "route": "controlled_reply",
        "kind": "standalone",
    },
    "expected_disposition": "controlled_response",
    "expected_router_policy_version": "conversation_router_v1",
    "scoring_notes": "The router should treat this as capability help, not a phrase-confirmation special case.",
}


def parse_eval_args(monkeypatch: pytest.MonkeyPatch, *arguments: str) -> object:
    monkeypatch.setattr(sys, "argv", ["run_baseline.py", *arguments])
    return run_baseline.parse_args()


def test_generation_config_defaults(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("EVAL_TEMPERATURE", raising=False)
    monkeypatch.delenv("EVAL_MAX_OUTPUT_TOKENS", raising=False)
    monkeypatch.delenv("EVAL_SYSTEM_PROMPT_VERSION", raising=False)

    args = parse_eval_args(monkeypatch)

    assert args.temperature == 0.2
    assert args.max_output_tokens == 1024
    assert args.system_prompt_version == "prompt_v1"


def test_generation_config_environment_and_cli_overrides(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("EVAL_TEMPERATURE", "0.7")
    monkeypatch.setenv("EVAL_MAX_OUTPUT_TOKENS", "768")
    monkeypatch.setenv("EVAL_SYSTEM_PROMPT_VERSION", "prompt_v2")

    environment_args = parse_eval_args(monkeypatch)
    assert environment_args.temperature == 0.7
    assert environment_args.max_output_tokens == 768
    assert environment_args.system_prompt_version == "prompt_v2"

    cli_args = parse_eval_args(
        monkeypatch,
        "--temperature",
        "0.4",
        "--max-output-tokens",
        "512",
        "--system-prompt-version",
        "prompt_v1",
    )
    assert cli_args.temperature == 0.4
    assert cli_args.max_output_tokens == 512
    assert cli_args.system_prompt_version == "prompt_v1"


@pytest.mark.parametrize(
    "arguments",
    [
        ("--temperature", "nan"),
        ("--temperature", "-0.1"),
        ("--max-output-tokens", "0"),
    ],
)
def test_generation_config_rejects_invalid_arguments(
    monkeypatch: pytest.MonkeyPatch,
    arguments: tuple[str, str],
) -> None:
    with pytest.raises(SystemExit):
        parse_eval_args(monkeypatch, *arguments)


def test_runtime_payload_includes_generation_overrides() -> None:
    generation_config = run_baseline.build_generation_config(0.4, 512)

    payload = run_baseline.build_runtime_chat_payload(
        RUNTIME_RECORD,
        model_id="llama3.2:latest",
        config_version="cfg_eval_runtime_v1",
        retrieval_config_version="rcfg_eval_runtime_v1",
        knowledge_snapshot_id="snapshot_active",
        generation_config=generation_config,
    )

    assert payload["response_overrides"] == {
        "system_prompt_version": "prompt_v1",
        "temperature": 0.4,
        "max_output_tokens": 512,
        "retrieval_strategy": "lexical_compat_v1",
    }


def test_runtime_payload_includes_router_fixture_context() -> None:
    payload = run_baseline.build_runtime_chat_payload(
        ROUTER_RECORD,
        model_id="qwen3:14b",
        config_version="cfg_eval_runtime_v1",
        retrieval_config_version="rcfg_eval_runtime_v1",
        knowledge_snapshot_id="snapshot_active",
        generation_config=run_baseline.build_generation_config(0.2, 1024),
    )

    assert payload["intent"] == "context_routing_required"
    assert payload["messages"] == ROUTER_RECORD["conversation_turns"]


def test_router_fixture_scoring_tracks_context_resolution() -> None:
    sample = run_baseline.evaluate_runtime_response(
        ROUTER_RECORD,
        {
            "assistant_message": {"role": "assistant", "content": "我只依据已发布知识库回答。"},
            "citations": [],
            "refusal": False,
            "finish_reason": "stop",
            "answer_disposition": "controlled_response",
            "context_resolution": {
                "intent": "capability_help",
                "route": "controlled_reply",
                "kind": "standalone",
                "basis_message_ids": [],
                "resolver_provider": "ollama_native",
                "resolver_model_id": "qwen3:14b",
                "resolver_performed": True,
                "confidence": 0.95,
            },
        },
        12.3,
    )

    assert sample["passed"] is True
    assert sample["router_evaluation"]["mismatches"] == []
    assert run_baseline.build_sample_failures(ROUTER_RECORD, sample) == []

    mismatched = run_baseline.evaluate_runtime_response(
        ROUTER_RECORD,
        {
            "assistant_message": {"role": "assistant", "content": "请提供具体问题。"},
            "citations": [],
            "refusal": False,
            "finish_reason": "stop",
            "answer_disposition": "clarification_required",
            "context_resolution": {
                "intent": "knowledge_query",
                "route": "clarification",
                "kind": "clarification_required",
                "basis_message_ids": [],
                "confidence": 0.2,
            },
        },
        12.3,
    )
    failures = run_baseline.build_sample_failures(ROUTER_RECORD, mismatched)

    assert mismatched["passed"] is False
    assert failures[0]["failure_type"] == "router_quality_mismatch"


def test_runtime_payload_includes_native_dense_embedding_override() -> None:
    payload = run_baseline.build_runtime_chat_payload(
        RUNTIME_RECORD,
        model_id="llama3.2:latest",
        config_version="cfg_eval_runtime_v1",
        retrieval_config_version="rcfg_eval_runtime_v1",
        knowledge_snapshot_id="snapshot_active",
        generation_config=run_baseline.build_generation_config(0.2, 1024),
        embedding_model="embeddinggemma",
    )

    assert payload["response_overrides"]["embedding_model"] == "embeddinggemma"


def test_runtime_payload_uses_registered_retrieval_strategy() -> None:
    payload = run_baseline.build_runtime_chat_payload(
        RUNTIME_RECORD,
        model_id="llama3.2:latest",
        config_version="cfg_eval_runtime_v1",
        retrieval_config_version="rcfg_eval_runtime_v1",
        knowledge_snapshot_id="snapshot_active",
        generation_config=run_baseline.build_generation_config(0.2, 1024),
        retrieval_strategy="native_dense_v1",
    )

    assert payload["response_overrides"]["retrieval_strategy"] == "native_dense_v1"


def test_runtime_payload_carries_explicit_k8_r2_scope_and_retrieval_parameters() -> None:
    scope = {
        "snapshot_id": "snapshot_k8_r2_fixture_v1",
        "manifest_hash": "sha256:fixture-manifest",
        "revision_ids": ["eval-r2-revision-a-v1"],
        "resolved_at": "2026-07-17T00:00:00Z",
    }
    payload = run_baseline.build_runtime_chat_payload(
        RUNTIME_RECORD,
        model_id="llama3.2:latest",
        config_version="cfg_eval_runtime_v1",
        retrieval_config_version="rcfg_eval_runtime_v1",
        knowledge_snapshot_id="snapshot_k8_r2_fixture_v1",
        generation_config=run_baseline.build_generation_config(0.2, 1024, "prompt_v2"),
        embedding_model="qwen3-embedding:0.6b",
        retrieval_strategy="native_dense_v1",
        knowledge_scope=scope,
        top_k=3,
        rerank_enabled=False,
        evidence_min_score=0.42,
    )

    assert payload["knowledge_scope"] == scope
    assert payload["response_overrides"]["top_k"] == 3
    assert payload["response_overrides"]["rerank_enabled"] is False
    assert payload["response_overrides"]["evidence_min_score"] == 0.42


def test_runtime_payload_rejects_scope_for_a_different_snapshot() -> None:
    with pytest.raises(run_baseline.DatasetError, match="must match"):
        run_baseline.build_runtime_chat_payload(
            RUNTIME_RECORD,
            model_id="llama3.2:latest",
            config_version="cfg_eval_runtime_v1",
            retrieval_config_version="rcfg_eval_runtime_v1",
            knowledge_snapshot_id="snapshot_active",
            generation_config=run_baseline.build_generation_config(0.2, 1024),
            knowledge_scope={
                "snapshot_id": "other_snapshot",
                "manifest_hash": "sha256:fixture-manifest",
                "revision_ids": ["eval-r2-revision-a-v1"],
                "resolved_at": "2026-07-17T00:00:00Z",
            },
        )


def test_remote_execution_observation_requires_real_generation_and_retrieval() -> None:
    payload = {
        "execution": {
            "fallback_used": False,
            "generation": {"status": "performed", "provider": "openai_compatible", "model_id": "llama3.2:latest"},
            "retrieval": {
                "status": "performed",
                "strategy": "lexical_compat_v1",
                "embedding_model": "baseline-lexical-embedder",
                "collection": "see_agent_chunks",
                "candidate_count": 1,
            },
        }
    }

    run_baseline.validate_remote_execution_observation(payload, model_id="llama3.2:latest")

    payload["execution"]["generation"]["provider"] = "test_injected"
    with pytest.raises(run_baseline.DatasetError, match="eligible generation provider"):
        run_baseline.validate_remote_execution_observation(payload, model_id="llama3.2:latest")


def test_remote_scope_file_is_required_and_preserves_identity(tmp_path: Path) -> None:
    scope_path = tmp_path / "scope.json"
    scope_path.write_text(
        json.dumps({"snapshot_id": "snap_001", "manifest_hash": "sha256:manifest", "revision_ids": ["rev_001"], "resolved_at": "2026-07-27T00:00:00Z"}),
        encoding="utf-8",
    )

    assert run_baseline.load_knowledge_scope(scope_path)["snapshot_id"] == "snap_001"
    scope_path.write_text(
        json.dumps({"snapshot_id": "snap_001", "manifest_hash": "sha256:manifest", "revision_ids": ["rev_001"]}),
        encoding="utf-8",
    )
    with pytest.raises(run_baseline.DatasetError, match="resolved_at"):
        run_baseline.load_knowledge_scope(scope_path)
    with pytest.raises(run_baseline.DatasetError, match="knowledge scope file is required"):
        run_baseline.load_knowledge_scope(None)


def test_backend_cookie_header_reads_curl_cookie_jar(tmp_path: Path) -> None:
    jar = tmp_path / "cookies"
    jar.write_text("# Netscape HTTP Cookie File\n#HttpOnly_127.0.0.1\tFALSE\t/\tFALSE\t0\tsee_agent_session\tvalue\n", encoding="utf-8")

    assert run_baseline.load_cookie_header(jar) == "see_agent_session=value"


def test_loopback_runtime_request_bypasses_ambient_proxy(monkeypatch: pytest.MonkeyPatch) -> None:
    request = run_baseline.Request("http://127.0.0.1:8000/v1/chat/run")
    called = {"proxy": False}

    class FakeOpener:
        def open(self, _request: object, *, timeout: float) -> str:
            called["proxy"] = timeout == 3
            return "opened"

    monkeypatch.setattr(run_baseline, "build_opener", lambda _handler: FakeOpener())
    assert run_baseline.open_http_request(request, timeout=3) == "opened"
    assert called["proxy"] is True


def test_shared_runtime_contract_declares_registered_response_overrides_and_policy_versions() -> None:
    schema_path = Path("packages/contracts/chat-runtime-request.schema.json")
    schema = json.loads(schema_path.read_text(encoding="utf-8"))
    properties = schema["$defs"]["ResponseOverrides"]["properties"]

    assert properties["system_prompt_version"]["enum"] == [
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
    ]
    assert schema["properties"]["answer_policy_version"]["enum"] == [
        "answer_policy_v1",
        "answer_policy_v2",
        "answer_policy_v3",
        "answer_policy_v4",
        "answer_policy_v5",
    ]
    assert properties["response_style"]["minLength"] == 1
    assert properties["top_p"] == {"type": "number", "minimum": 0, "maximum": 1}
    assert properties["rerank_enabled"]["type"] == "boolean"
    assert properties["evidence_min_score"] == {"type": "number", "exclusiveMinimum": 0, "maximum": 1}
    assert properties["retrieval_strategy"]["enum"] == ["lexical_compat_v1", "native_dense_v1"]


def test_mock_report_records_generation_config() -> None:
    default_report = run_baseline.build_report(
        [MOCK_REFUSAL_RECORD],
        Path("v1-seed.jsonl"),
        mode="mock",
        model_id="llama3.2:latest",
        config_version="cfg_eval_runtime_v1",
        retrieval_config_version="rcfg_eval_runtime_v1",
        knowledge_snapshot_id="snapshot_active",
    )
    explicit_config = run_baseline.build_generation_config(0.4, 512)
    explicit_report = run_baseline.build_report(
        [MOCK_REFUSAL_RECORD],
        Path("v1-seed.jsonl"),
        mode="mock",
        model_id="llama3.2:latest",
        config_version="cfg_eval_runtime_v1",
        retrieval_config_version="rcfg_eval_runtime_v1",
        knowledge_snapshot_id="snapshot_active",
        generation_config=explicit_config,
    )

    assert default_report["generation_config"] == {
        "system_prompt_version": "prompt_v1",
        "temperature": 0.2,
        "max_output_tokens": 1024,
    }
    assert explicit_report["generation_config"] == explicit_config
    assert "- System prompt version: `prompt_v1`" in run_baseline.render_markdown(explicit_report)
    assert "- Temperature: `0.4`" in run_baseline.render_markdown(explicit_report)
    assert "- Max output tokens: `512`" in run_baseline.render_markdown(explicit_report)


def test_report_records_native_dense_retrieval_configuration(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("RUNTIME_RETRIEVAL_MODE", "native_dense")
    monkeypatch.setenv("EMBEDDING_BASE_URL", "http://embedding.test/v1")
    monkeypatch.setenv("EMBEDDING_DIMENSIONS", "768")
    monkeypatch.setenv("QDRANT_COLLECTION", "eval_dense")

    report = run_baseline.build_report(
        [MOCK_REFUSAL_RECORD],
        Path("v1-seed.jsonl"),
        mode="mock",
        model_id="llama3.2:latest",
        config_version="cfg_eval_runtime_v1",
        retrieval_config_version="rcfg_eval_runtime_v1",
        knowledge_snapshot_id="snapshot_active",
        embedding_model="embeddinggemma",
        evidence_min_score=0.42,
    )

    assert report["retrieval"] == {
        "mode": "native_dense",
        "strategy": "native_dense_v1",
        "embedding_base_url": "http://embedding.test/v1",
        "embedding_model": "embeddinggemma",
        "embedding_dimensions": "768",
        "qdrant_collection": "eval_dense",
        "top_k": None,
        "rerank_enabled": None,
        "evidence_min_score": 0.42,
    }
    assert report["retrieval_strategy"] == "native_dense_v1"
    assert "- Strategy: `native_dense_v1`" in run_baseline.render_markdown(report)
    assert "- Mode: `native_dense`" in run_baseline.render_markdown(report)
    assert "- Evidence minimum score: `0.42`" in run_baseline.render_markdown(report)
    assert report["samples"][0]["threshold_decision"] == "refused"


def test_report_uses_registered_strategy_to_record_mode(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("RUNTIME_RETRIEVAL_MODE", raising=False)
    report = run_baseline.build_report(
        [MOCK_REFUSAL_RECORD],
        Path("v1-seed.jsonl"),
        mode="mock",
        model_id="llama3.2:latest",
        config_version="cfg_eval_runtime_v1",
        retrieval_config_version="rcfg_eval_runtime_v1",
        knowledge_snapshot_id="snapshot_active",
        retrieval_strategy="native_dense_v1",
    )

    assert report["retrieval"]["mode"] == "native_dense"


def test_experiment_scope_is_deterministic_and_model_independent() -> None:
    identities = [
        {
            "document_id": "eval-r2-document-b",
            "revision_id": "eval-r2-revision-b-v1",
            "checksum_sha256": "sha256:b",
            "file_id": "file-b",
            "source_format": "md",
        },
        {
            "document_id": "eval-r2-document-a",
            "revision_id": "eval-r2-revision-a-v1",
            "checksum_sha256": "sha256:a",
            "file_id": "file-a",
            "source_format": "txt",
        },
    ]

    scope = run_baseline.build_experiment_knowledge_scope(identities)

    assert scope["snapshot_id"] == "snapshot_k8_r2_fixture_v1"
    assert scope["revision_ids"] == ["eval-r2-revision-a-v1", "eval-r2-revision-b-v1"]
    assert scope["manifest_hash"].startswith("sha256:")


def test_quality_experiment_metadata_is_opt_in_and_recorded_in_report(monkeypatch: pytest.MonkeyPatch) -> None:
    assert run_baseline.build_quality_experiment_metadata(parse_eval_args(monkeypatch)) is None

    args = parse_eval_args(
        monkeypatch,
        "--quality-experiment-id",
        "quality-exp-1",
        "--quality-experiment-role",
        "candidate",
        "--quality-experiment-conditions-sha256",
        "sha256:" + "a" * 64,
        "--backend-eval-report-endpoint",
        "https://backend.test/api/eval/reports",
        "--quality-experiment-report-endpoint",
        "https://backend.test/api/admin/quality/experiments/{experiment_id}/reports",
        "--router-policy-version",
        "conversation_router_v1",
    )
    metadata = run_baseline.build_quality_experiment_metadata(args)
    report = run_baseline.build_report(
        [MOCK_REFUSAL_RECORD],
        Path("v1-seed.jsonl"),
        mode="mock",
        model_id="llama3.2:latest",
        config_version="cfg_eval_runtime_v1",
        retrieval_config_version="rcfg_eval_runtime_v1",
        knowledge_snapshot_id="snapshot_active",
        quality_experiment=metadata,
        router_policy_version=args.router_policy_version,
    )

    assert metadata == {
        "experiment_id": "quality-exp-1",
        "role": "candidate",
        "conditions_sha256": "sha256:" + "a" * 64,
    }
    assert report["quality_experiment"] == metadata
    assert report["router_policy_version"] == "conversation_router_v1"
    assert report["dataset_sha256"].startswith("sha256:")
    assert "## Quality Experiment" in run_baseline.render_markdown(report)
    assert "- Experiment: `quality-exp-1`" in run_baseline.render_markdown(report)
    assert "- Router policy: `conversation_router_v1`" in run_baseline.render_markdown(report)


def test_quality_experiment_metadata_rejects_partial_or_invalid_frozen_identity(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    partial = parse_eval_args(monkeypatch, "--quality-experiment-id", "quality-exp-1")
    with pytest.raises(run_baseline.DatasetError, match="requires"):
        run_baseline.build_quality_experiment_metadata(partial)

    invalid_hash = parse_eval_args(
        monkeypatch,
        "--quality-experiment-id",
        "quality-exp-1",
        "--quality-experiment-role",
        "baseline",
        "--quality-experiment-conditions-sha256",
        "not-a-sha",
        "--backend-eval-report-endpoint",
        "https://backend.test/api/eval/reports",
        "--quality-experiment-report-endpoint",
        "https://backend.test/api/admin/quality/experiments/{experiment_id}/reports",
    )
    with pytest.raises(run_baseline.DatasetError, match="conditions_sha256"):
        run_baseline.build_quality_experiment_metadata(invalid_hash)


def test_quality_experiment_registration_writes_raw_report_then_attaches_to_q1_endpoint(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    args = parse_eval_args(
        monkeypatch,
        "--quality-experiment-id",
        "quality exp/1",
        "--quality-experiment-role",
        "candidate",
        "--quality-experiment-conditions-sha256",
        "sha256:" + "b" * 64,
        "--backend-eval-report-endpoint",
        "https://backend.test/api/eval/reports",
        "--quality-experiment-report-endpoint",
        "https://backend.test/api/admin/quality/experiments/{experiment_id}/reports",
        "--backend-report-bearer-token",
        "runner-token",
    )
    metadata = run_baseline.build_quality_experiment_metadata(args)
    report = run_baseline.build_report(
        [MOCK_REFUSAL_RECORD],
        Path("v1-seed.jsonl"),
        mode="mock",
        model_id="llama3.2:latest",
        config_version="cfg_eval_runtime_v1",
        retrieval_config_version="rcfg_eval_runtime_v1",
        knowledge_snapshot_id="snapshot_active",
        quality_experiment=metadata,
    )
    report["run_id"] = "run-123"
    requests: list[tuple[str, dict[str, object], dict[str, str]]] = []

    class FakeResponse:
        status = 201

        def __init__(self, payload: dict[str, object]) -> None:
            self._payload = payload

        def read(self) -> bytes:
            return json.dumps(self._payload).encode("utf-8")

        def __enter__(self) -> "FakeResponse":
            return self

        def __exit__(self, *_args: object) -> None:
            return None

    def fake_urlopen(request: object, *, timeout: float) -> FakeResponse:
        request_data = request.data.decode("utf-8")  # type: ignore[attr-defined]
        request_headers = {key.lower(): value for key, value in request.header_items()}  # type: ignore[attr-defined]
        requests.append((request.full_url, json.loads(request_data), request_headers))  # type: ignore[attr-defined]
        if len(requests) == 1:
            return FakeResponse({"report": {"report_id": "run-123"}})
        return FakeResponse({"experiment": {"experiment_id": "quality exp/1"}})

    monkeypatch.setattr(run_baseline, "urlopen", fake_urlopen)

    result = run_baseline.register_quality_experiment_report(report, args, metadata)

    assert result["status"] == "registered"
    assert [request[0] for request in requests] == [
        "https://backend.test/api/eval/reports",
        "https://backend.test/api/admin/quality/experiments/quality%20exp%2F1/reports",
    ]
    assert requests[0][1]["report_id"] == "run-123"
    assert requests[0][1]["quality_experiment"] == metadata
    assert requests[0][2]["authorization"] == "Bearer runner-token"
    assert requests[1][1] == {"report_id": "run-123", "role": "candidate"}
    assert requests[1][2]["idempotency-key"] == "eval-runner-experiment-run-123-candidate"
