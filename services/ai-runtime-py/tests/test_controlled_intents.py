from __future__ import annotations

import pytest

from app.api.schemas import ChatRequest
from app.pipelines.answer_pipeline import AnswerPipeline


class _MustNotRun:
    def execute(self, _: ChatRequest):
        raise AssertionError("retrieval must not run for a controlled intent")

    def generate(self, **_: object):
        raise AssertionError("generation must not run for a controlled intent")

    def stream_generate(self, **_: object):
        raise AssertionError("generation must not run for a controlled intent")


@pytest.mark.parametrize(
    ("intent", "refusal", "text"),
    [
        ("social_greeting", False, "RAG 检索结果"),
        ("social_thanks", False, "不客气"),
        ("social_farewell", False, "再见"),
        ("capability_help", False, "RAG 检索结果"),
        ("off_topic", True, "RAG 检索结果"),
        ("out_of_scope", True, "公共开放世界问题"),
    ],
)
def test_controlled_intent_skips_retrieval_and_generation(intent: str, refusal: bool, text: str) -> None:
    request = _request(intent)
    response = AnswerPipeline(_MustNotRun(), _MustNotRun()).execute(request)  # type: ignore[arg-type]

    assert text in response.assistant_message.content
    assert response.intent == intent
    assert response.refusal is refusal
    assert response.retrieval_performed is False
    assert response.generation_performed is False
    assert response.knowledge_used is False
    assert response.knowledge_snapshot_id is None
    assert response.citations == []
    assert response.usage is not None and response.usage.prompt_tokens == 0


def test_controlled_intent_stream_has_same_execution_facts() -> None:
    events = list(AnswerPipeline(_MustNotRun(), _MustNotRun()).stream(_request("social_greeting")))  # type: ignore[arg-type]
    completed = events[-1].payload["chat_runtime_response"]

    assert completed.intent == "social_greeting"
    assert completed.retrieval_performed is False
    assert completed.generation_performed is False
    assert completed.knowledge_used is False
    assert "".join(event.payload.get("delta", "") for event in events) == completed.assistant_message.content


def test_out_of_scope_stream_has_same_execution_facts() -> None:
    events = list(AnswerPipeline(_MustNotRun(), _MustNotRun()).stream(_request("out_of_scope")))  # type: ignore[arg-type]
    completed = events[-1].payload["chat_runtime_response"]

    assert completed.intent == "out_of_scope"
    assert completed.refusal is True
    assert completed.retrieval_performed is False
    assert completed.generation_performed is False
    assert completed.knowledge_used is False
    assert completed.citations == []
    assert "RAG 检索结果" in completed.assistant_message.content
    assert "".join(event.payload.get("delta", "") for event in events) == completed.assistant_message.content




def _request(intent: str) -> ChatRequest:
    return ChatRequest(
        request_id="req_controlled",
        conversation_id="conv_controlled",
        message_id="msg_controlled",
        user_query="你好",
        model_id="local-model",
        config_version="cfg_001",
        retrieval_config_version="retrieval_001",
        knowledge_snapshot_id="",
        intent=intent,  # type: ignore[arg-type]
        stream=False,
        messages=[],
    )
