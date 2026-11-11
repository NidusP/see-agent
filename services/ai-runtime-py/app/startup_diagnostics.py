from __future__ import annotations

import logging
import os
from collections.abc import Mapping
from dataclasses import dataclass
from urllib.parse import urlsplit, urlunsplit

from app.retrieval_strategies import resolve_active_strategy


@dataclass(frozen=True)
class StartupDiagnostic:
    level: int
    message: str


def runtime_startup_diagnostics(
    environment: Mapping[str, str] | None = None,
) -> tuple[StartupDiagnostic, ...]:
    environment = os.environ if environment is None else environment
    strategy = resolve_active_strategy(environment)
    qdrant_url = environment.get("QDRANT_URL", "").strip()

    diagnostics = [
        StartupDiagnostic(logging.INFO, f"runtime startup retrieval strategy={strategy}"),
    ]

    if qdrant_url:
        default_collection = "see_agent_dense_qwen_chunks_v1"
        collection = environment.get("QDRANT_COLLECTION", default_collection).strip() or default_collection
        diagnostics.append(
            StartupDiagnostic(
                logging.INFO,
                "runtime startup vector_store=qdrant "
                f"endpoint={sanitize_endpoint(qdrant_url)} collection={collection} status=initialized",
            )
        )
    else:
        diagnostics.append(
            StartupDiagnostic(logging.WARNING, "runtime startup vector_store=memory endpoint=none status=ephemeral")
        )

    diagnostics.append(
        StartupDiagnostic(
            logging.INFO,
            "runtime startup embedding provider=openai_compatible "
            f"endpoint={sanitize_endpoint(environment.get('EMBEDDING_BASE_URL', ''))} "
            f"model={environment.get('EMBEDDING_MODEL', '').strip() or 'unset'} "
            f"dimensions={environment.get('EMBEDDING_DIMENSIONS', '').strip() or 'unset'}",
        )
    )

    model_endpoint = environment.get("LOCAL_LLM_BASE_URL", "").strip()
    model_hint = environment.get("LOCAL_LLM_DEFAULT_MODEL_ID", "").strip() or "unset"
    model_provider = environment.get("LOCAL_LLM_PROVIDER", "openai_compatible").strip() or "openai_compatible"
    if not model_endpoint:
        diagnostics.append(
            StartupDiagnostic(
                logging.WARNING,
                "runtime startup generation provider=unavailable endpoint=none "
                f"default_model_hint={model_hint} status=disabled",
            )
        )
    else:
        diagnostics.append(
            StartupDiagnostic(
                logging.INFO,
                f"runtime startup generation provider={model_provider} "
                f"endpoint={sanitize_endpoint(model_endpoint)} default_model_hint={model_hint} "
                f"thinking={'disabled' if model_provider == 'ollama_native' else 'provider_default'} status=configured",
            )
        )

    object_endpoint = environment.get("OBJECT_STORE_ENDPOINT", "").strip()
    if object_endpoint:
        diagnostics.append(
            StartupDiagnostic(
                logging.INFO,
                "runtime startup object_resolver=s3_compatible "
                f"endpoint={sanitize_endpoint(object_endpoint)} "
                f"bucket={environment.get('OBJECT_STORE_BUCKET', '').strip() or 'request_uri'}",
            )
        )
    else:
        diagnostics.append(
            StartupDiagnostic(logging.INFO, "runtime startup object_resolver=local_file endpoint=none")
        )

    return tuple(diagnostics)


def log_runtime_startup_diagnostics(
    logger: logging.Logger,
    environment: Mapping[str, str] | None = None,
) -> None:
    for diagnostic in runtime_startup_diagnostics(environment):
        logger.log(diagnostic.level, diagnostic.message)


def sanitize_endpoint(value: str) -> str:
    value = value.strip()
    if not value:
        return "none"
    try:
        parsed = urlsplit(value)
    except ValueError:
        return "invalid"
    if not parsed.scheme or not parsed.hostname:
        return "invalid"

    host = f"[{parsed.hostname}]" if ":" in parsed.hostname else parsed.hostname
    try:
        port = f":{parsed.port}" if parsed.port is not None else ""
    except ValueError:
        return "invalid"
    return urlunsplit((parsed.scheme, f"{host}{port}", parsed.path.rstrip("/"), "", ""))
