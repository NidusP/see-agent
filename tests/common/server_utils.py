from __future__ import annotations

import contextlib
import json
import os
import socket
import subprocess
import tempfile
import time
import uuid
from http.cookiejar import CookieJar
from collections.abc import Iterator
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from urllib import error, request

_DIRECT_OPENER = request.build_opener(request.ProxyHandler({}))


def find_free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        sock.listen(1)
        return int(sock.getsockname()[1])


def wait_for_url(url: str, *, timeout_seconds: float = 30.0) -> None:
    deadline = time.time() + timeout_seconds
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            with _DIRECT_OPENER.open(url, timeout=2) as response:
                if 200 <= response.status < 500:
                    return
        except Exception as exc:  # pragma: no cover - exercised in failure cases
            last_error = exc
        time.sleep(0.25)
    raise RuntimeError(f"Timed out waiting for {url}: {last_error}")


@dataclass
class ManagedProcess:
    command: list[str]
    cwd: Path
    env: dict[str, str] | None = None
    startup_url: str | None = None
    startup_timeout_seconds: float = 45.0

    def __post_init__(self) -> None:
        self._log_file = tempfile.TemporaryFile(mode="w+", encoding="utf-8")
        self._process: subprocess.Popen[str] | None = None

    def start(self) -> None:
        child_env = os.environ.copy()
        if self.env:
            child_env.update(self.env)

        self._process = subprocess.Popen(
            self.command,
            cwd=self.cwd,
            env=child_env,
            stdout=self._log_file,
            stderr=subprocess.STDOUT,
            text=True,
        )
        if self.startup_url:
            try:
                wait_for_url(self.startup_url, timeout_seconds=self.startup_timeout_seconds)
            except Exception as exc:  # pragma: no cover - failure helper
                self.stop()
                raise RuntimeError(f"Failed to start {' '.join(self.command)}: {self.read_logs()}") from exc

    def stop(self) -> None:
        if self._process is None:
            return
        if self._process.poll() is None:
            self._process.terminate()
            try:
                self._process.wait(timeout=10)
            except subprocess.TimeoutExpired:  # pragma: no cover - unlikely
                self._process.kill()
                self._process.wait(timeout=5)
        self._process = None

    def read_logs(self) -> str:
        self._log_file.flush()
        self._log_file.seek(0)
        return self._log_file.read()


@contextlib.contextmanager
def running_process(
    command: list[str],
    *,
    cwd: Path,
    env: dict[str, str] | None = None,
    startup_url: str | None = None,
    startup_timeout_seconds: float = 45.0,
) -> Iterator[ManagedProcess]:
    process = ManagedProcess(
        command=command,
        cwd=cwd,
        env=env,
        startup_url=startup_url,
        startup_timeout_seconds=startup_timeout_seconds,
    )
    process.start()
    try:
        yield process
    finally:
        process.stop()


def fetch_json(url: str, *, method: str = "GET", payload: Any | None = None, headers: dict[str, str] | None = None) -> tuple[int, Any]:
    return _fetch_json_with_opener(_DIRECT_OPENER, url, method=method, payload=payload, headers=headers)


def fetch_text(
    url: str,
    *,
    method: str = "GET",
    payload: Any | None = None,
    headers: dict[str, str] | None = None,
) -> tuple[int, str]:
    return _fetch_text_with_opener(_DIRECT_OPENER, url, method=method, payload=payload, headers=headers)


def build_multipart_form(*, fields: dict[str, str] | None = None, files: dict[str, tuple[str, bytes, str]] | None = None) -> tuple[bytes, str]:
    boundary = f"see-agent-{uuid.uuid4().hex}"
    lines: list[bytes] = []

    for name, value in (fields or {}).items():
        lines.extend(
            [
                f"--{boundary}".encode("utf-8"),
                f'Content-Disposition: form-data; name="{name}"'.encode("utf-8"),
                b"",
                value.encode("utf-8"),
            ]
        )

    for name, (filename, content, mime_type) in (files or {}).items():
        lines.extend(
            [
                f"--{boundary}".encode("utf-8"),
                f'Content-Disposition: form-data; name="{name}"; filename="{filename}"'.encode("utf-8"),
                f"Content-Type: {mime_type}".encode("utf-8"),
                b"",
                content,
            ]
        )

    lines.append(f"--{boundary}--".encode("utf-8"))
    lines.append(b"")
    body = b"\r\n".join(lines)
    return body, f"multipart/form-data; boundary={boundary}"


def fetch_multipart_json(url: str, *, fields: dict[str, str] | None = None, files: dict[str, tuple[str, bytes, str]] | None = None) -> tuple[int, Any]:
    return _fetch_multipart_json_with_opener(_DIRECT_OPENER, url, fields=fields, files=files)


class HttpSession:
    def __init__(self) -> None:
        self._opener = request.build_opener(request.ProxyHandler({}), request.HTTPCookieProcessor(CookieJar()))

    def fetch_json(
        self,
        url: str,
        *,
        method: str = "GET",
        payload: Any | None = None,
        headers: dict[str, str] | None = None,
    ) -> tuple[int, Any]:
        return _fetch_json_with_opener(self._opener, url, method=method, payload=payload, headers=headers)

    def fetch_text(
        self,
        url: str,
        *,
        method: str = "GET",
        payload: Any | None = None,
        headers: dict[str, str] | None = None,
    ) -> tuple[int, str]:
        return _fetch_text_with_opener(self._opener, url, method=method, payload=payload, headers=headers)

    def fetch_multipart_json(
        self,
        url: str,
        *,
        fields: dict[str, str] | None = None,
        files: dict[str, tuple[str, bytes, str]] | None = None,
    ) -> tuple[int, Any]:
        return _fetch_multipart_json_with_opener(self._opener, url, fields=fields, files=files)


def login_session(base_url: str, *, username: str = "admin", password: str = "admin123") -> HttpSession:
    session = HttpSession()
    status, payload = session.fetch_json(
        f"{base_url}/api/auth/login",
        method="POST",
        payload={"username": username, "password": password},
    )
    if status != 200:
        raise RuntimeError(f"Failed to login as {username}: status={status}, payload={payload}")
    return session


def _fetch_json_with_opener(
    opener: request.OpenerDirector,
    url: str,
    *,
    method: str = "GET",
    payload: Any | None = None,
    headers: dict[str, str] | None = None,
) -> tuple[int, Any]:
    body = None
    request_headers = {"Accept": "application/json"}
    if headers:
        request_headers.update(headers)
    if payload is not None:
        body = json.dumps(payload).encode("utf-8")
        request_headers["Content-Type"] = "application/json"

    req = request.Request(url, method=method, data=body, headers=request_headers)
    try:
        with opener.open(req, timeout=10) as response:
            return response.status, json.loads(response.read().decode("utf-8"))
    except error.HTTPError as exc:
        return exc.code, json.loads(exc.read().decode("utf-8"))


def _fetch_text_with_opener(
    opener: request.OpenerDirector,
    url: str,
    *,
    method: str = "GET",
    payload: Any | None = None,
    headers: dict[str, str] | None = None,
) -> tuple[int, str]:
    body = None
    request_headers = dict(headers or {})
    if payload is not None:
        body = json.dumps(payload).encode("utf-8")
        request_headers.setdefault("Content-Type", "application/json")

    req = request.Request(url, method=method, data=body, headers=request_headers)
    try:
        with opener.open(req, timeout=10) as response:
            return response.status, response.read().decode("utf-8")
    except error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8")


def _fetch_multipart_json_with_opener(
    opener: request.OpenerDirector,
    url: str,
    *,
    fields: dict[str, str] | None = None,
    files: dict[str, tuple[str, bytes, str]] | None = None,
) -> tuple[int, Any]:
    body, content_type = build_multipart_form(fields=fields, files=files)
    req = request.Request(
        url,
        method="POST",
        data=body,
        headers={
            "Accept": "application/json",
            "Content-Type": content_type,
        },
    )
    try:
        with opener.open(req, timeout=10) as response:
            return response.status, json.loads(response.read().decode("utf-8"))
    except error.HTTPError as exc:
        return exc.code, json.loads(exc.read().decode("utf-8"))
