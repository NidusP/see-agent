package repository

import (
	"context"
	"encoding/json"
	"time"

	"see-agent/services/backend-go/internal/domain"
)

// HealthStore performs a bounded, read-only dependency probe. It is used by
// strict-profile readiness only; a memory store must not claim durable
// full-stack readiness.
type HealthStore interface {
	Ping(context.Context) error
}

// KnowledgeEvolutionStore is intentionally separate from Store until K4/K5 migrate the product API and chat path.
// The existing MemoryStore must not pretend to implement durable revision/snapshot semantics.
type KnowledgeEvolutionStore interface {
	CreateKnowledgeDocument(ctx context.Context, document domain.KnowledgeDocument) (domain.KnowledgeDocument, error)
	CreateKnowledgeRevision(ctx context.Context, revision domain.KnowledgeRevision) (domain.KnowledgeRevision, error)
	QueueKnowledgeRevision(ctx context.Context, revisionID string) (domain.KnowledgeRevision, error)
	MarkKnowledgeRevisionReady(ctx context.Context, revisionID, chunkManifestSHA256, embeddingModel string) (domain.KnowledgeRevision, error)
	PublishKnowledgeRevision(ctx context.Context, revisionID, actorID, correlationID string) (domain.KnowledgeSnapshot, error)
	GetActivePublishedKnowledgeSnapshot(ctx context.Context) (domain.KnowledgeSnapshot, bool, error)
	ListSchemaMigrations(ctx context.Context) ([]string, error)
	GetLegacyKnowledgeRevision(ctx context.Context, fileID string) (domain.KnowledgeRevision, bool, error)
}

// KnowledgeLifecycleStore is the K4 authoritative write/read boundary. It is
// intentionally Postgres-only: MemoryStore remains the legacy local smoke
// implementation and must not claim durable revision/snapshot semantics.
type KnowledgeLifecycleStore interface {
	GetKnowledgeMutation(ctx context.Context, key domain.KnowledgeMutationKey) (json.RawMessage, bool, error)
	CreateKnowledgeDocumentRevision(ctx context.Context, document domain.KnowledgeDocument, revision domain.KnowledgeRevision, key domain.KnowledgeMutationKey) (domain.KnowledgeRevisionUpload, error)
	CreateKnowledgeReplacementRevision(ctx context.Context, revision domain.KnowledgeRevision, key domain.KnowledgeMutationKey) (domain.KnowledgeRevisionUpload, error)
	ListKnowledgeDocuments(ctx context.Context, status, cursor string, limit int) (domain.KnowledgeDocumentPage, error)
	GetKnowledgeDocument(ctx context.Context, documentID string) (domain.KnowledgeDocumentDetail, error)
	GetKnowledgeRetirementReadiness(ctx context.Context, documentID string) (domain.KnowledgeRetirementReadiness, error)
	PrepareKnowledgeRevisionPublish(ctx context.Context, revisionID string, key domain.KnowledgeMutationKey) (domain.KnowledgeLifecycleMutation, error)
	ActivateKnowledgeSnapshot(ctx context.Context, snapshotID string, key domain.KnowledgeMutationKey, acknowledgeUnvalidated bool) (domain.KnowledgeLifecycleMutation, error)
	PublishKnowledgeRevisionLifecycle(ctx context.Context, revisionID string, key domain.KnowledgeMutationKey, acknowledgeUnvalidated bool) (domain.KnowledgeLifecycleMutation, error)
	DisableKnowledgeDocument(ctx context.Context, documentID string, key domain.KnowledgeMutationKey) (domain.KnowledgeLifecycleMutation, error)
	DeleteKnowledgeDocument(ctx context.Context, documentID string, key domain.KnowledgeMutationKey) (domain.KnowledgeLifecycleMutation, error)
	DisableKnowledgeDocumentAfterReadiness(ctx context.Context, documentID string, key domain.KnowledgeMutationKey) (domain.KnowledgeLifecycleMutation, error)
	DeleteKnowledgeDocumentAfterReadiness(ctx context.Context, documentID string, key domain.KnowledgeMutationKey) (domain.KnowledgeLifecycleMutation, error)
}

// KnowledgeScopedChatStore is the K5 production boundary. It is deliberately
// separate from Store so the local MemoryStore remains an explicit legacy-only
// development path rather than claiming durable snapshot semantics.
type KnowledgeScopedChatStore interface {
	ResolveActiveKnowledgeScope(ctx context.Context) (domain.RuntimeKnowledgeScope, error)
	AppendScopedRuntimeExchange(ctx context.Context, conversationID, userMessageID, content string, scope domain.RuntimeKnowledgeScope, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, error)
	GetMessageSources(messageID string) (domain.MessageSources, bool)
}

// ClarificationStore is the durable A1 state authority. It is intentionally
// Postgres-only: MemoryStore must not imitate conflict-safe pending state.
type ClarificationStore interface {
	GetPendingClarification(ctx context.Context, conversationID string) (domain.RuntimePendingClarification, bool, error)
	AppendScopedRuntimeExchangeWithPending(ctx context.Context, conversationID, userMessageID, content string, scope domain.RuntimeKnowledgeScope, expectedPending *domain.RuntimePendingClarification, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, error)
}

// ControlledChatStore persists non-knowledge answers and their execution facts
// atomically without inventing snapshot provenance.
type ControlledChatStore interface {
	AppendControlledRuntimeExchange(ctx context.Context, conversationID, userMessageID, content string, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, error)
}

// ChatExecutionAttemptStore is the durable failure/audit boundary. Product
// callers use it to record the dispatch lifecycle; repositories must never
// turn a failed attempt into a completed answer.
type ChatExecutionAttemptStore interface {
	CreateChatExecutionAttempt(ctx context.Context, attempt domain.ChatExecutionAttempt) (domain.ChatExecutionAttempt, error)
	TransitionChatExecutionAttempt(ctx context.Context, attemptID, state, errorCode, errorStage string, retryable bool) (domain.ChatExecutionAttempt, error)
	GetChatExecutionAttempt(ctx context.Context, attemptID string) (domain.ChatExecutionAttempt, bool, error)
}

// ConversationOwnershipStore is the product-facing authorization read/write
// boundary. Background workers may use Store.GetConversation internally, but
// every external chat route must use these owner-filtered methods.
type ConversationOwnershipStore interface {
	CreateOwnedConversation(ownerID, title string) domain.Conversation
	ListOwnedConversations(ownerID string) []domain.Conversation
	GetOwnedConversation(ownerID, conversationID string) (domain.Conversation, []domain.Message, bool)
	RenameOwnedConversation(ownerID, conversationID, title string) (domain.Conversation, bool)
}

// KnowledgeFreshnessStore is K6's durable Postgres boundary. MemoryStore must
// not emulate calendar, stale detection or revalidation evidence.
type KnowledgeFreshnessStore interface {
	EnsureKnowledgeFreshnessScans(ctx context.Context) error
	ClaimKnowledgeFreshnessScan(ctx context.Context, lease time.Duration) (domain.KnowledgeFreshnessScan, domain.KnowledgeUpdateEvent, bool, error)
	CompleteKnowledgeFreshnessScan(ctx context.Context, eventID string) (domain.KnowledgeFreshnessScan, error)
	RetryKnowledgeFreshnessScan(ctx context.Context, eventID, failure string, availableAt time.Time) error
	ListKnowledgeUpdates(ctx context.Context, documentID, cursor string, limit int) (domain.KnowledgeUpdatePage, error)
	GetKnowledgeSnapshot(ctx context.Context, snapshotID string) (domain.KnowledgeSnapshot, bool, error)
	CreateRevalidationRun(ctx context.Context, request domain.RevalidationRequest) (domain.RevalidationRun, error)
	GetRevalidationRun(ctx context.Context, conversationID, revalidationID string) (domain.RevalidationRun, bool, error)
	ClaimRevalidationRun(ctx context.Context, lease time.Duration) (domain.RevalidationRun, bool, error)
	CompleteRevalidationRun(ctx context.Context, runID string, response domain.ChatRuntimeResponse, outcome string) (domain.RevalidationRun, error)
	FailRevalidationRun(ctx context.Context, runID, code, message string) (domain.RevalidationRun, error)
}

// ExecutionEvidenceStore is the K7 admin read boundary. It is Postgres-only
// because a memory response cannot honestly represent durable audit evidence.
type ExecutionEvidenceStore interface {
	GetMessageExecutionEvidence(ctx context.Context, messageID string) (domain.MessageExecutionEvidence, bool, error)
	ListMessageExecutionEvidence(ctx context.Context, conversationID, snapshotID, revisionID, cursor string, limit int) (domain.MessageExecutionEvidencePage, error)
	RegisterEvalReport(ctx context.Context, idempotencyKey string, registration domain.EvalReportRegistration) (domain.EvalReport, error)
	GetEvalReport(ctx context.Context, reportID string) (domain.EvalReport, bool, error)
	ListEvalReports(ctx context.Context, configVersion, retrievalConfigVersion, cursor string, limit int) (domain.EvalReportPage, error)
	ListKnowledgeDeliveryEvidence(ctx context.Context, status, documentID, revisionID, cursor string, limit int) (domain.KnowledgeDeliveryEvidencePage, error)
}

// QualityOperationsStore is deliberately Postgres-only. MemoryStore remains a
// legacy developer smoke store and must never claim it has durable feedback,
// release approval, or an active quality-release pointer.
type QualityOperationsStore interface {
	CreateAnswerFeedback(context.Context, string, string, domain.AnswerFeedback) (domain.AnswerFeedback, error)
	ListAnswerFeedback(context.Context, string, string, int) ([]domain.AnswerFeedback, error)
	CreateQualityExperiment(context.Context, string, string, domain.CreateQualityExperimentRequest) (domain.QualityExperiment, error)
	ListQualityExperiments(context.Context, string, int) (domain.QualityExperimentPage, error)
	GetQualityExperiment(context.Context, string) (domain.QualityExperiment, bool, error)
	AttachQualityExperimentFeedback(context.Context, string, string, string, string) (domain.QualityExperiment, error)
	FreezeQualityExperiment(context.Context, string, string, string) (domain.QualityExperiment, error)
	RegisterQualityExperimentReport(context.Context, string, string, string, string, string) (domain.QualityExperiment, error)
	ApproveQualityExperiment(context.Context, string, string, string, string) (domain.QualityExperiment, error)
	PublishQualityExperiment(context.Context, string, string, string) (domain.QualityRelease, error)
	ListQualityReleases(context.Context, int) (domain.QualityReleasePage, error)
	RollbackQualityRelease(context.Context, string, string, string) (domain.QualityRelease, error)
}

// OnboardingQualityStore owns administrator-reviewed truth and its immutable
// snapshot-scoped execution records. It is Postgres-only; the MemoryStore is
// intentionally not a pretend production quality authority.
type OnboardingQualityStore interface {
	CreateOnboardingDataset(context.Context, string, string, string, string) (domain.OnboardingDataset, error)
	ListOnboardingDatasets(context.Context, int) (domain.OnboardingDatasetPage, error)
	GetOnboardingDataset(context.Context, string) (domain.OnboardingDataset, bool, error)
	CreateOnboardingCase(context.Context, string, string, domain.OnboardingCase) (domain.OnboardingCase, error)
	FreezeOnboardingDataset(context.Context, string, string, string) (domain.OnboardingDataset, error)
	GenerateOnboardingDrafts(context.Context, string, string, string) ([]domain.OnboardingCase, error)
	ReviewOnboardingCase(context.Context, string, string, string, string, domain.OnboardingCase) (domain.OnboardingCase, error)
	CreateOnboardingRun(context.Context, string, string, string) (domain.OnboardingRun, error)
	GetOnboardingRun(context.Context, string) (domain.OnboardingRun, bool, error)
	ClaimOnboardingRun(context.Context, time.Duration) (domain.OnboardingRun, bool, error)
	CompleteOnboardingRun(context.Context, string, string, []domain.OnboardingCaseResult, json.RawMessage) (domain.OnboardingRun, error)
	FailOnboardingRun(context.Context, string, string, string) (domain.OnboardingRun, error)
}

// KnowledgeDelivery is K3's durable view of a revision-index outbox record.
// The revision is read under the same lock when a worker claims the delivery.
type KnowledgeDelivery struct {
	DeliveryID      string
	Topic           string
	DocumentID      string
	RevisionID      string
	SnapshotID      string
	Status          string
	AttemptCount    int
	AvailableAt     time.Time
	StreamMessageID string
	LeaseUntil      *time.Time
	LastError       string
	CompletedAt     *time.Time
	Revision        domain.KnowledgeRevision
}

type KnowledgeDeliveryResult struct {
	DeliveryID          string
	DocumentID          string
	RevisionID          string
	ChecksumSHA256      string
	ChunkManifestSHA256 string
	EmbeddingModel      string
}

// KnowledgeDeliveryStore separates the K3 relay/consumer write path from K2's
// publication transaction. A MemoryStore intentionally does not implement it.
type KnowledgeDeliveryStore interface {
	ListPendingKnowledgeRevisionDeliveries(ctx context.Context, limit int) ([]KnowledgeDelivery, error)
	MarkKnowledgeDeliveryStreamed(ctx context.Context, deliveryID, streamMessageID string) (KnowledgeDelivery, error)
	ClaimKnowledgeRevisionDelivery(ctx context.Context, deliveryID, streamMessageID string, leaseFor time.Duration) (KnowledgeDelivery, bool, error)
	CompleteKnowledgeRevisionDelivery(ctx context.Context, streamMessageID string, result KnowledgeDeliveryResult) (KnowledgeDelivery, error)
	ScheduleKnowledgeRevisionDeliveryRetry(ctx context.Context, deliveryID, streamMessageID, failure string, availableAt time.Time) (KnowledgeDelivery, error)
	DeadLetterKnowledgeRevisionDelivery(ctx context.Context, deliveryID, streamMessageID, failure string) (KnowledgeDelivery, error)
}

type Store interface {
	ActiveConfig() domain.AgentConfig
	GetConfig(configVersion string) (domain.AgentConfig, bool)
	CreateConfig(modelID, promptVersion, responseStyle, answerPolicyVersion string, temperature, topP float64, maxOutputTokens int) domain.AgentConfig
	ListConfigs() []domain.AgentConfig
	ActiveRetrievalConfig() domain.RetrievalConfig
	GetRetrievalConfig(retrievalConfigVersion string) (domain.RetrievalConfig, bool)
	CreateRetrievalConfig(retrievalStrategy, embeddingModel string, topK int, rerankEnabled bool, evidenceMinScore float64) domain.RetrievalConfig
	ListRetrievalConfigs() []domain.RetrievalConfig
	CreateConversation(title string) domain.Conversation
	ListConversations() []domain.Conversation
	GetConversation(id string) (domain.Conversation, []domain.Message, bool)
	ReserveMessageID() string
	AppendRuntimeExchange(conversationID, content string, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, bool)
	AppendRuntimeExchangeWithIDs(conversationID, userMessageID, content string, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, bool)
	GetSources(messageID string) []domain.Citation
	GetMessageSources(messageID string) (domain.MessageSources, bool)
	ListFiles(status, sourceFormat string) []domain.KnowledgeFile
	ReserveFileID() string
	CreateFile(fileID, filename, providedMIMEType, objectURI string, forceReindex bool) (domain.KnowledgeFile, domain.IngestionTaskSummary)
	MarkTaskRunning(fileID, taskID string) bool
	GetFile(fileID string) (domain.KnowledgeFile, domain.IngestionTaskSummary, bool)
	ReindexFile(fileID string, forceReindex bool) (domain.KnowledgeFile, domain.IngestionTaskSummary, bool)
	ListChunks(fileID string) ([]domain.ChunkRecord, bool)
	ApplyIngestionResult(fileID, taskID string, result domain.IngestionTaskResult) bool
}

var _ Store = (*MemoryStore)(nil)
