from typing import Any, cast

import pytest

from app.adapters.model_client import ModelClientError
from app.api.schemas import ChatRequest, KnowledgeScope, ResponseOverrides
from app.pipelines.answer_pipeline import AnswerPipeline


def make_request(version: str, *, response_style: str | None = None) -> ChatRequest:
    return ChatRequest(
        request_id="req_prompt_version",
        conversation_id="conv_prompt_version",
        message_id="msg_prompt_version",
        user_query="年假需要提前多久提交？",
        model_id="llama3.2:latest",
        config_version="cfg_prompt_version",
        retrieval_config_version="rcfg_prompt_version",
        knowledge_snapshot_id="snapshot_active",
        knowledge_scope=KnowledgeScope(snapshot_id="snapshot_active", manifest_hash="sha256:prompt", revision_ids=["rev_prompt"], resolved_at="2026-07-24T00:00:00Z"),
        stream=False,
        messages=[],
        response_overrides=ResponseOverrides(
            system_prompt_version=version,
            response_style=response_style,
        ),
    )


def make_pipeline() -> AnswerPipeline:
    return AnswerPipeline(cast(Any, object()), cast(Any, object()))


def test_prompt_v1_remains_unchanged_and_new_prompt_versions_keep_their_distinct_protocols() -> None:
    pipeline = make_pipeline()

    prompt_v1 = pipeline._system_prompt_for(make_request("prompt_v1"))
    prompt_v2 = pipeline._system_prompt_for(make_request("prompt_v2"))
    prompt_v3 = pipeline._system_prompt_for(make_request("prompt_v3"))
    prompt_v4 = pipeline._system_prompt_for(make_request("prompt_v4"))
    prompt_v5 = pipeline._system_prompt_for(make_request("prompt_v5"))
    prompt_v6 = pipeline._system_prompt_for(make_request("prompt_v6"))
    prompt_v7 = pipeline._system_prompt_for(make_request("prompt_v7"))
    prompt_v8 = pipeline._system_prompt_for(make_request("prompt_v8"))
    prompt_v9 = pipeline._system_prompt_for(make_request("prompt_v9"))
    prompt_v10 = pipeline._system_prompt_for(make_request("prompt_v10"))
    prompt_v11 = pipeline._system_prompt_for(make_request("prompt_v11"))
    prompt_v12 = pipeline._system_prompt_for(make_request("prompt_v12"))

    assert "Evidence wording" not in prompt_v1
    assert "Evidence wording" in prompt_v2
    assert "逐字复制" in prompt_v2
    assert "目标行、字段标签和值" in prompt_v2
    assert "[[disposition:insufficient_evidence]]" in prompt_v3
    assert "正确示例" in prompt_v3
    assert "默认规则和例外" in prompt_v4
    assert "输出前完成这三个检查" in prompt_v5
    assert "不得输出思考过程" in prompt_v5
    assert prompt_v6.endswith("/no_think")
    assert "答案必须按以下顺序" in prompt_v7
    assert prompt_v7.endswith("/no_think")
    assert "Return exactly one JSON object" in prompt_v8
    assert "Return exactly one JSON object" in prompt_v9
    assert "untrusted" in prompt_v9
    assert prompt_v10 == prompt_v9
    assert prompt_v11 == prompt_v10
    assert "final answer-policy" in prompt_v12
    assert prompt_v12 != prompt_v11


def test_prompt_version_keeps_response_style_as_separate_guidance() -> None:
    prompt = make_pipeline()._system_prompt_for(make_request("prompt_v2", response_style="structured"))

    assert prompt.endswith("[Response Style]\nstructured")


def test_unknown_prompt_version_fails_closed() -> None:
    with pytest.raises(ModelClientError, match="unknown system_prompt_version"):
        make_pipeline()._system_prompt_for(make_request("prompt_unknown"))
