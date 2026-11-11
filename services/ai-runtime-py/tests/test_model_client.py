from __future__ import annotations

import logging

import pytest
import httpx
from fastapi.testclient import TestClient

from app.adapters.model_client import (
    ModelProviderUnavailable,
    OllamaNativeModelClient,
    OpenAICompatibleModelClient,
    UnavailableModelClient,
    _model_http_error,
    _should_trust_environment,
    build_default_model_client,
)
from app.main import create_app
from app.pipelines.retrieval_pipeline import build_default_corpus_documents


def test_build_default_model_client_requires_explicit_provider(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("LOCAL_LLM_BASE_URL", raising=False)

    assert isinstance(build_default_model_client(), UnavailableModelClient)


def test_unavailable_model_client_fails_closed_and_logs_requested_model(caplog) -> None:
    client = UnavailableModelClient()
    caplog.set_level(logging.ERROR, logger="uvicorn.error")

    with pytest.raises(ModelProviderUnavailable, match="LOCAL_LLM_BASE_URL is required"):
        client.generate(
            model_id="llama3.2:latest",
            messages=[{"role": "system", "content": "test"}],
            user_query="测试",
            citations=[],
        )

    assert "provider=unavailable" in caplog.text
    assert "requested_model=llama3.2:latest" in caplog.text
    assert "reason=endpoint_not_configured" in caplog.text


def test_unavailable_model_client_stream_fails_closed() -> None:
    client = UnavailableModelClient()

    with pytest.raises(ModelProviderUnavailable, match="LOCAL_LLM_BASE_URL is required"):
        list(
            client.stream_generate(
                model_id="llama3.2:latest",
                messages=[{"role": "system", "content": "test"}],
                user_query="测试",
                citations=[],
            )
        )


def test_build_default_model_client_uses_openai_compatible_provider(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("LOCAL_LLM_BASE_URL", "http://127.0.0.1:11434/v1")
    monkeypatch.setenv("LOCAL_LLM_API_KEY", "ollama")
    monkeypatch.setenv("LOCAL_LLM_TIMEOUT_SECONDS", "90")

    assert isinstance(build_default_model_client(), OpenAICompatibleModelClient)


def test_build_default_model_client_uses_explicit_ollama_native_provider(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("LOCAL_LLM_BASE_URL", "http://127.0.0.1:11434/v1")
    monkeypatch.setenv("LOCAL_LLM_PROVIDER", "ollama_native")

    assert isinstance(build_default_model_client(), OllamaNativeModelClient)


def test_ollama_native_client_disables_thinking_and_uses_native_chat_endpoint() -> None:
    client = OllamaNativeModelClient("http://127.0.0.1:11434/v1", timeout_seconds=90)

    assert client._ollama_chat_url() == "http://127.0.0.1:11434/api/chat"
    assert client._payload(
        model_id="qwen3:14b",
        messages=[{"role": "user", "content": "测试"}],
        stream=False,
        temperature=0.2,
        top_p=1,
        max_output_tokens=256,
        json_mode=True,
    ) == {
        "model": "qwen3:14b",
        "messages": [{"role": "user", "content": "测试"}],
        "stream": False,
        "think": False,
        "format": "json",
        "options": {"temperature": 0.2, "top_p": 1, "num_predict": 256},
    }


def test_ollama_native_client_sends_a_json_schema_when_the_policy_provides_one() -> None:
    client = OllamaNativeModelClient("http://127.0.0.1:11434/v1", timeout_seconds=90)
    schema = {"type": "object", "properties": {"pending_action": {"type": "null"}}}

    payload = client._payload(
        model_id="qwen3:14b",
        messages=[{"role": "user", "content": "测试"}],
        stream=False,
        temperature=0.0,
        top_p=1,
        max_output_tokens=128,
        json_mode=True,
        json_schema=schema,
    )

    assert payload["format"] == schema


def test_build_default_model_client_rejects_unknown_provider(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("LOCAL_LLM_BASE_URL", "http://127.0.0.1:11434/v1")
    monkeypatch.setenv("LOCAL_LLM_PROVIDER", "unknown")

    with pytest.raises(ValueError, match="LOCAL_LLM_PROVIDER"):
        build_default_model_client()


def test_chat_run_returns_structured_503_when_generation_provider_is_unavailable() -> None:
    client = TestClient(create_app(model_client_override=UnavailableModelClient(), corpus_documents_override=build_default_corpus_documents()))

    response = client.post(
        "/v1/chat/run",
        json={
            "request_id": "req_model_unavailable",
            "conversation_id": "conv_model_unavailable",
            "message_id": "msg_model_unavailable",
            "user_query": "差旅报销需要几天内提交？",
            "model_id": "llama3.2:latest",
            "config_version": "cfg_001",
            "retrieval_config_version": "rcfg_001",
            "knowledge_snapshot_id": "snap_001",
            "knowledge_scope": {"snapshot_id": "snap_001", "manifest_hash": "sha256:test", "revision_ids": ["rev_test_fixture"], "resolved_at": "2026-07-24T00:00:00Z"},
                    "response_overrides": {"retrieval_strategy": "native_dense_v1", "evidence_min_score": 0.15},
            "stream": False,
            "messages": [],
        },
    )

    assert response.status_code == 503
    assert response.json() == {
        "error": {
            "code": "model_unavailable",
            "message": "LOCAL_LLM_BASE_URL is required for knowledge answer generation",
            "retryable": True,
        }
    }


def test_loopback_model_endpoints_ignore_environment_proxy() -> None:
    assert _should_trust_environment("http://127.0.0.1:11434/v1") is False
    assert _should_trust_environment("http://localhost:11434/v1") is False
    assert _should_trust_environment("http://[::1]:11434/v1") is False
    assert _should_trust_environment("https://models.example.internal/v1") is True


@pytest.mark.parametrize(
    ("error", "code"),
    [
        (httpx.ReadTimeout("slow"), "model_generation_timeout"),
        (httpx.ConnectError("offline"), "model_provider_unreachable"),
        (httpx.HTTPStatusError("rejected", request=httpx.Request("POST", "http://model"), response=httpx.Response(503)), "model_provider_rejected"),
    ],
)
def test_model_http_errors_have_stable_failure_codes(error: httpx.HTTPError, code: str) -> None:
    assert _model_http_error("test request", error).code == code
