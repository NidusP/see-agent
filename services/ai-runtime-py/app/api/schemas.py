from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator

type SourceFormat = Literal["txt", "md", "json", "pdf", "docx", "xlsx", "pptx"]
type ConversationRole = Literal["system", "user", "assistant"]
type AssistantRole = Literal["assistant"]
type FinishReason = Literal["stop", "length", "refusal", "error"]
type AnswerMode = Literal["normal", "revalidate"]
type FreshnessStatus = Literal["current", "possibly_stale", "revalidated", "unknown"]
type RevalidationOutcome = Literal["still_valid", "needs_update", "insufficient_evidence"]
type AnswerDisposition = Literal[
    "grounded_answer",
    "partial_answer",
    "clarification_required",
    "insufficient_evidence",
    "controlled_response",
]
type ExecutionStatus = Literal["performed", "not_run"]
type ScopeShape = Literal["fact_lookup", "branch_dependent", "unknown_target"]
type ClarificationResolutionAction = Literal["resolved", "abandoned", "needs_more"]
type ResolvedChatIntent = Literal[
    "knowledge_query",
    "social_greeting",
    "social_thanks",
    "social_farewell",
    "capability_help",
    "off_topic",
    "out_of_scope",
]
type ChatRequestIntent = Literal[
    "context_routing_required",
    "knowledge_query",
    "social_greeting",
    "social_thanks",
    "social_farewell",
    "capability_help",
    "off_topic",
    "out_of_scope",
]
# Retains the historic lexical identifier only so old immutable conversations
# can be decoded and then fail closed against the native-only active profile.
type RetrievalStrategy = Literal["lexical_compat_v1", "native_dense_v1"]


class StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class HealthResponse(StrictModel):
    status: str
    service: str
    timestamp: str


class IngestionPreviewRequest(StrictModel):
    file_id: str
    source_format: SourceFormat
    object_uri: str


class IngestionPreviewResponse(StrictModel):
    file_id: str
    source_format: SourceFormat
    parser_strategy: str
    supported: bool
    locator_fields: list[str] = Field(default_factory=list)
    preview_metadata: dict[str, Any] = Field(default_factory=dict)
    estimated_blocks: int | None = None
    estimated_chunks: int | None = None
    warnings: list[str] = Field(default_factory=list)


class IngestionOptions(StrictModel):
    enable_ocr_fallback: bool = True
    include_pptx_notes: bool = False
    normalize_json_paths: bool = True


class IngestionRunRequest(StrictModel):
    request_id: str
    task_id: str
    file_id: str
    document_id: str | None = None
    revision_id: str | None = None
    delivery_id: str | None = None
    attempt: int | None = Field(default=None, ge=1)
    filename: str
    mime_type: str
    source_format: SourceFormat
    object_uri: str
    object_size_bytes: int | None = Field(default=None, ge=0)
    checksum_sha256: str | None = None
    effective_at: str | None = None
    parse_version: str
    index_version: str
    requested_at: str
    requested_by: str | None = None
    preview_only: bool = False
    force_reindex: bool = False
    ingestion_options: IngestionOptions | None = None
    status_callback_url: str | None = None

    @model_validator(mode="after")
    def require_revision_identity(self) -> "IngestionRunRequest":
        if self.revision_id and (not self.document_id or not self.checksum_sha256):
            raise ValueError("revision_id requires document_id and checksum_sha256")
        if self.revision_id and not self.delivery_id:
            raise ValueError("revision_id requires delivery_id for K3 delivery")
        return self


class ErrorSummary(StrictModel):
    code: str
    message: str
    retryable: bool


class IngestionRunResponse(StrictModel):
    request_id: str
    task_id: str
    file_id: str
    document_id: str | None = None
    revision_id: str | None = None
    delivery_id: str | None = None
    status: str
    updated_at: str
    started_at: str | None = None
    completed_at: str | None = None
    chunks_created: int | None = None
    pages_processed: int | None = None
    chunk_manifest_sha256: str | None = None
    embedding_model: str | None = None
    warning_messages: list[str] | None = None
    error: ErrorSummary | None = None

    @model_validator(mode="after")
    def require_revision_success_manifest(self) -> "IngestionRunResponse":
        if self.revision_id and self.status == "succeeded" and (
            not self.document_id
            or not self.delivery_id
            or not self.chunk_manifest_sha256
            or not self.embedding_model
        ):
            raise ValueError(
                "successful revision result requires document_id, delivery_id, "
                "chunk_manifest_sha256 and embedding_model"
            )
        return self


class SourceLocator(StrictModel):
    source_format: SourceFormat
    page_no: int | None = Field(default=None, ge=1)
    heading_path: list[str] | None = None
    paragraph_index: int | None = Field(default=None, ge=0)
    sheet_name: str | None = None
    cell_range: str | None = None
    slide_no: int | None = Field(default=None, ge=1)
    slide_title: str | None = None
    block_label: str | None = None
    json_path: str | None = None


class RetrievedSource(StrictModel):
    chunk_id: str
    file_id: str
    filename: str
    document_id: str | None = None
    revision_id: str | None = None
    chunk_content_sha256: str | None = None
    knowledge_snapshot_id: str | None = None
    rank: int = Field(ge=1)
    score: float
    citation_label: str
    source_locator: SourceLocator
    snippet: str


class CitationAnchor(StrictModel):
    start_rune: int = Field(ge=0)
    end_rune: int = Field(ge=1)
    citation_ranks: list[int] = Field(min_length=1)

    @model_validator(mode="after")
    def validate_anchor(self) -> "CitationAnchor":
        if self.end_rune <= self.start_rune:
            raise ValueError("end_rune must be greater than start_rune")
        if any(rank < 1 for rank in self.citation_ranks):
            raise ValueError("citation_ranks must be positive")
        if len(set(self.citation_ranks)) != len(self.citation_ranks):
            raise ValueError("citation_ranks must be unique")
        return self


class ConversationMessage(StrictModel):
    message_id: str
    role: ConversationRole
    content: str
    created_at: str | None = None


class SourceFilters(StrictModel):
    file_ids: list[str] | None = None
    source_formats: list[SourceFormat] | None = None


class ResponseOverrides(StrictModel):
    # Backend owns version selection; Runtime resolves the version to a bundled prompt.
    system_prompt_version: str | None = None
    response_style: str | None = None
    temperature: float | None = None
    top_p: float | None = Field(default=None, ge=0, le=1)
    max_output_tokens: int | None = Field(default=None, ge=1)
    top_k: int | None = Field(default=None, ge=1)
    rerank_enabled: bool | None = None
    evidence_min_score: float | None = Field(default=None, gt=0, le=1)
    retrieval_strategy: RetrievalStrategy | None = None
    embedding_model: str | None = None


class KnowledgeScope(StrictModel):
    snapshot_id: str
    manifest_hash: str
    revision_ids: list[str] = Field(min_length=1)
    resolved_at: str

    @model_validator(mode="after")
    def reject_duplicate_revisions(self) -> "KnowledgeScope":
        if len(set(self.revision_ids)) != len(self.revision_ids):
            raise ValueError("revision_ids must be unique")
        return self


class RevalidationContext(StrictModel):
    original_message_id: str
    original_snapshot_id: str
    original_answer: str
    original_citation_revision_ids: list[str] = Field(default_factory=list)
    related_update_event_ids: list[str] = Field(default_factory=list)


class PendingClarification(StrictModel):
    clarification_id: str = Field(min_length=1)
    state_version: int = Field(ge=1)
    original_user_message_id: str = Field(min_length=1)
    original_query: str = Field(min_length=1, max_length=600)
    missing_dimensions: list[str] = Field(min_length=1)
    question: str = Field(min_length=1, max_length=600)
    turn_count: int = Field(ge=1)

    @model_validator(mode="after")
    def validate_pending_dimensions(self) -> "PendingClarification":
        if any(not dimension.strip() for dimension in self.missing_dimensions):
            raise ValueError("missing_dimensions must not contain blank values")
        if len(set(self.missing_dimensions)) != len(self.missing_dimensions):
            raise ValueError("missing_dimensions must be unique")
        if not self.original_query.strip() or not self.question.strip():
            raise ValueError("original_query and question must not be blank")
        return self


class ChatRequest(StrictModel):
    request_id: str
    conversation_id: str
    message_id: str
    user_id: str | None = None
    user_query: str = Field(min_length=1)
    model_id: str
    config_version: str
    retrieval_config_version: str
    knowledge_snapshot_id: str = ""
    knowledge_scope: KnowledgeScope | None = None
    answer_mode: AnswerMode = "normal"
    intent: ChatRequestIntent = "knowledge_query"
    answer_policy_version: str | None = Field(default=None, min_length=1)
    pending_clarification: PendingClarification | None = None
    revalidation_context: RevalidationContext | None = None
    stream: bool = True
    messages: list[ConversationMessage]
    source_filters: SourceFilters | None = None
    response_overrides: ResponseOverrides | None = None

    @model_validator(mode="after")
    def validate_knowledge_execution_context(self) -> "ChatRequest":
        if self.knowledge_scope and self.knowledge_scope.snapshot_id != self.knowledge_snapshot_id:
            raise ValueError("knowledge_scope.snapshot_id must match knowledge_snapshot_id")
        if self.answer_mode == "revalidate" and not self.revalidation_context:
            raise ValueError("revalidation_context is required for revalidate mode")
        if self.answer_mode == "revalidate" and self.knowledge_scope is None:
            raise ValueError("knowledge_scope is required for knowledge_query and revalidate")
        if self.answer_policy_version is not None and self.intent not in {"context_routing_required", "knowledge_query"}:
            raise ValueError("answer_policy_version is only allowed for context_routing_required or knowledge_query")
        if self.pending_clarification is not None:
            if self.intent not in {"context_routing_required", "knowledge_query"}:
                raise ValueError("pending_clarification is only allowed for context_routing_required or knowledge_query")
            if self.answer_policy_version is None:
                raise ValueError("pending_clarification requires answer_policy_version")
        return self


class AssistantMessage(StrictModel):
    role: AssistantRole = "assistant"
    content: str


class TokenUsage(StrictModel):
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int


class RetrievalObservation(StrictModel):
    status: ExecutionStatus
    strategy: str | None = None
    embedding_model: str | None = None
    collection: str | None = None
    candidate_count: int | None = Field(default=None, ge=0)
    top_k: int | None = Field(default=None, ge=1)
    rerank_enabled: bool | None = None
    evidence_min_score: float | None = Field(default=None, gt=0, le=1)

    @model_validator(mode="after")
    def require_performed_details(self) -> "RetrievalObservation":
        if self.status == "performed" and (
            not self.strategy
            or not self.embedding_model
            or not self.collection
            or self.candidate_count is None
            or self.top_k is None
            or self.rerank_enabled is None
            or self.evidence_min_score is None
        ):
            raise ValueError(
                "performed retrieval requires strategy, embedding_model, collection, candidate_count, "
                "top_k, rerank_enabled and evidence_min_score"
            )
        return self

class GenerationObservation(StrictModel):
    status: ExecutionStatus
    provider: str | None = None
    model_id: str | None = None

    @model_validator(mode="after")
    def require_performed_details(self) -> "GenerationObservation":
        if self.status == "performed" and (not self.provider or not self.model_id):
            raise ValueError("performed generation requires provider and model_id")
        return self


class ExecutionObservation(StrictModel):
    policy: ExecutionStatus
    policy_provider: str | None = None
    policy_model_id: str | None = None
    policy_call_count: int | None = Field(default=None, ge=1)
    scope_shape: ScopeShape | None = None
    retrieval_target: str | None = Field(default=None, min_length=1, max_length=1200)
    requested_fact_fields: list[str] | None = None
    unresolved_branch_dimensions: list[str] | None = None
    untrusted_instruction_present: bool | None = None
    retrieval: RetrievalObservation
    generation: GenerationObservation
    fallback_used: Literal[False]

    @model_validator(mode="after")
    def reject_policy_details_without_policy_execution(self) -> "ExecutionObservation":
        if self.policy == "not_run" and (
            self.policy_provider is not None
            or self.policy_model_id is not None
            or self.policy_call_count is not None
        ):
            raise ValueError("not_run policy cannot contain provider, model id or call count")
        has_scope_evidence = any(
            value is not None
            for value in (
                self.retrieval_target,
                self.requested_fact_fields,
                self.unresolved_branch_dimensions,
                self.untrusted_instruction_present,
            )
        )
        if has_scope_evidence and self.scope_shape is None:
            raise ValueError("scope evidence requires scope_shape")
        if self.requested_fact_fields is not None and len(set(self.requested_fact_fields)) != len(self.requested_fact_fields):
            raise ValueError("requested_fact_fields must be unique")
        if self.unresolved_branch_dimensions is not None and len(set(self.unresolved_branch_dimensions)) != len(self.unresolved_branch_dimensions):
            raise ValueError("unresolved_branch_dimensions must be unique")
        return self


class ContextResolution(StrictModel):
    intent: ResolvedChatIntent
    route: Literal["rag", "controlled_reply", "clarification"]
    kind: Literal["follow_up", "standalone", "topic_shift", "clarification_required"]
    resolved_query: str | None = Field(default=None, max_length=600)
    basis_message_ids: list[str] = Field(default_factory=list, max_length=6)
    resolver_provider: str | None = None
    resolver_model_id: str | None = None
    resolver_performed: bool
    confidence: float = Field(ge=0, le=1)

    @model_validator(mode="after")
    def validate_resolution(self) -> "ContextResolution":
        if self.route == "rag" and self.intent != "knowledge_query":
            raise ValueError("rag route requires knowledge_query intent")
        if self.route == "clarification" and (self.intent != "knowledge_query" or self.kind != "clarification_required"):
            raise ValueError("clarification route requires knowledge_query clarification_required")
        if self.route == "controlled_reply" and self.intent == "knowledge_query":
            raise ValueError("controlled reply cannot use knowledge_query intent")
        if self.kind == "follow_up":
            if not self.resolved_query or not self.basis_message_ids:
                raise ValueError("follow_up requires resolved_query and basis_message_ids")
        elif self.resolved_query or self.basis_message_ids:
            raise ValueError("only follow_up may contain resolved_query or basis_message_ids")
        if self.resolver_performed and (not self.resolver_provider or not self.resolver_model_id):
            raise ValueError("performed resolver requires provider and model id")
        return self


class Clarification(StrictModel):
    missing_dimensions: list[str] = Field(min_length=1)
    question: str = Field(min_length=1, max_length=600)

    @model_validator(mode="after")
    def validate_dimensions(self) -> "Clarification":
        if any(not dimension.strip() for dimension in self.missing_dimensions):
            raise ValueError("missing_dimensions must not contain blank values")
        if len(set(self.missing_dimensions)) != len(self.missing_dimensions):
            raise ValueError("missing_dimensions must be unique")
        if not self.question.strip():
            raise ValueError("question must not be blank")
        return self


class Coverage(StrictModel):
    requested_count: int = Field(ge=1)
    supported_count: int = Field(ge=0)
    uncovered_description: str = Field(min_length=1, max_length=1200)

    @model_validator(mode="after")
    def validate_partial_coverage(self) -> "Coverage":
        if self.supported_count >= self.requested_count:
            raise ValueError("partial coverage requires supported_count < requested_count")
        if not self.uncovered_description.strip():
            raise ValueError("uncovered_description must not be blank")
        return self


class ClarificationResolution(StrictModel):
    pending_clarification_id: str = Field(min_length=1)
    pending_state_version: int = Field(ge=1)
    action: ClarificationResolutionAction
    resolved_query: str | None = Field(default=None, min_length=1, max_length=600)

    @model_validator(mode="after")
    def validate_resolution(self) -> "ClarificationResolution":
        if self.action == "resolved":
            if self.resolved_query is None or not self.resolved_query.strip():
                raise ValueError("resolved action requires resolved_query")
        elif self.resolved_query is not None:
            raise ValueError("only resolved action may contain resolved_query")
        return self


class RetrievalCandidate(StrictModel):
    chunk_id: str = Field(min_length=1)
    file_id: str = Field(min_length=1)
    document_id: str = Field(min_length=1)
    revision_id: str = Field(min_length=1)
    chunk_content_sha256: str = Field(min_length=1)
    knowledge_snapshot_id: str = Field(min_length=1)
    rank: int = Field(ge=1)
    score: float
    source_locator: SourceLocator


class ChatResponse(StrictModel):
    request_id: str
    conversation_id: str
    message_id: str
    assistant_message: AssistantMessage
    citations: list[RetrievedSource]
    citation_anchors: list[CitationAnchor] = Field(default_factory=list)
    refusal: bool
    finish_reason: FinishReason
    completed_at: str
    knowledge_snapshot_id: str | None = None
    knowledge_snapshot_manifest_hash: str | None = None
    freshness_status: FreshnessStatus | None = None
    revalidation_outcome: RevalidationOutcome | None = None
    usage: TokenUsage | None = None
    intent: ResolvedChatIntent
    answer_disposition: AnswerDisposition
    answer_policy_version: str | None = None
    policy_evaluation_performed: bool | None = None
    answer_generation_performed: bool | None = None
    clarification: Clarification | None = None
    coverage: Coverage | None = None
    resolution: ClarificationResolution | None = None
    resolved_query: str | None = Field(default=None, min_length=1, max_length=600)
    retrieval_candidates: list[RetrievalCandidate] = Field(default_factory=list)
    execution: ExecutionObservation
    used_citation_ranks: list[int] = Field(default_factory=list)
    retrieval_performed: bool
    generation_performed: bool
    knowledge_used: bool
    context_resolution: ContextResolution | None = None

    @model_validator(mode="after")
    def validate_a1_contract(self) -> "ChatResponse":
        if any(rank < 1 for rank in self.used_citation_ranks):
            raise ValueError("used_citation_ranks must be positive")
        if len(set(self.used_citation_ranks)) != len(self.used_citation_ranks):
            raise ValueError("used_citation_ranks must be unique")

        candidate_ranks = [candidate.rank for candidate in self.retrieval_candidates]
        if len(set(candidate_ranks)) != len(candidate_ranks):
            raise ValueError("retrieval_candidates ranks must be unique")

        a1_fields = {
            "policy_evaluation_performed",
            "answer_generation_performed",
            "clarification",
            "coverage",
            "resolution",
            "resolved_query",
            "retrieval_candidates",
        }
        if not self.model_fields_set.intersection(a1_fields):
            return self

        if self.policy_evaluation_performed is None or self.answer_generation_performed is None:
            raise ValueError("A1 records require both performed fields")
        if (self.execution.policy == "performed") != self.policy_evaluation_performed:
            raise ValueError("execution.policy must mirror policy_evaluation_performed")
        if self.policy_evaluation_performed:
            if (
                not self.execution.policy_provider
                or not self.execution.policy_model_id
                or self.execution.policy_call_count is None
            ):
                raise ValueError("performed A1 policy requires provider, model id and call count")
        elif (
            self.execution.policy_provider is not None
            or self.execution.policy_model_id is not None
            or self.execution.policy_call_count is not None
        ):
            raise ValueError("unperformed A1 policy cannot contain provider, model id or call count")
        if (self.execution.retrieval.status == "performed") != self.retrieval_performed:
            raise ValueError("execution.retrieval.status must mirror retrieval_performed")
        if (self.execution.generation.status == "performed") != self.generation_performed:
            raise ValueError("execution.generation.status must mirror generation_performed")
        if self.generation_performed != self.answer_generation_performed:
            raise ValueError("generation_performed must mirror answer_generation_performed")
        if self.resolution is not None and self.resolution.action == "resolved":
            if self.resolved_query != self.resolution.resolved_query:
                raise ValueError("resolved_query must match resolved resolution")
        elif self.resolved_query is not None:
            raise ValueError("resolved_query requires a resolved clarification resolution")

        if self.context_resolution is not None:
            route = self.context_resolution.route
            if self.context_resolution.intent != self.intent:
                raise ValueError("context_resolution intent must match response intent")
            if route == "controlled_reply" and self.answer_disposition != "controlled_response":
                raise ValueError("controlled_reply route requires controlled_response")
            if route == "clarification" and self.answer_disposition != "clarification_required":
                raise ValueError("clarification route requires clarification_required")
            if route == "rag" and self.answer_disposition == "controlled_response":
                raise ValueError("rag route cannot use controlled_response")

        if self.answer_disposition == "controlled_response":
            self._validate_controlled_response()
        elif self.answer_disposition == "clarification_required":
            self._validate_clarification_required()
        elif self.answer_disposition == "insufficient_evidence":
            self._validate_insufficient_evidence()
        else:
            self._validate_grounded_or_partial()
        return self

    def _validate_controlled_response(self) -> None:
        if self.intent == "knowledge_query":
            raise ValueError("controlled_response cannot use knowledge_query intent")
        if self.answer_policy_version is not None or self.policy_evaluation_performed or self.answer_generation_performed:
            raise ValueError("controlled_response cannot contain A1 policy or answer generation facts")
        if self.retrieval_performed or self.generation_performed or self.knowledge_used:
            raise ValueError("controlled_response cannot contain retrieval or knowledge execution")
        if (
            self.clarification is not None
            or self.coverage is not None
            or self.resolution is not None
            or self.resolved_query is not None
            or self.retrieval_candidates
            or self.citations
            or self.citation_anchors
            or self.used_citation_ranks
            or self.knowledge_snapshot_id is not None
            or self.knowledge_snapshot_manifest_hash is not None
            or self.freshness_status is not None
            or self.revalidation_outcome is not None
        ):
            raise ValueError("controlled_response cannot contain knowledge fields")

    def _validate_clarification_required(self) -> None:
        self._require_knowledge_policy("clarification_required")
        if self.answer_generation_performed or self.generation_performed or self.knowledge_used:
            raise ValueError("clarification_required cannot generate a knowledge answer")
        if self.clarification is None:
            raise ValueError("clarification_required requires clarification")
        if self.coverage is not None:
            raise ValueError("clarification_required cannot contain coverage")
        self._reject_user_citations("clarification_required")

    def _validate_insufficient_evidence(self) -> None:
        self._require_knowledge_policy("insufficient_evidence")
        if not self.retrieval_performed:
            raise ValueError("insufficient_evidence requires performed retrieval")
        if self.answer_generation_performed or self.generation_performed or self.knowledge_used:
            raise ValueError("insufficient_evidence cannot generate a knowledge answer")
        if not self.refusal:
            raise ValueError("insufficient_evidence requires refusal")
        if self.clarification is not None or self.coverage is not None:
            raise ValueError("insufficient_evidence cannot contain clarification or coverage")
        self._reject_user_citations("insufficient_evidence")

    def _validate_grounded_or_partial(self) -> None:
        disposition = self.answer_disposition
        self._require_knowledge_policy(disposition)
        if not self.answer_generation_performed or not self.retrieval_performed or not self.knowledge_used:
            raise ValueError(f"{disposition} requires policy, retrieval, answer generation and knowledge use")
        if self.refusal:
            raise ValueError(f"{disposition} cannot be a refusal")
        if self.clarification is not None:
            raise ValueError(f"{disposition} cannot contain clarification")
        if not self.citations or not self.citation_anchors or not self.used_citation_ranks:
            raise ValueError(f"{disposition} requires citations, anchors and used_citation_ranks")
        if not self.retrieval_candidates:
            raise ValueError(f"{disposition} requires retrieval_candidates")
        if self.answer_disposition == "partial_answer":
            if self.coverage is None:
                raise ValueError("partial_answer requires coverage")
        elif self.coverage is not None:
            raise ValueError("grounded_answer cannot contain coverage")
        self._validate_visible_evidence()

    def _require_knowledge_policy(self, disposition: str) -> None:
        if self.intent != "knowledge_query":
            raise ValueError(f"{disposition} requires knowledge_query intent")
        if self.answer_policy_version is None or not self.answer_policy_version.strip():
            raise ValueError(f"{disposition} requires answer_policy_version")
        if not self.policy_evaluation_performed:
            raise ValueError(f"{disposition} requires performed answer policy evaluation")

    def _reject_user_citations(self, disposition: str) -> None:
        if self.citations or self.citation_anchors or self.used_citation_ranks:
            raise ValueError(f"{disposition} cannot contain user-visible citations or anchors")

    def _validate_visible_evidence(self) -> None:
        candidate_ranks = {candidate.rank for candidate in self.retrieval_candidates}
        used_ranks = set(self.used_citation_ranks)
        citation_ranks = {citation.rank for citation in self.citations}
        if not used_ranks.issubset(candidate_ranks):
            raise ValueError("used_citation_ranks must be a subset of retrieval_candidates")
        if citation_ranks != used_ranks:
            raise ValueError("citations must match used_citation_ranks")
        if any(not set(anchor.citation_ranks).issubset(used_ranks) for anchor in self.citation_anchors):
            raise ValueError("citation_anchors must reference used_citation_ranks only")
