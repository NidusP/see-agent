package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
)

func TestPostgresOnboardingDatasetLifecycle(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("ONBOARDING_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set ONBOARDING_TEST_DATABASE_URL to run the isolated onboarding Postgres smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("onboarding_%d", time.Now().UTC().UnixNano())
	if _, err = adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteK3Identifier(schema)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quoteK3Identifier(schema)+` CASCADE`)
	}()
	store, err := NewPostgresStore(ctx, k3DatabaseURLWithSearchPath(t, baseURL, schema))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	revision := k3ReadyRevision(t, ctx, store, "onboarding", "sha256:onboarding")
	if _, err = store.PublishKnowledgeRevision(ctx, revision.RevisionID, "admin", "onboarding"); err != nil {
		t.Fatal(err)
	}
	scope, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		t.Fatal(err)
	}

	dataset, err := store.CreateOnboardingDataset(ctx, "admin", "onboarding-dataset", "new policy acceptance", scope.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := store.CreateOnboardingDataset(ctx, "admin", "onboarding-dataset", "new policy acceptance", scope.SnapshotID)
	if err != nil || repeated.DatasetID != dataset.DatasetID {
		t.Fatalf("idempotent dataset=%#v err=%v", repeated, err)
	}
	if _, err = store.CreateOnboardingCase(ctx, "admin", "onboarding-wrong-revision", domain.OnboardingCase{DatasetID: dataset.DatasetID, CaseType: "must_answer", Question: "what applies?", ExpectedRevisionIDs: []string{"missing"}, ExpectedLocatorHints: []string{"policy"}, ExpectedAnswerPoints: []string{"answer"}, ReviewStatus: "reviewed"}); !errors.Is(err, ErrQualityInvalid) {
		t.Fatalf("foreign revision accepted: %v", err)
	}
	answer, err := store.CreateOnboardingCase(ctx, "admin", "onboarding-answer", domain.OnboardingCase{DatasetID: dataset.DatasetID, CaseType: "must_answer", Question: "what applies?", ExpectedRevisionIDs: []string{revision.RevisionID}, ExpectedLocatorHints: []string{"policy"}, ExpectedAnswerPoints: []string{"answer"}, ReviewStatus: "reviewed"})
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		if _, err = store.CreateOnboardingCase(ctx, "admin", fmt.Sprintf("onboarding-refuse-%d", index), domain.OnboardingCase{DatasetID: dataset.DatasetID, CaseType: "must_refuse", Question: fmt.Sprintf("unknown-%d", index), ReviewStatus: "reviewed"}); err != nil {
			t.Fatal(err)
		}
	}
	frozen, err := store.FreezeOnboardingDataset(ctx, "admin", dataset.DatasetID, "onboarding-freeze")
	if err != nil || frozen.Status != "ready" || frozen.CasesSHA256 == "" {
		t.Fatalf("freeze=%#v err=%v", frozen, err)
	}
	if _, err = store.CreateOnboardingCase(ctx, "admin", "onboarding-after-freeze", domain.OnboardingCase{DatasetID: dataset.DatasetID, CaseType: "must_refuse", Question: "late", ReviewStatus: "reviewed"}); !errors.Is(err, ErrQualityConflict) {
		t.Fatalf("frozen dataset changed: %v", err)
	}

	run, err := store.CreateOnboardingRun(ctx, "admin", dataset.DatasetID, "onboarding-run")
	if err != nil || run.Status != "queued" || run.KnowledgeSnapshotID != scope.SnapshotID || run.KnowledgeSnapshotManifestHash != scope.ManifestHash || run.ConfigVersion == "" || run.RetrievalConfigVersion == "" {
		t.Fatalf("run=%#v err=%v", run, err)
	}
	loaded, found, err := store.GetOnboardingDataset(ctx, dataset.DatasetID)
	if err != nil || !found || len(loaded.Cases) != 4 || loaded.Cases[0].CaseID != answer.CaseID {
		t.Fatalf("dataset=%#v found=%v err=%v", loaded, found, err)
	}
	claimed, claimedOK, err := store.ClaimOnboardingRun(ctx, time.Minute)
	if err != nil || !claimedOK || claimed.Status != "running" || claimed.RunID != run.RunID {
		t.Fatalf("claim=%#v claimed=%v err=%v", claimed, claimedOK, err)
	}
	results := make([]domain.OnboardingCaseResult, 0, len(loaded.Cases))
	for _, item := range loaded.Cases {
		results = append(results, domain.OnboardingCaseResult{RunID: run.RunID, CaseID: item.CaseID, Passed: true, CompletedAt: time.Now().UTC().Format(time.RFC3339)})
	}
	completed, err := store.CompleteOnboardingRun(ctx, run.RunID, "passed", results, json.RawMessage(`{"total_cases":4,"passed_cases":4}`))
	if err != nil || completed.Status != "passed" || len(completed.Results) != 4 {
		t.Fatalf("complete=%#v err=%v", completed, err)
	}

	// Retiring old knowledge needs two distinct current-snapshot proofs: one
	// while both old and replacement are present, and one after old is absent.
	replacement := k3ReadyRevision(t, ctx, store, "retirement-replacement", "sha256:retirement-replacement")
	if _, err = store.PublishKnowledgeRevision(ctx, replacement.RevisionID, "admin", "retirement-replacement"); err != nil {
		t.Fatal(err)
	}
	withReplacement, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	completePassingOnboardingScope(t, ctx, store, "with-replacement", withReplacement.SnapshotID, replacement.RevisionID)
	readiness, err := store.GetKnowledgeRetirementReadiness(ctx, revision.DocumentID)
	if err != nil || readiness.Stage != "ready_to_disable" || readiness.PassingOnboardingRunID == "" {
		t.Fatalf("disable readiness=%#v err=%v", readiness, err)
	}
	if _, err = store.DisableKnowledgeDocumentAfterReadiness(ctx, revision.DocumentID, domain.KnowledgeMutationKey{ActorID: "admin", Operation: KnowledgeOperationDisableDocument(), Key: "retirement-disable", RequestHash: "sha256:retirement-disable"}); err != nil {
		t.Fatal(err)
	}
	readiness, err = store.GetKnowledgeRetirementReadiness(ctx, revision.DocumentID)
	if err != nil || readiness.Stage != "blocked" {
		t.Fatalf("delete should need new-only proof: readiness=%#v err=%v", readiness, err)
	}
	newOnly, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	completePassingOnboardingScope(t, ctx, store, "new-only", newOnly.SnapshotID, replacement.RevisionID)
	readiness, err = store.GetKnowledgeRetirementReadiness(ctx, revision.DocumentID)
	if err != nil || readiness.Stage != "ready_to_delete" || readiness.PassingOnboardingRunID == "" {
		t.Fatalf("delete readiness=%#v err=%v", readiness, err)
	}
	deleted, err := store.DeleteKnowledgeDocumentAfterReadiness(ctx, revision.DocumentID, domain.KnowledgeMutationKey{ActorID: "admin", Operation: KnowledgeOperationDeleteDocument(), Key: "retirement-delete", RequestHash: "sha256:retirement-delete"})
	if err != nil || deleted.Document.Status != "deleted" {
		t.Fatalf("delete=%#v err=%v", deleted, err)
	}
}

func completePassingOnboardingScope(t *testing.T, ctx context.Context, store *PostgresStore, suffix, snapshotID, expectedRevisionID string) {
	t.Helper()
	dataset, err := store.CreateOnboardingDataset(ctx, "admin", "retirement-dataset-"+suffix, "retirement "+suffix, snapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateOnboardingCase(ctx, "admin", "retirement-answer-"+suffix, domain.OnboardingCase{DatasetID: dataset.DatasetID, CaseType: "must_answer", Question: "what changed " + suffix, ExpectedRevisionIDs: []string{expectedRevisionID}, ExpectedLocatorHints: []string{"policy"}, ExpectedAnswerPoints: []string{"answer"}, ReviewStatus: "reviewed"}); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		if _, err = store.CreateOnboardingCase(ctx, "admin", fmt.Sprintf("retirement-refuse-%s-%d", suffix, index), domain.OnboardingCase{DatasetID: dataset.DatasetID, CaseType: "must_refuse", Question: fmt.Sprintf("unknown-%s-%d", suffix, index), ReviewStatus: "reviewed"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.FreezeOnboardingDataset(ctx, "admin", dataset.DatasetID, "retirement-freeze-"+suffix); err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateOnboardingRun(ctx, "admin", dataset.DatasetID, "retirement-run-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimOnboardingRun(ctx, time.Minute)
	if err != nil || !ok || claimed.RunID != run.RunID {
		t.Fatalf("claim=%#v ok=%v err=%v", claimed, ok, err)
	}
	loaded, found, err := store.GetOnboardingDataset(ctx, dataset.DatasetID)
	if err != nil || !found {
		t.Fatalf("dataset found=%v err=%v", found, err)
	}
	results := make([]domain.OnboardingCaseResult, 0, len(loaded.Cases))
	for _, item := range loaded.Cases {
		results = append(results, domain.OnboardingCaseResult{RunID: run.RunID, CaseID: item.CaseID, Passed: true, CompletedAt: time.Now().UTC().Format(time.RFC3339)})
	}
	if _, err = store.CompleteOnboardingRun(ctx, run.RunID, "passed", results, json.RawMessage(`{"total_cases":4,"passed_cases":4}`)); err != nil {
		t.Fatal(err)
	}
}
