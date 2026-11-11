from __future__ import annotations

import json
from typing import Any, Literal

from pydantic import Field, ValidationError, model_validator

from app.adapters.model_client import ModelClientError
from app.api.schemas import Clarification, Coverage, StrictModel


class AnswerPolicyError(ModelClientError):
    """Raised when the candidate answer-policy model violates its JSON contract."""

    code = "answer_policy_invalid"
    retryable = False


class ScopePolicyDecision(StrictModel):
    action: Literal["retrieve", "clarification_required"]
    clarification: Clarification | None
    pending_action: Literal["resolved", "abandoned", "needs_more"] | None
    resolved_query: str | None = None

    @model_validator(mode="after")
    def validate_scope_decision(self) -> "ScopePolicyDecision":
        if self.action == "clarification_required":
            if self.clarification is None:
                raise ValueError("clarification_required requires clarification")
            if self.resolved_query is not None:
                raise ValueError("clarification_required cannot contain resolved_query")
        elif self.clarification is not None:
            raise ValueError("retrieve cannot contain clarification")
        if self.pending_action == "resolved":
            if self.action != "retrieve" or not self.resolved_query or not self.resolved_query.strip():
                raise ValueError("resolved pending action requires retrieve and resolved_query")
        elif self.resolved_query is not None:
            raise ValueError("resolved_query requires a resolved pending action")
        return self


class ScopeAssessment(StrictModel):
    retrieval_target: str = Field(min_length=1, max_length=1200)
    unresolved_branch_dimensions: list[str]

    @model_validator(mode="after")
    def validate_scope_assessment(self) -> "ScopeAssessment":
        if not self.retrieval_target.strip():
            raise ValueError("retrieval_target must not be blank")
        if any(not dimension.strip() for dimension in self.unresolved_branch_dimensions):
            raise ValueError("unresolved_branch_dimensions must not contain blank values")
        normalized_dimensions = [dimension.strip() for dimension in self.unresolved_branch_dimensions]
        if len(set(normalized_dimensions)) != len(normalized_dimensions):
            raise ValueError("unresolved_branch_dimensions must be unique")
        return self


class ScopePolicyDecisionV3(ScopePolicyDecision):
    scope_assessment: ScopeAssessment

    @model_validator(mode="after")
    def validate_scope_assessment_decision(self) -> "ScopePolicyDecisionV3":
        unresolved_dimensions = {
            dimension.strip() for dimension in self.scope_assessment.unresolved_branch_dimensions
        }
        if self.action == "retrieve":
            if unresolved_dimensions:
                raise ValueError("retrieve requires no unresolved branch dimensions")
            return self

        if not unresolved_dimensions:
            raise ValueError("clarification_required requires unresolved branch dimensions")
        if self.clarification is None:
            raise ValueError("clarification_required requires clarification")
        clarification_dimensions = [dimension.strip() for dimension in self.clarification.missing_dimensions]
        if any(not dimension for dimension in clarification_dimensions):
            raise ValueError("clarification missing_dimensions must not contain blank values")
        if len(set(clarification_dimensions)) != len(clarification_dimensions):
            raise ValueError("clarification missing_dimensions must be unique")
        if not set(clarification_dimensions).issubset(unresolved_dimensions):
            raise ValueError("clarification missing_dimensions must be unresolved branch dimensions")
        return self


class ScopeShapeDecision(StrictModel):
    scope_shape: Literal["fact_lookup", "branch_dependent", "unknown_target"]
    retrieval_target: str | None = Field(max_length=1200)
    requested_fact_fields: list[str]
    unresolved_branch_dimensions: list[str]
    untrusted_instruction_present: bool
    clarification: Clarification | None
    pending_action: Literal["resolved", "abandoned", "needs_more"] | None
    resolved_query: str | None = Field(max_length=600)

    @model_validator(mode="after")
    def validate_scope_shape(self) -> "ScopeShapeDecision":
        target = self.retrieval_target.strip() if self.retrieval_target else None
        fact_fields = [field.strip() for field in self.requested_fact_fields]
        branch_dimensions = [dimension.strip() for dimension in self.unresolved_branch_dimensions]
        if any(not field for field in fact_fields) or len(set(fact_fields)) != len(fact_fields):
            raise ValueError("requested_fact_fields must be non-empty and unique when present")
        if any(not dimension for dimension in branch_dimensions) or len(set(branch_dimensions)) != len(branch_dimensions):
            raise ValueError("unresolved_branch_dimensions must be non-empty and unique when present")
        if self.scope_shape == "fact_lookup":
            if not target or not fact_fields or branch_dimensions or self.clarification is not None:
                raise ValueError("fact_lookup requires target and fact fields, with no branch or clarification")
        elif self.scope_shape == "branch_dependent":
            if not target or not fact_fields or not branch_dimensions or self.clarification is None:
                raise ValueError("branch_dependent requires target, fact fields, branch dimensions and clarification")
            if not set(self.clarification.missing_dimensions).issubset(set(branch_dimensions)):
                raise ValueError("clarification missing dimensions must be branch dimensions")
        elif not branch_dimensions or self.clarification is None:
            raise ValueError("unknown_target requires branch dimensions and clarification")
        if self.pending_action == "resolved":
            if self.scope_shape != "fact_lookup" or not self.resolved_query or not self.resolved_query.strip():
                raise ValueError("resolved pending action requires fact_lookup and resolved_query")
        elif self.resolved_query is not None:
            raise ValueError("resolved_query requires a resolved pending action")
        if self.pending_action == "needs_more" and self.scope_shape == "fact_lookup":
            raise ValueError("needs_more pending action requires a missing scope dimension")
        return self

    @property
    def action(self) -> Literal["retrieve", "clarification_required"]:
        return "retrieve" if self.scope_shape == "fact_lookup" else "clarification_required"


class AnswerPolicyDecision(StrictModel):
    answer_disposition: Literal[
        "grounded_answer",
        "partial_answer",
        "clarification_required",
        "insufficient_evidence",
    ]
    content: str | None
    clarification: Clarification | None
    coverage: Coverage | None

    @model_validator(mode="after")
    def validate_answer_decision(self) -> "AnswerPolicyDecision":
        if self.answer_disposition in {"grounded_answer", "partial_answer"}:
            if not self.content or not self.content.strip():
                raise ValueError("answer disposition requires non-empty content")
            if self.clarification is not None:
                raise ValueError("answer disposition cannot contain clarification")
            if self.answer_disposition == "partial_answer":
                if self.coverage is None:
                    raise ValueError("partial_answer requires coverage")
            elif self.coverage is not None:
                raise ValueError("grounded_answer cannot contain coverage")
        elif self.answer_disposition == "clarification_required":
            if self.content is not None or self.clarification is None or self.coverage is not None:
                raise ValueError("clarification_required requires clarification only")
        elif self.content is not None or self.clarification is not None or self.coverage is not None:
            raise ValueError("insufficient_evidence cannot contain content, clarification or coverage")
        return self


def parse_scope_policy(
    content: str,
    *,
    requires_scope_assessment: bool = False,
    requires_scope_shape: bool = False,
) -> ScopePolicyDecision | ScopePolicyDecisionV3 | ScopeShapeDecision:
    if requires_scope_assessment and requires_scope_shape:
        raise AnswerPolicyError("scope policy cannot require both v3 assessment and v4 shape")
    model = (
        ScopeShapeDecision
        if requires_scope_shape
        else ScopePolicyDecisionV3
        if requires_scope_assessment
        else ScopePolicyDecision
    )
    return _parse_exact_json(content, model, "scope policy")


def parse_answer_policy(content: str) -> AnswerPolicyDecision:
    return _parse_exact_json(content, AnswerPolicyDecision, "answer policy")


def scope_policy_json_schema(
    *,
    has_pending_clarification: bool,
    requires_scope_assessment: bool = False,
    requires_scope_shape: bool = False,
) -> dict[str, Any]:
    if requires_scope_assessment and requires_scope_shape:
        raise AnswerPolicyError("scope policy cannot require both v3 assessment and v4 shape")
    model = (
        ScopeShapeDecision
        if requires_scope_shape
        else ScopePolicyDecisionV3
        if requires_scope_assessment
        else ScopePolicyDecision
    )
    schema = model.model_json_schema()
    if has_pending_clarification:
        return schema
    properties = schema["properties"]
    properties["pending_action"] = {"type": "null"}
    properties["resolved_query"] = {"type": "null"}
    return schema


def _parse_exact_json[T: StrictModel](content: str, model: type[T], label: str) -> T:
    try:
        decoded = json.loads(content)
        if not isinstance(decoded, dict):
            raise ValueError("must be a JSON object")
        return model.model_validate(decoded)
    except (json.JSONDecodeError, ValidationError, ValueError) as exc:
        raise AnswerPolicyError(f"{label} returned an invalid structured result") from exc
