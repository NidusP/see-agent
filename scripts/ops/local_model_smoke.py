from __future__ import annotations

import math
import os
import sys
from pathlib import Path
from typing import Any

import httpx


ROOT = Path(__file__).resolve().parents[2]
RUNTIME_ROOT = ROOT / "services" / "ai-runtime-py"
sys.path.insert(0, str(RUNTIME_ROOT))

from app.adapters.model_client import ModelClientError, OpenAICompatibleModelClient


DEFAULT_BASE_URL = "http://127.0.0.1:11434/v1"
DEFAULT_MODEL_ID = "qwen3:14b"
DEFAULT_TIMEOUT_SECONDS = 90.0


def main() -> int:
    try:
        base_url = _required_config("LOCAL_LLM_BASE_URL", DEFAULT_BASE_URL)
        model_id = _required_config("LOCAL_LLM_DEFAULT_MODEL_ID", DEFAULT_MODEL_ID)
        timeout_seconds = _timeout_seconds()
        api_key = os.environ.get("LOCAL_LLM_API_KEY", "").strip() or None

        client = OpenAICompatibleModelClient(
            base_url,
            api_key=api_key,
            timeout_seconds=timeout_seconds,
        )
        model_ids = _list_model_ids(client, api_key=api_key, timeout_seconds=timeout_seconds)
        if model_id not in model_ids:
            available = ", ".join(sorted(model_ids)) or "none"
            raise RuntimeError(
                f"configured model {model_id!r} is not visible from {_models_url(client)}; "
                f"available model ids: {available}"
            )

        result = client.generate(
            model_id=model_id,
            messages=[
                {"role": "system", "content": "You are a local model smoke test. Reply briefly."},
                {"role": "user", "content": "Reply with a short confirmation."},
            ],
            user_query="Reply with a short confirmation.",
            citations=[],
            temperature=0,
            max_output_tokens=16,
        )
        if not result.content.strip():
            raise RuntimeError("OpenAI-compatible model response contained empty assistant content")
    except (ModelClientError, RuntimeError, ValueError) as exc:
        print(f"local model smoke failed: {exc}", file=sys.stderr)
        return 1

    print(
        "local model smoke ok: "
        f"model_id={model_id} models_url={_models_url(client)} content_chars={len(result.content.strip())}"
    )
    return 0


def _required_config(name: str, default: str) -> str:
    value = os.environ.get(name, default).strip()
    if not value:
        raise RuntimeError(f"{name} must be non-empty")
    return value


def _timeout_seconds() -> float:
    raw_timeout = os.environ.get("LOCAL_LLM_TIMEOUT_SECONDS", str(DEFAULT_TIMEOUT_SECONDS)).strip()
    try:
        timeout_seconds = float(raw_timeout)
    except ValueError as exc:
        raise RuntimeError(f"LOCAL_LLM_TIMEOUT_SECONDS must be a positive number, got {raw_timeout!r}") from exc
    if not math.isfinite(timeout_seconds) or timeout_seconds <= 0:
        raise RuntimeError(f"LOCAL_LLM_TIMEOUT_SECONDS must be a positive finite number, got {raw_timeout!r}")
    return timeout_seconds


def _models_url(client: OpenAICompatibleModelClient) -> str:
    chat_url = client._chat_completions_url()
    return chat_url.removesuffix("/chat/completions") + "/models"


def _list_model_ids(
    client: OpenAICompatibleModelClient,
    *,
    api_key: str | None,
    timeout_seconds: float,
) -> set[str]:
    headers = {"Content-Type": "application/json"}
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"

    try:
        response = httpx.get(
            _models_url(client),
            headers=headers,
            timeout=timeout_seconds,
            trust_env=False,
        )
        response.raise_for_status()
    except httpx.HTTPError as exc:
        raise RuntimeError(f"OpenAI-compatible model list request failed: {exc}") from exc

    try:
        payload: Any = response.json()
        models = payload["data"]
        if not isinstance(models, list):
            raise TypeError("data is not a list")
        return {model["id"] for model in models if isinstance(model, dict) and isinstance(model.get("id"), str)}
    except (KeyError, TypeError, ValueError) as exc:
        raise RuntimeError("OpenAI-compatible model list response was missing data[].id") from exc


if __name__ == "__main__":
    raise SystemExit(main())
