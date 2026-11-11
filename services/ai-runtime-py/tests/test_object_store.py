from __future__ import annotations

from datetime import datetime, timezone

import httpx
import pytest

from app.adapters.object_store import S3ObjectResolver
from app.parsers.base import ParserError


def test_s3_object_resolver_downloads_to_local_path() -> None:
    seen: list[tuple[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append((request.method, request.url.path))
        assert request.headers["authorization"].startswith("AWS4-HMAC-SHA256 Credential=see_agent/")
        assert request.headers["x-amz-date"] == "20260703T120000Z"
        if request.method == "GET" and request.url.path == "/knowledge-docs/kb/file_123/policy.txt":
            return httpx.Response(200, content=b"policy body")
        return httpx.Response(500, text=f"unexpected {request.method} {request.url.path}")

    resolver = S3ObjectResolver(
        "http://object-store.test",
        access_key="see_agent",
        secret_key="see_agent_dev",
        client=httpx.Client(transport=httpx.MockTransport(handler), trust_env=False),
        now=lambda: datetime(2026, 7, 3, 12, 0, 0, tzinfo=timezone.utc),
    )

    with resolver.local_path("s3://knowledge-docs/kb/file_123/policy.txt", "policy.txt") as local_path:
        assert local_path.name == "policy.txt"
        assert local_path.read_text(encoding="utf-8") == "policy body"

    assert seen == [("GET", "/knowledge-docs/kb/file_123/policy.txt")]
    assert not local_path.exists()


def test_s3_object_resolver_requires_endpoint_and_credentials() -> None:
    resolver = S3ObjectResolver("", access_key="", secret_key="")

    with pytest.raises(ParserError, match="object store endpoint and credentials"):
        with resolver.local_path("s3://knowledge-docs/kb/file_123/policy.txt", "policy.txt"):
            pass
