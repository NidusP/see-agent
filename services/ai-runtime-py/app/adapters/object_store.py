from __future__ import annotations

import hashlib
import hmac
import os
import tempfile
from collections.abc import Callable, Iterator
from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import quote, urlparse

import httpx

from app.parsers.base import ParserError, require_local_path

EMPTY_PAYLOAD_HASH = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"


class ObjectResolver:
    @contextmanager
    def local_path(self, object_uri: str, filename: str) -> Iterator[Path]:
        yield require_local_path(object_uri)


class S3ObjectResolver(ObjectResolver):
    def __init__(
        self,
        endpoint: str,
        *,
        access_key: str,
        secret_key: str,
        region: str = "us-east-1",
        timeout_seconds: float = 10.0,
        client: httpx.Client | None = None,
        now: Callable[[], datetime] | None = None,
    ) -> None:
        self._endpoint = endpoint.rstrip("/")
        self._access_key = access_key.strip()
        self._secret_key = secret_key.strip()
        self._region = region.strip() or "us-east-1"
        self._client = client or httpx.Client(timeout=timeout_seconds, trust_env=False)
        self._now = now or (lambda: datetime.now(timezone.utc))

    @contextmanager
    def local_path(self, object_uri: str, filename: str) -> Iterator[Path]:
        parsed = urlparse(object_uri)
        if parsed.scheme != "s3":
            yield require_local_path(object_uri)
            return

        if not parsed.netloc or not parsed.path.strip("/"):
            raise ParserError("s3 object_uri must include bucket and object key")
        if not self._endpoint or not self._access_key or not self._secret_key:
            raise ParserError("object store endpoint and credentials are required for s3 object_uri")

        bucket = parsed.netloc
        key = parsed.path.lstrip("/")
        body = self._download(bucket, key)
        with tempfile.TemporaryDirectory(prefix="see-agent-runtime-object-") as temp_dir:
            target = Path(temp_dir) / _safe_filename(filename or Path(key).name)
            target.write_bytes(body)
            yield target

    def _download(self, bucket: str, key: str) -> bytes:
        url = self._object_url(bucket, key)
        headers = self._signed_headers("GET", url)
        response = self._client.get(url, headers=headers)
        if response.status_code < 200 or response.status_code >= 300:
            message = response.text.strip()[:1024]
            raise ParserError(f"download object failed with status {response.status_code}: {message}")
        return response.content

    def _object_url(self, bucket: str, key: str) -> str:
        parsed = urlparse(self._endpoint)
        if not parsed.scheme or not parsed.netloc:
            raise ParserError("object store endpoint must include scheme and host")
        prefix = parsed.path.strip("/")
        parts = [part for part in [prefix, bucket, *key.split("/")] if part]
        escaped_path = "/" + "/".join(quote(part, safe="") for part in parts)
        return f"{parsed.scheme}://{parsed.netloc}{escaped_path}"

    def _signed_headers(self, method: str, url: str) -> dict[str, str]:
        parsed = urlparse(url)
        now = self._now().astimezone(timezone.utc)
        amz_date = now.strftime("%Y%m%dT%H%M%SZ")
        date_stamp = now.strftime("%Y%m%d")
        headers = {
            "host": parsed.netloc,
            "x-amz-content-sha256": EMPTY_PAYLOAD_HASH,
            "x-amz-date": amz_date,
        }
        signed_headers = ";".join(sorted(headers))
        canonical_headers = "".join(f"{name}:{headers[name]}\n" for name in sorted(headers))
        canonical_request = "\n".join(
            [
                method,
                parsed.path or "/",
                parsed.query,
                canonical_headers,
                signed_headers,
                EMPTY_PAYLOAD_HASH,
            ]
        )
        scope = f"{date_stamp}/{self._region}/s3/aws4_request"
        string_to_sign = "\n".join(
            [
                "AWS4-HMAC-SHA256",
                amz_date,
                scope,
                hashlib.sha256(canonical_request.encode("utf-8")).hexdigest(),
            ]
        )
        signature = hmac.new(
            _signing_key(self._secret_key, date_stamp, self._region),
            string_to_sign.encode("utf-8"),
            hashlib.sha256,
        ).hexdigest()
        return {
            "Authorization": (
                f"AWS4-HMAC-SHA256 Credential={self._access_key}/{scope}, "
                f"SignedHeaders={signed_headers}, Signature={signature}"
            ),
            "X-Amz-Content-Sha256": EMPTY_PAYLOAD_HASH,
            "X-Amz-Date": amz_date,
        }


def build_default_object_resolver() -> ObjectResolver:
    endpoint = os.getenv("OBJECT_STORE_ENDPOINT", "").strip()
    if not endpoint:
        return ObjectResolver()

    access_key = _first_non_empty(
        os.getenv("OBJECT_STORE_ACCESS_KEY", ""),
        os.getenv("MINIO_ROOT_USER", ""),
        "see_agent",
    )
    secret_key = _first_non_empty(
        os.getenv("OBJECT_STORE_SECRET_KEY", ""),
        os.getenv("MINIO_ROOT_PASSWORD", ""),
        "see_agent_dev",
    )
    region = _first_non_empty(os.getenv("OBJECT_STORE_REGION", ""), "us-east-1")
    timeout_seconds = float(os.getenv("OBJECT_STORE_TIMEOUT_SECONDS", "10"))
    return S3ObjectResolver(
        endpoint,
        access_key=access_key,
        secret_key=secret_key,
        region=region,
        timeout_seconds=timeout_seconds,
    )


def _signing_key(secret_key: str, date_stamp: str, region: str) -> bytes:
    date_key = hmac.new(("AWS4" + secret_key).encode("utf-8"), date_stamp.encode("utf-8"), hashlib.sha256).digest()
    region_key = hmac.new(date_key, region.encode("utf-8"), hashlib.sha256).digest()
    service_key = hmac.new(region_key, b"s3", hashlib.sha256).digest()
    return hmac.new(service_key, b"aws4_request", hashlib.sha256).digest()


def _first_non_empty(*values: str) -> str:
    for value in values:
        if value.strip():
            return value.strip()
    return ""


def _safe_filename(filename: str) -> str:
    name = Path(filename.strip()).name
    if not name or name in {".", "/"}:
        return "upload.bin"
    cleaned = "".join(char if char.isalnum() or char in {".", "-", "_"} else "-" for char in name)
    return cleaned.strip(".-") or "upload.bin"
