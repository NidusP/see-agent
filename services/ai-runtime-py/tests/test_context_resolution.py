from collections.abc import Iterator

import pytest

from app.adapters.model_client import ModelClientResult
from app.api.schemas import ChatRequest, ConversationMessage
from app.pipelines.answer_pipeline import AnswerPipeline, ContextResolutionError
from app.pipelines.retrieval_pipeline import RetrievalResult


class ResolverModel:
    provider_name = "test_resolver"

    def __init__(self, content: str) -> None:
        self.content = content
        self.calls = 0
        self.last_kwargs: dict[str, object] = {}

    def generate(self, **kwargs: object) -> ModelClientResult:
        self.calls += 1
        self.last_kwargs = kwargs
        return ModelClientResult(content=self.content, finish_reason="stop", prompt_tokens=1, completion_tokens=1, total_tokens=2)

    def stream_generate(self, **_: object) -> Iterator[str]:
        return iter(())


class MustNotRetrieve:
    def execute(self, *_: object, **__: object) -> object:
        raise AssertionError("clarification must not execute retrieval")


class TrackingRetrieval:
    def __init__(self) -> None:
        self.query_overrides: list[str | None] = []

    def execute(self, _request: ChatRequest, *, query_override: str | None = None) -> RetrievalResult:
        self.query_overrides.append(query_override)
        return RetrievalResult(
            citations=[],
            context_text="",
            top_score=0.0,
            top_k=3,
            rerank_enabled=False,
            evidence_min_score=0.51,
            refusal_reason="no scoped evidence",
        )

    def execution_profile(self) -> tuple[str, str, str]:
        return ("native_dense_v1", "bge-m3", "see_agent_chunks")


def _request(query: str) -> ChatRequest:
    return ChatRequest(
        request_id="req_context", conversation_id="conv_context", message_id="msg_current", user_query=query,
        model_id="test-model", config_version="cfg_test", retrieval_config_version="rcfg_test",
        knowledge_snapshot_id="snap_test", knowledge_scope={"snapshot_id": "snap_test", "manifest_hash": "sha256:test", "revision_ids": ["rev_test"], "resolved_at": "2026-07-28T00:00:00Z"},
        stream=False,
        messages=[
            {"message_id": "msg_user_1", "role": "user", "content": "差旅报销提交期限是什么？"},
            {"message_id": "msg_assistant_1", "role": "assistant", "content": "员工应在费用发生后的十个自然日内提交。"},
        ],
    )


def test_clear_standalone_question_is_model_routed() -> None:
    model = ResolverModel('{"intent":"knowledge_query","route":"rag","kind":"standalone","resolved_query":null,"basis_message_ids":[],"confidence":0.95}')
    pipeline = AnswerPipeline(None, model)  # type: ignore[arg-type]

    resolution = pipeline._resolve_context(_request("请假如何申请？"))

    assert resolution.kind == "standalone"
    assert resolution.resolver_performed is True
    assert model.calls == 1


def test_follow_up_requires_bounded_basis_and_records_provider() -> None:
    model = ResolverModel('{"intent":"knowledge_query","route":"rag","kind":"follow_up","resolved_query":"差旅报销超过十个自然日未提交怎么办？","basis_message_ids":["msg_user_1"],"confidence":0.95}')
    pipeline = AnswerPipeline(None, model)  # type: ignore[arg-type]

    resolution = pipeline._resolve_context(_request("超过期限呢？"))

    assert resolution.kind == "follow_up"
    assert "差旅报销" in (resolution.resolved_query or "")
    assert resolution.basis_message_ids == ["msg_user_1"]
    assert resolution.resolver_provider == "test_resolver"
    assert resolution.resolver_model_id == "test-model"
    assert resolution.confidence == 0.95
    assert model.last_kwargs["json_mode"] is True


def test_invalid_resolver_output_fails_closed() -> None:
    model = ResolverModel("not json")
    pipeline = AnswerPipeline(None, model)  # type: ignore[arg-type]

    with pytest.raises(ContextResolutionError):
        pipeline._resolve_context(_request("超过期限呢？"))


def test_ambiguous_context_returns_clarification_without_retrieval_in_json_and_sse() -> None:
    model = ResolverModel('{"intent":"knowledge_query","route":"clarification","kind":"clarification_required","resolved_query":null,"basis_message_ids":[],"confidence":0.2}')
    pipeline = AnswerPipeline(MustNotRetrieve(), model)  # type: ignore[arg-type]
    request = _request("那个怎么办？").model_copy(update={"intent": "context_routing_required"})

    response = pipeline.execute(request)
    events = list(pipeline.stream(request))
    completed = events[-1].payload["chat_runtime_response"]

    for value in (response, completed):
        assert value.answer_disposition == "clarification_required"
        assert value.context_resolution is not None
        assert value.context_resolution.kind == "clarification_required"
        assert value.context_resolution.resolver_performed is True
        assert value.retrieval_performed is False
        assert value.generation_performed is False
        assert value.citations == []


def test_follow_up_uses_resolved_query_but_topic_shift_keeps_raw_query() -> None:
    follow_up_model = ResolverModel('{"intent":"knowledge_query","route":"rag","kind":"follow_up","resolved_query":"差旅报销超过十个自然日未提交怎么办？","basis_message_ids":["msg_user_1"],"confidence":0.95}')
    follow_up_retrieval = TrackingRetrieval()
    follow_up = AnswerPipeline(follow_up_retrieval, follow_up_model).execute(_request("超过期限呢？").model_copy(update={"intent": "context_routing_required"}))

    assert follow_up_retrieval.query_overrides == ["差旅报销超过十个自然日未提交怎么办？"]
    assert follow_up.context_resolution is not None and follow_up.context_resolution.kind == "follow_up"

    topic_shift_model = ResolverModel('{"intent":"knowledge_query","route":"rag","kind":"topic_shift","resolved_query":null,"basis_message_ids":[],"confidence":0.95}')
    topic_shift_retrieval = TrackingRetrieval()
    topic_shift = AnswerPipeline(topic_shift_retrieval, topic_shift_model).execute(_request("那请假呢？").model_copy(update={"intent": "context_routing_required"}))

    assert topic_shift_retrieval.query_overrides == [None]
    assert topic_shift.context_resolution is not None and topic_shift.context_resolution.kind == "topic_shift"


def test_controlled_route_uses_fixed_response_without_retrieval() -> None:
    model = ResolverModel('{"intent":"capability_help","route":"controlled_reply","kind":"standalone","resolved_query":null,"basis_message_ids":[],"confidence":0.95}')
    pipeline = AnswerPipeline(MustNotRetrieve(), model)  # type: ignore[arg-type]

    response = pipeline.execute(_request("真的吗？").model_copy(update={"intent": "context_routing_required"}))

    assert response.intent == "capability_help"
    assert response.answer_disposition == "controlled_response"
    assert response.context_resolution is not None
    assert response.context_resolution.route == "controlled_reply"
    assert response.retrieval_performed is False
    assert response.generation_performed is False


def test_confirmation_of_capability_uses_contextual_capability_route() -> None:
    model = ResolverModel('{"intent":"capability_help","route":"controlled_reply","kind":"standalone","resolved_query":null,"basis_message_ids":[],"confidence":0.95}')
    pipeline = AnswerPipeline(MustNotRetrieve(), model)  # type: ignore[arg-type]
    request = _request("真的吗？").model_copy(update={
        "intent": "context_routing_required",
        "messages": [ConversationMessage(message_id="msg_assistant_capability", role="assistant", content="我只依据已发布知识库的 RAG 检索结果回答。")],
    })

    response = pipeline.execute(request)

    assert response.intent == "capability_help"
    assert response.context_resolution is not None
    assert response.context_resolution.intent == "capability_help"
