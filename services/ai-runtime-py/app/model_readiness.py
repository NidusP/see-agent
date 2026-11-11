from __future__ import annotations

import logging
import threading
from dataclasses import dataclass

from app.adapters.model_client import ModelClient, ModelClientError


_logger = logging.getLogger("uvicorn.error")


@dataclass(frozen=True)
class GenerationWarmupStatus:
    state: str
    provider: str
    model_id: str
    reason: str = ""


class GenerationReadiness:
    """Process-local proof that the configured generation model produced output.

    It deliberately stores no model response: warm-up is an operational probe,
    not a product conversation or a source of answer evidence.
    """

    def __init__(self, provider: str, model_id: str) -> None:
        self._provider = provider
        self._model_id = model_id
        self._state = "not_started"
        self._reason = ""
        self._lock = threading.Lock()

    def mark_warming(self) -> None:
        with self._lock:
            self._state = "warming"
            self._reason = ""

    def mark_ready(self) -> None:
        with self._lock:
            self._state = "ready"
            self._reason = ""

    def mark_not_required(self) -> None:
        with self._lock:
            self._state = "not_required"
            self._reason = ""

    def mark_failed(self, reason: str) -> None:
        with self._lock:
            self._state = "failed"
            self._reason = reason.strip() or "generation warm-up failed"

    def status(self) -> GenerationWarmupStatus:
        with self._lock:
            return GenerationWarmupStatus(
                state=self._state,
                provider=self._provider,
                model_id=self._model_id,
                reason=self._reason,
            )

    def is_ready(self) -> bool:
        return self.status().state in {"ready", "not_required"}


def warm_up_generation_model(model_client: ModelClient, readiness: GenerationReadiness) -> None:
    """Run one bounded, non-persistent generation before strict readiness.

    This bypasses the answer pipeline intentionally: no retrieval, conversation,
    citation, execution attempt, or probe text may become product state.
    """

    status = readiness.status()
    readiness.mark_warming()
    _logger.info(
        "runtime generation warmup provider=%s model=%s status=starting",
        status.provider,
        status.model_id,
    )
    try:
        result = model_client.generate(
            model_id=status.model_id,
            messages=[
                {"role": "system", "content": "You are a service readiness probe."},
                {"role": "user", "content": "Reply READY."},
            ],
            user_query="Reply READY.",
            citations=[],
            temperature=0,
            top_p=1,
            max_output_tokens=4,
        )
    except ModelClientError as exc:
        readiness.mark_failed(exc.code)
        _logger.error(
            "runtime generation warmup provider=%s model=%s status=failed code=%s",
            status.provider,
            status.model_id,
            exc.code,
        )
        raise RuntimeError(f"generation warm-up failed: {exc.code}") from exc
    if not result.content.strip():
        readiness.mark_failed("model_warmup_empty_response")
        _logger.error(
            "runtime generation warmup provider=%s model=%s status=failed code=model_warmup_empty_response",
            status.provider,
            status.model_id,
        )
        raise RuntimeError("generation warm-up failed: model_warmup_empty_response")
    readiness.mark_ready()
    _logger.info(
        "runtime generation warmup provider=%s model=%s status=ready",
        status.provider,
        status.model_id,
    )
