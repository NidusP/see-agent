from __future__ import annotations

import argparse
import json
import math
import os
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit, urlunsplit

import httpx


ROOT = Path(__file__).resolve().parents[2]
DEFAULT_BASE_URL = "http://127.0.0.1:11434/v1"
DEFAULT_MODEL_ID = "qwen3:14b"
DEFAULT_PROVIDER = "ollama_native"
DEFAULT_TIMEOUT_SECONDS = 90.0
DEFAULT_REPETITIONS = 3
DEFAULT_REPORT_DIR = ROOT / "data" / "eval-datasets" / "reports" / "provider-stability"
PROBE_VERSION = "provider-stability-v1"


class ProbeFailure(RuntimeError):
    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv)
    run_id = f"provider-stability-{datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')}"
    report: dict[str, Any] = {
        "probe_version": PROBE_VERSION,
        "run_id": run_id,
        "started_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "evaluation_purpose": "provider_stability_probe",
        "quality_eligible": False,
        "fallback_used": False,
        "samples": [],
    }

    try:
        config = _load_config(args)
        report["configuration"] = {
            "provider": config["provider"],
            "model_id": config["model_id"],
            "endpoint": _safe_endpoint(config["base_url"]),
            "timeout_seconds": config["timeout_seconds"],
            "repetitions": config["repetitions"],
            "include_stream": config["include_stream"],
        }
        report["model_list"] = _run_probe(config, report)
    except ProbeFailure as exc:
        report["failure"] = {"code": exc.code, "message": exc.message}
    except (ValueError, OSError) as exc:
        report["failure"] = {"code": "probe_configuration_invalid", "message": str(exc)}

    report["finished_at"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    report["passed"] = "failure" not in report and bool(report.get("model_list", {}).get("target_present")) and not any(
        sample.get("status") != "ok" for sample in report["samples"]
    )
    report["summary"] = _summarize_samples(report["samples"])

    report_path = _write_report(report, args.report_dir)
    print(
        "provider stability probe "
        f"{'passed' if report['passed'] else 'failed'}: "
        f"run_id={run_id} report={report_path} "
        f"samples={len(report['samples'])} "
        f"p95_ms={report['summary'].get('completion_latency_p95_ms')}"
    )
    if not report["passed"] and "failure" in report:
        failure = report["failure"]
        print(f"provider stability probe failure: {failure['code']}: {failure['message']}", file=sys.stderr)
    return 0 if report["passed"] else 1


def _parse_args(argv: list[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Probe the configured real generation provider without product state.")
    parser.add_argument("--repetitions", type=int, default=DEFAULT_REPETITIONS)
    parser.add_argument("--report-dir", type=Path, default=Path(os.environ.get("PROVIDER_STABILITY_REPORT_DIR", DEFAULT_REPORT_DIR)))
    parser.add_argument("--no-stream", action="store_true", help="Skip the single streaming transport check.")
    return parser.parse_args(argv)


def _load_config(args: argparse.Namespace) -> dict[str, Any]:
    provider = os.environ.get("LOCAL_LLM_PROVIDER", DEFAULT_PROVIDER).strip() or DEFAULT_PROVIDER
    if provider not in {"ollama_native", "openai_compatible"}:
        raise ProbeFailure("probe_configuration_invalid", "LOCAL_LLM_PROVIDER must be ollama_native or openai_compatible")

    base_url = os.environ.get("LOCAL_LLM_BASE_URL", DEFAULT_BASE_URL).strip()
    if not base_url:
        raise ProbeFailure("probe_configuration_invalid", "LOCAL_LLM_BASE_URL must be non-empty")
    model_id = os.environ.get("LOCAL_LLM_DEFAULT_MODEL_ID", DEFAULT_MODEL_ID).strip()
    if not model_id:
        raise ProbeFailure("probe_configuration_invalid", "LOCAL_LLM_DEFAULT_MODEL_ID must be non-empty")
    timeout_seconds = _positive_float(os.environ.get("LOCAL_LLM_TIMEOUT_SECONDS", str(DEFAULT_TIMEOUT_SECONDS)), "LOCAL_LLM_TIMEOUT_SECONDS")
    repetitions = args.repetitions
    if repetitions < 1 or repetitions > 20:
        raise ProbeFailure("probe_configuration_invalid", "--repetitions must be between 1 and 20")
    return {
        "provider": provider,
        "base_url": base_url,
        "model_id": model_id,
        "api_key": os.environ.get("LOCAL_LLM_API_KEY", "").strip() or None,
        "timeout_seconds": timeout_seconds,
        "repetitions": repetitions,
        "include_stream": not args.no_stream,
    }


def _positive_float(raw: str, name: str) -> float:
    try:
        value = float(raw)
    except ValueError as exc:
        raise ProbeFailure("probe_configuration_invalid", f"{name} must be a positive number") from exc
    if not math.isfinite(value) or value <= 0:
        raise ProbeFailure("probe_configuration_invalid", f"{name} must be a positive finite number")
    return value


def _run_probe(config: dict[str, Any], report: dict[str, Any], client: httpx.Client | None = None) -> dict[str, Any]:
    timeout = config["timeout_seconds"]
    headers = _headers(config["api_key"])
    models_url = _models_url(config["provider"], config["base_url"])
    started = time.perf_counter()
    owned_client = client is None
    active_client = client or httpx.Client(timeout=timeout, trust_env=False)
    try:
        response = active_client.get(models_url, headers=headers)
        response.raise_for_status()
        model_ids = _extract_model_ids(config["provider"], response.json())
        model_list = {
            "status": "ok",
            "url": _safe_endpoint(models_url),
            "latency_ms": _elapsed_ms(started),
            "target_present": config["model_id"] in model_ids,
            "model_count": len(model_ids),
            "model_ids": sorted(model_ids),
        }
        if not model_list["target_present"]:
            raise ProbeFailure("model_not_found", f"configured model {config['model_id']!r} was not returned by the provider")
        report["model_list"] = model_list
        _run_generation_samples(active_client, config, headers, report)
        return model_list
    except ProbeFailure:
        raise
    except httpx.TimeoutException as exc:
        raise ProbeFailure("model_generation_timeout", "model provider request timed out") from exc
    except httpx.ConnectError as exc:
        raise ProbeFailure("model_provider_unreachable", "model provider could not be reached") from exc
    except httpx.HTTPStatusError as exc:
        raise ProbeFailure("model_provider_rejected", f"model provider returned HTTP {exc.response.status_code}") from exc
    except (httpx.HTTPError, ValueError, TypeError, KeyError) as exc:
        raise ProbeFailure("model_provider_protocol_invalid", "model provider response was invalid") from exc
    finally:
        if owned_client:
            active_client.close()


def _run_generation_samples(client: httpx.Client, config: dict[str, Any], headers: dict[str, str], report: dict[str, Any]) -> None:
    for index in range(config["repetitions"]):
        report["samples"].append(_run_non_stream_sample(client, config, headers, kind="short_text", index=index, json_mode=False))
    for index in range(config["repetitions"]):
        report["samples"].append(_run_non_stream_sample(client, config, headers, kind="structured_json", index=index, json_mode=True))
    if config["include_stream"]:
        report["samples"].append(_run_stream_sample(client, config, headers))


def _run_non_stream_sample(
    client: httpx.Client,
    config: dict[str, Any],
    headers: dict[str, str],
    *,
    kind: str,
    index: int,
    json_mode: bool,
) -> dict[str, Any]:
    payload = _payload(config, _messages(kind), stream=False, json_mode=json_mode)
    started = time.perf_counter()
    try:
        response = client.post(_chat_url(config["provider"], config["base_url"]), headers=headers, json=payload)
        response.raise_for_status()
        content, finish_reason, completion_tokens = _non_stream_content(config["provider"], response.json())
    except httpx.TimeoutException as exc:
        raise ProbeFailure("model_generation_timeout", f"{kind} request {index + 1} timed out") from exc
    except httpx.ConnectError as exc:
        raise ProbeFailure("model_provider_unreachable", f"{kind} request {index + 1} could not connect") from exc
    except httpx.HTTPStatusError as exc:
        raise ProbeFailure("model_provider_rejected", f"{kind} request {index + 1} returned HTTP {exc.response.status_code}") from exc
    except (httpx.HTTPError, KeyError, IndexError, TypeError, ValueError) as exc:
        raise ProbeFailure("model_provider_protocol_invalid", f"{kind} request {index + 1} returned an invalid response") from exc
    if not content.strip():
        raise ProbeFailure("model_empty_response", f"{kind} request {index + 1} returned empty content")
    if json_mode:
        _validate_json_content(content, kind, index)
    return {
        "kind": kind,
        "transport": "non_stream",
        "index": index + 1,
        "status": "ok",
        "completion_latency_ms": _elapsed_ms(started),
        "first_event_latency_ms": None,
        "output_chars": len(content.strip()),
        "completion_tokens": completion_tokens,
        "finish_reason": finish_reason,
        "parsed": True,
    }


def _run_stream_sample(client: httpx.Client, config: dict[str, Any], headers: dict[str, str]) -> dict[str, Any]:
    started = time.perf_counter()
    first_event_at: float | None = None
    content_parts: list[str] = []
    finish_reason = "stop"
    done = False
    try:
        with client.stream("POST", _chat_url(config["provider"], config["base_url"]), headers=headers, json=_payload(config, _messages("short_text"), stream=True, json_mode=False)) as response:
            response.raise_for_status()
            for line in response.iter_lines():
                if not line:
                    continue
                if first_event_at is None:
                    first_event_at = time.perf_counter()
                event = _stream_event(config["provider"], line)
                content_parts.append(event["content"])
                finish_reason = event["finish_reason"] or finish_reason
                done = done or event["done"]
    except httpx.TimeoutException as exc:
        raise ProbeFailure("model_generation_timeout", "stream_text request timed out") from exc
    except httpx.ConnectError as exc:
        raise ProbeFailure("model_provider_unreachable", "stream_text request could not connect") from exc
    except httpx.HTTPStatusError as exc:
        raise ProbeFailure("model_provider_rejected", f"stream_text request returned HTTP {exc.response.status_code}") from exc
    except (httpx.HTTPError, KeyError, IndexError, TypeError, ValueError, json.JSONDecodeError) as exc:
        raise ProbeFailure("model_provider_protocol_invalid", "stream_text response was invalid") from exc
    content = "".join(content_parts).strip()
    if not done or not content:
        raise ProbeFailure("model_empty_response", "stream_text did not produce a complete non-empty response")
    return {
        "kind": "stream_text",
        "transport": "stream",
        "index": 1,
        "status": "ok",
        "completion_latency_ms": _elapsed_ms(started),
        "first_event_latency_ms": _elapsed_ms(started, first_event_at) if first_event_at is not None else None,
        "output_chars": len(content),
        "completion_tokens": None,
        "finish_reason": finish_reason,
        "parsed": True,
    }


def _messages(kind: str) -> list[dict[str, str]]:
    if kind == "structured_json":
        return [
            {"role": "system", "content": "Return only a JSON object with a boolean ready field."},
            {"role": "user", "content": "Return the ready status as JSON."},
        ]
    return [
        {"role": "system", "content": "Reply with one short confirmation. Do not explain."},
        {"role": "user", "content": "Reply READY."},
    ]


def _payload(config: dict[str, Any], messages: list[dict[str, str]], *, stream: bool, json_mode: bool) -> dict[str, Any]:
    payload: dict[str, Any] = {"model": config["model_id"], "messages": messages, "stream": stream}
    if config["provider"] == "ollama_native":
        payload["think"] = False
        payload["options"] = {"temperature": 0, "num_predict": 32}
        if json_mode:
            payload["format"] = {"type": "object", "properties": {"ready": {"type": "boolean"}}, "required": ["ready"]}
    else:
        payload["temperature"] = 0
        payload["max_tokens"] = 32
        if json_mode:
            payload["response_format"] = {"type": "json_object"}
    return payload


def _non_stream_content(provider: str, data: Any) -> tuple[str, str, int | None]:
    if provider == "ollama_native":
        message = data["message"]
        return _content(message.get("content")), str(data.get("done_reason") or "stop"), _as_int(data.get("eval_count"))
    choice = data["choices"][0]
    message = choice["message"]
    usage = data.get("usage") or {}
    return _content(message.get("content")), str(choice.get("finish_reason") or "stop"), _as_int(usage.get("completion_tokens"))


def _stream_event(provider: str, line: str) -> dict[str, Any]:
    if provider == "openai_compatible":
        raw = line[5:].strip() if line.startswith("data:") else line.strip()
        if raw == "[DONE]":
            return {"content": "", "finish_reason": "stop", "done": True}
        data = json.loads(raw)
        choice = (data.get("choices") or [{}])[0]
        delta = choice.get("delta") or {}
        return {"content": _content(delta.get("content")), "finish_reason": choice.get("finish_reason"), "done": False}
    data = json.loads(line)
    message = data.get("message") or {}
    return {"content": _content(message.get("content")), "finish_reason": data.get("done_reason"), "done": bool(data.get("done"))}


def _validate_json_content(content: str, kind: str, index: int) -> None:
    try:
        payload = json.loads(content)
    except json.JSONDecodeError as exc:
        raise ProbeFailure("model_json_invalid", f"{kind} request {index + 1} did not return valid JSON") from exc
    if not isinstance(payload, dict):
        raise ProbeFailure("model_json_invalid", f"{kind} request {index + 1} did not return a JSON object")


def _content(value: object) -> str:
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        return "".join(item.get("text", "") for item in value if isinstance(item, dict) and isinstance(item.get("text"), str))
    return ""


def _as_int(value: object) -> int | None:
    return value if isinstance(value, int) and not isinstance(value, bool) else None


def _headers(api_key: str | None) -> dict[str, str]:
    headers = {"Content-Type": "application/json"}
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"
    return headers


def _models_url(provider: str, base_url: str) -> str:
    if provider == "ollama_native":
        parts = urlsplit(base_url)
        path = parts.path.rstrip("/")
        if path.endswith("/v1"):
            path = path[:-3]
        return urlunsplit((parts.scheme, parts.netloc, f"{path}/api/tags", "", ""))
    base = base_url.rstrip("/")
    return f"{base}/models" if base.endswith("/v1") else f"{base}/v1/models"


def _chat_url(provider: str, base_url: str) -> str:
    if provider == "ollama_native":
        parts = urlsplit(base_url)
        path = parts.path.rstrip("/")
        if path.endswith("/v1"):
            path = path[:-3]
        return urlunsplit((parts.scheme, parts.netloc, f"{path}/api/chat", "", ""))
    base = base_url.rstrip("/")
    return f"{base}/chat/completions" if base.endswith("/v1") else f"{base}/v1/chat/completions"


def _extract_model_ids(provider: str, payload: Any) -> set[str]:
    raw_models = payload.get("models") if provider == "ollama_native" else payload.get("data")
    if not isinstance(raw_models, list):
        raise ProbeFailure("model_provider_protocol_invalid", "model list response did not contain a model list")
    ids: set[str] = set()
    for model in raw_models:
        if not isinstance(model, dict):
            continue
        model_id = model.get("name") if provider == "ollama_native" else model.get("id")
        if isinstance(model_id, str) and model_id.strip():
            ids.add(model_id.strip())
    return ids


def _safe_endpoint(url: str) -> str:
    parts = urlsplit(url)
    return urlunsplit((parts.scheme, parts.netloc, parts.path, "", ""))


def _elapsed_ms(started: float, ended: float | None = None) -> float:
    return round(((ended if ended is not None else time.perf_counter()) - started) * 1000, 3)


def _summarize_samples(samples: list[dict[str, Any]]) -> dict[str, Any]:
    latencies = [sample["completion_latency_ms"] for sample in samples if sample.get("status") == "ok"]
    first_events = [sample["first_event_latency_ms"] for sample in samples if sample.get("first_event_latency_ms") is not None]
    return {
        "sample_count": len(samples),
        "success_count": sum(sample.get("status") == "ok" for sample in samples),
        "failure_count": sum(sample.get("status") != "ok" for sample in samples),
        "completion_latency_min_ms": _percentile(latencies, 0.0),
        "completion_latency_median_ms": _percentile(latencies, 0.5),
        "completion_latency_p95_ms": _percentile(latencies, 0.95),
        "first_event_latency_median_ms": _percentile(first_events, 0.5),
    }


def _percentile(values: list[float], quantile: float) -> float | None:
    if not values:
        return None
    ordered = sorted(values)
    if quantile == 0:
        return ordered[0]
    return ordered[min(len(ordered) - 1, math.ceil(len(ordered) * quantile) - 1)]


def _write_report(report: dict[str, Any], report_dir: Path) -> Path:
    report_dir.mkdir(parents=True, exist_ok=True)
    path = report_dir / f"{report['run_id']}.json"
    path.write_text(json.dumps(report, ensure_ascii=True, indent=2) + "\n", encoding="utf-8")
    return path


if __name__ == "__main__":
    raise SystemExit(main())
