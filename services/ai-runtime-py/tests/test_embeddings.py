from __future__ import annotations

import httpx
import json
import pytest

from app.adapters.embeddings import EmbeddingProviderError, OpenAICompatibleEmbeddingAdapter


def test_openai_compatible_embedding_adapter_uses_model_auth_and_expected_dimension() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.method == "POST"
        assert request.url.path == "/v1/embeddings"
        assert request.headers["authorization"] == "Bearer internal-token"
        assert json.loads(request.content) == {"model": "embeddinggemma", "input": "policy text", "dimensions": 3}
        return httpx.Response(200, json={"data": [{"embedding": [0.2, 0.3, 0.4]}]})

    client = httpx.Client(transport=httpx.MockTransport(handler), base_url="http://embedding.test")
    adapter = OpenAICompatibleEmbeddingAdapter(
        "http://embedding.test/v1",
        model_name="embeddinggemma",
        dimensions=3,
        api_key="internal-token",
        client=client,
    )

    assert adapter.embed("policy text") == [0.2, 0.3, 0.4]


@pytest.mark.parametrize(
    "embedding",
    [
        [0.1, 0.2],
        [0.1, "not-a-number", 0.3],
        [0.0, 0.0, 0.0],
    ],
)
def test_openai_compatible_embedding_adapter_rejects_invalid_vectors(embedding: list[object]) -> None:
    client = httpx.Client(
        transport=httpx.MockTransport(lambda _request: httpx.Response(200, json={"data": [{"embedding": embedding}]})),
        base_url="http://embedding.test",
    )
    adapter = OpenAICompatibleEmbeddingAdapter(
        "http://embedding.test/v1",
        model_name="embeddinggemma",
        dimensions=3,
        client=client,
    )

    with pytest.raises(EmbeddingProviderError):
        adapter.embed("policy text")


def test_openai_compatible_embedding_adapter_converts_provider_http_error() -> None:
    client = httpx.Client(
        transport=httpx.MockTransport(lambda _request: httpx.Response(503, json={"error": "unavailable"})),
        base_url="http://embedding.test",
    )
    adapter = OpenAICompatibleEmbeddingAdapter(
        "http://embedding.test/v1",
        model_name="embeddinggemma",
        dimensions=3,
        client=client,
    )

    with pytest.raises(EmbeddingProviderError, match="request failed"):
        adapter.embed("policy text")
