from __future__ import annotations

import json

import httpx

from scripts.ops import provider_stability_probe as probe


def _config(provider: str = "ollama_native") -> dict[str, object]:
    return {
        "provider": provider,
        "base_url": "http://127.0.0.1:11434/v1",
        "model_id": "qwen3:14b",
        "api_key": "ollama",
        "timeout_seconds": 5.0,
        "repetitions": 1,
        "include_stream": True,
    }


def test_provider_urls_and_endpoint_redaction() -> None:
    assert probe._models_url("ollama_native", "http://127.0.0.1:11434/v1") == "http://127.0.0.1:11434/api/tags"
    assert probe._chat_url("ollama_native", "http://127.0.0.1:11434/v1") == "http://127.0.0.1:11434/api/chat"
    assert probe._models_url("openai_compatible", "https://models.example/v1") == "https://models.example/v1/models"
    assert probe._safe_endpoint("https://models.example/v1?token=secret#fragment") == "https://models.example/v1"


def test_ollama_probe_records_text_json_and_stream_without_model_content() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/api/tags":
            return httpx.Response(200, json={"models": [{"name": "qwen3:14b"}]})
        payload = json.loads(request.content)
        if payload["stream"]:
            return httpx.Response(
                200,
                text='{"message":{"content":"READY"},"done":false}\n{"done":true,"done_reason":"stop"}\n',
                headers={"content-type": "application/x-ndjson"},
            )
        content = '{"ready":true}' if payload.get("format") else "READY"
        return httpx.Response(200, json={"message": {"content": content}, "done": True, "done_reason": "stop", "eval_count": 2})

    report: dict[str, object] = {"samples": []}
    with httpx.Client(transport=httpx.MockTransport(handler), trust_env=False) as client:
        model_list = probe._run_probe(_config(), report, client)

    assert model_list["target_present"] is True
    samples = report["samples"]
    assert len(samples) == 3
    assert [sample["kind"] for sample in samples] == ["short_text", "structured_json", "stream_text"]
    assert all(sample["status"] == "ok" for sample in samples)
    assert samples[-1]["first_event_latency_ms"] is not None


def test_probe_rejects_timeout_without_retry() -> None:
    calls = 0

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal calls
        calls += 1
        if request.url.path == "/api/tags":
            return httpx.Response(200, json={"models": [{"name": "qwen3:14b"}]})
        raise httpx.ReadTimeout("slow")

    report: dict[str, object] = {"samples": []}
    with httpx.Client(transport=httpx.MockTransport(handler), trust_env=False) as client:
        try:
            probe._run_probe(_config(), report, client)
        except probe.ProbeFailure as exc:
            assert exc.code == "model_generation_timeout"
        else:  # pragma: no cover - assertion guard
            raise AssertionError("expected provider timeout")

    assert calls == 2
    assert report["samples"] == []


def test_json_validation_is_protocol_only() -> None:
    probe._validate_json_content('{"ready":true}', "structured_json", 0)

    try:
        probe._validate_json_content("not-json", "structured_json", 0)
    except probe.ProbeFailure as exc:
        assert exc.code == "model_json_invalid"
    else:  # pragma: no cover - assertion guard
        raise AssertionError("expected invalid JSON")
