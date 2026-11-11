package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"see-agent/services/backend-go/internal/domain"
	intentrouter "see-agent/services/backend-go/internal/intent"
	"see-agent/services/backend-go/internal/repository"
)

var (
	ErrRuntimeChatUnavailable         = errors.New("runtime chat unavailable")
	ErrKnowledgeSnapshotUnavailable   = errors.New("knowledge snapshot unavailable")
	ErrSnapshotScopeViolation         = errors.New("snapshot scope violation")
	ErrRuntimeCitationAnchorViolation = errors.New("runtime citation anchor violation")
	ErrRuntimeExecutionFactViolation  = errors.New("runtime execution fact violation")
)

const conversationRouterPolicyVersion = "conversation_router_v1"

func (s *Service) createMessageWithOptionalRuntime(ownerID, conversationID, content string, stream bool) (domain.Message, domain.Message, bool, error) {
	conversation, messages, ok := s.conversationForActor(ownerID, conversationID)
	if !ok {
		return domain.Message{}, domain.Message{}, false, nil
	}
	config, ok := s.store.GetConfig(conversation.ConfigVersion)
	if !ok {
		return domain.Message{}, domain.Message{}, true, missingConversationConfigError(conversation.ConfigVersion)
	}
	retrievalConfig, ok := s.store.GetRetrievalConfig(conversation.RetrievalConfigVersion)
	if !ok {
		return domain.Message{}, domain.Message{}, true, missingConversationConfigError(conversation.RetrievalConfigVersion)
	}
	if !s.chatRuntimeEnabled || s.runtimeClient == nil {
		return domain.Message{}, domain.Message{}, true, ErrRuntimeChatUnavailable
	}

	userMessageID := s.store.ReserveMessageID()
	assistantMessageID := s.store.ReserveMessageID()
	if strings.TrimSpace(userMessageID) == "" || strings.TrimSpace(assistantMessageID) == "" {
		return domain.Message{}, domain.Message{}, true, fmt.Errorf("%w: cannot reserve message identities", ErrRuntimeChatUnavailable)
	}
	request := buildChatRuntimeRequestWithMessageID(conversation, config, retrievalConfig, messages, content, stream, assistantMessageID)
	if strings.TrimSpace(ownerID) != "" {
		request.UserID = ownerID
	}
	request.Intent = "context_routing_required"
	// The legacy conversation field is not a request scope.  Only an active
	// immutable scope may supply a snapshot id to the Runtime.
	request.KnowledgeSnapshotID = ""
	scope, scopedStore, err := s.resolveChatKnowledgeScope(context.Background())
	// Every ordinary turn is sent to the Runtime router before we know whether
	// it is a knowledge turn.  An unavailable active snapshot therefore must
	// not prevent a greeting, thanks, or an out-of-scope refusal.  It remains a
	// fail-closed error if the router subsequently selects the RAG route.
	if err != nil && !errors.Is(err, ErrKnowledgeSnapshotUnavailable) {
		return domain.Message{}, domain.Message{}, true, err
	}
	if errors.Is(err, ErrKnowledgeSnapshotUnavailable) {
		scope = nil
	}
	attachKnowledgeScope(&request, scope)
	if err := s.attachPendingClarification(context.Background(), &request); err != nil {
		return domain.Message{}, domain.Message{}, true, err
	}
	attemptStore, attemptID, err := s.beginChatExecutionAttempt(context.Background(), request, userMessageID)
	if err != nil {
		return domain.Message{}, domain.Message{}, true, fmt.Errorf("%w: %v", ErrRuntimeChatUnavailable, err)
	}
	failAttempt := func(code, stage string, retryable bool) {
		if attemptStore != nil {
			_, _ = attemptStore.TransitionChatExecutionAttempt(context.Background(), attemptID, domain.ChatExecutionAttemptFailed, code, stage, retryable)
		}
	}
	succeedAttempt := func() {
		if attemptStore != nil {
			_, _ = attemptStore.TransitionChatExecutionAttempt(context.Background(), attemptID, domain.ChatExecutionAttemptSucceeded, "", "", false)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.RuntimeCallTimeout())
	defer cancel()

	result, err := s.runtimeClient.RunChat(ctx, request)
	if err != nil {
		code, stage := runtimeFailureFacts(err)
		failAttempt(code, stage, true)
		return domain.Message{}, domain.Message{}, true, mapRuntimeChatError(err)
	}
	if err := normalizeAndValidateCitationAnchors(&result); err != nil {
		failAttempt("runtime_contract_violation", "runtime_response_validation", true)
		return domain.Message{}, domain.Message{}, true, err
	}
	if err := validateRuntimeExecutionFacts(request, result); err != nil {
		failAttempt("runtime_contract_violation", "runtime_response_validation", true)
		return domain.Message{}, domain.Message{}, true, err
	}
	attachConversationRouterPolicyEvidence(&result)
	if result.Intent == intentrouter.KnowledgeQuery && scopedStore != nil {
		if err := validateScopedRuntimeResponse(request, result); err != nil {
			failAttempt("runtime_contract_violation", "runtime_response_validation", true)
			return domain.Message{}, domain.Message{}, true, err
		}
		userMessage, assistantMessage, _, err := s.appendScopedRuntimeExchange(context.Background(), scopedStore, request, userMessageID, content, *scope, result)
		if err != nil {
			failAttempt("dependency_unavailable", "state_writeback", true)
			return domain.Message{}, domain.Message{}, true, scopedPersistenceError(err)
		}
		succeedAttempt()
		return userMessage, assistantMessage, true, nil
	}
	if result.Intent != intentrouter.KnowledgeQuery {
		controlledStore, ok := s.store.(repository.ControlledChatStore)
		if ok {
			userMessage, assistantMessage, err := controlledStore.AppendControlledRuntimeExchange(context.Background(), conversationID, userMessageID, content, result)
			if err != nil {
				failAttempt("dependency_unavailable", "state_writeback", true)
				return domain.Message{}, domain.Message{}, true, err
			}
			succeedAttempt()
			return userMessage, assistantMessage, true, err
		}
		userMessage, assistantMessage, _, ok := s.store.AppendRuntimeExchangeWithIDs(conversationID, userMessageID, content, result)
		if !ok {
			failAttempt("dependency_unavailable", "state_writeback", true)
			return domain.Message{}, domain.Message{}, true, fmt.Errorf("persist runtime exchange")
		}
		succeedAttempt()
		return userMessage, assistantMessage, ok, nil
	}

	userMessage, assistantMessage, _, ok := s.store.AppendRuntimeExchangeWithIDs(conversationID, userMessageID, content, result)
	if !ok {
		failAttempt("dependency_unavailable", "state_writeback", true)
		return domain.Message{}, domain.Message{}, true, fmt.Errorf("persist runtime exchange")
	}
	succeedAttempt()
	return userMessage, assistantMessage, ok, nil
}

func (s *Service) beginChatExecutionAttempt(ctx context.Context, request domain.ChatRuntimeRequest, userMessageID string) (repository.ChatExecutionAttemptStore, string, error) {
	store, ok := s.store.(repository.ChatExecutionAttemptStore)
	if !ok {
		return nil, "", nil
	}
	attemptID := "attempt_" + request.RequestID
	attempt := domain.ChatExecutionAttempt{
		AttemptID:                     attemptID,
		RequestID:                     request.RequestID,
		ConversationID:                request.ConversationID,
		UserMessageID:                 userMessageID,
		AssistantMessageID:            request.MessageID,
		State:                         domain.ChatExecutionAttemptAccepted,
		ConfigVersion:                 request.ConfigVersion,
		RetrievalConfigVersion:        request.RetrievalConfigVersion,
		KnowledgeSnapshotID:           request.KnowledgeSnapshotID,
		KnowledgeSnapshotManifestHash: knowledgeScopeManifestHash(request.KnowledgeScope),
	}
	if _, err := store.CreateChatExecutionAttempt(ctx, attempt); err != nil {
		return nil, "", err
	}
	if _, err := store.TransitionChatExecutionAttempt(ctx, attemptID, domain.ChatExecutionAttemptRunning, "", "", false); err != nil {
		return nil, "", err
	}
	return store, attemptID, nil
}

func knowledgeScopeManifestHash(scope *domain.RuntimeKnowledgeScope) string {
	if scope == nil {
		return ""
	}
	return scope.ManifestHash
}

func (s *Service) transitionChatExecutionAttempt(ctx context.Context, request domain.ChatRuntimeRequest, state, errorCode, errorStage string, retryable bool) {
	store, ok := s.store.(repository.ChatExecutionAttemptStore)
	if !ok {
		return
	}
	_, _ = store.TransitionChatExecutionAttempt(ctx, "attempt_"+request.RequestID, state, errorCode, errorStage, retryable)
}

// FailRuntimeStreamAttempt records a Runtime stream terminal failure even when
// the client disconnects before it can receive an SSE error event.
func (s *Service) FailRuntimeStreamAttempt(ctx context.Context, request domain.ChatRuntimeRequest, code, stage string, retryable bool) {
	s.transitionChatExecutionAttempt(ctx, request, domain.ChatExecutionAttemptFailed, code, stage, retryable)
}

// FailRuntimeStreamAttemptForError preserves the runtime client's typed
// failure category for stream transport and HTTP terminal failures.
func (s *Service) FailRuntimeStreamAttemptForError(ctx context.Context, request domain.ChatRuntimeRequest, err error) {
	code, stage := runtimeFailureFacts(err)
	s.FailRuntimeStreamAttempt(ctx, request, code, stage, true)
}

// FailRuntimeStreamAttemptForEvent preserves a Runtime SSE error code. SSE
// errors have no stage field, so their established Backend stage is
// runtime_stream.
func (s *Service) FailRuntimeStreamAttemptForEvent(ctx context.Context, request domain.ChatRuntimeRequest, streamError *domain.ChatRuntimeStreamError) {
	code := "dependency_unavailable"
	retryable := true
	if streamError != nil {
		if resolved := strings.TrimSpace(streamError.Code); resolved != "" {
			code = resolved
		}
		retryable = streamError.Retryable
	}
	s.FailRuntimeStreamAttempt(ctx, request, code, "runtime_stream", retryable)
}

func (s *Service) prepareRuntimeStreamMessage(ownerID, conversationID, content string) (domain.ChatRuntimeRequest, domain.Message, domain.Message, bool, error) {
	conversation, messages, ok := s.conversationForActor(ownerID, conversationID)
	if !ok {
		return domain.ChatRuntimeRequest{}, domain.Message{}, domain.Message{}, false, nil
	}
	config, ok := s.store.GetConfig(conversation.ConfigVersion)
	if !ok {
		return domain.ChatRuntimeRequest{}, domain.Message{}, domain.Message{}, true, missingConversationConfigError(conversation.ConfigVersion)
	}
	retrievalConfig, ok := s.store.GetRetrievalConfig(conversation.RetrievalConfigVersion)
	if !ok {
		return domain.ChatRuntimeRequest{}, domain.Message{}, domain.Message{}, true, missingConversationConfigError(conversation.RetrievalConfigVersion)
	}
	userMessageID := s.store.ReserveMessageID()
	assistantMessageID := s.store.ReserveMessageID()
	request := buildChatRuntimeRequestWithMessageID(conversation, config, retrievalConfig, messages, content, true, assistantMessageID)
	if strings.TrimSpace(ownerID) != "" {
		request.UserID = ownerID
	}
	request.Intent = "context_routing_required"
	request.KnowledgeSnapshotID = ""
	scope, _, err := s.resolveChatKnowledgeScope(context.Background())
	if err != nil && !errors.Is(err, ErrKnowledgeSnapshotUnavailable) {
		return domain.ChatRuntimeRequest{}, domain.Message{}, domain.Message{}, true, err
	}
	if errors.Is(err, ErrKnowledgeSnapshotUnavailable) {
		scope = nil
	}
	attachKnowledgeScope(&request, scope)
	if err := s.attachPendingClarification(context.Background(), &request); err != nil {
		return domain.ChatRuntimeRequest{}, domain.Message{}, domain.Message{}, true, err
	}
	if _, _, err := s.beginChatExecutionAttempt(context.Background(), request, userMessageID); err != nil {
		return domain.ChatRuntimeRequest{}, domain.Message{}, domain.Message{}, true, fmt.Errorf("%w: %v", ErrRuntimeChatUnavailable, err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	userMessage := domain.Message{
		MessageID:      userMessageID,
		ConversationID: conversationID,
		Role:           "user",
		Content:        content,
		Status:         "completed",
		CreatedAt:      now,
		Intent:         request.Intent,
		KnowledgeUsed:  false,
	}
	assistantMessage := domain.Message{
		MessageID:      assistantMessageID,
		ConversationID: conversationID,
		Role:           "assistant",
		Content:        "",
		Status:         "streaming",
		CreatedAt:      now,
		Intent:         request.Intent,
		KnowledgeUsed:  false,
	}
	return request, userMessage, assistantMessage, true, nil
}

func (s *Service) conversationForActor(ownerID, conversationID string) (domain.Conversation, []domain.Message, bool) {
	if strings.TrimSpace(ownerID) == "" {
		return s.store.GetConversation(conversationID)
	}
	store, ok := s.store.(repository.ConversationOwnershipStore)
	if !ok {
		return domain.Conversation{}, nil, false
	}
	return store.GetOwnedConversation(ownerID, conversationID)
}

func (s *Service) streamRuntimeChat(ctx context.Context, request domain.ChatRuntimeRequest, emit func(domain.ChatRuntimeStreamEvent) error) error {
	if !s.RuntimeStreamingEnabled() {
		return ErrRuntimeChatUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, s.RuntimeCallTimeout())
	defer cancel()
	return s.runtimeClient.StreamChat(ctx, request, emit)
}

type runtimeFailure interface {
	RuntimeFailure() (string, string)
}

func runtimeFailureFacts(err error) (string, string) {
	var failure runtimeFailure
	if errors.As(err, &failure) {
		code, stage := failure.RuntimeFailure()
		if strings.TrimSpace(code) != "" && strings.TrimSpace(stage) != "" {
			return code, stage
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "backend_deadline_exceeded", "backend_runtime_deadline"
	}
	return "dependency_unavailable", "runtime_call"
}

func mapRuntimeChatError(err error) error {
	code, _ := runtimeFailureFacts(err)
	if code == "knowledge_snapshot_unavailable" {
		return fmt.Errorf("%w: %w", ErrKnowledgeSnapshotUnavailable, err)
	}
	return fmt.Errorf("%w: %w", ErrRuntimeChatUnavailable, err)
}

func (s *Service) resolveChatKnowledgeScope(ctx context.Context) (*domain.RuntimeKnowledgeScope, repository.KnowledgeScopedChatStore, error) {
	scopedStore, ok := s.store.(repository.KnowledgeScopedChatStore)
	if !ok {
		return nil, nil, nil
	}
	scope, err := scopedStore.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		if errors.Is(err, repository.ErrActiveSnapshotUnavailable) {
			return nil, scopedStore, fmt.Errorf("%w: %v", ErrKnowledgeSnapshotUnavailable, err)
		}
		return nil, scopedStore, fmt.Errorf("%w: resolve active knowledge snapshot: %v", ErrRuntimeChatUnavailable, err)
	}
	return &scope, scopedStore, nil
}

func attachKnowledgeScope(request *domain.ChatRuntimeRequest, scope *domain.RuntimeKnowledgeScope) {
	if scope == nil {
		return
	}
	request.KnowledgeSnapshotID = scope.SnapshotID
	request.KnowledgeScope = scope
}

func (s *Service) attachPendingClarification(ctx context.Context, request *domain.ChatRuntimeRequest) error {
	if strings.TrimSpace(request.AnswerPolicyVersion) == "" {
		return nil
	}
	store, ok := s.store.(repository.ClarificationStore)
	if !ok {
		return fmt.Errorf("%w: answer policy requires durable clarification state", ErrRuntimeChatUnavailable)
	}
	pending, found, err := store.GetPendingClarification(ctx, request.ConversationID)
	if err != nil {
		return fmt.Errorf("%w: read pending clarification: %v", ErrRuntimeChatUnavailable, err)
	}
	if found {
		request.PendingClarification = &pending
	}
	return nil
}

func (s *Service) appendScopedRuntimeExchange(ctx context.Context, store repository.KnowledgeScopedChatStore, request domain.ChatRuntimeRequest, userMessageID, content string, scope domain.RuntimeKnowledgeScope, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, error) {
	if strings.TrimSpace(request.AnswerPolicyVersion) == "" {
		return store.AppendScopedRuntimeExchange(ctx, request.ConversationID, userMessageID, content, scope, result)
	}
	clarificationStore, ok := s.store.(repository.ClarificationStore)
	if !ok {
		return domain.Message{}, domain.Message{}, nil, fmt.Errorf("%w: answer policy requires durable clarification state", ErrRuntimeChatUnavailable)
	}
	return clarificationStore.AppendScopedRuntimeExchangeWithPending(ctx, request.ConversationID, userMessageID, content, scope, request.PendingClarification, result)
}

func validateScopedRuntimeResponse(request domain.ChatRuntimeRequest, result domain.ChatRuntimeResponse) error {
	scope := request.KnowledgeScope
	if scope == nil || strings.TrimSpace(scope.SnapshotID) == "" || strings.TrimSpace(scope.ManifestHash) == "" || len(scope.RevisionIDs) == 0 {
		return fmt.Errorf("%w: request has no complete knowledge scope", ErrSnapshotScopeViolation)
	}
	if request.KnowledgeSnapshotID != scope.SnapshotID || result.RequestID != request.RequestID || result.ConversationID != request.ConversationID || result.MessageID != request.MessageID {
		return fmt.Errorf("%w: runtime response identity does not match request", ErrSnapshotScopeViolation)
	}
	if result.KnowledgeSnapshotID != scope.SnapshotID || result.KnowledgeSnapshotManifestHash != scope.ManifestHash || result.FreshnessStatus != "current" {
		return fmt.Errorf("%w: runtime response does not echo the request knowledge scope", ErrSnapshotScopeViolation)
	}
	allowedRevisions := make(map[string]struct{}, len(scope.RevisionIDs))
	for _, revisionID := range scope.RevisionIDs {
		allowedRevisions[revisionID] = struct{}{}
	}
	for _, citation := range result.Citations {
		if strings.TrimSpace(citation.DocumentID) == "" || strings.TrimSpace(citation.RevisionID) == "" || strings.TrimSpace(citation.ChunkContentSHA256) == "" || citation.KnowledgeSnapshotID != scope.SnapshotID {
			return fmt.Errorf("%w: runtime citation is missing immutable provenance", ErrSnapshotScopeViolation)
		}
		if _, ok := allowedRevisions[citation.RevisionID]; !ok {
			return fmt.Errorf("%w: runtime citation revision %q is outside the request scope", ErrSnapshotScopeViolation, citation.RevisionID)
		}
	}
	return nil
}

func validateRuntimeExecutionFacts(request domain.ChatRuntimeRequest, result domain.ChatRuntimeResponse) error {
	if strings.TrimSpace(result.RequestID) == "" || strings.TrimSpace(result.ConversationID) == "" || strings.TrimSpace(result.MessageID) == "" {
		return fmt.Errorf("%w: response identity is incomplete", ErrRuntimeExecutionFactViolation)
	}
	if result.RequestID != request.RequestID || result.ConversationID != request.ConversationID || result.MessageID != request.MessageID {
		return fmt.Errorf("%w: response identity does not match request", ErrRuntimeExecutionFactViolation)
	}
	if strings.TrimSpace(result.AssistantMessage.Content) == "" {
		return fmt.Errorf("%w: assistant answer is empty", ErrRuntimeExecutionFactViolation)
	}
	seenRanks := make(map[int]struct{}, len(result.Citations))
	for _, citation := range result.Citations {
		if citation.Rank < 1 {
			return fmt.Errorf("%w: citation rank must be positive", ErrRuntimeExecutionFactViolation)
		}
		if _, exists := seenRanks[citation.Rank]; exists {
			return fmt.Errorf("%w: citation rank %d is duplicated", ErrRuntimeExecutionFactViolation, citation.Rank)
		}
		seenRanks[citation.Rank] = struct{}{}
		if strings.TrimSpace(citation.CitationLabel) == "" {
			return fmt.Errorf("%w: citation label is empty", ErrRuntimeExecutionFactViolation)
		}
	}
	routedRequest := request.Intent == "context_routing_required"
	if !routedRequest && result.Intent != request.Intent {
		return fmt.Errorf("%w: response intent %q does not match request %q", ErrRuntimeExecutionFactViolation, result.Intent, request.Intent)
	}
	if routedRequest && !isRoutableResponseIntent(result.Intent) {
		return fmt.Errorf("%w: router returned invalid response intent %q", ErrRuntimeExecutionFactViolation, result.Intent)
	}
	if err := validateRuntimeContextResolution(request, result); err != nil {
		return err
	}
	effectiveRequest := request
	if routedRequest {
		effectiveRequest.Intent = result.Intent
	}
	if strings.TrimSpace(effectiveRequest.AnswerPolicyVersion) != "" {
		return validateA1RuntimeFacts(effectiveRequest, result)
	}
	if strictRuntimeResponseRequired() {
		if err := validateStrictRuntimeObservation(effectiveRequest, result); err != nil {
			return err
		}
	}
	if effectiveRequest.Intent != intentrouter.KnowledgeQuery {
		if result.RetrievalPerformed || result.GenerationPerformed || result.KnowledgeUsed || len(result.Citations) > 0 || len(result.CitationAnchors) > 0 ||
			strings.TrimSpace(result.KnowledgeSnapshotID) != "" || strings.TrimSpace(result.KnowledgeSnapshotManifestHash) != "" || strings.TrimSpace(result.FreshnessStatus) != "" {
			return fmt.Errorf("%w: controlled response contains knowledge or model evidence", ErrRuntimeExecutionFactViolation)
		}
		return nil
	}
	if result.AnswerDisposition == "clarification_required" {
		if result.RetrievalPerformed || result.GenerationPerformed || result.KnowledgeUsed || len(result.Citations) != 0 || len(result.CitationAnchors) != 0 {
			return fmt.Errorf("%w: clarification response contains execution evidence", ErrRuntimeExecutionFactViolation)
		}
		return nil
	}
	if !result.RetrievalPerformed {
		return fmt.Errorf("%w: knowledge response did not record retrieval", ErrRuntimeExecutionFactViolation)
	}
	if result.KnowledgeUsed && len(result.Citations) == 0 {
		return fmt.Errorf("%w: knowledge_used response has no citations", ErrRuntimeExecutionFactViolation)
	}
	return nil
}

func validateA1RuntimeFacts(request domain.ChatRuntimeRequest, result domain.ChatRuntimeResponse) error {
	if !domain.IsRegisteredAnswerPolicyVersion(request.AnswerPolicyVersion) {
		return fmt.Errorf("%w: answer policy version is not registered", ErrRuntimeExecutionFactViolation)
	}
	if request.Intent == intentrouter.KnowledgeQuery && result.AnswerPolicyVersion != request.AnswerPolicyVersion {
		return fmt.Errorf("%w: answer policy version is not conversation-bound", ErrRuntimeExecutionFactViolation)
	}
	if request.Intent != intentrouter.KnowledgeQuery && strings.TrimSpace(result.AnswerPolicyVersion) != "" {
		return fmt.Errorf("%w: controlled response exposed answer policy version", ErrRuntimeExecutionFactViolation)
	}
	if result.PolicyEvaluationPerformed == nil || result.AnswerGenerationPerformed == nil || result.Execution == nil || result.Execution.FallbackUsed {
		return fmt.Errorf("%w: A1 execution facts are incomplete", ErrRuntimeExecutionFactViolation)
	}
	if result.Execution.Policy != "performed" && result.Execution.Policy != "not_run" {
		return fmt.Errorf("%w: A1 policy execution status is invalid", ErrRuntimeExecutionFactViolation)
	}
	if result.Execution.Retrieval.Status != "performed" && result.Execution.Retrieval.Status != "not_run" {
		return fmt.Errorf("%w: A1 retrieval execution status is invalid", ErrRuntimeExecutionFactViolation)
	}
	if result.Execution.Generation.Status != "performed" && result.Execution.Generation.Status != "not_run" {
		return fmt.Errorf("%w: A1 generation execution status is invalid", ErrRuntimeExecutionFactViolation)
	}
	if (result.Execution.Policy == "performed") != *result.PolicyEvaluationPerformed ||
		(result.Execution.Retrieval.Status == "performed") != result.RetrievalPerformed ||
		(result.Execution.Generation.Status == "performed") != result.GenerationPerformed ||
		result.GenerationPerformed != *result.AnswerGenerationPerformed {
		return fmt.Errorf("%w: A1 execution facts are inconsistent", ErrRuntimeExecutionFactViolation)
	}
	if err := validateA1PolicyExecutionEvidence(result); err != nil {
		return err
	}

	if request.Intent != intentrouter.KnowledgeQuery {
		if result.AnswerDisposition != "controlled_response" || *result.PolicyEvaluationPerformed || *result.AnswerGenerationPerformed || result.Execution.Policy != "not_run" || result.Execution.Retrieval.Status != "not_run" || result.Execution.Generation.Status != "not_run" || result.RetrievalPerformed || result.GenerationPerformed || result.KnowledgeUsed || len(result.Citations) != 0 || len(result.CitationAnchors) != 0 || len(result.UsedCitationRanks) != 0 || len(result.RetrievalCandidates) != 0 || result.Clarification != nil || result.Coverage != nil || result.Resolution != nil || strings.TrimSpace(result.ResolvedQuery) != "" {
			return fmt.Errorf("%w: controlled A1 response contains knowledge facts", ErrRuntimeExecutionFactViolation)
		}
		return nil
	}
	if err := validateA1PendingClarificationResolution(request, result); err != nil {
		return err
	}
	if request.AnswerMode != "revalidate" && (request.KnowledgeScope == nil || strings.TrimSpace(request.KnowledgeScope.SnapshotID) == "" || strings.TrimSpace(request.KnowledgeScope.ManifestHash) == "" || len(request.KnowledgeScope.RevisionIDs) == 0) {
		return fmt.Errorf("%w: A1 knowledge response has no immutable scope", ErrRuntimeExecutionFactViolation)
	}
	switch result.AnswerDisposition {
	case "grounded_answer", "partial_answer":
		if !*result.PolicyEvaluationPerformed || !*result.AnswerGenerationPerformed || !result.RetrievalPerformed || !result.GenerationPerformed || !result.KnowledgeUsed || result.Refusal || result.Clarification != nil || len(result.Citations) == 0 || len(result.CitationAnchors) == 0 || len(result.UsedCitationRanks) == 0 || len(result.RetrievalCandidates) == 0 {
			return fmt.Errorf("%w: grounded A1 response is incomplete", ErrRuntimeExecutionFactViolation)
		}
		if result.AnswerDisposition == "partial_answer" {
			if result.Coverage == nil || result.Coverage.RequestedCount < 1 || result.Coverage.SupportedCount < 0 || result.Coverage.SupportedCount >= result.Coverage.RequestedCount || strings.TrimSpace(result.Coverage.UncoveredDescription) == "" {
				return fmt.Errorf("%w: partial answer coverage is invalid", ErrRuntimeExecutionFactViolation)
			}
		} else if result.Coverage != nil {
			return fmt.Errorf("%w: grounded answer cannot contain coverage", ErrRuntimeExecutionFactViolation)
		}
		if err := validateA1VisibleEvidence(result); err != nil {
			return err
		}
	case "clarification_required":
		if !*result.PolicyEvaluationPerformed || *result.AnswerGenerationPerformed || result.GenerationPerformed || result.KnowledgeUsed || result.Refusal || result.Clarification == nil || result.Coverage != nil || len(result.Citations) != 0 || len(result.CitationAnchors) != 0 || len(result.UsedCitationRanks) != 0 {
			return fmt.Errorf("%w: clarification A1 response is invalid", ErrRuntimeExecutionFactViolation)
		}
		if result.RetrievalPerformed && len(result.RetrievalCandidates) == 0 {
			return fmt.Errorf("%w: post-retrieval clarification has no candidates", ErrRuntimeExecutionFactViolation)
		}
		if !result.RetrievalPerformed && len(result.RetrievalCandidates) != 0 {
			return fmt.Errorf("%w: pre-retrieval clarification exposed candidates", ErrRuntimeExecutionFactViolation)
		}
	case "insufficient_evidence":
		if !*result.PolicyEvaluationPerformed || !result.RetrievalPerformed || *result.AnswerGenerationPerformed || result.GenerationPerformed || result.KnowledgeUsed || !result.Refusal || result.Clarification != nil || result.Coverage != nil || len(result.Citations) != 0 || len(result.CitationAnchors) != 0 || len(result.UsedCitationRanks) != 0 {
			return fmt.Errorf("%w: insufficient-evidence A1 response is invalid", ErrRuntimeExecutionFactViolation)
		}
	default:
		return fmt.Errorf("%w: invalid A1 answer disposition", ErrRuntimeExecutionFactViolation)
	}
	return nil
}

func validateA1PolicyExecutionEvidence(result domain.ChatRuntimeResponse) error {
	if *result.PolicyEvaluationPerformed {
		if strings.TrimSpace(result.Execution.PolicyProvider) == "" || strings.TrimSpace(result.Execution.PolicyModelID) == "" || result.Execution.PolicyCallCount == nil || *result.Execution.PolicyCallCount < 1 {
			return fmt.Errorf("%w: performed A1 policy has incomplete provider evidence", ErrRuntimeExecutionFactViolation)
		}
		return nil
	}
	if strings.TrimSpace(result.Execution.PolicyProvider) != "" || strings.TrimSpace(result.Execution.PolicyModelID) != "" || result.Execution.PolicyCallCount != nil {
		return fmt.Errorf("%w: unperformed A1 policy exposed provider evidence", ErrRuntimeExecutionFactViolation)
	}
	return nil
}

func validateA1PendingClarificationResolution(request domain.ChatRuntimeRequest, result domain.ChatRuntimeResponse) error {
	if result.Resolution == nil {
		if request.PendingClarification != nil || strings.TrimSpace(result.ResolvedQuery) != "" {
			return fmt.Errorf("%w: pending clarification was not resolved explicitly", ErrRuntimeExecutionFactViolation)
		}
		return nil
	}
	if request.PendingClarification == nil || result.Resolution.PendingClarificationID != request.PendingClarification.ClarificationID || result.Resolution.PendingStateVersion != request.PendingClarification.StateVersion {
		return fmt.Errorf("%w: clarification resolution does not match request", ErrRuntimeExecutionFactViolation)
	}
	switch result.Resolution.Action {
	case "resolved":
		if result.AnswerDisposition == "clarification_required" || strings.TrimSpace(result.Resolution.ResolvedQuery) == "" || result.ResolvedQuery != result.Resolution.ResolvedQuery {
			return fmt.Errorf("%w: resolved clarification has no valid final answer query", ErrRuntimeExecutionFactViolation)
		}
	case "needs_more":
		if result.AnswerDisposition != "clarification_required" || strings.TrimSpace(result.Resolution.ResolvedQuery) != "" || strings.TrimSpace(result.ResolvedQuery) != "" {
			return fmt.Errorf("%w: needs-more clarification has invalid answer facts", ErrRuntimeExecutionFactViolation)
		}
	case "abandoned":
		if strings.TrimSpace(result.Resolution.ResolvedQuery) != "" || strings.TrimSpace(result.ResolvedQuery) != "" {
			return fmt.Errorf("%w: abandoned clarification leaked resolved query", ErrRuntimeExecutionFactViolation)
		}
	default:
		return fmt.Errorf("%w: clarification resolution action is invalid", ErrRuntimeExecutionFactViolation)
	}
	return nil
}

func validateA1VisibleEvidence(result domain.ChatRuntimeResponse) error {
	candidates := make(map[int]domain.RuntimeRetrievalCandidate, len(result.RetrievalCandidates))
	for _, candidate := range result.RetrievalCandidates {
		if _, duplicate := candidates[candidate.Rank]; duplicate {
			return fmt.Errorf("%w: duplicate retrieval candidate rank", ErrRuntimeExecutionFactViolation)
		}
		candidates[candidate.Rank] = candidate
	}
	usedRanks := make(map[int]struct{}, len(result.UsedCitationRanks))
	for _, rank := range result.UsedCitationRanks {
		if _, found := candidates[rank]; !found {
			return fmt.Errorf("%w: used citation rank is not a retrieval candidate", ErrRuntimeExecutionFactViolation)
		}
		usedRanks[rank] = struct{}{}
	}
	if len(usedRanks) == 0 || len(result.Citations) != len(usedRanks) {
		return fmt.Errorf("%w: answer citation ranks do not match used ranks", ErrRuntimeExecutionFactViolation)
	}
	for _, citation := range result.Citations {
		candidate, found := candidates[citation.Rank]
		if !found || candidate.ChunkID != citation.ChunkID || candidate.FileID != citation.FileID || candidate.DocumentID != citation.DocumentID || candidate.RevisionID != citation.RevisionID || candidate.ChunkContentSHA256 != citation.ChunkContentSHA256 || candidate.KnowledgeSnapshotID != citation.KnowledgeSnapshotID {
			return fmt.Errorf("%w: answer citation does not match retrieval candidate", ErrRuntimeExecutionFactViolation)
		}
		if _, used := usedRanks[citation.Rank]; !used {
			return fmt.Errorf("%w: answer citation was not selected", ErrRuntimeExecutionFactViolation)
		}
	}
	for _, anchor := range result.CitationAnchors {
		for _, rank := range anchor.CitationRanks {
			if _, used := usedRanks[rank]; !used {
				return fmt.Errorf("%w: citation anchor references an unselected rank", ErrRuntimeExecutionFactViolation)
			}
		}
	}
	return nil
}

func attachConversationRouterPolicyEvidence(result *domain.ChatRuntimeResponse) {
	if result.ContextResolution == nil || !result.ContextResolution.ResolverPerformed {
		return
	}
	result.ContextResolution.RouterPolicyVersion = conversationRouterPolicyVersion
}

func isRoutableResponseIntent(intent string) bool {
	switch intent {
	case intentrouter.KnowledgeQuery,
		intentrouter.SocialGreeting,
		intentrouter.SocialThanks,
		intentrouter.SocialFarewell,
		intentrouter.CapabilityHelp,
		intentrouter.OffTopic,
		intentrouter.OutOfScope:
		return true
	default:
		return false
	}
}

func strictRuntimeResponseRequired() bool {
	profile := strings.TrimSpace(os.Getenv("SEE_AGENT_PROFILE"))
	return profile == "fullstack_dev" || profile == "production"
}

func validateStrictRuntimeObservation(request domain.ChatRuntimeRequest, result domain.ChatRuntimeResponse) error {
	if result.Execution == nil || result.Execution.FallbackUsed || result.Execution.Policy != "performed" {
		return fmt.Errorf("%w: strict execution observation is missing or invalid", ErrRuntimeExecutionFactViolation)
	}
	execution := result.Execution
	if request.Intent != intentrouter.KnowledgeQuery {
		if result.AnswerDisposition != "controlled_response" || execution.Retrieval.Status != "not_run" || execution.Generation.Status != "not_run" || len(result.UsedCitationRanks) != 0 {
			return fmt.Errorf("%w: controlled response strict observation is invalid", ErrRuntimeExecutionFactViolation)
		}
		return nil
	}
	if request.AnswerMode != "revalidate" && (request.KnowledgeScope == nil || strings.TrimSpace(request.KnowledgeScope.SnapshotID) == "" || strings.TrimSpace(request.KnowledgeScope.ManifestHash) == "" || len(request.KnowledgeScope.RevisionIDs) == 0 || request.KnowledgeSnapshotID != request.KnowledgeScope.SnapshotID) {
		return fmt.Errorf("%w: strict knowledge response has no immutable scope", ErrRuntimeExecutionFactViolation)
	}
	if result.AnswerDisposition == "clarification_required" {
		if result.ContextResolution == nil || result.ContextResolution.Kind != "clarification_required" || execution.Retrieval.Status != "not_run" || execution.Generation.Status != "not_run" || len(result.Citations) != 0 || len(result.CitationAnchors) != 0 || len(result.UsedCitationRanks) != 0 {
			return fmt.Errorf("%w: clarification strict observation is invalid", ErrRuntimeExecutionFactViolation)
		}
		return nil
	}
	if execution.Retrieval.Status != "performed" || strings.TrimSpace(execution.Retrieval.Strategy) == "" || strings.TrimSpace(execution.Retrieval.EmbeddingModel) == "" || strings.TrimSpace(execution.Retrieval.Collection) == "" || execution.Retrieval.CandidateCount == nil {
		return fmt.Errorf("%w: retrieval observation is incomplete", ErrRuntimeExecutionFactViolation)
	}
	if request.ResponseOverrides != nil && (execution.Retrieval.Strategy != request.ResponseOverrides.RetrievalStrategy || execution.Retrieval.EmbeddingModel != request.ResponseOverrides.EmbeddingModel) {
		return fmt.Errorf("%w: retrieval observation does not match immutable config", ErrRuntimeExecutionFactViolation)
	}
	if request.AnswerMode != "revalidate" && result.ContextResolution == nil {
		return fmt.Errorf("%w: knowledge response is missing context resolution evidence", ErrRuntimeExecutionFactViolation)
	}
	if result.AnswerDisposition == "grounded_answer" || result.AnswerDisposition == "partial_answer" {
		if strings.TrimSpace(result.AnswerPolicyVersion) == "" || execution.Generation.Status != "performed" || strings.TrimSpace(execution.Generation.Provider) == "" || execution.Generation.ModelID != request.ModelID || len(result.Citations) == 0 || len(result.CitationAnchors) == 0 || len(result.UsedCitationRanks) == 0 {
			return fmt.Errorf("%w: grounded strict observation is incomplete", ErrRuntimeExecutionFactViolation)
		}
		return nil
	}
	if result.AnswerDisposition != "insufficient_evidence" && result.AnswerDisposition != "clarification_required" {
		return fmt.Errorf("%w: answer disposition is invalid", ErrRuntimeExecutionFactViolation)
	}
	if len(result.Citations) != 0 || len(result.CitationAnchors) != 0 || len(result.UsedCitationRanks) != 0 {
		return fmt.Errorf("%w: non-grounded response exposed citations", ErrRuntimeExecutionFactViolation)
	}
	return nil
}

func validateRuntimeContextResolution(request domain.ChatRuntimeRequest, result domain.ChatRuntimeResponse) error {
	resolution := result.ContextResolution
	if resolution == nil && request.Intent != "context_routing_required" {
		return nil
	}
	if resolution == nil {
		return fmt.Errorf("%w: context routing request has no router evidence", ErrRuntimeExecutionFactViolation)
	}
	allowedKinds := map[string]struct{}{
		"follow_up":              {},
		"standalone":             {},
		"topic_shift":            {},
		"clarification_required": {},
	}
	if _, ok := allowedKinds[resolution.Kind]; !ok {
		return fmt.Errorf("%w: context resolution kind is invalid", ErrRuntimeExecutionFactViolation)
	}
	if resolution.Confidence < 0 || resolution.Confidence > 1 {
		return fmt.Errorf("%w: context router confidence is invalid", ErrRuntimeExecutionFactViolation)
	}
	if request.Intent == "context_routing_required" && !resolution.ResolverPerformed {
		return fmt.Errorf("%w: context routing must record a performed router", ErrRuntimeExecutionFactViolation)
	}
	if resolution.ResolverPerformed {
		if strings.TrimSpace(resolution.ResolverProvider) == "" || strings.TrimSpace(resolution.ResolverModelID) == "" {
			return fmt.Errorf("%w: performed context resolver has no provider or model", ErrRuntimeExecutionFactViolation)
		}
	} else if strings.TrimSpace(resolution.ResolverProvider) != "" || strings.TrimSpace(resolution.ResolverModelID) != "" {
		return fmt.Errorf("%w: unperformed context resolver contains provider evidence", ErrRuntimeExecutionFactViolation)
	}
	if request.Intent == "context_routing_required" {
		if resolution.Intent != result.Intent || !isRoutableResponseIntent(resolution.Intent) {
			return fmt.Errorf("%w: router intent does not match the response", ErrRuntimeExecutionFactViolation)
		}
		switch resolution.Route {
		case "rag":
			if result.Intent != intentrouter.KnowledgeQuery || resolution.Kind == "clarification_required" {
				return fmt.Errorf("%w: RAG route has inconsistent result", ErrRuntimeExecutionFactViolation)
			}
		case "clarification":
			if result.Intent != intentrouter.KnowledgeQuery || result.AnswerDisposition != "clarification_required" || resolution.Kind != "clarification_required" {
				return fmt.Errorf("%w: clarification route has inconsistent result", ErrRuntimeExecutionFactViolation)
			}
		case "controlled_reply":
			if result.Intent == intentrouter.KnowledgeQuery || resolution.Kind != "standalone" {
				return fmt.Errorf("%w: controlled route has inconsistent result", ErrRuntimeExecutionFactViolation)
			}
		default:
			return fmt.Errorf("%w: router route is invalid", ErrRuntimeExecutionFactViolation)
		}
	}
	if resolution.Kind == "follow_up" {
		if strings.TrimSpace(resolution.ResolvedQuery) == "" || utf8.RuneCountInString(resolution.ResolvedQuery) > 600 || len(resolution.BasisMessageIDs) == 0 || len(resolution.BasisMessageIDs) > 6 {
			return fmt.Errorf("%w: follow-up context resolution is incomplete", ErrRuntimeExecutionFactViolation)
		}
		allowedIDs := make(map[string]struct{}, len(request.Messages))
		for _, message := range request.Messages {
			allowedIDs[message.MessageID] = struct{}{}
		}
		seenIDs := make(map[string]struct{}, len(resolution.BasisMessageIDs))
		for _, messageID := range resolution.BasisMessageIDs {
			if _, duplicate := seenIDs[messageID]; duplicate {
				return fmt.Errorf("%w: context resolution contains duplicate basis message", ErrRuntimeExecutionFactViolation)
			}
			seenIDs[messageID] = struct{}{}
			if _, ok := allowedIDs[messageID]; !ok {
				return fmt.Errorf("%w: context resolution references a message outside request history", ErrRuntimeExecutionFactViolation)
			}
		}
		return nil
	}
	if strings.TrimSpace(resolution.ResolvedQuery) != "" || len(resolution.BasisMessageIDs) != 0 {
		return fmt.Errorf("%w: only follow-up context resolution may carry resolved query or basis", ErrRuntimeExecutionFactViolation)
	}
	return nil
}

func normalizeAndValidateCitationAnchors(result *domain.ChatRuntimeResponse) error {
	content := result.AssistantMessage.Content
	normalized := strings.TrimSpace(content)
	if content != normalized && len(result.CitationAnchors) > 0 {
		return fmt.Errorf("%w: Runtime response has anchors but non-normalized content", ErrRuntimeCitationAnchorViolation)
	}
	result.AssistantMessage.Content = normalized
	if len(result.CitationAnchors) == 0 {
		return nil
	}

	knownRanks := make(map[int]struct{}, len(result.Citations))
	for _, citation := range result.Citations {
		if citation.Rank < 1 {
			return fmt.Errorf("%w: citation rank must be positive", ErrRuntimeCitationAnchorViolation)
		}
		knownRanks[citation.Rank] = struct{}{}
	}
	contentRunes := utf8.RuneCountInString(result.AssistantMessage.Content)
	previousEnd := 0
	for index, anchor := range result.CitationAnchors {
		if anchor.StartRune < 0 || anchor.EndRune <= anchor.StartRune || anchor.EndRune > contentRunes {
			return fmt.Errorf("%w: anchor %d range is outside answer content", ErrRuntimeCitationAnchorViolation, index)
		}
		if index > 0 && anchor.StartRune < previousEnd {
			return fmt.Errorf("%w: anchor %d overlaps a previous anchor", ErrRuntimeCitationAnchorViolation, index)
		}
		if len(anchor.CitationRanks) == 0 {
			return fmt.Errorf("%w: anchor %d has no citation ranks", ErrRuntimeCitationAnchorViolation, index)
		}
		seenRanks := make(map[int]struct{}, len(anchor.CitationRanks))
		for _, rank := range anchor.CitationRanks {
			if _, exists := knownRanks[rank]; !exists {
				return fmt.Errorf("%w: anchor %d references unknown citation rank %d", ErrRuntimeCitationAnchorViolation, index, rank)
			}
			if _, exists := seenRanks[rank]; exists {
				return fmt.Errorf("%w: anchor %d repeats citation rank %d", ErrRuntimeCitationAnchorViolation, index, rank)
			}
			seenRanks[rank] = struct{}{}
		}
		previousEnd = anchor.EndRune
	}
	return nil
}

func scopedPersistenceError(err error) error {
	if errors.Is(err, repository.ErrKnowledgeSnapshotCitationMismatch) {
		return fmt.Errorf("%w: %v", ErrSnapshotScopeViolation, err)
	}
	return fmt.Errorf("persist scoped runtime exchange: %w", err)
}

func buildChatRuntimeRequest(conversation domain.Conversation, config domain.AgentConfig, retrievalConfig domain.RetrievalConfig, messages []domain.Message, content string, stream bool) domain.ChatRuntimeRequest {
	return buildChatRuntimeRequestWithMessageID(conversation, config, retrievalConfig, messages, content, stream, "")
}

func buildChatRuntimeRequestWithMessageID(conversation domain.Conversation, config domain.AgentConfig, retrievalConfig domain.RetrievalConfig, messages []domain.Message, content string, stream bool, messageID string) domain.ChatRuntimeRequest {
	now := time.Now().UTC()
	runtimeMessages := make([]domain.RuntimeConversationMessage, 0, 6)
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		if role != "user" && role != "assistant" {
			continue
		}
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		runtimeMessages = append(runtimeMessages, domain.RuntimeConversationMessage{
			MessageID: message.MessageID,
			Role:      role,
			Content:   message.Content,
			CreatedAt: message.CreatedAt,
		})
	}
	if len(runtimeMessages) > 6 {
		runtimeMessages = runtimeMessages[len(runtimeMessages)-6:]
	}
	const maxHistoryCharacters = 4000
	for total := historyCharacters(runtimeMessages); total > maxHistoryCharacters && len(runtimeMessages) > 0; total = historyCharacters(runtimeMessages) {
		runtimeMessages = runtimeMessages[1:]
	}
	if strings.TrimSpace(messageID) == "" {
		messageID = fmt.Sprintf("msg_runtime_%d", now.UnixNano())
	}

	answerPolicyVersion := ""
	if domain.IsRegisteredAnswerPolicyVersion(config.AnswerPolicyVersion) {
		answerPolicyVersion = config.AnswerPolicyVersion
	}
	return domain.ChatRuntimeRequest{
		RequestID:              fmt.Sprintf("req_chat_%s_%d", conversation.ConversationID, now.UnixNano()),
		ConversationID:         conversation.ConversationID,
		MessageID:              messageID,
		UserID:                 "user_dev",
		UserQuery:              content,
		ModelID:                conversation.ModelID,
		ConfigVersion:          conversation.ConfigVersion,
		RetrievalConfigVersion: conversation.RetrievalConfigVersion,
		AnswerPolicyVersion:    answerPolicyVersion,
		KnowledgeSnapshotID:    conversation.KnowledgeSnapshotID,
		Stream:                 stream,
		Messages:               runtimeMessages,
		ResponseOverrides: &domain.RuntimeResponseOverrides{
			SystemPromptVersion: config.SystemPromptVersion,
			ResponseStyle:       config.ResponseStyle,
			RetrievalStrategy:   retrievalConfig.RetrievalStrategy,
			EmbeddingModel:      retrievalConfig.EmbeddingModel,
			Temperature:         &config.Temperature,
			TopP:                &config.TopP,
			MaxOutputTokens:     &config.MaxOutputTokens,
			TopK:                &retrievalConfig.TopK,
			RerankEnabled:       &retrievalConfig.RerankEnabled,
			EvidenceMinScore:    &retrievalConfig.EvidenceMinScore,
		},
	}
}

func historyCharacters(messages []domain.RuntimeConversationMessage) int {
	total := 0
	for _, message := range messages {
		total += utf8.RuneCountInString(message.Content)
	}
	return total
}

func missingConversationConfigError(configVersion string) error {
	return fmt.Errorf("%w: conversation config snapshot %q was not found", ErrRuntimeChatUnavailable, configVersion)
}
