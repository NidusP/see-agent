from __future__ import annotations

import logging
import os
import json
from collections.abc import Iterator
from dataclasses import dataclass
from typing import Any, Protocol
from urllib.parse import urlparse, urlsplit, urlunsplit

import httpx

from app.api.schemas import RetrievedSource
from app.parsers.common import count_tokens
from app.startup_diagnostics import sanitize_endpoint


request_logger = logging.getLogger("uvicorn.error")


@dataclass(frozen=True)
class ModelClientResult:
    content: str
    finish_reason: str
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int


class ModelClient(Protocol):
    provider_name: str

    def generate(
        self,
        *,
        model_id: str,
        messages: list[dict[str, str]],
        user_query: str,
        citations: list[RetrievedSource],
        temperature: float | None = None,
        top_p: float | None = None,
        max_output_tokens: int | None = None,
        json_mode: bool = False,
        json_schema: dict[str, Any] | None = None,
    ) -> ModelClientResult:
        ...

    def stream_generate(
        self,
        *,
        model_id: str,
        messages: list[dict[str, str]],
        user_query: str,
        citations: list[RetrievedSource],
        temperature: float | None = None,
        top_p: float | None = None,
        max_output_tokens: int | None = None,
    ) -> Iterator[str]:
        ...


class ModelClientError(RuntimeError):
    """Raised when the configured model backend cannot produce a response."""

    code = "model_request_failed"
    retryable = True


class ModelProviderUnavailable(ModelClientError):
    """Raised when Runtime has no configured generation provider."""

    code = "model_unavailable"


class ModelGenerationTimeout(ModelClientError):
    code = "model_generation_timeout"


class ModelProviderUnreachable(ModelClientError):
    code = "model_provider_unreachable"


class ModelProviderRejected(ModelClientError):
    code = "model_provider_rejected"


class OpenAICompatibleModelClient:
    provider_name = "openai_compatible"

    def __init__(self, base_url: str, *, api_key: str | None = None, timeout_seconds: float = 30.0) -> None:
        self._base_url = base_url.rstrip("/")
        self._api_key = api_key or None
        self._timeout_seconds = timeout_seconds

    def generate(
        self,
        *,
        model_id: str,
        messages: list[dict[str, str]],
        user_query: str,
        citations: list[RetrievedSource],
        temperature: float | None = None,
        top_p: float | None = None,
        max_output_tokens: int | None = None,
        json_mode: bool = False,
        json_schema: dict[str, Any] | None = None,
    ) -> ModelClientResult:
        request_logger.info(
            "generation request provider=openai_compatible endpoint=%s model=%s stream=false",
            sanitize_endpoint(self._base_url),
            model_id,
        )
        payload: dict[str, object] = {
            "model": model_id,
            "messages": messages,
            "stream": False,
        }
        if temperature is not None:
            payload["temperature"] = temperature
        if top_p is not None:
            payload["top_p"] = top_p
        if max_output_tokens is not None:
            payload["max_tokens"] = max_output_tokens
        if json_mode:
            payload["response_format"] = {"type": "json_object"}

        headers = {"Content-Type": "application/json"}
        if self._api_key:
            headers["Authorization"] = f"Bearer {self._api_key}"

        try:
            with self._http_client() as client:
                response = client.post(
                    self._chat_completions_url(),
                    json=payload,
                    headers=headers,
                )
                response.raise_for_status()
        except httpx.HTTPError as exc:
            raise _model_http_error("OpenAI-compatible model request", exc) from exc

        try:
            data = response.json()
            choice = data["choices"][0]
            content = _coerce_message_content(choice["message"]["content"])
        except (KeyError, IndexError, TypeError, ValueError) as exc:
            raise ModelClientError("OpenAI-compatible model response was missing choices[0].message.content") from exc

        if not content:
            raise ModelClientError("OpenAI-compatible model response was empty")

        finish_reason = _normalize_finish_reason(choice.get("finish_reason"))
        usage = data.get("usage") or {}
        prompt_tokens = int(usage.get("prompt_tokens") or _count_message_tokens(messages))
        completion_tokens = int(usage.get("completion_tokens") or count_tokens(content))
        total_tokens = int(usage.get("total_tokens") or prompt_tokens + completion_tokens)

        return ModelClientResult(
            content=content.strip(),
            finish_reason=finish_reason,
            prompt_tokens=prompt_tokens,
            completion_tokens=completion_tokens,
            total_tokens=total_tokens,
        )

    def _chat_completions_url(self) -> str:
        if self._base_url.endswith("/chat/completions"):
            return self._base_url
        if self._base_url.endswith("/v1"):
            return f"{self._base_url}/chat/completions"
        return f"{self._base_url}/v1/chat/completions"

    def stream_generate(
        self,
        *,
        model_id: str,
        messages: list[dict[str, str]],
        user_query: str,
        citations: list[RetrievedSource],
        temperature: float | None = None,
        top_p: float | None = None,
        max_output_tokens: int | None = None,
    ) -> Iterator[str]:
        request_logger.info(
            "generation request provider=openai_compatible endpoint=%s model=%s stream=true",
            sanitize_endpoint(self._base_url),
            model_id,
        )
        payload: dict[str, object] = {
            "model": model_id,
            "messages": messages,
            "stream": True,
        }
        if temperature is not None:
            payload["temperature"] = temperature
        if top_p is not None:
            payload["top_p"] = top_p
        if max_output_tokens is not None:
            payload["max_tokens"] = max_output_tokens

        headers = {"Content-Type": "application/json"}
        if self._api_key:
            headers["Authorization"] = f"Bearer {self._api_key}"

        try:
            with self._http_client() as client:
                with client.stream(
                    "POST",
                    self._chat_completions_url(),
                    json=payload,
                    headers=headers,
                ) as response:
                    response.raise_for_status()
                    for line in response.iter_lines():
                        if not line:
                            continue
                        if line.startswith("data:"):
                            line = line.removeprefix("data:").strip()
                        if line == "[DONE]":
                            break
                        try:
                            data = json.loads(line)
                            choice = data["choices"][0]
                            delta = choice.get("delta") or {}
                            content = _coerce_message_content(delta.get("content", ""))
                        except (KeyError, IndexError, TypeError, ValueError, json.JSONDecodeError) as exc:
                            raise ModelClientError("OpenAI-compatible stream chunk was malformed") from exc
                        if content:
                            yield content
        except httpx.HTTPError as exc:
            raise _model_http_error("OpenAI-compatible model stream", exc) from exc

    def _http_client(self) -> httpx.Client:
        return httpx.Client(
            timeout=self._timeout_seconds,
            trust_env=_should_trust_environment(self._base_url),
        )


class OllamaNativeModelClient:
    """Use Ollama's native chat contract when adapter-specific facts matter."""

    provider_name = "ollama_native"

    def __init__(self, base_url: str, *, timeout_seconds: float = 30.0) -> None:
        self._base_url = base_url.rstrip("/")
        self._timeout_seconds = timeout_seconds

    def generate(
        self,
        *,
        model_id: str,
        messages: list[dict[str, str]],
        user_query: str,
        citations: list[RetrievedSource],
        temperature: float | None = None,
        top_p: float | None = None,
        max_output_tokens: int | None = None,
        json_mode: bool = False,
        json_schema: dict[str, Any] | None = None,
    ) -> ModelClientResult:
        request_logger.info(
            "generation request provider=ollama_native endpoint=%s model=%s stream=false think=false",
            sanitize_endpoint(self._ollama_chat_url()),
            model_id,
        )
        payload = self._payload(
            model_id=model_id,
            messages=messages,
            stream=False,
            temperature=temperature,
            top_p=top_p,
            max_output_tokens=max_output_tokens,
            json_mode=json_mode,
            json_schema=json_schema,
        )
        try:
            with self._http_client() as client:
                response = client.post(self._ollama_chat_url(), json=payload)
                response.raise_for_status()
        except httpx.HTTPError as exc:
            raise _model_http_error("Ollama native model request", exc) from exc
        try:
            data = response.json()
            content = _coerce_message_content(data["message"]["content"])
        except (KeyError, TypeError, ValueError) as exc:
            raise ModelClientError("Ollama native model response was missing message.content") from exc
        if not content:
            raise ModelClientError("Ollama native model response was empty")
        prompt_tokens = int(data.get("prompt_eval_count") or _count_message_tokens(messages))
        completion_tokens = int(data.get("eval_count") or count_tokens(content))
        return ModelClientResult(
            content=content.strip(),
            finish_reason=_normalize_finish_reason(data.get("done_reason")),
            prompt_tokens=prompt_tokens,
            completion_tokens=completion_tokens,
            total_tokens=prompt_tokens + completion_tokens,
        )

    def stream_generate(
        self,
        *,
        model_id: str,
        messages: list[dict[str, str]],
        user_query: str,
        citations: list[RetrievedSource],
        temperature: float | None = None,
        top_p: float | None = None,
        max_output_tokens: int | None = None,
    ) -> Iterator[str]:
        request_logger.info(
            "generation request provider=ollama_native endpoint=%s model=%s stream=true think=false",
            sanitize_endpoint(self._ollama_chat_url()),
            model_id,
        )
        payload = self._payload(
            model_id=model_id,
            messages=messages,
            stream=True,
            temperature=temperature,
            top_p=top_p,
            max_output_tokens=max_output_tokens,
        )
        try:
            with self._http_client() as client:
                with client.stream("POST", self._ollama_chat_url(), json=payload) as response:
                    response.raise_for_status()
                    for line in response.iter_lines():
                        if not line:
                            continue
                        try:
                            data = json.loads(line)
                            content = _coerce_message_content((data.get("message") or {}).get("content", ""))
                        except (TypeError, ValueError, json.JSONDecodeError) as exc:
                            raise ModelClientError("Ollama native stream chunk was malformed") from exc
                        if content:
                            yield content
        except httpx.HTTPError as exc:
            raise _model_http_error("Ollama native model stream", exc) from exc

    def _payload(
        self,
        *,
        model_id: str,
        messages: list[dict[str, str]],
        stream: bool,
        temperature: float | None,
        top_p: float | None,
        max_output_tokens: int | None,
        json_mode: bool = False,
        json_schema: dict[str, Any] | None = None,
    ) -> dict[str, object]:
        options: dict[str, object] = {}
        if temperature is not None:
            options["temperature"] = temperature
        if top_p is not None:
            options["top_p"] = top_p
        if max_output_tokens is not None:
            options["num_predict"] = max_output_tokens
        payload: dict[str, object] = {"model": model_id, "messages": messages, "stream": stream, "think": False}
        if json_mode:
            payload["format"] = json_schema or "json"
        if options:
            payload["options"] = options
        return payload

    def _ollama_chat_url(self) -> str:
        parsed = urlsplit(self._base_url)
        path = parsed.path.rstrip("/")
        if path.endswith("/v1"):
            path = path[:-3]
        return urlunsplit((parsed.scheme, parsed.netloc, f"{path}/api/chat", "", ""))

    def _http_client(self) -> httpx.Client:
        return httpx.Client(timeout=self._timeout_seconds, trust_env=_should_trust_environment(self._base_url))


class UnavailableModelClient:
    provider_name = "unavailable"

    def generate(
        self,
        *,
        model_id: str,
        messages: list[dict[str, str]],
        user_query: str,
        citations: list[RetrievedSource],
        temperature: float | None = None,
        top_p: float | None = None,
        max_output_tokens: int | None = None,
        json_mode: bool = False,
        json_schema: dict[str, Any] | None = None,
    ) -> ModelClientResult:
        request_logger.error(
            "generation request provider=unavailable requested_model=%s reason=endpoint_not_configured",
            model_id,
        )
        raise ModelProviderUnavailable("LOCAL_LLM_BASE_URL is required for knowledge answer generation")

    def stream_generate(
        self,
        *,
        model_id: str,
        messages: list[dict[str, str]],
        user_query: str,
        citations: list[RetrievedSource],
        temperature: float | None = None,
        top_p: float | None = None,
        max_output_tokens: int | None = None,
    ) -> Iterator[str]:
        request_logger.error(
            "generation request provider=unavailable requested_model=%s reason=endpoint_not_configured stream=true",
            model_id,
        )
        raise ModelProviderUnavailable("LOCAL_LLM_BASE_URL is required for knowledge answer generation")
        yield  # pragma: no cover - keeps this method an Iterator implementation


def build_default_model_client() -> ModelClient:
    base_url = os.getenv("LOCAL_LLM_BASE_URL", "").strip()
    api_key = os.getenv("LOCAL_LLM_API_KEY", "").strip()
    timeout_seconds = float(os.getenv("LOCAL_LLM_TIMEOUT_SECONDS", "30"))
    if not base_url:
        return UnavailableModelClient()
    provider = os.getenv("LOCAL_LLM_PROVIDER", "openai_compatible").strip() or "openai_compatible"
    if provider == "ollama_native":
        return OllamaNativeModelClient(base_url, timeout_seconds=timeout_seconds)
    if provider != "openai_compatible":
        raise ValueError("LOCAL_LLM_PROVIDER must be openai_compatible or ollama_native")
    return OpenAICompatibleModelClient(
        base_url,
        api_key=api_key or None,
        timeout_seconds=timeout_seconds,
    )


def _should_trust_environment(base_url: str) -> bool:
    hostname = urlparse(base_url).hostname
    return hostname not in {"localhost", "127.0.0.1", "::1"}


def _coerce_message_content(content: object) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts: list[str] = []
        for item in content:
            if isinstance(item, dict) and item.get("type") == "text" and isinstance(item.get("text"), str):
                parts.append(item["text"])
        return "\n".join(part for part in parts if part)
    return ""


def _model_http_error(operation: str, exc: httpx.HTTPError) -> ModelClientError:
    if isinstance(exc, httpx.TimeoutException):
        return ModelGenerationTimeout(f"{operation} timed out")
    if isinstance(exc, httpx.ConnectError):
        return ModelProviderUnreachable(f"{operation} could not connect")
    if isinstance(exc, httpx.HTTPStatusError):
        return ModelProviderRejected(f"{operation} returned HTTP {exc.response.status_code}")
    return ModelClientError(f"{operation} failed: {exc}")


def _count_message_tokens(messages: list[dict[str, str]]) -> int:
    return sum(count_tokens(message.get("content", "")) for message in messages)




def _normalize_finish_reason(raw_reason: object) -> str:
    if raw_reason in {"stop", "length", "refusal", "error"}:
        return str(raw_reason)
    if raw_reason in {"content_filter", "tool_calls"}:
        return "stop"
    return "stop"
