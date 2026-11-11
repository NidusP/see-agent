package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/repository"
)

var (
	ErrKnowledgeFreshnessUnavailable = errors.New("knowledge freshness requires postgres")
	ErrConversationNotFound          = errors.New("conversation not found")
)

const (
	k6WorkerInterval = 250 * time.Millisecond
	k6Lease          = 30 * time.Second
)

func (s *Service) KnowledgeFreshnessEnabled() bool {
	_, ok := s.store.(repository.KnowledgeFreshnessStore)
	return ok
}

func (s *Service) ListKnowledgeUpdates(ctx context.Context, documentID, cursor string, limit int) (domain.KnowledgeUpdatePage, error) {
	store, err := s.knowledgeFreshnessStore()
	if err != nil {
		return domain.KnowledgeUpdatePage{}, err
	}
	if err := store.EnsureKnowledgeFreshnessScans(ctx); err != nil {
		return domain.KnowledgeUpdatePage{}, err
	}
	return store.ListKnowledgeUpdates(ctx, documentID, cursor, limit)
}

func (s *Service) GetKnowledgeSnapshot(ctx context.Context, snapshotID string) (domain.KnowledgeSnapshot, bool, error) {
	store, err := s.knowledgeFreshnessStore()
	if err != nil {
		return domain.KnowledgeSnapshot{}, false, err
	}
	return store.GetKnowledgeSnapshot(ctx, snapshotID)
}

func (s *Service) CreateRevalidation(ctx context.Context, requesterID, conversationID, messageID, idempotencyKey, reason string) (domain.RevalidationRun, error) {
	store, err := s.knowledgeFreshnessStore()
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	scope, _, err := s.resolveChatKnowledgeScope(ctx)
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	if scope == nil {
		return domain.RevalidationRun{}, ErrKnowledgeSnapshotUnavailable
	}
	return store.CreateRevalidationRun(ctx, domain.RevalidationRequest{
		RequesterID: requesterID, ConversationID: conversationID, OriginalMessageID: messageID,
		IdempotencyKey: idempotencyKey, Reason: strings.TrimSpace(reason), LatestScope: *scope,
	})
}

func (s *Service) CreateRevalidationAs(ctx context.Context, requesterID, conversationID, messageID, idempotencyKey, reason string) (domain.RevalidationRun, error) {
	if _, _, ok := s.GetConversationAs(requesterID, conversationID); !ok {
		return domain.RevalidationRun{}, ErrConversationNotFound
	}
	return s.CreateRevalidation(ctx, requesterID, conversationID, messageID, idempotencyKey, reason)
}

func (s *Service) GetRevalidation(ctx context.Context, conversationID, revalidationID string) (domain.RevalidationRun, bool, error) {
	store, err := s.knowledgeFreshnessStore()
	if err != nil {
		return domain.RevalidationRun{}, false, err
	}
	return store.GetRevalidationRun(ctx, conversationID, revalidationID)
}

func (s *Service) GetRevalidationAs(ctx context.Context, requesterID, conversationID, revalidationID string) (domain.RevalidationRun, bool, error) {
	if _, _, ok := s.GetConversationAs(requesterID, conversationID); !ok {
		return domain.RevalidationRun{}, false, nil
	}
	return s.GetRevalidation(ctx, conversationID, revalidationID)
}

// RunK6Once is exposed for deterministic integration tests. The background
// worker invokes the same method, so scan and revalidation transitions share
// one recovery path.
func (s *Service) RunK6Once(ctx context.Context) error {
	store, err := s.knowledgeFreshnessStore()
	if err != nil {
		return err
	}
	if err := store.EnsureKnowledgeFreshnessScans(ctx); err != nil {
		return err
	}
	if scan, _, claimed, err := store.ClaimKnowledgeFreshnessScan(ctx, k6Lease); err != nil {
		return err
	} else if claimed {
		if _, err := store.CompleteKnowledgeFreshnessScan(ctx, scan.EventID); err != nil {
			_ = store.RetryKnowledgeFreshnessScan(context.Background(), scan.EventID, err.Error(), time.Now().UTC().Add(time.Second))
		}
	}
	run, claimed, err := store.ClaimRevalidationRun(ctx, k6Lease)
	if err != nil || !claimed {
		return err
	}
	s.runRevalidation(ctx, store, run)
	return nil
}

func (s *Service) runRevalidation(ctx context.Context, store repository.KnowledgeFreshnessStore, run domain.RevalidationRun) {
	fail := func(code, message string) {
		_, _ = store.FailRevalidationRun(context.Background(), run.RevalidationID, code, message)
	}
	if !s.chatRuntimeEnabled || s.runtimeClient == nil {
		fail("runtime_unavailable", "runtime chat endpoint is unavailable")
		return
	}
	snapshot, found, err := store.GetKnowledgeSnapshot(ctx, run.LatestSnapshotID)
	if err != nil || !found || snapshot.ManifestSHA256 != run.LatestSnapshotManifestHash || len(snapshot.Members) == 0 {
		fail("knowledge_snapshot_unavailable", "the frozen latest knowledge snapshot is unavailable")
		return
	}
	conversation, _, ok := s.store.GetConversation(run.ConversationID)
	if !ok {
		fail("revalidation_invalid", "the original conversation is unavailable")
		return
	}
	config, ok := s.store.GetConfig(conversation.ConfigVersion)
	if !ok {
		fail("runtime_unavailable", "the conversation config snapshot is unavailable")
		return
	}
	retrievalConfig, ok := s.store.GetRetrievalConfig(conversation.RetrievalConfigVersion)
	if !ok {
		fail("runtime_unavailable", "the conversation retrieval config snapshot is unavailable")
		return
	}
	revisionIDs := make([]string, 0, len(snapshot.Members))
	for _, member := range snapshot.Members {
		revisionIDs = append(revisionIDs, member.RevisionID)
	}
	scope := domain.RuntimeKnowledgeScope{SnapshotID: snapshot.SnapshotID, ManifestHash: snapshot.ManifestSHA256, RevisionIDs: revisionIDs, ResolvedAt: time.Now().UTC().Format(time.RFC3339)}
	request := domain.ChatRuntimeRequest{
		RequestID:      fmt.Sprintf("req_revalidate_%s_%d", run.RevalidationID, time.Now().UTC().UnixNano()),
		ConversationID: run.ConversationID, MessageID: run.RevalidationID, UserID: run.RequesterID,
		UserQuery: run.OriginalAnswer, ModelID: conversation.ModelID, ConfigVersion: conversation.ConfigVersion,
		RetrievalConfigVersion: conversation.RetrievalConfigVersion, KnowledgeSnapshotID: scope.SnapshotID,
		KnowledgeScope: &scope, AnswerMode: "revalidate", Stream: false, Messages: []domain.RuntimeConversationMessage{},
		RevalidationContext: &domain.RuntimeRevalidationContext{
			OriginalMessageID: run.OriginalMessageID, OriginalSnapshotID: run.OriginalSnapshotID, OriginalAnswer: run.OriginalAnswer,
			OriginalCitationRevisionIDs: run.OriginalCitationRevisionIDs, RelatedUpdateEventIDs: run.RelatedUpdateEventIDs,
		},
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
	runtimeCtx, cancel := context.WithTimeout(ctx, s.RuntimeCallTimeout())
	defer cancel()
	response, err := s.runtimeClient.RunChat(runtimeCtx, request)
	if err != nil {
		fail("runtime_unavailable", err.Error())
		return
	}
	if err := validateRevalidationRuntimeResponse(request, response); err != nil {
		fail("snapshot_scope_violation", err.Error())
		return
	}
	outcome := response.RevalidationOutcome
	if response.Refusal || len(response.Citations) == 0 {
		outcome = "insufficient_evidence"
	}
	if !validK6RevalidationOutcome(outcome) {
		fail("revalidation_invalid", "runtime returned an invalid revalidation outcome")
		return
	}
	if _, err := store.CompleteRevalidationRun(ctx, run.RevalidationID, response, outcome); err != nil {
		code := "revalidation_failed"
		if errors.Is(err, repository.ErrKnowledgeSnapshotCitationMismatch) {
			code = "snapshot_scope_violation"
		}
		fail(code, err.Error())
	}
}

func validK6RevalidationOutcome(value string) bool {
	return value == "still_valid" || value == "needs_update" || value == "insufficient_evidence"
}

// K6 uses the same immutable request/citation identity checks as K5 but has a
// distinct Runtime freshness contract: a completed revalidation must echo
// `revalidated`, never the normal-answer value `current`.
func validateRevalidationRuntimeResponse(request domain.ChatRuntimeRequest, result domain.ChatRuntimeResponse) error {
	scope := request.KnowledgeScope
	if scope == nil || strings.TrimSpace(scope.SnapshotID) == "" || strings.TrimSpace(scope.ManifestHash) == "" || len(scope.RevisionIDs) == 0 {
		return fmt.Errorf("%w: request has no complete knowledge scope", ErrSnapshotScopeViolation)
	}
	if request.KnowledgeSnapshotID != scope.SnapshotID || result.RequestID != request.RequestID || result.ConversationID != request.ConversationID || result.MessageID != request.MessageID {
		return fmt.Errorf("%w: runtime response identity does not match request", ErrSnapshotScopeViolation)
	}
	if result.KnowledgeSnapshotID != scope.SnapshotID || result.KnowledgeSnapshotManifestHash != scope.ManifestHash || result.FreshnessStatus != "revalidated" {
		return fmt.Errorf("%w: runtime response does not echo the revalidation knowledge scope", ErrSnapshotScopeViolation)
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

func (s *Service) startK6Worker() {
	if !s.KnowledgeFreshnessEnabled() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.k6WorkerCancel = cancel
	go func() {
		ticker := time.NewTicker(k6WorkerInterval)
		defer ticker.Stop()
		for {
			_ = s.RunK6Once(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Service) knowledgeFreshnessStore() (repository.KnowledgeFreshnessStore, error) {
	store, ok := s.store.(repository.KnowledgeFreshnessStore)
	if !ok {
		return nil, ErrKnowledgeFreshnessUnavailable
	}
	return store, nil
}
