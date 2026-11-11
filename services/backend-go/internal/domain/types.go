package domain

import (
	"encoding/json"
	"strings"
)

type Conversation struct {
	ConversationID         string `json:"conversation_id"`
	OwnerID                string `json:"owner_id"`
	Title                  string `json:"title"`
	TitleSource            string `json:"-"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
	ConfigVersion          string `json:"config_version"`
	RetrievalConfigVersion string `json:"retrieval_config_version"`
	// Deprecated: retained for legacy API compatibility. K5 records the actual snapshot on assistant messages.
	KnowledgeSnapshotID string `json:"knowledge_snapshot_id"`
	ModelID             string `json:"model_id"`
}

type Message struct {
	MessageID                     string           `json:"message_id"`
	ConversationID                string           `json:"conversation_id"`
	Role                          string           `json:"role"`
	Content                       string           `json:"content"`
	Status                        string           `json:"status"`
	CreatedAt                     string           `json:"created_at"`
	FinishReason                  *string          `json:"finish_reason,omitempty"`
	CitationsCount                int              `json:"citations_count,omitempty"`
	KnowledgeSnapshotID           string           `json:"knowledge_snapshot_id,omitempty"`
	KnowledgeSnapshotManifestHash string           `json:"knowledge_snapshot_manifest_hash,omitempty"`
	AnsweredAt                    *string          `json:"answered_at,omitempty"`
	FreshnessStatus               string           `json:"freshness_status,omitempty"`
	CitationAnchors               []CitationAnchor `json:"citation_anchors,omitempty"`
	Intent                        string           `json:"intent,omitempty"`
	KnowledgeUsed                 bool             `json:"knowledge_used"`
	AnswerDisposition             string           `json:"answer_disposition,omitempty"`
	AnswerPolicyVersion           string           `json:"answer_policy_version,omitempty"`
	PolicyEvaluationPerformed     *bool            `json:"policy_evaluation_performed,omitempty"`
	AnswerGenerationPerformed     *bool            `json:"answer_generation_performed,omitempty"`
	ClarificationID               string           `json:"clarification_id,omitempty"`
	Coverage                      *RuntimeCoverage `json:"coverage,omitempty"`
}

type SourceLocator struct {
	SourceFormat   string   `json:"source_format"`
	PageNo         *int     `json:"page_no,omitempty"`
	HeadingPath    []string `json:"heading_path,omitempty"`
	ParagraphIndex *int     `json:"paragraph_index,omitempty"`
	SheetName      string   `json:"sheet_name,omitempty"`
	CellRange      string   `json:"cell_range,omitempty"`
	SlideNo        *int     `json:"slide_no,omitempty"`
	SlideTitle     string   `json:"slide_title,omitempty"`
	BlockLabel     string   `json:"block_label,omitempty"`
	JSONPath       string   `json:"json_path,omitempty"`
}

type Citation struct {
	MessageID           string        `json:"message_id"`
	ChunkID             string        `json:"chunk_id"`
	FileID              string        `json:"file_id"`
	DocumentID          string        `json:"document_id,omitempty"`
	RevisionID          string        `json:"revision_id,omitempty"`
	ChunkContentSHA256  string        `json:"chunk_content_sha256,omitempty"`
	KnowledgeSnapshotID string        `json:"knowledge_snapshot_id,omitempty"`
	Filename            string        `json:"filename"`
	Rank                int           `json:"rank"`
	Score               float64       `json:"score"`
	CitationLabel       string        `json:"citation_label"`
	SourceLocator       SourceLocator `json:"source_locator"`
	Snippet             string        `json:"snippet"`
}

// CitationAnchor maps a visible Unicode rune range in one assistant message
// to the immutable citation ranks that support that exact conclusion.
type CitationAnchor struct {
	StartRune     int   `json:"start_rune"`
	EndRune       int   `json:"end_rune"`
	CitationRanks []int `json:"citation_ranks"`
}

// MessageSources is the historical evidence attached to one assistant answer.
// Its snapshot fields are persisted with the answer and never derived from the
// current active snapshot.
type MessageSources struct {
	MessageID                     string     `json:"message_id"`
	KnowledgeSnapshotID           string     `json:"knowledge_snapshot_id,omitempty"`
	KnowledgeSnapshotManifestHash string     `json:"knowledge_snapshot_manifest_hash,omitempty"`
	FreshnessStatus               string     `json:"freshness_status,omitempty"`
	Items                         []Citation `json:"items"`
}

type RuntimeConversationMessage struct {
	MessageID string `json:"message_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at,omitempty"`
}

type RuntimeContextResolution struct {
	Intent              string   `json:"intent"`
	Route               string   `json:"route"`
	Kind                string   `json:"kind"`
	ResolvedQuery       string   `json:"resolved_query,omitempty"`
	BasisMessageIDs     []string `json:"basis_message_ids,omitempty"`
	RouterPolicyVersion string   `json:"router_policy_version,omitempty"`
	ResolverProvider    string   `json:"resolver_provider,omitempty"`
	ResolverModelID     string   `json:"resolver_model_id,omitempty"`
	ResolverPerformed   bool     `json:"resolver_performed"`
	Confidence          float64  `json:"confidence"`
}

type RuntimeSourceFilters struct {
	FileIDs       []string `json:"file_ids,omitempty"`
	SourceFormats []string `json:"source_formats,omitempty"`
}

type RuntimeResponseOverrides struct {
	SystemPromptVersion string   `json:"system_prompt_version,omitempty"`
	ResponseStyle       string   `json:"response_style,omitempty"`
	RetrievalStrategy   string   `json:"retrieval_strategy,omitempty"`
	EmbeddingModel      string   `json:"embedding_model,omitempty"`
	Temperature         *float64 `json:"temperature,omitempty"`
	TopP                *float64 `json:"top_p,omitempty"`
	MaxOutputTokens     *int     `json:"max_output_tokens,omitempty"`
	TopK                *int     `json:"top_k,omitempty"`
	RerankEnabled       *bool    `json:"rerank_enabled,omitempty"`
	EvidenceMinScore    *float64 `json:"evidence_min_score,omitempty"`
}

type RuntimeKnowledgeScope struct {
	SnapshotID   string   `json:"snapshot_id"`
	ManifestHash string   `json:"manifest_hash"`
	RevisionIDs  []string `json:"revision_ids"`
	ResolvedAt   string   `json:"resolved_at"`
}

type RuntimeRevalidationContext struct {
	OriginalMessageID           string   `json:"original_message_id"`
	OriginalSnapshotID          string   `json:"original_snapshot_id"`
	OriginalAnswer              string   `json:"original_answer"`
	OriginalCitationRevisionIDs []string `json:"original_citation_revision_ids"`
	RelatedUpdateEventIDs       []string `json:"related_update_event_ids"`
}

// RuntimePendingClarification is a Backend-owned pending state projected into
// a Runtime request. Runtime may propose a resolution but never creates IDs
// or changes this state directly.
type RuntimePendingClarification struct {
	ClarificationID       string   `json:"clarification_id"`
	StateVersion          int      `json:"state_version"`
	OriginalUserMessageID string   `json:"original_user_message_id"`
	OriginalQuery         string   `json:"original_query"`
	MissingDimensions     []string `json:"missing_dimensions"`
	Question              string   `json:"question"`
	TurnCount             int      `json:"turn_count"`
}

type RuntimeClarification struct {
	MissingDimensions []string `json:"missing_dimensions"`
	Question          string   `json:"question"`
}

type RuntimeCoverage struct {
	RequestedCount       int    `json:"requested_count"`
	SupportedCount       int    `json:"supported_count"`
	UncoveredDescription string `json:"uncovered_description"`
}

type RuntimeClarificationResolution struct {
	PendingClarificationID string `json:"pending_clarification_id"`
	PendingStateVersion    int    `json:"pending_state_version"`
	Action                 string `json:"action"`
	ResolvedQuery          string `json:"resolved_query,omitempty"`
}

type ChatRuntimeRequest struct {
	RequestID              string                       `json:"request_id"`
	ConversationID         string                       `json:"conversation_id"`
	MessageID              string                       `json:"message_id"`
	UserID                 string                       `json:"user_id,omitempty"`
	UserQuery              string                       `json:"user_query"`
	ModelID                string                       `json:"model_id"`
	ConfigVersion          string                       `json:"config_version"`
	RetrievalConfigVersion string                       `json:"retrieval_config_version"`
	KnowledgeSnapshotID    string                       `json:"knowledge_snapshot_id"`
	KnowledgeScope         *RuntimeKnowledgeScope       `json:"knowledge_scope,omitempty"`
	AnswerMode             string                       `json:"answer_mode,omitempty"`
	Intent                 string                       `json:"intent,omitempty"`
	AnswerPolicyVersion    string                       `json:"answer_policy_version,omitempty"`
	PendingClarification   *RuntimePendingClarification `json:"pending_clarification,omitempty"`
	RevalidationContext    *RuntimeRevalidationContext  `json:"revalidation_context,omitempty"`
	Stream                 bool                         `json:"stream"`
	Messages               []RuntimeConversationMessage `json:"messages"`
	SourceFilters          *RuntimeSourceFilters        `json:"source_filters,omitempty"`
	ResponseOverrides      *RuntimeResponseOverrides    `json:"response_overrides,omitempty"`
}

type RuntimeAssistantMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type RuntimeTokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type RuntimeCitation struct {
	ChunkID             string        `json:"chunk_id"`
	FileID              string        `json:"file_id"`
	DocumentID          string        `json:"document_id,omitempty"`
	RevisionID          string        `json:"revision_id,omitempty"`
	ChunkContentSHA256  string        `json:"chunk_content_sha256,omitempty"`
	KnowledgeSnapshotID string        `json:"knowledge_snapshot_id,omitempty"`
	Filename            string        `json:"filename"`
	Rank                int           `json:"rank"`
	Score               float64       `json:"score"`
	CitationLabel       string        `json:"citation_label"`
	SourceLocator       SourceLocator `json:"source_locator"`
	Snippet             string        `json:"snippet"`
}

// RuntimeRetrievalCandidate is diagnostic retrieval evidence. It deliberately
// omits snippets and labels, which are persisted only for answer citations.
type RuntimeRetrievalCandidate struct {
	ChunkID             string        `json:"chunk_id"`
	FileID              string        `json:"file_id"`
	DocumentID          string        `json:"document_id"`
	RevisionID          string        `json:"revision_id"`
	ChunkContentSHA256  string        `json:"chunk_content_sha256"`
	KnowledgeSnapshotID string        `json:"knowledge_snapshot_id"`
	Rank                int           `json:"rank"`
	Score               float64       `json:"score"`
	SourceLocator       SourceLocator `json:"source_locator"`
}

// RuntimeExecutionObservation is supplied by Runtime as an execution fact.
// Backend persists it verbatim and never synthesizes a provider, strategy or
// fallback result from configuration.
type RuntimeRetrievalObservation struct {
	Status         string `json:"status"`
	Strategy       string `json:"strategy,omitempty"`
	EmbeddingModel string `json:"embedding_model,omitempty"`
	Collection     string `json:"collection,omitempty"`
	CandidateCount *int   `json:"candidate_count,omitempty"`
}

type RuntimeGenerationObservation struct {
	Status   string `json:"status"`
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"model_id,omitempty"`
}

type RuntimeExecutionObservation struct {
	Policy                      string                       `json:"policy"`
	PolicyProvider              string                       `json:"policy_provider,omitempty"`
	PolicyModelID               string                       `json:"policy_model_id,omitempty"`
	PolicyCallCount             *int                         `json:"policy_call_count,omitempty"`
	ScopeShape                  string                       `json:"scope_shape,omitempty"`
	RetrievalTarget             string                       `json:"retrieval_target,omitempty"`
	RequestedFactFields         []string                     `json:"requested_fact_fields,omitempty"`
	UnresolvedBranchDimensions  []string                     `json:"unresolved_branch_dimensions,omitempty"`
	UntrustedInstructionPresent *bool                        `json:"untrusted_instruction_present,omitempty"`
	Retrieval                   RuntimeRetrievalObservation  `json:"retrieval"`
	Generation                  RuntimeGenerationObservation `json:"generation"`
	FallbackUsed                bool                         `json:"fallback_used"`
}

type ChatRuntimeResponse struct {
	RequestID                     string                          `json:"request_id"`
	ConversationID                string                          `json:"conversation_id"`
	MessageID                     string                          `json:"message_id"`
	AssistantMessage              RuntimeAssistantMessage         `json:"assistant_message"`
	Citations                     []RuntimeCitation               `json:"citations"`
	CitationAnchors               []CitationAnchor                `json:"citation_anchors,omitempty"`
	Refusal                       bool                            `json:"refusal"`
	FinishReason                  string                          `json:"finish_reason"`
	CompletedAt                   string                          `json:"completed_at"`
	KnowledgeSnapshotID           string                          `json:"knowledge_snapshot_id,omitempty"`
	KnowledgeSnapshotManifestHash string                          `json:"knowledge_snapshot_manifest_hash,omitempty"`
	FreshnessStatus               string                          `json:"freshness_status,omitempty"`
	RevalidationOutcome           string                          `json:"revalidation_outcome,omitempty"`
	Usage                         *RuntimeTokenUsage              `json:"usage,omitempty"`
	Intent                        string                          `json:"intent"`
	AnswerDisposition             string                          `json:"answer_disposition,omitempty"`
	AnswerPolicyVersion           string                          `json:"answer_policy_version,omitempty"`
	PolicyEvaluationPerformed     *bool                           `json:"policy_evaluation_performed,omitempty"`
	AnswerGenerationPerformed     *bool                           `json:"answer_generation_performed,omitempty"`
	Clarification                 *RuntimeClarification           `json:"clarification,omitempty"`
	Coverage                      *RuntimeCoverage                `json:"coverage,omitempty"`
	Resolution                    *RuntimeClarificationResolution `json:"resolution,omitempty"`
	ResolvedQuery                 string                          `json:"resolved_query,omitempty"`
	RetrievalCandidates           []RuntimeRetrievalCandidate     `json:"retrieval_candidates,omitempty"`
	Execution                     *RuntimeExecutionObservation    `json:"execution,omitempty"`
	UsedCitationRanks             []int                           `json:"used_citation_ranks,omitempty"`
	RetrievalPerformed            bool                            `json:"retrieval_performed"`
	GenerationPerformed           bool                            `json:"generation_performed"`
	KnowledgeUsed                 bool                            `json:"knowledge_used"`
	ContextResolution             *RuntimeContextResolution       `json:"context_resolution,omitempty"`
}

type ChatRuntimeStreamError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type ChatRuntimeStreamEvent struct {
	Event     string                  `json:"event"`
	RequestID string                  `json:"request_id,omitempty"`
	MessageID string                  `json:"message_id,omitempty"`
	Delta     string                  `json:"delta,omitempty"`
	Response  *ChatRuntimeResponse    `json:"chat_runtime_response,omitempty"`
	Error     *ChatRuntimeStreamError `json:"error,omitempty"`
}

const (
	ChatExecutionAttemptAccepted  = "accepted"
	ChatExecutionAttemptRunning   = "running"
	ChatExecutionAttemptSucceeded = "succeeded"
	ChatExecutionAttemptFailed    = "failed"
	ChatExecutionAttemptCancelled = "cancelled"
)

// ChatExecutionAttempt is an append-only audit record for one Backend chat
// dispatch. It deliberately records failure without manufacturing an
// assistant answer, citation, or inferred Runtime observation.
type ChatExecutionAttempt struct {
	AttemptID                     string `json:"attempt_id"`
	RequestID                     string `json:"request_id"`
	ParentAttemptID               string `json:"parent_attempt_id,omitempty"`
	ConversationID                string `json:"conversation_id"`
	UserMessageID                 string `json:"user_message_id,omitempty"`
	AssistantMessageID            string `json:"assistant_message_id"`
	State                         string `json:"state"`
	ConfigVersion                 string `json:"config_version"`
	RetrievalConfigVersion        string `json:"retrieval_config_version"`
	KnowledgeSnapshotID           string `json:"knowledge_snapshot_id,omitempty"`
	KnowledgeSnapshotManifestHash string `json:"knowledge_snapshot_manifest_hash,omitempty"`
	ErrorCode                     string `json:"error_code,omitempty"`
	ErrorStage                    string `json:"error_stage,omitempty"`
	Retryable                     bool   `json:"retryable"`
	AcceptedAt                    string `json:"accepted_at"`
	StartedAt                     string `json:"started_at,omitempty"`
	TerminalAt                    string `json:"terminal_at,omitempty"`
}

type KnowledgeFile struct {
	FileID       string `json:"file_id"`
	Filename     string `json:"filename"`
	MIMEType     string `json:"mime_type"`
	SourceFormat string `json:"source_format"`
	Status       string `json:"status"`
	UploadedBy   string `json:"uploaded_by"`
	UploadedAt   string `json:"uploaded_at"`
	ObjectURI    string `json:"object_uri"`
	ParseVersion string `json:"parse_version"`
	IndexVersion string `json:"index_version"`
}

// These K1 types freeze the revision vocabulary; K2 owns their persistence and state transitions.
type KnowledgeDocument struct {
	DocumentID  string `json:"document_id"`
	DisplayName string `json:"display_name"`
	OwnerID     string `json:"owner_id"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type KnowledgeRevision struct {
	RevisionID          string  `json:"revision_id"`
	DocumentID          string  `json:"document_id"`
	LegacyFileID        string  `json:"-"`
	Filename            string  `json:"filename"`
	MIMEType            string  `json:"mime_type"`
	SourceFormat        string  `json:"source_format"`
	ObjectURI           string  `json:"-"`
	ChecksumSHA256      string  `json:"checksum_sha256,omitempty"`
	ParseVersion        string  `json:"parse_version"`
	IndexVersion        string  `json:"index_version"`
	EmbeddingModel      string  `json:"embedding_model,omitempty"`
	EffectiveAt         *string `json:"effective_at,omitempty"`
	ChunkManifestSHA256 string  `json:"chunk_manifest_sha256,omitempty"`
	Status              string  `json:"status"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
}

type KnowledgeSnapshotMember struct {
	SnapshotID     string `json:"snapshot_id"`
	RevisionID     string `json:"revision_id"`
	DocumentID     string `json:"document_id"`
	ChecksumSHA256 string `json:"checksum_sha256"`
}

type KnowledgeSnapshot struct {
	SnapshotID       string                    `json:"snapshot_id"`
	ParentSnapshotID string                    `json:"parent_snapshot_id,omitempty"`
	Status           string                    `json:"status"`
	ManifestSHA256   string                    `json:"manifest_sha256"`
	CreatedAt        string                    `json:"created_at"`
	PublishedAt      *string                   `json:"published_at,omitempty"`
	Members          []KnowledgeSnapshotMember `json:"members,omitempty"`
}

type KnowledgeUpdateEvent struct {
	EventID       string `json:"event_id"`
	EventType     string `json:"event_type"`
	DocumentID    string `json:"document_id,omitempty"`
	RevisionID    string `json:"revision_id,omitempty"`
	SnapshotID    string `json:"snapshot_id,omitempty"`
	ActorID       string `json:"actor_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	OccurredAt    string `json:"occurred_at"`
}

type KnowledgeOutboxMessage struct {
	DeliveryID   string `json:"delivery_id"`
	Topic        string `json:"topic"`
	DocumentID   string `json:"document_id,omitempty"`
	RevisionID   string `json:"revision_id,omitempty"`
	SnapshotID   string `json:"snapshot_id,omitempty"`
	Payload      string `json:"payload"`
	Status       string `json:"status"`
	AttemptCount int    `json:"attempt_count"`
	AvailableAt  string `json:"available_at"`
	CreatedAt    string `json:"created_at"`
}

// KnowledgeMutationKey is the durable idempotency identity for an administrator
// lifecycle mutation. The request hash intentionally includes the immutable
// upload content hash where applicable.
type KnowledgeMutationKey struct {
	ActorID     string
	Operation   string
	Key         string
	RequestHash string
}

type RevisionDeliverySummary struct {
	DeliveryID   string `json:"delivery_id"`
	Status       string `json:"status"`
	AttemptCount int    `json:"attempt_count"`
	LastError    string `json:"last_error,omitempty"`
	UpdatedAt    string `json:"updated_at"`
}

type KnowledgeSnapshotSummary struct {
	SnapshotID     string `json:"snapshot_id"`
	Status         string `json:"status"`
	ManifestSHA256 string `json:"manifest_sha256"`
	CreatedAt      string `json:"created_at"`
	PublishedAt    string `json:"published_at,omitempty"`
}

type KnowledgeUpdateEventSummary struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	DocumentID string `json:"document_id"`
	RevisionID string `json:"revision_id,omitempty"`
	SnapshotID string `json:"snapshot_id,omitempty"`
	CreatedAt  string `json:"created_at"`
}

type KnowledgeDocumentListItem struct {
	KnowledgeDocument
	CurrentRevision *KnowledgeRevision `json:"current_revision,omitempty"`
}

type KnowledgeDocumentPage struct {
	Items      []KnowledgeDocumentListItem `json:"items"`
	Total      int                         `json:"total"`
	NextCursor string                      `json:"next_cursor,omitempty"`
}

type KnowledgeRevisionDetail struct {
	Revision         KnowledgeRevision        `json:"revision"`
	Delivery         *RevisionDeliverySummary `json:"delivery,omitempty"`
	InActiveSnapshot bool                     `json:"in_active_snapshot"`
}

type KnowledgeDocumentDetail struct {
	Document         KnowledgeDocument         `json:"document"`
	Revisions        []KnowledgeRevisionDetail `json:"revisions"`
	ActiveSnapshotID string                    `json:"active_snapshot_id,omitempty"`
}

// KnowledgeRetirementReadiness is the authoritative, current-snapshot proof
// required before a system administrator can retire an old document.
type KnowledgeRetirementReadiness struct {
	DocumentID                 string   `json:"document_id"`
	Stage                      string   `json:"stage"`
	ActiveSnapshotID           string   `json:"active_snapshot_id,omitempty"`
	ActiveSnapshotManifestHash string   `json:"active_snapshot_manifest_hash,omitempty"`
	PassingOnboardingRunID     string   `json:"passing_onboarding_run_id,omitempty"`
	Blockers                   []string `json:"blockers"`
}

type KnowledgeRevisionUpload struct {
	Document KnowledgeDocument       `json:"document"`
	Revision KnowledgeRevision       `json:"revision"`
	Delivery RevisionDeliverySummary `json:"delivery"`
}

type KnowledgeLifecycleMutation struct {
	Document    KnowledgeDocument            `json:"document"`
	Snapshot    *KnowledgeSnapshotSummary    `json:"snapshot,omitempty"`
	UpdateEvent *KnowledgeUpdateEventSummary `json:"update_event,omitempty"`
}

type KnowledgeFreshnessScan struct {
	EventID        string `json:"event_id"`
	Status         string `json:"status"`
	AttemptCount   int    `json:"attempt_count"`
	CandidateCount int    `json:"candidate_count"`
	MarkedCount    int    `json:"marked_count"`
	LastError      string `json:"last_error,omitempty"`
	UpdatedAt      string `json:"updated_at"`
	CompletedAt    string `json:"completed_at,omitempty"`
}

type KnowledgeUpdateCalendarItem struct {
	KnowledgeUpdateEvent
	FreshnessScan *KnowledgeFreshnessScan `json:"freshness_scan,omitempty"`
}

type KnowledgeUpdatePage struct {
	Items      []KnowledgeUpdateCalendarItem `json:"items"`
	NextCursor string                        `json:"next_cursor,omitempty"`
}

type RevalidationError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type RevalidationResult struct {
	RevalidationID                string     `json:"revalidation_id"`
	Content                       string     `json:"content"`
	KnowledgeSnapshotID           string     `json:"knowledge_snapshot_id"`
	KnowledgeSnapshotManifestHash string     `json:"knowledge_snapshot_manifest_hash"`
	Outcome                       string     `json:"outcome"`
	Citations                     []Citation `json:"citations"`
	CompletedAt                   string     `json:"completed_at"`
}

type RevalidationRun struct {
	RevalidationID               string              `json:"revalidation_id"`
	ConversationID               string              `json:"conversation_id"`
	OriginalMessageID            string              `json:"original_message_id"`
	Status                       string              `json:"status"`
	Trigger                      string              `json:"trigger"`
	Reason                       string              `json:"reason,omitempty"`
	LatestSnapshotID             string              `json:"latest_snapshot_id"`
	LatestSnapshotManifestHash   string              `json:"latest_snapshot_manifest_hash"`
	RelatedUpdateEventIDs        []string            `json:"related_update_event_ids"`
	RequestedAt                  string              `json:"requested_at"`
	CompletedAt                  string              `json:"completed_at,omitempty"`
	Outcome                      string              `json:"outcome,omitempty"`
	Result                       *RevalidationResult `json:"result,omitempty"`
	Error                        *RevalidationError  `json:"error,omitempty"`
	RequesterID                  string              `json:"-"`
	IdempotencyKey               string              `json:"-"`
	OriginalAnswer               string              `json:"-"`
	OriginalSnapshotID           string              `json:"-"`
	OriginalSnapshotManifestHash string              `json:"-"`
	OriginalCitationRevisionIDs  []string            `json:"-"`
}

type RevalidationRequest struct {
	RequesterID       string
	ConversationID    string
	OriginalMessageID string
	IdempotencyKey    string
	Reason            string
	LatestScope       RuntimeKnowledgeScope
}

type IngestionTaskError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type IngestionTaskSummary struct {
	TaskID         string              `json:"task_id"`
	FileID         string              `json:"file_id"`
	DocumentID     string              `json:"document_id,omitempty"`
	RevisionID     string              `json:"revision_id,omitempty"`
	DeliveryID     string              `json:"delivery_id,omitempty"`
	ChecksumSHA256 string              `json:"checksum_sha256,omitempty"`
	Attempt        int                 `json:"attempt,omitempty"`
	Status         string              `json:"status"`
	RequestedAt    string              `json:"requested_at"`
	UpdatedAt      string              `json:"updated_at"`
	ForceReindex   bool                `json:"force_reindex,omitempty"`
	Error          *IngestionTaskError `json:"error,omitempty"`
}

type IngestionTaskResult struct {
	RequestID           string              `json:"request_id"`
	TaskID              string              `json:"task_id"`
	FileID              string              `json:"file_id"`
	DocumentID          string              `json:"document_id,omitempty"`
	RevisionID          string              `json:"revision_id,omitempty"`
	DeliveryID          string              `json:"delivery_id,omitempty"`
	ChecksumSHA256      string              `json:"checksum_sha256,omitempty"`
	Status              string              `json:"status"`
	UpdatedAt           string              `json:"updated_at"`
	StartedAt           *string             `json:"started_at,omitempty"`
	CompletedAt         *string             `json:"completed_at,omitempty"`
	ChunksCreated       int                 `json:"chunks_created,omitempty"`
	PagesProcessed      *int                `json:"pages_processed,omitempty"`
	ChunkManifestSHA256 string              `json:"chunk_manifest_sha256,omitempty"`
	EmbeddingModel      string              `json:"embedding_model,omitempty"`
	WarningMessages     []string            `json:"warning_messages,omitempty"`
	Error               *IngestionTaskError `json:"error,omitempty"`
}

type ChunkRecord struct {
	ChunkID       string        `json:"chunk_id"`
	FileID        string        `json:"file_id"`
	SourceFormat  string        `json:"source_format"`
	BlockType     string        `json:"block_type"`
	HeadingPath   []string      `json:"heading_path,omitempty"`
	PageNo        *int          `json:"page_no,omitempty"`
	SourceLocator SourceLocator `json:"source_locator"`
	Text          string        `json:"text"`
	TokenCount    int           `json:"token_count,omitempty"`
}

type AgentConfig struct {
	ConfigVersion       string  `json:"config_version"`
	ModelID             string  `json:"model_id"`
	SystemPromptVersion string  `json:"system_prompt_version"`
	ResponseStyle       string  `json:"response_style"`
	Temperature         float64 `json:"temperature"`
	TopP                float64 `json:"top_p"`
	MaxOutputTokens     int     `json:"max_output_tokens"`
	AnswerPolicyVersion string  `json:"answer_policy_version"`
	CreatedAt           string  `json:"created_at"`
}

type RetrievalConfig struct {
	RetrievalConfigVersion string  `json:"retrieval_config_version"`
	RetrievalStrategy      string  `json:"retrieval_strategy"`
	EmbeddingModel         string  `json:"embedding_model"`
	TopK                   int     `json:"top_k"`
	RerankEnabled          bool    `json:"rerank_enabled"`
	EvidenceMinScore       float64 `json:"evidence_min_score"`
	CreatedAt              string  `json:"created_at"`
}

const (
	// LegacyAnswerPolicyVersion is a read-only compatibility projection for
	// immutable configurations created before A1 policy binding existed.
	LegacyAnswerPolicyVersion = "legacy_unknown"
	AnswerPolicyV1            = "answer_policy_v1"
	AnswerPolicyV2            = "answer_policy_v2"
	AnswerPolicyV3            = "answer_policy_v3"
	AnswerPolicyV4            = "answer_policy_v4"
	AnswerPolicyV5            = "answer_policy_v5"

	// HistoricalRetrievalStrategyLexicalCompatV1 exists solely to decode and
	// assert immutable legacy evidence. It is not a registered strategy and
	// cannot be selected by normal product configuration paths.
	HistoricalRetrievalStrategyLexicalCompatV1 = "lexical_compat_v1"
	RetrievalStrategyNativeDenseV1             = "native_dense_v1"
)

func IsRegisteredAnswerPolicyVersion(value string) bool {
	switch strings.TrimSpace(value) {
	case AnswerPolicyV1, AnswerPolicyV2, AnswerPolicyV3, AnswerPolicyV4, AnswerPolicyV5:
		return true
	default:
		return false
	}
}

// IsValidAgentConfigVersionPair is the single write-time contract for an
// AgentConfig prompt and answer-policy binding. Legacy configs intentionally
// have no policy; LegacyAnswerPolicyVersion is only their read projection.
func IsValidAgentConfigVersionPair(systemPromptVersion, answerPolicyVersion string) bool {
	prompt := strings.TrimSpace(systemPromptVersion)
	policy := strings.TrimSpace(answerPolicyVersion)

	switch policy {
	case "":
		switch prompt {
		case "prompt_v1", "prompt_v2", "prompt_v3", "prompt_v4", "prompt_v5", "prompt_v6", "prompt_v7":
			return true
		default:
			return false
		}
	case AnswerPolicyV1:
		return prompt == "prompt_v8"
	case AnswerPolicyV2:
		return prompt == "prompt_v9"
	case AnswerPolicyV3:
		return prompt == "prompt_v10"
	case AnswerPolicyV4:
		return prompt == "prompt_v11"
	case AnswerPolicyV5:
		return prompt == "prompt_v12"
	default:
		return false
	}
}

func IsRegisteredRetrievalStrategy(value string) bool {
	switch strings.TrimSpace(value) {
	case RetrievalStrategyNativeDenseV1:
		return true
	default:
		return false
	}
}

type MessageExecutionEvidence struct {
	MessageID                     string                          `json:"message_id"`
	ConversationID                string                          `json:"conversation_id"`
	RequestID                     string                          `json:"request_id"`
	ConfigVersion                 string                          `json:"config_version"`
	RetrievalConfigVersion        string                          `json:"retrieval_config_version"`
	KnowledgeSnapshotID           string                          `json:"knowledge_snapshot_id,omitempty"`
	KnowledgeSnapshotManifestHash string                          `json:"knowledge_snapshot_manifest_hash,omitempty"`
	FreshnessStatus               string                          `json:"freshness_status,omitempty"`
	ModelID                       string                          `json:"model_id"`
	GenerationConfig              json.RawMessage                 `json:"generation_config"`
	RetrievalConfig               json.RawMessage                 `json:"retrieval_config"`
	Usage                         json.RawMessage                 `json:"usage,omitempty"`
	Refusal                       bool                            `json:"refusal"`
	FinishReason                  string                          `json:"finish_reason"`
	CompletedAt                   string                          `json:"completed_at"`
	Citations                     []Citation                      `json:"citations,omitempty"`
	Intent                        string                          `json:"intent"`
	RetrievalPerformed            bool                            `json:"retrieval_performed"`
	GenerationPerformed           bool                            `json:"generation_performed"`
	KnowledgeUsed                 bool                            `json:"knowledge_used"`
	AnswerDisposition             string                          `json:"answer_disposition,omitempty"`
	AnswerPolicyVersion           string                          `json:"answer_policy_version,omitempty"`
	PolicyEvaluationPerformed     *bool                           `json:"policy_evaluation_performed,omitempty"`
	AnswerGenerationPerformed     *bool                           `json:"answer_generation_performed,omitempty"`
	Clarification                 *RuntimeClarification           `json:"clarification,omitempty"`
	Coverage                      *RuntimeCoverage                `json:"coverage,omitempty"`
	Resolution                    *RuntimeClarificationResolution `json:"resolution,omitempty"`
	ResolvedQuery                 string                          `json:"resolved_query,omitempty"`
	RetrievalCandidates           []RuntimeRetrievalCandidate     `json:"retrieval_candidates,omitempty"`
	Execution                     json.RawMessage                 `json:"execution,omitempty"`
	UsedCitationRanks             []int                           `json:"used_citation_ranks,omitempty"`
	LegacyFallback                bool                            `json:"legacy_fallback"`
	InvalidForQuality             bool                            `json:"invalid_for_quality"`
	ContextResolution             json.RawMessage                 `json:"context_resolution,omitempty"`
}

type MessageExecutionEvidencePage struct {
	Items      []MessageExecutionEvidence `json:"items"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

type EvalReportRegistration struct {
	ReportID               string          `json:"report_id"`
	DatasetID              string          `json:"dataset_id"`
	ModelID                string          `json:"model_id"`
	ConfigVersion          string          `json:"config_version"`
	RetrievalConfigVersion string          `json:"retrieval_config_version"`
	GenerationConfig       json.RawMessage `json:"generation_config"`
	Metrics                json.RawMessage `json:"metrics"`
	CompletedAt            string          `json:"completed_at"`
	Report                 json.RawMessage `json:"report_payload"`
}

type EvalReport struct {
	EvalReportRegistration
	CreatedAt         string `json:"created_at"`
	LegacyFallback    bool   `json:"legacy_fallback"`
	InvalidForQuality bool   `json:"invalid_for_quality"`
}

type EvalReportPage struct {
	Items      []EvalReport `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type AnswerFeedback struct {
	FeedbackID            string `json:"feedback_id"`
	MessageID             string `json:"message_id"`
	ConversationID        string `json:"conversation_id"`
	ActorID               string `json:"actor_id"`
	Kind                  string `json:"kind"`
	Comment               string `json:"comment,omitempty"`
	CorrectedAnswer       string `json:"corrected_answer,omitempty"`
	ExpectedCitationRanks []int  `json:"expected_citation_ranks,omitempty"`
	CreatedAt             string `json:"created_at"`
}

func IsAnswerFeedbackKind(value string) bool {
	switch strings.TrimSpace(value) {
	case "correct", "incorrect", "should_refuse", "missing_evidence", "wrong_evidence", "outdated_knowledge", "knowledge_gap", "should_clarify", "unnecessary_clarification", "incomplete_coverage":
		return true
	default:
		return false
	}
}

func IsA1DispositionFeedbackKind(value string) bool {
	switch strings.TrimSpace(value) {
	case "should_clarify", "unnecessary_clarification", "incomplete_coverage":
		return true
	default:
		return false
	}
}

type QualityExperiment struct {
	ExperimentID                    string          `json:"experiment_id"`
	Status                          string          `json:"status"`
	CreatedBy                       string          `json:"created_by"`
	BaselineConfigVersion           string          `json:"baseline_config_version"`
	BaselineRetrievalConfigVersion  string          `json:"baseline_retrieval_config_version"`
	CandidateConfigVersion          string          `json:"candidate_config_version"`
	CandidateRetrievalConfigVersion string          `json:"candidate_retrieval_config_version"`
	KnowledgeSnapshotID             string          `json:"knowledge_snapshot_id"`
	KnowledgeSnapshotManifestHash   string          `json:"knowledge_snapshot_manifest_hash"`
	DatasetID                       string          `json:"dataset_id"`
	DatasetSHA256                   string          `json:"dataset_sha256"`
	Conditions                      json.RawMessage `json:"conditions"`
	ConditionsSHA256                string          `json:"conditions_sha256"`
	Acceptance                      json.RawMessage `json:"acceptance"`
	FeedbackIDs                     []string        `json:"feedback_ids,omitempty"`
	BaselineReportID                string          `json:"baseline_report_id,omitempty"`
	CandidateReportID               string          `json:"candidate_report_id,omitempty"`
	DecisionReason                  string          `json:"decision_reason,omitempty"`
	ReleaseID                       string          `json:"release_id,omitempty"`
	CreatedAt                       string          `json:"created_at"`
	FrozenAt                        *string         `json:"frozen_at,omitempty"`
	ApprovedAt                      *string         `json:"approved_at,omitempty"`
	PublishedAt                     *string         `json:"published_at,omitempty"`
}

type QualityExperimentPage struct {
	Items      []QualityExperiment `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

type CreateQualityExperimentRequest struct {
	BaselineConfigVersion, BaselineRetrievalConfigVersion, CandidateConfigVersion, CandidateRetrievalConfigVersion string
	KnowledgeSnapshotID, KnowledgeSnapshotManifestHash, DatasetID, DatasetSHA256                                   string
	Conditions, Acceptance                                                                                         json.RawMessage
}

type QualityRelease struct {
	ReleaseID              string `json:"release_id"`
	Status                 string `json:"status"`
	ConfigVersion          string `json:"config_version"`
	RetrievalConfigVersion string `json:"retrieval_config_version"`
	ExperimentID           string `json:"experiment_id,omitempty"`
	PreviousReleaseID      string `json:"previous_release_id,omitempty"`
	CreatedBy              string `json:"created_by"`
	CreatedAt              string `json:"created_at"`
}
type QualityReleasePage struct {
	Items      []QualityRelease `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// OnboardingDataset is an administrator-owned, snapshot-scoped set of
// reviewed business truth. It is separate from QualityExperiment because
// knowledge acceptance does not by itself publish a config pair.
type OnboardingDataset struct {
	DatasetID                     string           `json:"dataset_id"`
	Name                          string           `json:"name"`
	Status                        string           `json:"status"`
	KnowledgeSnapshotID           string           `json:"knowledge_snapshot_id"`
	KnowledgeSnapshotManifestHash string           `json:"knowledge_snapshot_manifest_hash"`
	CasesSHA256                   string           `json:"cases_sha256,omitempty"`
	CreatedBy                     string           `json:"created_by"`
	CreatedAt                     string           `json:"created_at"`
	FrozenAt                      *string          `json:"frozen_at,omitempty"`
	Cases                         []OnboardingCase `json:"cases,omitempty"`
}

type OnboardingDatasetPage struct {
	Items []OnboardingDataset `json:"items"`
}

type OnboardingCase struct {
	CaseID               string   `json:"case_id"`
	DatasetID            string   `json:"dataset_id"`
	CaseType             string   `json:"case_type"`
	Question             string   `json:"question"`
	ExpectedRevisionIDs  []string `json:"expected_revision_ids,omitempty"`
	ExpectedLocatorHints []string `json:"expected_locator_hints,omitempty"`
	ExpectedAnswerPoints []string `json:"expected_answer_points,omitempty"`
	ReviewStatus         string   `json:"review_status"`
	ReviewedBy           string   `json:"reviewed_by,omitempty"`
	CreatedAt            string   `json:"created_at"`
	UpdatedAt            string   `json:"updated_at"`
}

func IsOnboardingCaseType(value string) bool {
	return value == "must_answer" || value == "must_refuse"
}

type OnboardingRun struct {
	RunID                         string                 `json:"run_id"`
	DatasetID                     string                 `json:"dataset_id"`
	Status                        string                 `json:"status"`
	KnowledgeSnapshotID           string                 `json:"knowledge_snapshot_id"`
	KnowledgeSnapshotManifestHash string                 `json:"knowledge_snapshot_manifest_hash"`
	ConfigVersion                 string                 `json:"config_version"`
	RetrievalConfigVersion        string                 `json:"retrieval_config_version"`
	CasesSHA256                   string                 `json:"cases_sha256"`
	Metrics                       json.RawMessage        `json:"metrics,omitempty"`
	Error                         *OnboardingRunError    `json:"error,omitempty"`
	CreatedAt                     string                 `json:"created_at"`
	StartedAt                     *string                `json:"started_at,omitempty"`
	CompletedAt                   *string                `json:"completed_at,omitempty"`
	Results                       []OnboardingCaseResult `json:"results,omitempty"`
}

type OnboardingRunError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type OnboardingCaseResult struct {
	RunID               string     `json:"run_id"`
	CaseID              string     `json:"case_id"`
	Passed              bool       `json:"passed"`
	Refusal             bool       `json:"refusal"`
	ObservedAnswer      string     `json:"observed_answer"`
	ObservedCitations   []Citation `json:"observed_citations,omitempty"`
	ExpectedRevisionHit *bool      `json:"expected_revision_hit,omitempty"`
	ExpectedLocatorHit  *bool      `json:"expected_locator_hit,omitempty"`
	AnswerPointHitRate  *float64   `json:"answer_point_hit_rate,omitempty"`
	FailureCode         string     `json:"failure_code,omitempty"`
	CompletedAt         string     `json:"completed_at"`
}

type KnowledgeDeliveryEvidence struct {
	DeliveryID      string  `json:"delivery_id"`
	Topic           string  `json:"topic"`
	DocumentID      string  `json:"document_id,omitempty"`
	RevisionID      string  `json:"revision_id,omitempty"`
	SnapshotID      string  `json:"snapshot_id,omitempty"`
	Status          string  `json:"status"`
	AttemptCount    int     `json:"attempt_count"`
	AvailableAt     string  `json:"available_at"`
	StreamMessageID string  `json:"stream_message_id,omitempty"`
	LeaseUntil      *string `json:"lease_until,omitempty"`
	LastError       string  `json:"last_error,omitempty"`
	CompletedAt     *string `json:"completed_at,omitempty"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

type KnowledgeDeliveryEvidencePage struct {
	Items      []KnowledgeDeliveryEvidence `json:"items"`
	NextCursor string                      `json:"next_cursor,omitempty"`
}
