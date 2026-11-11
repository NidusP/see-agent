package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/domain"
	intentrouter "see-agent/services/backend-go/internal/intent"
	"see-agent/services/backend-go/internal/repository"
)

const (
	onboardingWorkerInterval = 250 * time.Millisecond
	onboardingRunLease       = 2 * time.Minute
)

// RunOnboardingQualityOnce is deterministic for integration tests and is also
// the sole execution path used by the background worker. It never changes a
// release, config, retrieval config, snapshot, or knowledge document.
func (s *Service) RunOnboardingQualityOnce(ctx context.Context) error {
	store, err := s.onboardingQualityStore()
	if err != nil {
		return err
	}
	run, claimed, err := store.ClaimOnboardingRun(ctx, onboardingRunLease)
	if err != nil || !claimed {
		return err
	}
	s.runOnboardingQuality(ctx, store, run)
	return nil
}

func (s *Service) runOnboardingQuality(ctx context.Context, store repository.OnboardingQualityStore, run domain.OnboardingRun) {
	fail := func(code, message string) {
		_, _ = store.FailOnboardingRun(context.Background(), run.RunID, code, message)
	}
	if !s.chatRuntimeEnabled || s.runtimeClient == nil {
		fail("runtime_unavailable", "runtime chat endpoint is unavailable")
		return
	}
	dataset, found, err := store.GetOnboardingDataset(ctx, run.DatasetID)
	if err != nil || !found || dataset.Status != "ready" || dataset.CasesSHA256 != run.CasesSHA256 {
		fail("dataset_unavailable", "the frozen onboarding dataset is unavailable or changed")
		return
	}
	snapshotStore, ok := s.store.(repository.KnowledgeFreshnessStore)
	if !ok {
		fail("knowledge_snapshot_unavailable", "knowledge snapshots require PostgreSQL")
		return
	}
	snapshot, found, err := snapshotStore.GetKnowledgeSnapshot(ctx, run.KnowledgeSnapshotID)
	if err != nil || !found || snapshot.Status != "published" || snapshot.ManifestSHA256 != run.KnowledgeSnapshotManifestHash || len(snapshot.Members) == 0 {
		fail("knowledge_snapshot_unavailable", "the frozen knowledge snapshot is unavailable")
		return
	}
	config, ok := s.store.GetConfig(run.ConfigVersion)
	if !ok {
		fail("config_unavailable", "the frozen agent config is unavailable")
		return
	}
	retrievalConfig, ok := s.store.GetRetrievalConfig(run.RetrievalConfigVersion)
	if !ok {
		fail("retrieval_config_unavailable", "the frozen retrieval config is unavailable")
		return
	}
	revisionIDs := make([]string, 0, len(snapshot.Members))
	for _, member := range snapshot.Members {
		revisionIDs = append(revisionIDs, member.RevisionID)
	}
	scope := domain.RuntimeKnowledgeScope{SnapshotID: snapshot.SnapshotID, ManifestHash: snapshot.ManifestSHA256, RevisionIDs: revisionIDs, ResolvedAt: time.Now().UTC().Format(time.RFC3339)}
	results := make([]domain.OnboardingCaseResult, 0, len(dataset.Cases))
	for _, item := range dataset.Cases {
		request := domain.ChatRuntimeRequest{
			RequestID: fmt.Sprintf("req_onboard_%s_%s", run.RunID, item.CaseID), ConversationID: run.RunID, MessageID: item.CaseID,
			UserQuery: item.Question, ModelID: config.ModelID, ConfigVersion: run.ConfigVersion, RetrievalConfigVersion: run.RetrievalConfigVersion,
			KnowledgeSnapshotID: scope.SnapshotID, KnowledgeScope: &scope, Intent: intentrouter.KnowledgeQuery, Stream: false,
			ResponseOverrides: &domain.RuntimeResponseOverrides{SystemPromptVersion: config.SystemPromptVersion, ResponseStyle: config.ResponseStyle, RetrievalStrategy: retrievalConfig.RetrievalStrategy, EmbeddingModel: retrievalConfig.EmbeddingModel, Temperature: &config.Temperature, TopP: &config.TopP, MaxOutputTokens: &config.MaxOutputTokens, TopK: &retrievalConfig.TopK, RerankEnabled: &retrievalConfig.RerankEnabled, EvidenceMinScore: &retrievalConfig.EvidenceMinScore},
		}
		runtimeCtx, cancel := context.WithTimeout(ctx, s.RuntimeCallTimeout())
		response, callErr := s.runtimeClient.RunChat(runtimeCtx, request)
		cancel()
		if callErr != nil {
			fail("runtime_unavailable", callErr.Error())
			return
		}
		if err := validateRuntimeExecutionFacts(request, response); err != nil {
			fail("runtime_contract_violation", err.Error())
			return
		}
		if err := validateScopedRuntimeResponse(request, response); err != nil {
			fail("snapshot_scope_violation", err.Error())
			return
		}
		results = append(results, gradeOnboardingCase(run.RunID, item, response))
	}
	passed := 0
	for _, result := range results {
		if result.Passed {
			passed++
		}
	}
	status := "failed"
	if passed == len(results) {
		status = "passed"
	}
	metrics, _ := json.Marshal(map[string]any{"total_cases": len(results), "passed_cases": passed, "failed_cases": len(results) - passed, "gate_passed": status == "passed"})
	_, _ = store.CompleteOnboardingRun(ctx, run.RunID, status, results, metrics)
}

func gradeOnboardingCase(runID string, item domain.OnboardingCase, response domain.ChatRuntimeResponse) domain.OnboardingCaseResult {
	result := domain.OnboardingCaseResult{RunID: runID, CaseID: item.CaseID, Refusal: response.Refusal, ObservedAnswer: response.AssistantMessage.Content, ObservedCitations: runtimeCitations(response.Citations), CompletedAt: time.Now().UTC().Format(time.RFC3339)}
	if item.CaseType == "must_refuse" {
		result.Passed = response.Refusal && len(response.Citations) == 0
		if !result.Passed {
			result.FailureCode = "must_refuse_violation"
		}
		return result
	}
	expectedRevisions := make(map[string]struct{}, len(item.ExpectedRevisionIDs))
	for _, id := range item.ExpectedRevisionIDs {
		expectedRevisions[id] = struct{}{}
	}
	revisionHit := false
	for _, citation := range response.Citations {
		if _, ok := expectedRevisions[citation.RevisionID]; ok {
			revisionHit = true
			break
		}
	}
	locatorHit := allLocatorHintsPresent(item.ExpectedLocatorHints, response.Citations)
	rate := answerPointHitRate(item.ExpectedAnswerPoints, response.AssistantMessage.Content)
	result.ExpectedRevisionHit = &revisionHit
	result.ExpectedLocatorHit = &locatorHit
	result.AnswerPointHitRate = &rate
	result.Passed = !response.Refusal && revisionHit && locatorHit && rate == 1
	if !result.Passed {
		result.FailureCode = "must_answer_truth_mismatch"
	}
	return result
}

func runtimeCitations(items []domain.RuntimeCitation) []domain.Citation {
	result := make([]domain.Citation, 0, len(items))
	for _, item := range items {
		result = append(result, domain.Citation{ChunkID: item.ChunkID, FileID: item.FileID, DocumentID: item.DocumentID, RevisionID: item.RevisionID, ChunkContentSHA256: item.ChunkContentSHA256, KnowledgeSnapshotID: item.KnowledgeSnapshotID, Filename: item.Filename, Rank: item.Rank, Score: item.Score, CitationLabel: item.CitationLabel, SourceLocator: item.SourceLocator, Snippet: item.Snippet})
	}
	return result
}

func allLocatorHintsPresent(hints []string, citations []domain.RuntimeCitation) bool {
	for _, hint := range hints {
		needle := strings.ToLower(strings.TrimSpace(hint))
		if needle == "" {
			return false
		}
		found := false
		for _, citation := range citations {
			locator, _ := json.Marshal(citation.SourceLocator)
			haystack := strings.ToLower(citation.CitationLabel + "\n" + citation.Snippet + "\n" + string(locator))
			if strings.Contains(haystack, needle) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(hints) > 0
}

func answerPointHitRate(points []string, answer string) float64 {
	if len(points) == 0 {
		return 0
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	hits := 0
	for _, point := range points {
		if needle := strings.ToLower(strings.TrimSpace(point)); needle != "" && strings.Contains(answer, needle) {
			hits++
		}
	}
	return float64(hits) / float64(len(points))
}

func (s *Service) startOnboardingQualityWorker() {
	if _, err := s.onboardingQualityStore(); err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.onboardingWorkerCancel = cancel
	go func() {
		ticker := time.NewTicker(onboardingWorkerInterval)
		defer ticker.Stop()
		for {
			_ = s.RunOnboardingQualityOnce(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
