package service

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/repository"
)

type onboardingRuntimeStub struct {
	documentID string
	revisionID string
	requests   []domain.ChatRuntimeRequest
}

func (s *onboardingRuntimeStub) RunIngestion(context.Context, domain.KnowledgeFile, domain.IngestionTaskSummary) (domain.IngestionTaskResult, error) {
	return domain.IngestionTaskResult{}, nil
}
func (s *onboardingRuntimeStub) StreamChat(context.Context, domain.ChatRuntimeRequest, func(domain.ChatRuntimeStreamEvent) error) error {
	return nil
}
func (s *onboardingRuntimeStub) RunChat(_ context.Context, request domain.ChatRuntimeRequest) (domain.ChatRuntimeResponse, error) {
	s.requests = append(s.requests, request)
	if strings.HasPrefix(request.UserQuery, "unknown") {
		return domain.ChatRuntimeResponse{RequestID: request.RequestID, ConversationID: request.ConversationID, MessageID: request.MessageID, AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "没有足够证据，不能回答。"}, Refusal: true, FinishReason: "stop", CompletedAt: time.Now().UTC().Format(time.RFC3339), KnowledgeSnapshotID: request.KnowledgeScope.SnapshotID, KnowledgeSnapshotManifestHash: request.KnowledgeScope.ManifestHash, FreshnessStatus: "current", Intent: "knowledge_query", RetrievalPerformed: true}, nil
	}
	return domain.ChatRuntimeResponse{RequestID: request.RequestID, ConversationID: request.ConversationID, MessageID: request.MessageID, AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "The approved limit applies."}, Citations: []domain.RuntimeCitation{{Rank: 1, ChunkID: "chunk_1", FileID: "file_1", DocumentID: s.documentID, RevisionID: s.revisionID, ChunkContentSHA256: "sha256:chunk", KnowledgeSnapshotID: request.KnowledgeScope.SnapshotID, Filename: "policy.md", CitationLabel: "Policy section 2", SourceLocator: domain.SourceLocator{BlockLabel: "policy section"}, Snippet: "approved limit"}}, FinishReason: "stop", CompletedAt: time.Now().UTC().Format(time.RFC3339), KnowledgeSnapshotID: request.KnowledgeScope.SnapshotID, KnowledgeSnapshotManifestHash: request.KnowledgeScope.ManifestHash, FreshnessStatus: "current", Intent: "knowledge_query", RetrievalPerformed: true, GenerationPerformed: true, KnowledgeUsed: true}, nil
}

func TestGradeOnboardingCase(t *testing.T) {
	answerCase := domain.OnboardingCase{CaseID: "answer", CaseType: "must_answer", ExpectedRevisionIDs: []string{"rev_1"}, ExpectedLocatorHints: []string{"policy section"}, ExpectedAnswerPoints: []string{"approved limit"}}
	response := domain.ChatRuntimeResponse{AssistantMessage: domain.RuntimeAssistantMessage{Content: "The approved limit applies."}, Citations: []domain.RuntimeCitation{{RevisionID: "rev_1", CitationLabel: "Policy section 2"}}}
	result := gradeOnboardingCase("run", answerCase, response)
	if !result.Passed || result.ExpectedRevisionHit == nil || !*result.ExpectedRevisionHit || result.ExpectedLocatorHit == nil || !*result.ExpectedLocatorHit || result.AnswerPointHitRate == nil || *result.AnswerPointHitRate != 1 {
		t.Fatalf("expected complete must-answer pass, got %#v", result)
	}

	refuseCase := domain.OnboardingCase{CaseID: "refuse", CaseType: "must_refuse"}
	result = gradeOnboardingCase("run", refuseCase, domain.ChatRuntimeResponse{Refusal: true})
	if !result.Passed {
		t.Fatalf("expected refusal pass, got %#v", result)
	}
	result = gradeOnboardingCase("run", refuseCase, domain.ChatRuntimeResponse{Refusal: false, Citations: []domain.RuntimeCitation{{RevisionID: "rev_1"}}})
	if result.Passed || result.FailureCode != "must_refuse_violation" {
		t.Fatalf("expected refusal failure, got %#v", result)
	}
}

func TestOnboardingAnswerPointHitRateRequiresEveryPoint(t *testing.T) {
	if got := answerPointHitRate([]string{"one", "two"}, "one"); got != 0.5 {
		t.Fatalf("rate=%v", got)
	}
}

func TestPostgresOnboardingWorkerRunsFrozenScope(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("ONBOARDING_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set ONBOARDING_TEST_DATABASE_URL to run the isolated onboarding worker smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("onboarding_worker_%d", time.Now().UTC().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`) }()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	store, err := repository.NewPostgresStore(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	documentID, revisionID := "doc_onboarding_worker", "rev_onboarding_worker"
	if _, err = store.CreateKnowledgeDocument(ctx, domain.KnowledgeDocument{DocumentID: documentID, DisplayName: "Worker policy", OwnerID: "admin"}); err != nil {
		t.Fatal(err)
	}
	revision, err := store.CreateKnowledgeRevision(ctx, domain.KnowledgeRevision{RevisionID: revisionID, DocumentID: documentID, Filename: "policy.md", MIMEType: "text/markdown", SourceFormat: "md", ObjectURI: "s3://worker/policy.md", ChecksumSHA256: "sha256:worker", ParseVersion: "parse_v1", IndexVersion: "index_v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.QueueKnowledgeRevision(ctx, revision.RevisionID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.MarkKnowledgeRevisionReady(ctx, revision.RevisionID, "sha256:worker-chunks", "test-embedding"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishKnowledgeRevision(ctx, revision.RevisionID, "admin", "onboarding-worker"); err != nil {
		t.Fatal(err)
	}
	scope, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := store.CreateOnboardingDataset(ctx, "admin", "worker-dataset", "worker gate", scope.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateOnboardingCase(ctx, "admin", "worker-answer", domain.OnboardingCase{DatasetID: dataset.DatasetID, CaseType: "must_answer", Question: "what applies?", ExpectedRevisionIDs: []string{revisionID}, ExpectedLocatorHints: []string{"policy section"}, ExpectedAnswerPoints: []string{"approved limit"}, ReviewStatus: "reviewed"}); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		if _, err = store.CreateOnboardingCase(ctx, "admin", fmt.Sprintf("worker-refuse-%d", index), domain.OnboardingCase{DatasetID: dataset.DatasetID, CaseType: "must_refuse", Question: fmt.Sprintf("unknown-%d", index), ReviewStatus: "reviewed"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.FreezeOnboardingDataset(ctx, "admin", dataset.DatasetID, "worker-freeze"); err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateOnboardingRun(ctx, "admin", dataset.DatasetID, "worker-run")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &onboardingRuntimeStub{documentID: documentID, revisionID: revisionID}
	svc := New(store, runtime, "", true)
	defer svc.Close()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		loaded, found, getErr := store.GetOnboardingRun(ctx, run.RunID)
		if getErr != nil || !found {
			t.Fatalf("run loaded=%#v found=%v err=%v", loaded, found, getErr)
		}
		if loaded.Status == "passed" {
			if len(loaded.Results) != 4 || len(runtime.requests) != 4 {
				t.Fatalf("run=%#v requests=%d", loaded, len(runtime.requests))
			}
			for _, request := range runtime.requests {
				if request.KnowledgeSnapshotID != scope.SnapshotID || request.ConfigVersion != run.ConfigVersion || request.RetrievalConfigVersion != run.RetrievalConfigVersion {
					t.Fatalf("unfrozen request=%#v", request)
				}
			}
			return
		}
	}
	loaded, _, _ := store.GetOnboardingRun(ctx, run.RunID)
	t.Fatalf("worker did not pass run: %#v", loaded)
}
