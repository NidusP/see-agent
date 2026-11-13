package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/objectstore"
	"see-agent/services/backend-go/internal/repository"
	"see-agent/services/backend-go/internal/taskqueue"
)

var (
	ErrExecutionEvidenceUnavailable = errors.New("execution evidence requires PostgreSQL")
	ErrAgentConfigInvalid           = errors.New("agent config version pair is invalid")
	ErrEvalReportInvalid            = errors.New("eval report registration is invalid")
	ErrDeliveryEvidenceFilter       = errors.New("delivery evidence filter is invalid")
	ErrQualityOperationsUnavailable = errors.New("quality operations require PostgreSQL")
	ErrQualityOperationsInvalid     = errors.New("quality operation is invalid")
	ErrOnboardingQualityUnavailable = errors.New("onboarding quality requires PostgreSQL")
	ErrOnboardingQualityInvalid     = errors.New("onboarding quality operation is invalid")
)

type RuntimeClient interface {
	RunIngestion(ctx context.Context, file domain.KnowledgeFile, task domain.IngestionTaskSummary) (domain.IngestionTaskResult, error)
	RunChat(ctx context.Context, request domain.ChatRuntimeRequest) (domain.ChatRuntimeResponse, error)
	StreamChat(ctx context.Context, request domain.ChatRuntimeRequest, emit func(domain.ChatRuntimeStreamEvent) error) error
}

type Service struct {
	store                  repository.Store
	runtimeClient          RuntimeClient
	uploadStore            objectstore.Store
	ingestionQueue         taskqueue.Queue
	workerCancel           context.CancelFunc
	k6WorkerCancel         context.CancelFunc
	onboardingWorkerCancel context.CancelFunc
	chatRuntimeEnabled     bool
	runtimeCallTimeout     time.Duration
}

const defaultRuntimeCallTimeout = 20 * time.Second

func New(store repository.Store, runtimeClient RuntimeClient, uploadRoot string, chatRuntimeEnabled bool) *Service {
	return NewWithUploadStore(store, runtimeClient, objectstore.LocalStore{Root: uploadRoot}, chatRuntimeEnabled)
}

func NewWithUploadStore(store repository.Store, runtimeClient RuntimeClient, uploadStore objectstore.Store, chatRuntimeEnabled bool) *Service {
	return NewWithDependencies(store, runtimeClient, uploadStore, taskqueue.NewLocalQueue(0), chatRuntimeEnabled)
}

func NewWithDependencies(store repository.Store, runtimeClient RuntimeClient, uploadStore objectstore.Store, ingestionQueue taskqueue.Queue, chatRuntimeEnabled bool) *Service {
	if uploadStore == nil {
		uploadStore = objectstore.LocalStore{}
	}
	if ingestionQueue == nil {
		ingestionQueue = taskqueue.NewLocalQueue(0)
	}
	svc := &Service{
		store:              store,
		runtimeClient:      runtimeClient,
		uploadStore:        uploadStore,
		ingestionQueue:     ingestionQueue,
		chatRuntimeEnabled: chatRuntimeEnabled,
		runtimeCallTimeout: defaultRuntimeCallTimeout,
	}
	svc.startIngestionWorker()
	svc.startK6Worker()
	svc.startOnboardingQualityWorker()
	return svc
}

// SetRuntimeCallTimeout is startup configuration owned by the outer server.
// It applies identically to normal chat, SSE, revalidation and onboarding;
// a non-positive value is rejected by retaining the known-safe default.
func (s *Service) SetRuntimeCallTimeout(timeout time.Duration) {
	if timeout > 0 {
		s.runtimeCallTimeout = timeout
	}
}

func (s *Service) RuntimeCallTimeout() time.Duration {
	if s.runtimeCallTimeout <= 0 {
		return defaultRuntimeCallTimeout
	}
	return s.runtimeCallTimeout
}

func (s *Service) Close() error {
	if s.workerCancel != nil {
		s.workerCancel()
	}
	if s.k6WorkerCancel != nil {
		s.k6WorkerCancel()
	}
	if s.onboardingWorkerCancel != nil {
		s.onboardingWorkerCancel()
	}
	if s.ingestionQueue != nil {
		return s.ingestionQueue.Close()
	}
	return nil
}

func (s *Service) ActiveConfig() domain.AgentConfig {
	return s.store.ActiveConfig()
}

func (s *Service) UpdateConfig(modelID, promptVersion, responseStyle, answerPolicyVersion string, temperature, topP float64, maxOutputTokens int) (domain.AgentConfig, error) {
	promptVersion = strings.TrimSpace(promptVersion)
	answerPolicyVersion = strings.TrimSpace(answerPolicyVersion)
	if !domain.IsValidAgentConfigVersionPair(promptVersion, answerPolicyVersion) {
		return domain.AgentConfig{}, ErrAgentConfigInvalid
	}
	return s.store.CreateConfig(modelID, promptVersion, responseStyle, answerPolicyVersion, temperature, topP, maxOutputTokens), nil
}

func (s *Service) ListConfigs() []domain.AgentConfig {
	return s.store.ListConfigs()
}

func (s *Service) ActiveRetrievalConfig() domain.RetrievalConfig {
	return s.store.ActiveRetrievalConfig()
}

func (s *Service) UpdateRetrievalConfig(retrievalStrategy, embeddingModel string, topK int, rerankEnabled bool, evidenceMinScore float64) domain.RetrievalConfig {
	return s.store.CreateRetrievalConfig(retrievalStrategy, embeddingModel, topK, rerankEnabled, evidenceMinScore)
}

func (s *Service) ListRetrievalConfigs() []domain.RetrievalConfig {
	return s.store.ListRetrievalConfigs()
}

func (s *Service) GetMessageExecutionEvidence(ctx context.Context, messageID string) (domain.MessageExecutionEvidence, bool, error) {
	store, ok := s.store.(repository.ExecutionEvidenceStore)
	if !ok {
		return domain.MessageExecutionEvidence{}, false, ErrExecutionEvidenceUnavailable
	}
	return store.GetMessageExecutionEvidence(ctx, messageID)
}

func (s *Service) ListMessageExecutionEvidence(ctx context.Context, conversationID, snapshotID, revisionID, cursor string, limit int) (domain.MessageExecutionEvidencePage, error) {
	store, ok := s.store.(repository.ExecutionEvidenceStore)
	if !ok {
		return domain.MessageExecutionEvidencePage{}, ErrExecutionEvidenceUnavailable
	}
	return store.ListMessageExecutionEvidence(ctx, conversationID, snapshotID, revisionID, cursor, limit)
}

func (s *Service) RegisterEvalReport(ctx context.Context, idempotencyKey string, registration domain.EvalReportRegistration) (domain.EvalReport, error) {
	if strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(registration.ReportID) == "" || strings.TrimSpace(registration.DatasetID) == "" ||
		strings.TrimSpace(registration.ModelID) == "" || strings.TrimSpace(registration.ConfigVersion) == "" || strings.TrimSpace(registration.RetrievalConfigVersion) == "" ||
		!json.Valid(registration.GenerationConfig) || !json.Valid(registration.Metrics) || !json.Valid(registration.Report) {
		return domain.EvalReport{}, ErrEvalReportInvalid
	}
	completedAt, err := time.Parse(time.RFC3339, registration.CompletedAt)
	if err != nil {
		return domain.EvalReport{}, ErrEvalReportInvalid
	}
	registration.CompletedAt = completedAt.UTC().Format(time.RFC3339)
	store, ok := s.store.(repository.ExecutionEvidenceStore)
	if !ok {
		return domain.EvalReport{}, ErrExecutionEvidenceUnavailable
	}
	return store.RegisterEvalReport(ctx, idempotencyKey, registration)
}

func (s *Service) GetEvalReport(ctx context.Context, reportID string) (domain.EvalReport, bool, error) {
	store, ok := s.store.(repository.ExecutionEvidenceStore)
	if !ok {
		return domain.EvalReport{}, false, ErrExecutionEvidenceUnavailable
	}
	return store.GetEvalReport(ctx, reportID)
}

func (s *Service) ListEvalReports(ctx context.Context, configVersion, retrievalConfigVersion, cursor string, limit int) (domain.EvalReportPage, error) {
	store, ok := s.store.(repository.ExecutionEvidenceStore)
	if !ok {
		return domain.EvalReportPage{}, ErrExecutionEvidenceUnavailable
	}
	return store.ListEvalReports(ctx, configVersion, retrievalConfigVersion, cursor, limit)
}

func (s *Service) ListKnowledgeDeliveryEvidence(ctx context.Context, status, documentID, revisionID, cursor string, limit int) (domain.KnowledgeDeliveryEvidencePage, error) {
	status = strings.TrimSpace(status)
	if status != "" {
		allowed := map[string]bool{"pending": true, "streamed": true, "indexing": true, "retry": true, "delivered": true, "dead_letter": true}
		if !allowed[status] {
			return domain.KnowledgeDeliveryEvidencePage{}, ErrDeliveryEvidenceFilter
		}
	}
	store, ok := s.store.(repository.ExecutionEvidenceStore)
	if !ok {
		return domain.KnowledgeDeliveryEvidencePage{}, ErrExecutionEvidenceUnavailable
	}
	return store.ListKnowledgeDeliveryEvidence(ctx, status, documentID, revisionID, cursor, limit)
}

func (s *Service) qualityOperationsStore() (repository.QualityOperationsStore, error) {
	store, ok := s.store.(repository.QualityOperationsStore)
	if !ok {
		return nil, ErrQualityOperationsUnavailable
	}
	return store, nil
}
func (s *Service) onboardingQualityStore() (repository.OnboardingQualityStore, error) {
	store, ok := s.store.(repository.OnboardingQualityStore)
	if !ok {
		return nil, ErrOnboardingQualityUnavailable
	}
	return store, nil
}
func (s *Service) CreateOnboardingDataset(ctx context.Context, actor, key, name, snapshotID string) (domain.OnboardingDataset, error) {
	store, err := s.onboardingQualityStore()
	if err != nil {
		return domain.OnboardingDataset{}, err
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(snapshotID) == "" {
		return domain.OnboardingDataset{}, ErrOnboardingQualityInvalid
	}
	return store.CreateOnboardingDataset(ctx, actor, key, name, snapshotID)
}
func (s *Service) ListOnboardingDatasets(ctx context.Context, limit int) (domain.OnboardingDatasetPage, error) {
	store, err := s.onboardingQualityStore()
	if err != nil {
		return domain.OnboardingDatasetPage{}, err
	}
	return store.ListOnboardingDatasets(ctx, limit)
}
func (s *Service) GetOnboardingDataset(ctx context.Context, id string) (domain.OnboardingDataset, bool, error) {
	store, err := s.onboardingQualityStore()
	if err != nil {
		return domain.OnboardingDataset{}, false, err
	}
	return store.GetOnboardingDataset(ctx, id)
}
func (s *Service) CreateOnboardingCase(ctx context.Context, actor, key string, input domain.OnboardingCase) (domain.OnboardingCase, error) {
	store, err := s.onboardingQualityStore()
	if err != nil {
		return domain.OnboardingCase{}, err
	}
	if !domain.IsOnboardingCaseType(input.CaseType) || strings.TrimSpace(input.Question) == "" || (input.ReviewStatus != "draft" && input.ReviewStatus != "reviewed") {
		return domain.OnboardingCase{}, ErrOnboardingQualityInvalid
	}
	return store.CreateOnboardingCase(ctx, actor, key, input)
}
func (s *Service) FreezeOnboardingDataset(ctx context.Context, actor, id, key string) (domain.OnboardingDataset, error) {
	store, err := s.onboardingQualityStore()
	if err != nil {
		return domain.OnboardingDataset{}, err
	}
	return store.FreezeOnboardingDataset(ctx, actor, id, key)
}
func (s *Service) GenerateOnboardingDrafts(ctx context.Context, actor, id, key string) ([]domain.OnboardingCase, error) {
	store, err := s.onboardingQualityStore()
	if err != nil {
		return nil, err
	}
	return store.GenerateOnboardingDrafts(ctx, actor, id, key)
}
func (s *Service) ReviewOnboardingCase(ctx context.Context, actor, datasetID, caseID, key string, input domain.OnboardingCase) (domain.OnboardingCase, error) {
	store, err := s.onboardingQualityStore()
	if err != nil {
		return domain.OnboardingCase{}, err
	}
	return store.ReviewOnboardingCase(ctx, actor, datasetID, caseID, key, input)
}
func (s *Service) CreateOnboardingRun(ctx context.Context, actor, id, key string) (domain.OnboardingRun, error) {
	store, err := s.onboardingQualityStore()
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	return store.CreateOnboardingRun(ctx, actor, id, key)
}
func (s *Service) GetOnboardingRun(ctx context.Context, id string) (domain.OnboardingRun, bool, error) {
	store, err := s.onboardingQualityStore()
	if err != nil {
		return domain.OnboardingRun{}, false, err
	}
	return store.GetOnboardingRun(ctx, id)
}
func (s *Service) CreateAnswerFeedback(ctx context.Context, actor, key string, input domain.AnswerFeedback) (domain.AnswerFeedback, error) {
	store, err := s.qualityOperationsStore()
	if err != nil {
		return domain.AnswerFeedback{}, err
	}
	if !domain.IsAnswerFeedbackKind(input.Kind) {
		return domain.AnswerFeedback{}, ErrQualityOperationsInvalid
	}
	return store.CreateAnswerFeedback(ctx, actor, key, input)
}
func (s *Service) ListAnswerFeedback(ctx context.Context, messageID, kind string, limit int) ([]domain.AnswerFeedback, error) {
	store, err := s.qualityOperationsStore()
	if err != nil {
		return nil, err
	}
	if kind != "" && !domain.IsAnswerFeedbackKind(kind) {
		return nil, ErrQualityOperationsInvalid
	}
	return store.ListAnswerFeedback(ctx, messageID, kind, limit)
}
func (s *Service) CreateQualityExperiment(ctx context.Context, actor, key string, input domain.CreateQualityExperimentRequest) (domain.QualityExperiment, error) {
	store, err := s.qualityOperationsStore()
	if err != nil {
		return domain.QualityExperiment{}, err
	}
	return store.CreateQualityExperiment(ctx, actor, key, input)
}
func (s *Service) ListQualityExperiments(ctx context.Context, status string, limit int) (domain.QualityExperimentPage, error) {
	store, err := s.qualityOperationsStore()
	if err != nil {
		return domain.QualityExperimentPage{}, err
	}
	return store.ListQualityExperiments(ctx, status, limit)
}
func (s *Service) GetQualityExperiment(ctx context.Context, id string) (domain.QualityExperiment, bool, error) {
	store, err := s.qualityOperationsStore()
	if err != nil {
		return domain.QualityExperiment{}, false, err
	}
	return store.GetQualityExperiment(ctx, id)
}
func (s *Service) AttachQualityExperimentFeedback(ctx context.Context, actor, id, key, feedbackID string) (domain.QualityExperiment, error) {
	store, err := s.qualityOperationsStore()
	if err != nil {
		return domain.QualityExperiment{}, err
	}
	return store.AttachQualityExperimentFeedback(ctx, actor, id, key, feedbackID)
}
func (s *Service) FreezeQualityExperiment(ctx context.Context, actor, id, key string) (domain.QualityExperiment, error) {
	store, err := s.qualityOperationsStore()
	if err != nil {
		return domain.QualityExperiment{}, err
	}
	return store.FreezeQualityExperiment(ctx, actor, id, key)
}
func (s *Service) RegisterQualityExperimentReport(ctx context.Context, actor, id, key, reportID, role string) (domain.QualityExperiment, error) {
	store, err := s.qualityOperationsStore()
	if err != nil {
		return domain.QualityExperiment{}, err
	}
	return store.RegisterQualityExperimentReport(ctx, actor, id, key, reportID, role)
}
func (s *Service) ApproveQualityExperiment(ctx context.Context, actor, id, key, reason string) (domain.QualityExperiment, error) {
	store, err := s.qualityOperationsStore()
	if err != nil {
		return domain.QualityExperiment{}, err
	}
	return store.ApproveQualityExperiment(ctx, actor, id, key, reason)
}
func (s *Service) PublishQualityExperiment(ctx context.Context, actor, id, key string) (domain.QualityRelease, error) {
	store, err := s.qualityOperationsStore()
	if err != nil {
		return domain.QualityRelease{}, err
	}
	return store.PublishQualityExperiment(ctx, actor, id, key)
}
func (s *Service) ListQualityReleases(ctx context.Context, limit int) (domain.QualityReleasePage, error) {
	store, err := s.qualityOperationsStore()
	if err != nil {
		return domain.QualityReleasePage{}, err
	}
	return store.ListQualityReleases(ctx, limit)
}
func (s *Service) RollbackQualityRelease(ctx context.Context, actor, id, key string) (domain.QualityRelease, error) {
	store, err := s.qualityOperationsStore()
	if err != nil {
		return domain.QualityRelease{}, err
	}
	return store.RollbackQualityRelease(ctx, actor, id, key)
}

func (s *Service) CreateConversation(title string) domain.Conversation {
	return s.store.CreateConversation(title)
}

func (s *Service) CreateConversationAs(ownerID, title string) domain.Conversation {
	store, ok := s.store.(repository.ConversationOwnershipStore)
	if !ok || strings.TrimSpace(ownerID) == "" {
		return domain.Conversation{}
	}
	return store.CreateOwnedConversation(ownerID, title)
}

func (s *Service) ListConversations() []domain.Conversation {
	return s.store.ListConversations()
}

func (s *Service) ListConversationsAs(ownerID string) []domain.Conversation {
	store, ok := s.store.(repository.ConversationOwnershipStore)
	if !ok || strings.TrimSpace(ownerID) == "" {
		return []domain.Conversation{}
	}
	return store.ListOwnedConversations(ownerID)
}

func (s *Service) GetConversation(id string) (domain.Conversation, []domain.Message, bool) {
	return s.store.GetConversation(id)
}

func (s *Service) GetConversationAs(ownerID, id string) (domain.Conversation, []domain.Message, bool) {
	store, ok := s.store.(repository.ConversationOwnershipStore)
	if !ok || strings.TrimSpace(ownerID) == "" {
		return domain.Conversation{}, nil, false
	}
	return store.GetOwnedConversation(ownerID, id)
}

func (s *Service) RenameConversationAs(ownerID, id, title string) (domain.Conversation, bool, error) {
	normalized, err := repository.NormalizeConversationTitle(title)
	if err != nil {
		return domain.Conversation{}, false, err
	}
	store, ok := s.store.(repository.ConversationOwnershipStore)
	if !ok || strings.TrimSpace(ownerID) == "" {
		return domain.Conversation{}, false, nil
	}
	conversation, found := store.RenameOwnedConversation(ownerID, id, normalized)
	return conversation, found, nil
}

func (s *Service) CreateMessage(conversationID, content string, stream bool) (domain.Message, domain.Message, bool, error) {
	return s.createMessageWithOptionalRuntime("", conversationID, content, stream)
}

func (s *Service) CreateMessageAs(ownerID, conversationID, content string, stream bool) (domain.Message, domain.Message, bool, error) {
	return s.createMessageWithOptionalRuntime(ownerID, conversationID, content, stream)
}

func (s *Service) RuntimeStreamingEnabled() bool {
	return s.chatRuntimeEnabled && s.runtimeClient != nil
}

func (s *Service) PrepareRuntimeStreamMessage(conversationID, content string) (domain.ChatRuntimeRequest, domain.Message, domain.Message, bool, error) {
	return s.prepareRuntimeStreamMessage("", conversationID, content)
}

func (s *Service) PrepareRuntimeStreamMessageAs(ownerID, conversationID, content string) (domain.ChatRuntimeRequest, domain.Message, domain.Message, bool, error) {
	return s.prepareRuntimeStreamMessage(ownerID, conversationID, content)
}

func (s *Service) StreamRuntimeChat(ctx context.Context, request domain.ChatRuntimeRequest, emit func(domain.ChatRuntimeStreamEvent) error) error {
	return s.streamRuntimeChat(ctx, request, emit)
}

func (s *Service) CompleteRuntimeStreamMessage(request domain.ChatRuntimeRequest, userMessageID, content string, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, bool, error) {
	if err := normalizeAndValidateCitationAnchors(&result); err != nil {
		return domain.Message{}, domain.Message{}, true, err
	}
	if err := validateRuntimeExecutionFacts(request, result); err != nil {
		return domain.Message{}, domain.Message{}, true, err
	}
	attachConversationRouterPolicyEvidence(&result)
	if result.Intent == "knowledge_query" && request.KnowledgeScope != nil {
		scopedStore, ok := s.store.(repository.KnowledgeScopedChatStore)
		if !ok {
			return domain.Message{}, domain.Message{}, true, fmt.Errorf("%w: scoped request requires durable knowledge store", ErrSnapshotScopeViolation)
		}
		if err := validateScopedRuntimeResponse(request, result); err != nil {
			return domain.Message{}, domain.Message{}, true, err
		}
		userMessage, assistantMessage, _, err := s.appendScopedRuntimeExchange(context.Background(), scopedStore, request, userMessageID, content, *request.KnowledgeScope, result)
		if err != nil {
			return domain.Message{}, domain.Message{}, true, scopedPersistenceError(err)
		}
		s.transitionChatExecutionAttempt(context.Background(), request, domain.ChatExecutionAttemptSucceeded, "", "", false)
		return userMessage, assistantMessage, true, nil
	}
	if result.Intent != "knowledge_query" {
		controlledStore, ok := s.store.(repository.ControlledChatStore)
		if ok {
			userMessage, assistantMessage, err := controlledStore.AppendControlledRuntimeExchange(context.Background(), request.ConversationID, userMessageID, content, result)
			if err == nil {
				s.transitionChatExecutionAttempt(context.Background(), request, domain.ChatExecutionAttemptSucceeded, "", "", false)
			}
			return userMessage, assistantMessage, true, err
		}
		userMessage, assistantMessage, _, ok := s.store.AppendRuntimeExchangeWithIDs(request.ConversationID, userMessageID, content, result)
		if ok {
			s.transitionChatExecutionAttempt(context.Background(), request, domain.ChatExecutionAttemptSucceeded, "", "", false)
		}
		return userMessage, assistantMessage, ok, nil
	}
	userMessage, assistantMessage, _, ok := s.store.AppendRuntimeExchangeWithIDs(request.ConversationID, userMessageID, content, result)
	if ok {
		s.transitionChatExecutionAttempt(context.Background(), request, domain.ChatExecutionAttemptSucceeded, "", "", false)
	}
	return userMessage, assistantMessage, ok, nil
}

func (s *Service) GetSources(messageID string) []domain.Citation {
	return s.store.GetSources(messageID)
}

func (s *Service) GetMessageSources(messageID string) (domain.MessageSources, bool) {
	return s.store.GetMessageSources(messageID)
}

func (s *Service) GetMessageSourcesAs(ownerID, conversationID, messageID string) (domain.MessageSources, bool) {
	_, messages, ok := s.GetConversationAs(ownerID, conversationID)
	if !ok {
		return domain.MessageSources{}, false
	}
	belongs := false
	for _, message := range messages {
		if message.MessageID == messageID && message.Role == "assistant" {
			belongs = true
			break
		}
	}
	if !belongs {
		return domain.MessageSources{}, false
	}
	return s.store.GetMessageSources(messageID)
}

func (s *Service) ListFiles(status, sourceFormat string) []domain.KnowledgeFile {
	return s.store.ListFiles(status, sourceFormat)
}

func (s *Service) CreateFile(filename, mimeType string, content []byte, forceReindex bool) (domain.KnowledgeFile, domain.IngestionTaskSummary, error) {
	return s.CreateLegacyFileAs(context.Background(), "admin", filename, mimeType, content, forceReindex)
}

func (s *Service) GetFile(fileID string) (domain.KnowledgeFile, domain.IngestionTaskSummary, bool) {
	return s.store.GetFile(fileID)
}

func (s *Service) ReindexFile(fileID string, forceReindex bool) (domain.IngestionTaskSummary, bool, error) {
	return s.reindexFileWithDispatch(fileID, forceReindex)
}

func (s *Service) ListChunks(fileID string) ([]domain.ChunkRecord, bool) {
	return s.store.ListChunks(fileID)
}

func ValidSourceFormat(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "txt", "md", "json", "pdf", "docx", "xlsx", "pptx":
		return true
	default:
		return false
	}
}

func ValidFileStatus(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "uploaded", "queued", "running", "succeeded", "failed", "deleted":
		return true
	default:
		return false
	}
}
