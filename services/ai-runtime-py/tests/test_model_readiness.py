from __future__ import annotations

import pytest

from app.adapters.model_client import ModelClientError, ModelClientResult
from app.model_readiness import GenerationReadiness, warm_up_generation_model


class WarmupModel:
    provider_name = "test_provider"

    def __init__(self, result: ModelClientResult | None = None, error: Exception | None = None) -> None:
        self.result = result or ModelClientResult(content="READY", finish_reason="stop", prompt_tokens=1, completion_tokens=1, total_tokens=2)
        self.error = error
        self.calls: list[dict[str, object]] = []

    def generate(self, **kwargs: object) -> ModelClientResult:
        self.calls.append(kwargs)
        if self.error is not None:
            raise self.error
        return self.result


def test_warmup_runs_nonpersistent_minimal_generation() -> None:
    client = WarmupModel()
    readiness = GenerationReadiness(client.provider_name, "qwen3:14b")

    warm_up_generation_model(client, readiness)

    assert readiness.status().state == "ready"
    assert len(client.calls) == 1
    assert client.calls[0]["model_id"] == "qwen3:14b"
    assert client.calls[0]["citations"] == []
    assert client.calls[0]["max_output_tokens"] == 4


def test_warmup_failure_never_marks_generation_ready() -> None:
    client = WarmupModel(error=ModelClientError("timeout"))
    readiness = GenerationReadiness(client.provider_name, "qwen3:14b")

    with pytest.raises(RuntimeError, match="model_request_failed"):
        warm_up_generation_model(client, readiness)

    assert readiness.status().state == "failed"
    assert readiness.status().reason == "model_request_failed"
