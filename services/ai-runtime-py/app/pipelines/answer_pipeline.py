from __future__ import annotations

from collections.abc import Iterator
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
import json
import re
from typing import Any

from app.adapters.model_client import ModelClient, ModelClientError, ModelClientResult
from app.api.schemas import (
    AssistantMessage,
    ChatRequest,
    ChatResponse,
    Clarification,
    ClarificationResolution,
    ContextResolution,
    ExecutionObservation,
    GenerationObservation,
    RetrievalCandidate,
    RetrievalObservation,
    RetrievedSource,
    TokenUsage,
)
from app.pipelines.answer_policy import AnswerPolicyError, AnswerPolicyDecision, ScopePolicyDecision, ScopeShapeDecision, parse_answer_policy, parse_scope_policy, scope_policy_json_schema
from app.parsers.common import count_tokens
from app.pipelines.citation_anchors import CitationAnchorParseResult, CitationAnchorStreamParser, parse_citation_anchors
from app.pipelines.retrieval_pipeline import KnowledgeScopeError, RetrievalPipeline, RetrievalResult


_WORKSPACE_ROOT = Path(__file__).resolve().parents[4]
_PROMPTS_ROOT = _WORKSPACE_ROOT / "packages" / "prompts"
_A1_POLICY_PAIRS = {
    ("prompt_v8", "answer_policy_v1"),
    ("prompt_v9", "answer_policy_v2"),
    ("prompt_v10", "answer_policy_v3"),
    ("prompt_v11", "answer_policy_v4"),
    ("prompt_v12", "answer_policy_v5"),
}
_A1_SCOPE_ASSESSMENT_PAIR = ("prompt_v10", "answer_policy_v3")
_A1_SCOPE_SHAPE_PAIRS = {
    ("prompt_v11", "answer_policy_v4"),
    ("prompt_v12", "answer_policy_v5"),
}
_LEGACY_PROMPT_VERSIONS = frozenset(f"prompt_v{version}" for version in range(1, 8))
_REVALIDATION_VERDICT_RE = re.compile(
    r"\[\[VERDICT:(still_valid|needs_update|insufficient_evidence)\]\]",
    re.IGNORECASE,
)
_REVALIDATION_INSUFFICIENT_EVIDENCE = "当前已发布知识不足以确认这条历史回答是否仍然准确。"
_INSUFFICIENT_EVIDENCE_MARKER_RE = re.compile(r"\[\[disposition:insufficient_evidence\]\]", re.IGNORECASE)
_REVALIDATION_SYSTEM_PROMPT = """You are reviewing a historical answer against the latest published knowledge.
The historical answer is evidence under review, not current truth. Use only the latest retrieved context.
Return exactly one control marker [[VERDICT:still_valid]], [[VERDICT:needs_update]], or
[[VERDICT:insufficient_evidence]], followed by a concise user-readable explanation. Do not repeat the marker.
Every factual sentence in a still_valid or needs_update explanation must include one or more citation
markers in the form [[cite:<rank>]], using only ranks from the retrieved context."""
_CONTROLLED_RESPONSES = {
    "social_greeting": ("你好！我是一个大模型助手，只依据已发布知识库的 RAG 检索结果回答制度、流程和业务问题。你想查询什么？", False, "stop"),
    "social_thanks": ("不客气。我作为大模型助手只依据已发布知识库的 RAG 检索结果回答；如需查询制度、流程或业务知识，可以继续问我。", False, "stop"),
    "social_farewell": ("再见！我是一个只依据已发布知识库 RAG 检索结果回答的大模型助手，需要查询内部知识时随时来问我。", False, "stop"),
    "capability_help": ("我是一个大模型助手，只依据已发布知识库的 RAG 检索结果回答制度、流程和业务问题，并提供可追溯的引用。你可以直接描述要查询的事项。", False, "stop"),
    "off_topic": ("我不能处理开放式闲聊。我作为大模型助手只依据已发布知识库的 RAG 检索结果回答制度、流程和业务问题；你可以换成知识库相关的问题。", True, "refusal"),
    "out_of_scope": ("我不能回答天气、美国总统等公共开放世界问题。我作为大模型助手只依据已发布知识库的 RAG 检索结果回答制度、流程和业务问题；你可以换成知识库相关的问题。", True, "refusal"),
}

_CONTEXT_RESOLUTION_SYSTEM_PROMPT = """Return only one JSON object with exactly intent, route, kind, resolved_query, basis_message_ids, confidence.
Classify the current question against bounded history. intent is knowledge_query, social_greeting, social_thanks, social_farewell, capability_help, off_topic, or out_of_scope. route is rag, controlled_reply, or clarification.
Business/internal questions must use knowledge_query plus rag. Greetings, thanks, farewell, capability questions, open-world requests and casual chat use controlled_reply. Ambiguous business questions use knowledge_query plus clarification.
kind is follow_up, standalone, topic_shift, or clarification_required. confidence must be a JSON number from 0 to 1. follow_up requires confidence at least 0.9, a concise resolved query, and only history message IDs. Only follow_up may use resolved_query or basis_message_ids.

Use history, not just surface words. Exact standalone example: with empty history and current “差旅报销提交期限是什么？”, return {"intent":"knowledge_query","route":"rag","kind":"standalone","resolved_query":null,"basis_message_ids":[],"confidence":0.95}. A standalone question never repeats itself in resolved_query.
Example: after an assistant says it only answers from the published RAG knowledge base, current question “真的吗？” is a confirmation of that capability. Return intent capability_help, route controlled_reply, kind standalone, resolved_query null, basis_message_ids [], confidence 0.95. Exact follow-up example: history message_id "msg_user_1" asks “差旅报销提交期限是什么？”, and current “超过期限呢？” must return {"intent":"knowledge_query","route":"rag","kind":"follow_up","resolved_query":"超过差旅报销提交期限的后果是什么？","basis_message_ids":["msg_user_1"],"confidence":0.95}. basis_message_ids must contain the literal string message_id values from history; never use numeric positions such as 0 or 1. If the necessary referent is absent or genuinely ambiguous, return knowledge_query, clarification, clarification_required. Never answer the user question."""


class ContextResolutionError(ModelClientError):
    code = "context_resolution_failed"


@dataclass(frozen=True)
class ChatStreamEvent:
    event: str
    payload: dict[str, Any]




class AnswerPipeline:
    def __init__(self, retrieval_pipeline: RetrievalPipeline, model_client: ModelClient) -> None:
        self._retrieval_pipeline = retrieval_pipeline
        self._model_client = model_client
        self._system_prompts = {
            "prompt_v1": (_PROMPTS_ROOT / "runtime-chat-system-v1.txt").read_text(encoding="utf-8").strip(),
            "prompt_v2": (_PROMPTS_ROOT / "runtime-chat-system-v2.txt").read_text(encoding="utf-8").strip(),
            "prompt_v3": (_PROMPTS_ROOT / "runtime-chat-system-v3.txt").read_text(encoding="utf-8").strip(),
            "prompt_v4": (_PROMPTS_ROOT / "runtime-chat-system-v4.txt").read_text(encoding="utf-8").strip(),
            "prompt_v5": (_PROMPTS_ROOT / "runtime-chat-system-v5.txt").read_text(encoding="utf-8").strip(),
            "prompt_v6": (_PROMPTS_ROOT / "runtime-chat-system-v6.txt").read_text(encoding="utf-8").strip(),
            "prompt_v7": (_PROMPTS_ROOT / "runtime-chat-system-v7.txt").read_text(encoding="utf-8").strip(),
            "prompt_v8": (_PROMPTS_ROOT / "runtime-chat-system-v8.txt").read_text(encoding="utf-8").strip(),
            "prompt_v9": (_PROMPTS_ROOT / "runtime-chat-system-v9.txt").read_text(encoding="utf-8").strip(),
            "prompt_v10": (_PROMPTS_ROOT / "runtime-chat-system-v10.txt").read_text(encoding="utf-8").strip(),
            "prompt_v11": (_PROMPTS_ROOT / "runtime-chat-system-v11.txt").read_text(encoding="utf-8").strip(),
            "prompt_v12": (_PROMPTS_ROOT / "runtime-chat-system-v12.txt").read_text(encoding="utf-8").strip(),
        }
        self._scope_policy_prompts = {
            ("prompt_v8", "answer_policy_v1"): (_PROMPTS_ROOT / "runtime-a1-scope-policy-v1.txt").read_text(encoding="utf-8").strip(),
            ("prompt_v9", "answer_policy_v2"): (_PROMPTS_ROOT / "runtime-a1-scope-policy-v2.txt").read_text(encoding="utf-8").strip(),
            ("prompt_v10", "answer_policy_v3"): (_PROMPTS_ROOT / "runtime-a1-scope-policy-v3.txt").read_text(encoding="utf-8").strip(),
            ("prompt_v11", "answer_policy_v4"): (_PROMPTS_ROOT / "runtime-a1-scope-policy-v4.txt").read_text(encoding="utf-8").strip(),
            ("prompt_v12", "answer_policy_v5"): (_PROMPTS_ROOT / "runtime-a1-scope-policy-v5.txt").read_text(encoding="utf-8").strip(),
        }
        self._refusal_template = (_PROMPTS_ROOT / "runtime-refusal-v1.txt").read_text(encoding="utf-8").strip()

    def _resolve_context(self, request: ChatRequest) -> ContextResolution:
        history = [message for message in request.messages if message.content.strip()][-6:]
        allowed_ids = {message.message_id for message in history}
        payload = {"history": [{"message_id": message.message_id, "role": message.role, "content": message.content} for message in history], "current_question": request.user_query}
        result = self._model_client.generate(
            model_id=request.model_id,
            messages=[{"role": "system", "content": _CONTEXT_RESOLUTION_SYSTEM_PROMPT}, {"role": "user", "content": json.dumps(payload, ensure_ascii=False)}],
            user_query=request.user_query,
            citations=[],
            temperature=0.0,
            max_output_tokens=220,
            json_mode=True,
        )
        try:
            parsed = json.loads(result.content)
            if not isinstance(parsed, dict) or set(parsed) != {"intent", "route", "kind", "resolved_query", "basis_message_ids", "confidence"}:
                raise ValueError("invalid resolver shape")
            confidence = parsed["confidence"]
            if isinstance(confidence, bool) or not isinstance(confidence, (int, float)) or not 0 <= confidence <= 1:
                raise ValueError("resolver confidence must be a number from 0 to 1")
            if parsed["kind"] == "follow_up" and confidence < 0.9:
                raise ValueError("follow_up requires confidence >= 0.9")
            resolution = ContextResolution(
                intent=parsed["intent"], route=parsed["route"], kind=parsed["kind"], resolved_query=parsed.get("resolved_query"), basis_message_ids=parsed.get("basis_message_ids") or [],
                resolver_provider=getattr(self._model_client, "provider_name", "test_injected"), resolver_model_id=request.model_id, resolver_performed=True, confidence=confidence,
            )
        except (TypeError, ValueError, json.JSONDecodeError) as exc:
            raise ContextResolutionError("context resolver returned an invalid structured result") from exc
        if any(message_id not in allowed_ids for message_id in resolution.basis_message_ids):
            raise ContextResolutionError("context resolver referenced a message outside bounded history")
        return resolution

    def execute(self, request: ChatRequest, *, resolution: ContextResolution | None = None) -> ChatResponse:
        if request.answer_mode == "normal":
            _a1_policy_pair(request)
        if request.answer_mode == "normal" and request.intent == "context_routing_required" and resolution is None:
            resolution = self._resolve_context(request)
            if resolution.route == "controlled_reply":
                routed = request.model_copy(update={"intent": resolution.intent})
                return _controlled_response(routed, datetime.now(timezone.utc).isoformat()).model_copy(update={"context_resolution": resolution})
            routed = request.model_copy(update={"intent": "knowledge_query"})
            if _uses_a1_policy(routed):
                response = self._execute_a1_policy(
                    routed,
                    resolution=resolution,
                    force_clarification=resolution.route == "clarification",
                )
                return response.model_copy(update={"context_resolution": resolution})
            if resolution.route == "clarification":
                return _clarification_response(request, resolution)
            response = self.execute(routed, resolution=resolution)
            return response.model_copy(update={"context_resolution": resolution})
        completed_at = datetime.now(timezone.utc).isoformat()
        if request.answer_mode != "revalidate" and request.intent != "knowledge_query":
            return _controlled_response(request, completed_at)
        if _uses_a1_policy(request):
            return self._execute_a1_policy(request, resolution=resolution, force_clarification=False)

        retrieval = self._retrieval_pipeline.execute(request, query_override=resolution.resolved_query if resolution and resolution.kind == "follow_up" else None)

        if request.answer_mode == "revalidate":
            return self._execute_revalidation(request, retrieval, completed_at)

        if retrieval.refusal_reason is not None:
            refusal_message = self._refusal_template.format(question=request.user_query)
            prompt_tokens = count_tokens(self._system_prompt_for(request)) + count_tokens(request.user_query)
            completion_tokens = count_tokens(refusal_message)
            return ChatResponse(
                request_id=request.request_id,
                conversation_id=request.conversation_id,
                message_id=request.message_id,
                assistant_message=AssistantMessage(content=refusal_message),
                citations=[],
                refusal=True,
                finish_reason="refusal",
                completed_at=completed_at,
                intent="knowledge_query",
                answer_disposition="insufficient_evidence",
                execution=self._execution_observation(retrieval, generation_performed=False, model_id=request.model_id),
                used_citation_ranks=[],
                retrieval_performed=True,
                generation_performed=False,
                knowledge_used=False,
                **_knowledge_provenance(request),
                usage=TokenUsage(
                    prompt_tokens=prompt_tokens,
                    completion_tokens=completion_tokens,
                    total_tokens=prompt_tokens + completion_tokens,
                ),
            )

        temperature = request.response_overrides.temperature if request.response_overrides else None
        top_p = request.response_overrides.top_p if request.response_overrides else None
        max_output_tokens = request.response_overrides.max_output_tokens if request.response_overrides else None
        prompt_messages = self._assemble_messages(request, retrieval.context_text)
        generation = self._model_client.generate(
            model_id=request.model_id,
            messages=prompt_messages,
            user_query=request.user_query,
            citations=retrieval.citations,
            temperature=temperature,
            top_p=top_p,
            max_output_tokens=max_output_tokens,
        )
        if _model_signalled_insufficient_evidence(generation.content):
            return _model_insufficient_evidence_response(
                request,
                completed_at,
                generation,
                self._refusal_template,
                self._execution_observation(retrieval, generation_performed=True, model_id=request.model_id),
            )
        parsed = parse_citation_anchors(generation.content, retrieval.citations)
        citations, used_ranks = _visible_evidence(parsed.anchors, retrieval.citations)

        return ChatResponse(
            request_id=request.request_id,
            conversation_id=request.conversation_id,
            message_id=request.message_id,
            assistant_message=AssistantMessage(content=parsed.content),
            citations=citations,
            citation_anchors=parsed.anchors,
            refusal=False,
            finish_reason=generation.finish_reason,
            completed_at=completed_at,
            intent="knowledge_query",
            answer_disposition="grounded_answer",
            answer_policy_version="answer_policy_v1",
            execution=self._execution_observation(retrieval, generation_performed=True, model_id=request.model_id),
            used_citation_ranks=used_ranks,
            retrieval_performed=True,
            generation_performed=True,
            knowledge_used=True,
            **_knowledge_provenance(request),
            usage=TokenUsage(
                prompt_tokens=generation.prompt_tokens,
                completion_tokens=generation.completion_tokens,
                total_tokens=generation.total_tokens,
            ),
        )

    def stream(self, request: ChatRequest, *, resolution: ContextResolution | None = None) -> Iterator[ChatStreamEvent]:
        if request.answer_mode == "normal":
            _a1_policy_pair(request)
        if _uses_a1_policy(request):
            # The candidate policy must be fully validated before exposing any user-visible token.
            yield from _response_events(request, self.execute(request, resolution=resolution))
            return
        if request.answer_mode == "normal" and request.intent == "context_routing_required" and resolution is None:
            resolution = self._resolve_context(request)
            if resolution.route == "clarification":
                yield from _response_events(request, _clarification_response(request, resolution))
                return
            if resolution.route == "controlled_reply":
                routed = request.model_copy(update={"intent": resolution.intent})
                yield from _response_events(request, _controlled_response(routed, datetime.now(timezone.utc).isoformat()).model_copy(update={"context_resolution": resolution}))
                return
            routed = request.model_copy(update={"intent": "knowledge_query"})
            for event in self.stream(routed, resolution=resolution):
                if event.event == "message.completed" and isinstance(event.payload.get("chat_runtime_response"), ChatResponse):
                    response = event.payload["chat_runtime_response"].model_copy(update={"context_resolution": resolution})
                    event = ChatStreamEvent(event=event.event, payload={**event.payload, "chat_runtime_response": response})
                yield event
            return
        if request.answer_mode != "revalidate" and request.intent != "knowledge_query":
            yield from _response_events(request, _controlled_response(request, datetime.now(timezone.utc).isoformat()))
            return

        retrieval = self._retrieval_pipeline.execute(request, query_override=resolution.resolved_query if resolution and resolution.kind == "follow_up" else None)

        if request.answer_mode == "revalidate":
            yield from self._stream_revalidation(request, retrieval)
            return

        if retrieval.refusal_reason is not None:
            refusal_message = self._refusal_template.format(question=request.user_query)
            for delta in _split_text_deltas(refusal_message):
                yield _delta_event(request, delta)
            prompt_tokens = count_tokens(self._system_prompt_for(request)) + count_tokens(request.user_query)
            completion_tokens = count_tokens(refusal_message)
            yield _completed_event(
                ChatResponse(
                    request_id=request.request_id,
                    conversation_id=request.conversation_id,
                    message_id=request.message_id,
                    assistant_message=AssistantMessage(content=refusal_message),
                    citations=[],
                    refusal=True,
                    finish_reason="refusal",
                    completed_at=datetime.now(timezone.utc).isoformat(),
                    intent="knowledge_query",
                    answer_disposition="insufficient_evidence",
                    execution=self._execution_observation(retrieval, generation_performed=False, model_id=request.model_id),
                    used_citation_ranks=[],
                    retrieval_performed=True,
                    generation_performed=False,
                    knowledge_used=False,
                    **_knowledge_provenance(request),
                    usage=TokenUsage(
                        prompt_tokens=prompt_tokens,
                        completion_tokens=completion_tokens,
                        total_tokens=prompt_tokens + completion_tokens,
                    ),
                )
            )
            return

        temperature = request.response_overrides.temperature if request.response_overrides else None
        top_p = request.response_overrides.top_p if request.response_overrides else None
        max_output_tokens = request.response_overrides.max_output_tokens if request.response_overrides else None
        prompt_messages = self._assemble_messages(request, retrieval.context_text)

        if _uses_disposition_protocol(request):
            yield from self._stream_disposition_protocol(request, retrieval, prompt_messages)
            return

        parser = CitationAnchorStreamParser(retrieval.citations)
        for delta in self._model_client.stream_generate(
            model_id=request.model_id,
            messages=prompt_messages,
            user_query=request.user_query,
            citations=retrieval.citations,
            temperature=temperature,
            top_p=top_p,
            max_output_tokens=max_output_tokens,
        ):
            if not delta:
                continue
            visible_delta = parser.push(delta)
            if visible_delta:
                yield _delta_event(request, visible_delta)

        final_delta = parser.finalize()
        if final_delta:
            yield _delta_event(request, final_delta)
        content = parser.content.strip()
        if not content:
            raise ModelClientError("model stream response was empty")
        citations, used_ranks = _visible_evidence(parser.anchors, retrieval.citations)

        prompt_tokens = _count_message_tokens(prompt_messages)
        completion_tokens = count_tokens(content)
        yield _completed_event(
            ChatResponse(
                request_id=request.request_id,
                conversation_id=request.conversation_id,
                message_id=request.message_id,
                assistant_message=AssistantMessage(content=content),
                citations=citations,
                citation_anchors=parser.anchors,
                refusal=False,
                finish_reason="stop",
                completed_at=datetime.now(timezone.utc).isoformat(),
                intent="knowledge_query",
                answer_disposition="grounded_answer",
                answer_policy_version="answer_policy_v1",
                execution=self._execution_observation(retrieval, generation_performed=True, model_id=request.model_id),
                used_citation_ranks=used_ranks,
                retrieval_performed=True,
                generation_performed=True,
                knowledge_used=True,
                **_knowledge_provenance(request),
                usage=TokenUsage(
                    prompt_tokens=prompt_tokens,
                    completion_tokens=completion_tokens,
                    total_tokens=prompt_tokens + completion_tokens,
                ),
            )
        )

    def _execute_a1_policy(
        self,
        request: ChatRequest,
        *,
        resolution: ContextResolution | None,
        force_clarification: bool,
    ) -> ChatResponse:
        if request.answer_mode != "normal" or request.intent != "knowledge_query":
            raise AnswerPolicyError("answer policy is only available for normal knowledge requests")

        scope_decision, scope_usage = self._evaluate_scope_policy(
            request,
            resolution=resolution,
            force_clarification=force_clarification,
        )
        if scope_decision.action == "clarification_required":
            return self._a1_clarification_response(
                request,
                scope_decision,
                scope_usage,
                post_retrieval=False,
            )

        retrieval_query = (
            scope_decision.resolved_query
            if scope_decision.pending_action == "resolved"
            else resolution.resolved_query
            if resolution and resolution.kind == "follow_up"
            else None
        )
        retrieval = self._retrieval_pipeline.execute(request, query_override=retrieval_query)
        if retrieval.refusal_reason is not None or not retrieval.citations:
            return self._a1_insufficient_evidence_response(request, retrieval, scope_decision, scope_usage)

        prompt_messages = self._assemble_messages(request, retrieval.context_text)
        final_generation = self._model_client.generate(
            model_id=request.model_id,
            messages=prompt_messages,
            user_query=request.user_query,
            citations=retrieval.citations,
            temperature=request.response_overrides.temperature if request.response_overrides else None,
            top_p=request.response_overrides.top_p if request.response_overrides else None,
            max_output_tokens=request.response_overrides.max_output_tokens if request.response_overrides else None,
            json_mode=True,
        )
        decision = parse_answer_policy(final_generation.content)
        combined_usage = TokenUsage(
            prompt_tokens=scope_usage.prompt_tokens + final_generation.prompt_tokens,
            completion_tokens=scope_usage.completion_tokens + final_generation.completion_tokens,
            total_tokens=scope_usage.total_tokens + final_generation.total_tokens,
        )
        if decision.answer_disposition == "clarification_required":
            return self._a1_clarification_response(
                request,
                scope_decision,
                combined_usage,
                clarification=decision.clarification,
                post_retrieval=True,
                retrieval=retrieval,
            )
        if decision.answer_disposition == "insufficient_evidence":
            return self._a1_insufficient_evidence_response(
                request,
                retrieval,
                scope_decision,
                combined_usage,
                policy_call_count=2,
            )

        content = decision.content
        if content is None:
            raise AnswerPolicyError("answer policy omitted answer content")
        parsed, citations, used_ranks = _parse_a1_cited_answer(content, retrieval.citations)
        return ChatResponse(
            request_id=request.request_id,
            conversation_id=request.conversation_id,
            message_id=request.message_id,
            assistant_message=AssistantMessage(content=parsed.content),
            citations=citations,
            citation_anchors=parsed.anchors,
            refusal=False,
            finish_reason=final_generation.finish_reason,
            completed_at=datetime.now(timezone.utc).isoformat(),
            intent="knowledge_query",
            answer_disposition=decision.answer_disposition,
            answer_policy_version=request.answer_policy_version,
            policy_evaluation_performed=True,
            answer_generation_performed=True,
            coverage=decision.coverage,
            resolution=_answer_resolution(request, scope_decision),
            resolved_query=_resolved_query(scope_decision),
            retrieval_candidates=_retrieval_candidates(retrieval.citations),
            execution=self._execution_observation(
                retrieval,
                generation_performed=True,
                model_id=request.model_id,
                policy_performed=True,
                policy_call_count=2,
                scope_decision=scope_decision,
            ),
            used_citation_ranks=used_ranks,
            retrieval_performed=True,
            generation_performed=True,
            knowledge_used=True,
            **_knowledge_provenance(request),
            usage=combined_usage,
        )

    def _evaluate_scope_policy(
        self,
        request: ChatRequest,
        *,
        resolution: ContextResolution | None,
        force_clarification: bool,
    ) -> tuple[ScopePolicyDecision, ModelClientResult]:
        history = [
            {"message_id": message.message_id, "role": message.role, "content": message.content}
            for message in request.messages[-6:]
            if message.content.strip()
        ]
        payload = {
            "history": history,
            "current_question": request.user_query,
            "context_resolution": resolution.model_dump(mode="json") if resolution else None,
            "pending_clarification": request.pending_clarification.model_dump(mode="json") if request.pending_clarification else None,
            "force_clarification": force_clarification,
        }
        generation = self._model_client.generate(
            model_id=request.model_id,
            messages=[
                {"role": "system", "content": self._scope_policy_prompt_for(request)},
                {"role": "user", "content": json.dumps(payload, ensure_ascii=False)},
            ],
            user_query=request.user_query,
            citations=[],
            temperature=0.0,
            max_output_tokens=320,
            json_mode=True,
            json_schema=scope_policy_json_schema(
                has_pending_clarification=request.pending_clarification is not None,
                requires_scope_assessment=_a1_policy_pair(request) == _A1_SCOPE_ASSESSMENT_PAIR,
                requires_scope_shape=_a1_policy_pair(request) in _A1_SCOPE_SHAPE_PAIRS,
            ),
        )
        decision = parse_scope_policy(
            generation.content,
            requires_scope_assessment=_a1_policy_pair(request) == _A1_SCOPE_ASSESSMENT_PAIR,
            requires_scope_shape=_a1_policy_pair(request) in _A1_SCOPE_SHAPE_PAIRS,
        )
        self._validate_scope_policy_request(request, decision, force_clarification=force_clarification)
        return decision, generation

    @staticmethod
    def _validate_scope_policy_request(
        request: ChatRequest,
        decision: ScopePolicyDecision,
        *,
        force_clarification: bool,
    ) -> None:
        if force_clarification and decision.action != "clarification_required":
            raise AnswerPolicyError("scope policy ignored the forced clarification route")
        if request.pending_clarification is None:
            if decision.pending_action is not None or decision.resolved_query is not None:
                raise AnswerPolicyError("scope policy referenced a missing pending clarification")
            return
        if decision.pending_action is None:
            raise AnswerPolicyError("scope policy did not dispose of the pending clarification")
        if decision.pending_action == "needs_more" and decision.action != "clarification_required":
            raise AnswerPolicyError("needs_more pending action requires clarification")
        if decision.pending_action == "abandoned" and decision.resolved_query is not None:
            raise AnswerPolicyError("abandoned pending action cannot contain resolved_query")

    def _a1_clarification_response(
        self,
        request: ChatRequest,
        scope_decision: ScopePolicyDecision | ScopeShapeDecision,
        usage: TokenUsage | ModelClientResult,
        *,
        clarification: Clarification | None = None,
        post_retrieval: bool,
        retrieval: RetrievalResult | None = None,
    ) -> ChatResponse:
        selected_clarification = clarification or scope_decision.clarification
        if selected_clarification is None:
            raise AnswerPolicyError("clarification policy omitted clarification")
        token_usage = _as_usage(usage)
        return ChatResponse(
            request_id=request.request_id,
            conversation_id=request.conversation_id,
            message_id=request.message_id,
            assistant_message=AssistantMessage(content=selected_clarification.question),
            citations=[],
            citation_anchors=[],
            refusal=False,
            finish_reason="stop",
            completed_at=datetime.now(timezone.utc).isoformat(),
            intent="knowledge_query",
            answer_disposition="clarification_required",
            answer_policy_version=request.answer_policy_version,
            policy_evaluation_performed=True,
            answer_generation_performed=False,
            clarification=selected_clarification,
            resolution=_clarification_resolution(request, scope_decision),
            retrieval_candidates=_retrieval_candidates(retrieval.citations) if retrieval else [],
            execution=self._execution_observation(
                retrieval,
                generation_performed=False,
                model_id=request.model_id,
                policy_performed=True,
                policy_call_count=2 if post_retrieval else 1,
                scope_decision=scope_decision,
            ),
            used_citation_ranks=[],
            retrieval_performed=retrieval is not None,
            generation_performed=False,
            knowledge_used=False,
            **_knowledge_provenance(request),
            usage=token_usage,
        )

    def _a1_insufficient_evidence_response(
        self,
        request: ChatRequest,
        retrieval: RetrievalResult,
        scope_decision: ScopePolicyDecision | ScopeShapeDecision,
        usage: TokenUsage | ModelClientResult,
        *,
        policy_call_count: int = 1,
    ) -> ChatResponse:
        content = self._refusal_template.format(question=request.user_query)
        return ChatResponse(
            request_id=request.request_id,
            conversation_id=request.conversation_id,
            message_id=request.message_id,
            assistant_message=AssistantMessage(content=content),
            citations=[],
            citation_anchors=[],
            refusal=True,
            finish_reason="refusal",
            completed_at=datetime.now(timezone.utc).isoformat(),
            intent="knowledge_query",
            answer_disposition="insufficient_evidence",
            answer_policy_version=request.answer_policy_version,
            policy_evaluation_performed=True,
            answer_generation_performed=False,
            resolution=_answer_resolution(request, scope_decision),
            retrieval_candidates=[],
            execution=self._execution_observation(
                retrieval,
                generation_performed=False,
                model_id=request.model_id,
                policy_performed=True,
                policy_call_count=policy_call_count,
                scope_decision=scope_decision,
            ),
            used_citation_ranks=[],
            retrieval_performed=True,
            generation_performed=False,
            knowledge_used=False,
            **_knowledge_provenance(request),
            usage=_as_usage(usage),
        )

    def _stream_disposition_protocol(
        self,
        request: ChatRequest,
        retrieval: RetrievalResult,
        prompt_messages: list[dict[str, str]],
    ) -> Iterator[ChatStreamEvent]:
        """Keep the protocol marker private before selecting a safe visible response."""
        raw_content = "".join(
            delta
            for delta in self._model_client.stream_generate(
                model_id=request.model_id,
                messages=prompt_messages,
                user_query=request.user_query,
                citations=retrieval.citations,
                temperature=request.response_overrides.temperature if request.response_overrides else None,
                top_p=request.response_overrides.top_p if request.response_overrides else None,
                max_output_tokens=request.response_overrides.max_output_tokens if request.response_overrides else None,
            )
            if delta
        ).strip()
        if not raw_content:
            raise ModelClientError("model stream response was empty")

        generation = _stream_generation_result(raw_content, prompt_messages)
        if _model_signalled_insufficient_evidence(raw_content):
            yield from _response_events(
                request,
                _model_insufficient_evidence_response(
                    request,
                    datetime.now(timezone.utc).isoformat(),
                    generation,
                    self._refusal_template,
                    self._execution_observation(retrieval, generation_performed=True, model_id=request.model_id),
                ),
            )
            return

        parsed = parse_citation_anchors(raw_content, retrieval.citations)
        citations, used_ranks = _visible_evidence(parsed.anchors, retrieval.citations)
        response = ChatResponse(
            request_id=request.request_id,
            conversation_id=request.conversation_id,
            message_id=request.message_id,
            assistant_message=AssistantMessage(content=parsed.content),
            citations=citations,
            citation_anchors=parsed.anchors,
            refusal=False,
            finish_reason="stop",
            completed_at=datetime.now(timezone.utc).isoformat(),
            intent="knowledge_query",
            answer_disposition="grounded_answer",
            answer_policy_version="answer_policy_v1",
            execution=self._execution_observation(retrieval, generation_performed=True, model_id=request.model_id),
            used_citation_ranks=used_ranks,
            retrieval_performed=True,
            generation_performed=True,
            knowledge_used=True,
            **_knowledge_provenance(request),
            usage=TokenUsage(
                prompt_tokens=generation.prompt_tokens,
                completion_tokens=generation.completion_tokens,
                total_tokens=generation.total_tokens,
            ),
        )
        yield from _response_events(request, response)

    def _execute_revalidation(
        self,
        request: ChatRequest,
        retrieval: RetrievalResult,
        completed_at: str,
    ) -> ChatResponse:
        if retrieval.refusal_reason is not None or not retrieval.citations:
            return _insufficient_evidence_response(
                request,
                retrieval.citations,
                completed_at,
                execution=self._execution_observation(retrieval, generation_performed=False, model_id=request.model_id),
            )

        temperature = request.response_overrides.temperature if request.response_overrides else None
        top_p = request.response_overrides.top_p if request.response_overrides else None
        max_output_tokens = request.response_overrides.max_output_tokens if request.response_overrides else None
        prompt_messages = self._assemble_revalidation_messages(request, retrieval.context_text)
        generation = self._model_client.generate(
            model_id=request.model_id,
            messages=prompt_messages,
            user_query=request.user_query,
            citations=retrieval.citations,
            temperature=temperature,
            top_p=top_p,
            max_output_tokens=max_output_tokens,
        )
        verdict = _parse_revalidation_verdict(generation.content)
        if verdict is None:
            return _insufficient_evidence_response(
                request,
                retrieval.citations,
                completed_at,
                usage=TokenUsage(
                    prompt_tokens=generation.prompt_tokens,
                    completion_tokens=generation.completion_tokens,
                    total_tokens=generation.total_tokens,
                ),
                execution=self._execution_observation(retrieval, generation_performed=True, model_id=request.model_id),
            )

        outcome, content = verdict
        parsed = parse_citation_anchors(content, retrieval.citations)
        citations, used_ranks = _visible_evidence(parsed.anchors, retrieval.citations)
        return ChatResponse(
            request_id=request.request_id,
            conversation_id=request.conversation_id,
            message_id=request.message_id,
            assistant_message=AssistantMessage(content=parsed.content),
            citations=citations,
            citation_anchors=parsed.anchors,
            refusal=False,
            finish_reason=generation.finish_reason,
            completed_at=completed_at,
            intent="knowledge_query",
            answer_disposition="grounded_answer",
            answer_policy_version="answer_policy_v1",
            execution=self._execution_observation(retrieval, generation_performed=True, model_id=request.model_id),
            used_citation_ranks=used_ranks,
            retrieval_performed=True,
            generation_performed=True,
            knowledge_used=True,
            **_knowledge_provenance(request),
            revalidation_outcome=outcome,
            usage=TokenUsage(
                prompt_tokens=generation.prompt_tokens,
                completion_tokens=generation.completion_tokens,
                total_tokens=generation.total_tokens,
            ),
        )

    def _stream_revalidation(
        self,
        request: ChatRequest,
        retrieval: RetrievalResult,
    ) -> Iterator[ChatStreamEvent]:
        if retrieval.refusal_reason is not None or not retrieval.citations:
            response = _insufficient_evidence_response(
                request,
                retrieval.citations,
                datetime.now(timezone.utc).isoformat(),
                execution=self._execution_observation(retrieval, generation_performed=False, model_id=request.model_id),
            )
            yield from _response_events(request, response)
            return

        temperature = request.response_overrides.temperature if request.response_overrides else None
        top_p = request.response_overrides.top_p if request.response_overrides else None
        max_output_tokens = request.response_overrides.max_output_tokens if request.response_overrides else None
        prompt_messages = self._assemble_revalidation_messages(request, retrieval.context_text)
        raw_content = "".join(
            delta
            for delta in self._model_client.stream_generate(
                model_id=request.model_id,
                messages=prompt_messages,
                user_query=request.user_query,
                citations=retrieval.citations,
                temperature=temperature,
                top_p=top_p,
                max_output_tokens=max_output_tokens,
            )
            if delta
        ).strip()
        if not raw_content:
            raise ModelClientError("model stream response was empty")

        verdict = _parse_revalidation_verdict(raw_content)
        if verdict is None:
            response = _insufficient_evidence_response(
                request,
                retrieval.citations,
                datetime.now(timezone.utc).isoformat(),
                usage=TokenUsage(
                    prompt_tokens=_count_message_tokens(prompt_messages),
                    completion_tokens=count_tokens(raw_content),
                    total_tokens=_count_message_tokens(prompt_messages) + count_tokens(raw_content),
                ),
                execution=self._execution_observation(retrieval, generation_performed=True, model_id=request.model_id),
            )
        else:
            outcome, content = verdict
            parsed = parse_citation_anchors(content, retrieval.citations)
            citations, used_ranks = _visible_evidence(parsed.anchors, retrieval.citations)
            prompt_tokens = _count_message_tokens(prompt_messages)
            completion_tokens = count_tokens(content)
            response = ChatResponse(
                request_id=request.request_id,
                conversation_id=request.conversation_id,
                message_id=request.message_id,
                assistant_message=AssistantMessage(content=parsed.content),
                citations=citations,
                citation_anchors=parsed.anchors,
                refusal=False,
                finish_reason="stop",
                completed_at=datetime.now(timezone.utc).isoformat(),
                intent="knowledge_query",
                answer_disposition="grounded_answer",
                answer_policy_version="answer_policy_v1",
                execution=self._execution_observation(retrieval, generation_performed=True, model_id=request.model_id),
                used_citation_ranks=used_ranks,
                retrieval_performed=True,
                generation_performed=True,
                knowledge_used=True,
                **_knowledge_provenance(request),
                revalidation_outcome=outcome,
                usage=TokenUsage(
                    prompt_tokens=prompt_tokens,
                    completion_tokens=completion_tokens,
                    total_tokens=prompt_tokens + completion_tokens,
                ),
            )
        yield from _response_events(request, response)

    def _assemble_messages(self, request: ChatRequest, context_text: str) -> list[dict[str, str]]:
        history_lines = [
            f"{message.role}: {message.content}"
            for message in request.messages[-6:]
            if message.content.strip()
        ]
        history_block = "\n".join(history_lines) if history_lines else "No prior conversation history."
        user_prompt = (
            "[Conversation History]\n"
            f"{history_block}\n\n"
            "[Retrieved Context]\n"
            f"{context_text}\n\n"
            "[Current User Question]\n"
            f"{request.user_query}"
        )
        return [
            {"role": "system", "content": self._system_prompt_for(request)},
            {"role": "user", "content": user_prompt},
        ]

    def _execution_observation(
        self,
        retrieval: RetrievalResult | None,
        *,
        generation_performed: bool,
        model_id: str,
        policy_performed: bool = True,
        policy_call_count: int | None = None,
        scope_decision: ScopePolicyDecision | ScopeShapeDecision | None = None,
    ) -> ExecutionObservation:
        if retrieval is None:
            retrieval_observation = RetrievalObservation(status="not_run")
        else:
            strategy, embedding_model, collection = self._retrieval_pipeline.execution_profile()
            retrieval_observation = RetrievalObservation(
                status="performed",
                strategy=strategy,
                embedding_model=embedding_model,
                collection=collection,
                candidate_count=len(retrieval.citations),
                top_k=retrieval.top_k,
                rerank_enabled=retrieval.rerank_enabled,
                evidence_min_score=retrieval.evidence_min_score,
            )
        generation_observation = GenerationObservation(
            status="performed" if generation_performed else "not_run",
            provider=getattr(self._model_client, "provider_name", "test_injected") if generation_performed else None,
            model_id=model_id if generation_performed else None,
        )
        scope_evidence: dict[str, Any] = {}
        if isinstance(scope_decision, ScopeShapeDecision):
            scope_evidence = {
                "scope_shape": scope_decision.scope_shape,
                "retrieval_target": scope_decision.retrieval_target,
                "requested_fact_fields": scope_decision.requested_fact_fields,
                "unresolved_branch_dimensions": scope_decision.unresolved_branch_dimensions,
                "untrusted_instruction_present": scope_decision.untrusted_instruction_present,
            }
        return ExecutionObservation(
            policy="performed" if policy_performed else "not_run",
            policy_provider=getattr(self._model_client, "provider_name", "test_injected") if policy_performed and policy_call_count is not None else None,
            policy_model_id=model_id if policy_performed and policy_call_count is not None else None,
            policy_call_count=policy_call_count,
            retrieval=retrieval_observation,
            generation=generation_observation,
            fallback_used=False,
            **scope_evidence,
        )

    def _system_prompt_for(self, request: ChatRequest) -> str:
        version = request.response_overrides.system_prompt_version if request.response_overrides else None
        prompt = self._system_prompts.get(version or "prompt_v1")
        if prompt is None:
            raise ModelClientError(f"unknown system_prompt_version: {version}")
        style = request.response_overrides.response_style if request.response_overrides else None
        return f"{prompt}\n\n[Response Style]\n{style.strip()}" if style and style.strip() else prompt

    def _scope_policy_prompt_for(self, request: ChatRequest) -> str:
        pair = _a1_policy_pair(request)
        if pair is None:
            raise AnswerPolicyError("scope policy is unavailable without a registered A1 policy pair")
        return self._scope_policy_prompts[pair]

    @staticmethod
    def _assemble_revalidation_messages(request: ChatRequest, context_text: str) -> list[dict[str, str]]:
        context = request.revalidation_context
        if context is None:
            raise KnowledgeScopeError("revalidation context is unavailable")
        original_revisions = ", ".join(context.original_citation_revision_ids) or "None recorded."
        update_events = ", ".join(context.related_update_event_ids) or "None recorded."
        user_prompt = (
            "[Historical Answer Under Review - Not Current Truth]\n"
            f"original_message_id={context.original_message_id}\n"
            f"original_snapshot_id={context.original_snapshot_id}\n"
            f"original_answer={context.original_answer}\n\n"
            "[Historical Citation Revision IDs]\n"
            f"{original_revisions}\n\n"
            "[Related Knowledge Update Event IDs]\n"
            f"{update_events}\n\n"
            "[Latest Published Knowledge Context]\n"
            f"{context_text}\n\n"
            "[Review Task]\n"
            "Assess the historical answer only against the latest published context. "
            "Do not present the historical answer itself as current knowledge."
        )
        return [
            {"role": "system", "content": _REVALIDATION_SYSTEM_PROMPT},
            {"role": "user", "content": user_prompt},
        ]


def _delta_event(request: ChatRequest, delta: str) -> ChatStreamEvent:
    return ChatStreamEvent(
        event="message.delta",
        payload={
            "request_id": request.request_id,
            "message_id": request.message_id,
            "delta": delta,
        },
    )


def _knowledge_provenance(request: ChatRequest) -> dict[str, str]:
    if request.knowledge_scope:
        return {
            "knowledge_snapshot_id": request.knowledge_scope.snapshot_id,
            "knowledge_snapshot_manifest_hash": request.knowledge_scope.manifest_hash,
            "freshness_status": "revalidated" if request.answer_mode == "revalidate" else "current",
        }
    return {"freshness_status": "unknown"}


def _controlled_response(request: ChatRequest, completed_at: str) -> ChatResponse:
    content, refusal, finish_reason = _CONTROLLED_RESPONSES[request.intent]
    completion_tokens = count_tokens(content)
    is_a1_candidate = _uses_a1_policy(request)
    response = ChatResponse(
        request_id=request.request_id,
        conversation_id=request.conversation_id,
        message_id=request.message_id,
        assistant_message=AssistantMessage(content=content),
        citations=[],
        citation_anchors=[],
        refusal=refusal,
        finish_reason=finish_reason,
        completed_at=completed_at,
        intent=request.intent,
        answer_disposition="controlled_response",
        execution=ExecutionObservation(
            policy="not_run" if is_a1_candidate else "performed",
            retrieval=RetrievalObservation(status="not_run"),
            generation=GenerationObservation(status="not_run"),
            fallback_used=False,
        ),
        **(
            {
                "policy_evaluation_performed": False,
                "answer_generation_performed": False,
            }
            if is_a1_candidate
            else {}
        ),
        used_citation_ranks=[],
        retrieval_performed=False,
        generation_performed=False,
        knowledge_used=False,
        usage=TokenUsage(prompt_tokens=0, completion_tokens=completion_tokens, total_tokens=completion_tokens),
    )
    return response


def _clarification_response(request: ChatRequest, resolution: ContextResolution) -> ChatResponse:
    content = "请说明你指的是哪项制度、流程或具体事项，我再继续查询。"
    tokens = count_tokens(content)
    return ChatResponse(
        request_id=request.request_id, conversation_id=request.conversation_id, message_id=request.message_id,
        assistant_message=AssistantMessage(content=content), citations=[], citation_anchors=[], refusal=False, finish_reason="stop",
        completed_at=datetime.now(timezone.utc).isoformat(), intent="knowledge_query", answer_disposition="clarification_required",
        execution=ExecutionObservation(policy="performed", retrieval=RetrievalObservation(status="not_run"), generation=GenerationObservation(status="not_run"), fallback_used=False),
        used_citation_ranks=[], retrieval_performed=False, generation_performed=False, knowledge_used=False,
        context_resolution=resolution, usage=TokenUsage(prompt_tokens=0, completion_tokens=tokens, total_tokens=tokens),
    )


def _parse_revalidation_verdict(content: str) -> tuple[str, str] | None:
    matches = list(_REVALIDATION_VERDICT_RE.finditer(content))
    if len(matches) != 1:
        return None
    stripped = _REVALIDATION_VERDICT_RE.sub("", content).strip()
    stripped = re.sub(r"[ \t]{2,}", " ", stripped)
    if not stripped:
        return None
    return matches[0].group(1).lower(), stripped


def _uses_disposition_protocol(request: ChatRequest) -> bool:
    version = request.response_overrides.system_prompt_version if request.response_overrides else None
    return version in {"prompt_v3", "prompt_v4", "prompt_v5", "prompt_v6", "prompt_v7"}


def _uses_a1_policy(request: ChatRequest) -> bool:
    return _a1_policy_pair(request) is not None


def _a1_policy_pair(request: ChatRequest) -> tuple[str, str] | None:
    if request.answer_mode != "normal":
        return None
    prompt_version = request.response_overrides.system_prompt_version if request.response_overrides else None
    normalized_prompt_version = prompt_version or "prompt_v1"
    answer_policy_version = (request.answer_policy_version or "").strip()
    pair = (normalized_prompt_version, answer_policy_version)
    if pair in _A1_POLICY_PAIRS:
        return pair
    if not answer_policy_version and normalized_prompt_version in _LEGACY_PROMPT_VERSIONS:
        return None
    raise AnswerPolicyError(
        "unsupported answer policy and system prompt version pair"
    )


def _as_usage(value: TokenUsage | ModelClientResult) -> TokenUsage:
    if isinstance(value, TokenUsage):
        return value
    return TokenUsage(
        prompt_tokens=value.prompt_tokens,
        completion_tokens=value.completion_tokens,
        total_tokens=value.total_tokens,
    )


def _retrieval_candidates(citations: list[RetrievedSource]) -> list[RetrievalCandidate]:
    candidates: list[RetrievalCandidate] = []
    for citation in citations:
        if (
            not citation.document_id
            or not citation.revision_id
            or not citation.chunk_content_sha256
            or not citation.knowledge_snapshot_id
        ):
            raise AnswerPolicyError("retrieval candidate is missing immutable provenance")
        candidates.append(
            RetrievalCandidate(
                chunk_id=citation.chunk_id,
                file_id=citation.file_id,
                document_id=citation.document_id,
                revision_id=citation.revision_id,
                chunk_content_sha256=citation.chunk_content_sha256,
                knowledge_snapshot_id=citation.knowledge_snapshot_id,
                rank=citation.rank,
                score=citation.score,
                source_locator=citation.source_locator,
            )
        )
    return candidates


def _parse_a1_cited_answer(
    content: str,
    candidates: list[RetrievedSource],
) -> tuple[CitationAnchorParseResult, list[RetrievedSource], list[int]]:
    referenced_ranks = {int(match.group(1)) for match in re.finditer(r"\[\[cite:(\d+)\]\]", content)}
    available_ranks = {candidate.rank for candidate in candidates}
    if not referenced_ranks or not referenced_ranks.issubset(available_ranks):
        raise AnswerPolicyError("answer policy contains an unavailable citation rank")
    parsed = parse_citation_anchors(content, candidates)
    if "[[cite:" in parsed.content:
        raise AnswerPolicyError("answer policy leaked a malformed citation marker")
    try:
        citations, used_ranks = _visible_evidence(parsed.anchors, candidates)
    except ModelClientError as exc:
        raise AnswerPolicyError("answer policy has no valid visible citation anchors") from exc
    return parsed, citations, used_ranks


def _resolved_query(scope_decision: ScopePolicyDecision) -> str | None:
    return scope_decision.resolved_query if scope_decision.pending_action == "resolved" else None


def _answer_resolution(
    request: ChatRequest,
    scope_decision: ScopePolicyDecision,
) -> ClarificationResolution | None:
    pending = request.pending_clarification
    if pending is None:
        return None
    if scope_decision.pending_action not in {"resolved", "abandoned"}:
        raise AnswerPolicyError("answer policy cannot complete a needs_more pending clarification")
    return ClarificationResolution(
        pending_clarification_id=pending.clarification_id,
        pending_state_version=pending.state_version,
        action=scope_decision.pending_action,
        resolved_query=scope_decision.resolved_query if scope_decision.pending_action == "resolved" else None,
    )


def _clarification_resolution(
    request: ChatRequest,
    scope_decision: ScopePolicyDecision,
) -> ClarificationResolution | None:
    pending = request.pending_clarification
    if pending is None:
        return None
    action = "needs_more" if scope_decision.pending_action == "resolved" else scope_decision.pending_action
    if action not in {"abandoned", "needs_more"}:
        raise AnswerPolicyError("clarification policy has no pending clarification action")
    return ClarificationResolution(
        pending_clarification_id=pending.clarification_id,
        pending_state_version=pending.state_version,
        action=action,
    )


def _model_signalled_insufficient_evidence(content: str) -> bool:
    markers = list(_INSUFFICIENT_EVIDENCE_MARKER_RE.finditer(content))
    if not markers:
        return False
    if len(markers) != 1 or markers[0].start() != 0:
        raise ModelClientError("model insufficient-evidence disposition marker is malformed")
    if "[[cite:" in content.lower():
        raise ModelClientError("model insufficient-evidence disposition must not contain citation markers")
    return True


def _stream_generation_result(content: str, messages: list[dict[str, str]]) -> ModelClientResult:
    prompt_tokens = _count_message_tokens(messages)
    completion_tokens = count_tokens(content)
    return ModelClientResult(
        content=content,
        finish_reason="stop",
        prompt_tokens=prompt_tokens,
        completion_tokens=completion_tokens,
        total_tokens=prompt_tokens + completion_tokens,
    )


def _model_insufficient_evidence_response(
    request: ChatRequest,
    completed_at: str,
    generation: ModelClientResult,
    refusal_template: str,
    execution: ExecutionObservation,
) -> ChatResponse:
    content = refusal_template.format(question=request.user_query)
    return ChatResponse(
        request_id=request.request_id,
        conversation_id=request.conversation_id,
        message_id=request.message_id,
        assistant_message=AssistantMessage(content=content),
        citations=[],
        citation_anchors=[],
        refusal=True,
        finish_reason="refusal",
        completed_at=completed_at,
        intent="knowledge_query",
        answer_disposition="insufficient_evidence",
        answer_policy_version="answer_policy_v1",
        execution=execution,
        used_citation_ranks=[],
        retrieval_performed=True,
        generation_performed=True,
        knowledge_used=False,
        **_knowledge_provenance(request),
        usage=TokenUsage(
            prompt_tokens=generation.prompt_tokens,
            completion_tokens=generation.completion_tokens,
            total_tokens=generation.total_tokens,
        ),
    )


def _visible_evidence(
    anchors: list[object],
    candidates: list[RetrievedSource],
) -> tuple[list[RetrievedSource], list[int]]:
    used_ranks = sorted({rank for anchor in anchors for rank in anchor.citation_ranks})
    if not used_ranks:
        raise ModelClientError("model answer contains no valid citation anchors")
    candidates_by_rank = {candidate.rank: candidate for candidate in candidates}
    if any(rank not in candidates_by_rank for rank in used_ranks):
        raise ModelClientError("model answer anchor references a retrieval candidate that is unavailable")
    return [candidates_by_rank[rank] for rank in used_ranks], used_ranks


def _insufficient_evidence_response(
    request: ChatRequest,
    citations: list[RetrievedSource],
    completed_at: str,
    *,
    usage: TokenUsage | None = None,
    execution: ExecutionObservation,
) -> ChatResponse:
    response_usage = usage or TokenUsage(
        prompt_tokens=count_tokens(request.user_query),
        completion_tokens=count_tokens(_REVALIDATION_INSUFFICIENT_EVIDENCE),
        total_tokens=count_tokens(request.user_query) + count_tokens(_REVALIDATION_INSUFFICIENT_EVIDENCE),
    )
    return ChatResponse(
        request_id=request.request_id,
        conversation_id=request.conversation_id,
        message_id=request.message_id,
        assistant_message=AssistantMessage(content=_REVALIDATION_INSUFFICIENT_EVIDENCE),
        citations=[],
        refusal=False,
        finish_reason="stop",
        completed_at=completed_at,
        intent="knowledge_query",
        answer_disposition="insufficient_evidence",
        execution=execution,
        used_citation_ranks=[],
        retrieval_performed=True,
        generation_performed=usage is not None,
        knowledge_used=False,
        **_knowledge_provenance(request),
        revalidation_outcome="insufficient_evidence",
        usage=response_usage,
    )


def _response_events(request: ChatRequest, response: ChatResponse) -> Iterator[ChatStreamEvent]:
    for delta in _split_text_deltas(response.assistant_message.content):
        yield _delta_event(request, delta)
    yield _completed_event(response)


def _completed_event(response: ChatResponse) -> ChatStreamEvent:
    return ChatStreamEvent(
        event="message.completed",
        payload={
            "request_id": response.request_id,
            "message_id": response.message_id,
            "chat_runtime_response": response,
        },
    )


def _split_text_deltas(content: str, chunk_size: int = 12) -> Iterator[str]:
    if chunk_size < 1:
        chunk_size = 12
    for start in range(0, len(content), chunk_size):
        yield content[start : start + chunk_size]


def _count_message_tokens(messages: list[dict[str, str]]) -> int:
    return sum(count_tokens(message.get("content", "")) for message in messages)
