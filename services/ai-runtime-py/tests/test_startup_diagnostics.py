from __future__ import annotations

import logging

from app.startup_diagnostics import runtime_startup_diagnostics, sanitize_endpoint


def test_runtime_startup_diagnostics_report_real_providers_without_secrets() -> None:
    environment = {
        "RUNTIME_RETRIEVAL_STRATEGY": "native_dense_v1",
        "QDRANT_URL": "http://user:qdrant-secret@127.0.0.1:16333?token=hidden",
        "QDRANT_COLLECTION": "dense_dev",
        "EMBEDDING_BASE_URL": "http://embed-user:embed-secret@127.0.0.1:11434/v1?key=hidden",
        "EMBEDDING_MODEL": "qwen3-embedding:0.6b",
        "EMBEDDING_DIMENSIONS": "1024",
        "LOCAL_LLM_BASE_URL": "http://model-user:model-secret@127.0.0.1:11434/v1?key=hidden",
        "LOCAL_LLM_DEFAULT_MODEL_ID": "llama3.2:latest",
        "OBJECT_STORE_ENDPOINT": "http://minio-user:minio-secret@127.0.0.1:19000?token=hidden",
        "OBJECT_STORE_BUCKET": "knowledge-docs",
    }

    diagnostics = runtime_startup_diagnostics(environment)
    output = "\n".join(item.message for item in diagnostics)

    assert "strategy=native_dense_v1" in output
    assert "collection=dense_dev" in output
    assert "model=qwen3-embedding:0.6b" in output
    assert "default_model_hint=llama3.2:latest" in output
    assert "status=configured" in output
    assert "bucket=knowledge-docs" in output
    assert "secret" not in output
    assert "hidden" not in output
    assert all(item.level == logging.INFO for item in diagnostics)


def test_runtime_startup_diagnostics_warn_when_generation_provider_is_unavailable() -> None:
    diagnostics = runtime_startup_diagnostics({"QDRANT_URL": "http://127.0.0.1:16333"})
    generation = next(item for item in diagnostics if "generation provider=" in item.message)

    assert generation.level == logging.WARNING
    assert "provider=unavailable" in generation.message
    assert "status=disabled" in generation.message


def test_sanitize_endpoint_rejects_non_urls_and_strips_credentials_query_and_fragment() -> None:
    assert sanitize_endpoint("not-a-url") == "invalid"
    assert (
        sanitize_endpoint("https://user:password@example.test:8443/v1/?api_key=secret#fragment")
        == "https://example.test:8443/v1"
    )
