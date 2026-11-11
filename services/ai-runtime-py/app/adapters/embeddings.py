from __future__ import annotations

import re
from collections import Counter
from math import isfinite
from typing import Protocol
from urllib.parse import urlparse

import httpx


type SparseEmbeddingVector = dict[str, float]
type DenseEmbeddingVector = list[float]
type EmbeddingVector = SparseEmbeddingVector | DenseEmbeddingVector


class EmbeddingAdapter(Protocol):
    model_name: str
    requires_config_model_match: bool

    def embed(self, text: str) -> EmbeddingVector:
        ...


class EmbeddingProviderError(RuntimeError):
    """The configured embedding provider did not return a safe vector."""

_ASCII_TOKEN_RE = re.compile(r"[a-z0-9]+")
_CJK_SEQUENCE_RE = re.compile(r"[\u4e00-\u9fff]+")
_ASCII_STOPWORDS = {
    "a",
    "an",
    "and",
    "as",
    "at",
    "for",
    "how",
    "in",
    "is",
    "it",
    "of",
    "on",
    "or",
    "that",
    "the",
    "this",
    "to",
    "what",
    "when",
    "where",
    "who",
    "with",
}
_CJK_EVIDENCE_STOPWORDS = (
    "员工",
    "公司",
    "样例",
)
_DOCX_QUERY_ALIASES = (
    ("年假", ("annual", "leave")),
    ("酒店", ("hotel",)),
    ("条目", ("line", "item")),
    ("业务说明", ("business", "justification")),
    ("连续请假", ("requests", "longer", "consecutive")),
    ("连续工作日的请假", ("requests", "longer", "consecutive")),
    ("审批", ("direct", "manager", "approval", "hr", "confirmation")),
    ("确认", ("direct", "manager", "approval", "hr", "confirmation")),
    ("经理", ("managers",)),
    ("覆盖安排", ("coverage", "expectations", "approval", "thread")),
    ("收据", ("receipts",)),
    ("例外", ("exception", "finance", "leadership")),
)


def tokenize_text(text: str) -> list[str]:
    normalized = text.lower()
    tokens: list[str] = []
    tokens.extend(
        token
        for token in _ASCII_TOKEN_RE.findall(normalized)
        if token not in _ASCII_STOPWORDS
    )

    for sequence in _CJK_SEQUENCE_RE.findall(text):
        for evidence_sequence in _cjk_evidence_sequences(sequence):
            if len(evidence_sequence) == 1:
                tokens.append(evidence_sequence)
                continue

            tokens.append(evidence_sequence)
            tokens.extend(
                evidence_sequence[index : index + 2]
                for index in range(len(evidence_sequence) - 1)
            )
            tokens.extend(evidence_sequence)

    return tokens


def build_docx_query_representation(text: str) -> str:
    aliases = [
        alias
        for chinese_term, english_terms in _DOCX_QUERY_ALIASES
        if chinese_term in text
        for alias in english_terms
    ]
    if not aliases:
        return text
    return " ".join((text, *aliases))


def _cjk_evidence_sequences(sequence: str) -> list[str]:
    for stopword in _CJK_EVIDENCE_STOPWORDS:
        sequence = sequence.replace(stopword, " ")
    return [part for part in sequence.split() if part]


class LexicalEmbeddingAdapter:
    model_name = "baseline-lexical-embedder"
    requires_config_model_match = False

    def embed(self, text: str) -> EmbeddingVector:
        counts = Counter(tokenize_text(text))
        if not counts:
            return {}

        magnitude = sum(value * value for value in counts.values()) ** 0.5
        return {token: value / magnitude for token, value in counts.items()}


class OpenAICompatibleEmbeddingAdapter:
    """Dense embeddings through an OpenAI-compatible `/v1/embeddings` endpoint."""

    requires_config_model_match = True

    def __init__(
        self,
        base_url: str,
        *,
        model_name: str,
        dimensions: int,
        api_key: str | None = None,
        timeout_seconds: float = 15.0,
        client: httpx.Client | None = None,
    ) -> None:
        normalized_base_url = base_url.rstrip("/")
        if not normalized_base_url:
            raise ValueError("embedding base URL is required")
        if not model_name.strip():
            raise ValueError("embedding model is required")
        if dimensions < 1:
            raise ValueError("embedding dimensions must be positive")
        self.model_name = model_name.strip()
        self._dimensions = dimensions
        self._api_key = api_key or None
        self._base_url = normalized_base_url
        self._client = client or httpx.Client(
            timeout=timeout_seconds,
            trust_env=_should_trust_environment(normalized_base_url),
        )

    def embed(self, text: str) -> DenseEmbeddingVector:
        if not text.strip():
            raise EmbeddingProviderError("embedding input must not be empty")
        headers = {"Content-Type": "application/json"}
        if self._api_key:
            headers["Authorization"] = f"Bearer {self._api_key}"
        try:
            response = self._client.post(
                self._embeddings_url(),
                json={"model": self.model_name, "input": text, "dimensions": self._dimensions},
                headers=headers,
            )
            response.raise_for_status()
        except httpx.HTTPError as exc:
            raise EmbeddingProviderError(f"embedding provider request failed: {exc}") from exc

        try:
            data = response.json()
            vector = data["data"][0]["embedding"]
        except (KeyError, IndexError, TypeError, ValueError) as exc:
            raise EmbeddingProviderError("embedding provider response was missing data[0].embedding") from exc
        if not isinstance(vector, list) or len(vector) != self._dimensions:
            actual = len(vector) if isinstance(vector, list) else "non-list"
            raise EmbeddingProviderError(
                f"embedding provider dimension mismatch: expected {self._dimensions}, got {actual}"
            )
        try:
            normalized = [float(value) for value in vector]
        except (TypeError, ValueError) as exc:
            raise EmbeddingProviderError("embedding provider returned non-numeric values") from exc
        if not all(isfinite(value) for value in normalized):
            raise EmbeddingProviderError("embedding provider returned non-finite values")
        if not any(value != 0 for value in normalized):
            raise EmbeddingProviderError("embedding provider returned a zero vector")
        return normalized

    def _embeddings_url(self) -> str:
        if self._base_url.endswith("/embeddings"):
            return self._base_url
        if self._base_url.endswith("/v1"):
            return f"{self._base_url}/embeddings"
        return f"{self._base_url}/v1/embeddings"


def cosine_similarity(left: EmbeddingVector, right: EmbeddingVector) -> float:
    if not left or not right:
        return 0.0

    if isinstance(left, list) and isinstance(right, list):
        if len(left) != len(right):
            raise ValueError("dense embedding dimensions do not match")
        return sum(a * b for a, b in zip(left, right, strict=True))
    if isinstance(left, list) or isinstance(right, list):
        raise ValueError("cannot compare dense and sparse embeddings")

    if len(left) > len(right):
        left, right = right, left

    return sum(weight * right.get(token, 0.0) for token, weight in left.items())


def _should_trust_environment(base_url: str) -> bool:
    hostname = urlparse(base_url).hostname
    return hostname not in {"localhost", "127.0.0.1", "::1"}
