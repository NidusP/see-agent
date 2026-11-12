package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
)

func TestOptionalPrepublishOnboarding(t *testing.T) {
	ctx, store := openPrepublishStore(t)
	defer store.Close()

	base := k3ReadyRevision(t, ctx, store, "base", "sha256:base")
	basePublished, err := store.PublishKnowledgeRevisionLifecycle(ctx, base.RevisionID, prepublishKey(knowledgeOperationPublishRevision, "publish-base"), false)
	if !errors.As(err, &UnvalidatedAcknowledgementError{}) || basePublished.Snapshot != nil {
		t.Fatalf("direct publish without acknowledgement = %#v err=%v", basePublished, err)
	}
	basePublished, err = store.PublishKnowledgeRevisionLifecycle(ctx, base.RevisionID, prepublishKey(knowledgeOperationPublishRevision, "publish-base-ack"), true)
	if err != nil || basePublished.Snapshot == nil || basePublished.Snapshot.Status != "published" || basePublished.Snapshot.Acceptance == nil || basePublished.Snapshot.Acceptance.DisplayLabel != "已发布 · 未验收" {
		t.Fatalf("acknowledged direct publish = %#v err=%v", basePublished, err)
	}
	if basePublished.UpdateEvent == nil || basePublished.UpdateEvent.AcceptanceStatus != "unvalidated" || basePublished.UpdateEvent.OnboardingRunID != "" {
		t.Fatalf("publish event = %#v", basePublished.UpdateEvent)
	}
	baseSnapshotID := basePublished.Snapshot.SnapshotID

	next := k3ReadyRevision(t, ctx, store, "next", "sha256:next")
	prepared, err := store.PrepareKnowledgeRevisionPublish(ctx, next.RevisionID, prepublishKey(knowledgeOperationPreparePublish, "prepare-next"))
	if err != nil || prepared.Snapshot == nil || prepared.Snapshot.Status != "pending_publish" {
		t.Fatalf("prepare = %#v err=%v", prepared, err)
	}
	repeated, err := store.PrepareKnowledgeRevisionPublish(ctx, next.RevisionID, prepublishKey(knowledgeOperationPreparePublish, "prepare-next-again"))
	if err != nil || repeated.Snapshot == nil || repeated.Snapshot.SnapshotID != prepared.Snapshot.SnapshotID {
		t.Fatalf("repeated prepare = %#v err=%v", repeated, err)
	}
	active, found, err := store.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil || !found || active.SnapshotID != baseSnapshotID || active.Status != "published" {
		t.Fatalf("prepare changed active snapshot: %#v found=%v err=%v", active, found, err)
	}
	scope, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil || scope.SnapshotID != baseSnapshotID || containsString(scope.RevisionIDs, next.RevisionID) {
		t.Fatalf("chat scope saw pending revision: %#v err=%v", scope, err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE knowledge_active_snapshot SET snapshot_id = $1`, prepared.Snapshot.SnapshotID); err == nil {
		t.Fatal("active pointer accepted a pending_publish snapshot")
	}
	active, found, err = store.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil || !found || active.SnapshotID != baseSnapshotID {
		t.Fatalf("pointer changed after rejected pending target: %#v found=%v err=%v", active, found, err)
	}

	pendingDataset, err := store.CreateOnboardingDataset(ctx, "admin", "pending-dataset", "pending acceptance", prepared.Snapshot.SnapshotID)
	if err != nil || pendingDataset.KnowledgeSnapshotID != prepared.Snapshot.SnapshotID {
		t.Fatalf("onboarding dataset on pending snapshot = %#v err=%v", pendingDataset, err)
	}
	if _, err := store.PublishKnowledgeRevisionLifecycle(ctx, next.RevisionID, prepublishKey(knowledgeOperationPublishRevision, "publish-next-no-ack"), false); !errors.As(err, &UnvalidatedAcknowledgementError{}) {
		t.Fatalf("activate without acknowledgement error = %v", err)
	}
	active, _, err = store.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil || active.SnapshotID != baseSnapshotID {
		t.Fatalf("rejected activation changed active snapshot: %#v err=%v", active, err)
	}

	passingRunID := passOnboarding(t, ctx, store, prepared.Snapshot.SnapshotID, next.RevisionID, "pending-pass")
	activated, err := store.PublishKnowledgeRevisionLifecycle(ctx, next.RevisionID, prepublishKey(knowledgeOperationPublishRevision, "publish-next-accepted"), false)
	if err != nil || activated.Snapshot == nil || activated.Snapshot.Status != "published" || activated.Snapshot.Acceptance == nil || activated.Snapshot.Acceptance.DisplayLabel != "已发布 · 已验收" || activated.Snapshot.Acceptance.OnboardingRunID != passingRunID {
		t.Fatalf("accepted activation = %#v err=%v", activated, err)
	}
	if activated.UpdateEvent == nil || activated.UpdateEvent.AcceptanceStatus != "accepted" || activated.UpdateEvent.OnboardingRunID != passingRunID {
		t.Fatalf("accepted publish event = %#v", activated.UpdateEvent)
	}
	scope, err = store.ResolveActiveKnowledgeScope(ctx)
	if err != nil || scope.SnapshotID != activated.Snapshot.SnapshotID || !containsString(scope.RevisionIDs, next.RevisionID) || !containsString(scope.RevisionIDs, base.RevisionID) {
		t.Fatalf("chat scope after accepted publish = %#v err=%v", scope, err)
	}
	if _, err := store.CreateOnboardingDataset(ctx, "admin", "retired-dataset", "retired", baseSnapshotID); !errors.Is(err, ErrQualityConflict) {
		t.Fatalf("retired snapshot accepted onboarding: %v", err)
	}

	third := k3ReadyRevision(t, ctx, store, "third", "sha256:third")
	thirdPrepared, err := store.PrepareKnowledgeRevisionPublish(ctx, third.RevisionID, prepublishKey(knowledgeOperationPreparePublish, "prepare-third"))
	if err != nil || thirdPrepared.Snapshot == nil {
		t.Fatalf("prepare third = %#v err=%v", thirdPrepared, err)
	}
	fourth := k3ReadyRevision(t, ctx, store, "fourth", "sha256:fourth")
	fourthPrepared, err := store.PrepareKnowledgeRevisionPublish(ctx, fourth.RevisionID, prepublishKey(knowledgeOperationPreparePublish, "prepare-fourth"))
	if err != nil || fourthPrepared.Snapshot == nil || fourthPrepared.Snapshot.SnapshotID == thirdPrepared.Snapshot.SnapshotID {
		t.Fatalf("prepare fourth = %#v err=%v", fourthPrepared, err)
	}
	retiredPending, found, err := store.GetKnowledgeSnapshot(ctx, thirdPrepared.Snapshot.SnapshotID)
	if err != nil || !found || retiredPending.Status != "retired" {
		t.Fatalf("stale pending snapshot = %#v found=%v err=%v", retiredPending, found, err)
	}
	active, _, err = store.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil || active.SnapshotID != activated.Snapshot.SnapshotID {
		t.Fatalf("preparing another revision changed active snapshot: %#v err=%v", active, err)
	}

	unvalidated, err := store.ActivateKnowledgeSnapshot(ctx, fourthPrepared.Snapshot.SnapshotID, prepublishKey(knowledgeOperationActivateSnapshot, "activate-fourth-no-ack"), false)
	if !errors.As(err, &UnvalidatedAcknowledgementError{}) {
		t.Fatalf("unvalidated activate without acknowledgement = %#v err=%v", unvalidated, err)
	}
	unvalidated, err = store.ActivateKnowledgeSnapshot(ctx, fourthPrepared.Snapshot.SnapshotID, prepublishKey(knowledgeOperationActivateSnapshot, "activate-fourth"), true)
	if err != nil || unvalidated.Snapshot == nil || unvalidated.Snapshot.Acceptance == nil || unvalidated.Snapshot.Acceptance.DisplayLabel != "已发布 · 未验收" {
		t.Fatalf("unvalidated activate = %#v err=%v", unvalidated, err)
	}
	if scope, err = store.ResolveActiveKnowledgeScope(ctx); err != nil || scope.SnapshotID != unvalidated.Snapshot.SnapshotID {
		t.Fatalf("chat blocked after unvalidated publish: %#v err=%v", scope, err)
	}
	assertRetirementBlocked(t, ctx, store, base.DocumentID)
	historicalStatus, historicalRun := publishEventAcceptance(t, ctx, store, unvalidated.Snapshot.SnapshotID)
	if historicalStatus != "unvalidated" || historicalRun != "" {
		t.Fatalf("historical unvalidated event = %s %s", historicalStatus, historicalRun)
	}
	makeupRunID := passOnboarding(t, ctx, store, unvalidated.Snapshot.SnapshotID, fourth.RevisionID, "makeup")
	upgraded, found, err := store.GetKnowledgeSnapshot(ctx, unvalidated.Snapshot.SnapshotID)
	if err != nil || !found || upgraded.Acceptance == nil || upgraded.Acceptance.DisplayLabel != "已发布 · 已验收" || upgraded.Acceptance.OnboardingRunID != makeupRunID || upgraded.Status != "published" {
		t.Fatalf("makeup display = %#v found=%v err=%v", upgraded.Acceptance, found, err)
	}
	if status, runID := publishEventAcceptance(t, ctx, store, unvalidated.Snapshot.SnapshotID); status != "unvalidated" || runID != "" {
		t.Fatalf("makeup rewrote historical publish event: %s %s", status, runID)
	}
	page, err := store.ListKnowledgeUpdates(ctx, fourth.DocumentID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	sawLiveLabel := false
	for _, item := range page.Items {
		if item.SnapshotID == unvalidated.Snapshot.SnapshotID && item.EventType == "published" {
			if item.AcceptanceStatus != "unvalidated" || item.Acceptance == nil || item.Acceptance.DisplayLabel != "已发布 · 已验收" {
				t.Fatalf("calendar acceptance = historical %s live %#v", item.AcceptanceStatus, item.Acceptance)
			}
			sawLiveLabel = true
		}
	}
	if !sawLiveLabel {
		t.Fatal("calendar did not return the live accepted label")
	}

	failedRevision := k3ReadyRevision(t, ctx, store, "failed", "sha256:failed")
	failedPrepared, err := store.PrepareKnowledgeRevisionPublish(ctx, failedRevision.RevisionID, prepublishKey(knowledgeOperationPreparePublish, "prepare-failed"))
	if err != nil || failedPrepared.Snapshot == nil {
		t.Fatalf("prepare failed revision = %#v err=%v", failedPrepared, err)
	}
	_ = failOnboarding(t, ctx, store, failedPrepared.Snapshot.SnapshotID, failedRevision.RevisionID, "failed-run")
	failedActivate, err := store.ActivateKnowledgeSnapshot(ctx, failedPrepared.Snapshot.SnapshotID, prepublishKey(knowledgeOperationActivateSnapshot, "activate-failed-no-ack"), false)
	var acknowledgement UnvalidatedAcknowledgementError
	if !errors.As(err, &acknowledgement) || !strings.Contains(acknowledgement.Message, "发布后这份知识会立刻服务用户，但验收未通过") {
		t.Fatalf("failed activate without acknowledgement = %#v err=%v", failedActivate, err)
	}
	failedActivate, err = store.ActivateKnowledgeSnapshot(ctx, failedPrepared.Snapshot.SnapshotID, prepublishKey(knowledgeOperationActivateSnapshot, "activate-failed"), true)
	if err != nil || failedActivate.Snapshot == nil || failedActivate.Snapshot.Acceptance == nil || failedActivate.Snapshot.Acceptance.DisplayLabel != "已发布 · 验收未通过（仍在服务）" {
		t.Fatalf("failed activate = %#v err=%v", failedActivate, err)
	}
	if failedActivate.UpdateEvent == nil || failedActivate.UpdateEvent.AcceptanceStatus != "failed" {
		t.Fatalf("failed publish event = %#v", failedActivate.UpdateEvent)
	}
	if scope, err = store.ResolveActiveKnowledgeScope(ctx); err != nil || scope.SnapshotID != failedActivate.Snapshot.SnapshotID {
		t.Fatalf("chat blocked after failed acceptance publish: %#v err=%v", scope, err)
	}
	assertRetirementBlocked(t, ctx, store, next.DocumentID)
	if _, err := store.DeleteKnowledgeDocumentAfterReadiness(ctx, next.DocumentID, prepublishKey(knowledgeOperationDeleteDocument, "delete-without-pass")); !errors.Is(err, ErrKnowledgeDocumentNotDisabled) {
		t.Fatalf("delete without retirement permission = %v", err)
	}
}

func TestPrepublishDraftQuestionsDoNotPublish(t *testing.T) {
	ctx, store := openPrepublishStore(t)
	defer store.Close()

	base := k3ReadyRevision(t, ctx, store, "draft-base", "sha256:draft-base")
	if _, err := store.PublishKnowledgeRevisionLifecycle(ctx, base.RevisionID, prepublishKey(knowledgeOperationPublishRevision, "draft-base"), true); err != nil {
		t.Fatal(err)
	}
	next := k3ReadyRevision(t, ctx, store, "draft-next", "sha256:draft-next")
	path := filepath.Join(t.TempDir(), "policy.md")
	if err := os.WriteFile(path, []byte("# 差旅标准\n国内机票须提前七天预订并走公司渠道。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE knowledge_revisions SET object_uri = $2 WHERE revision_id = $1`, next.RevisionID, path); err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareKnowledgeRevisionPublish(ctx, next.RevisionID, prepublishKey(knowledgeOperationPreparePublish, "draft-prepare"))
	if err != nil || prepared.Snapshot == nil {
		t.Fatalf("prepare = %#v err=%v", prepared, err)
	}
	dataset, err := store.CreateOnboardingDataset(ctx, "admin", "draft-dataset", "draft questions", prepared.Snapshot.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	drafts, err := store.GenerateOnboardingDrafts(ctx, "admin", dataset.DatasetID, "generate-drafts")
	if err != nil || len(drafts) != 4 {
		t.Fatalf("drafts = %#v err=%v", drafts, err)
	}
	answer, refuse := 0, 0
	for _, item := range drafts {
		if item.ReviewStatus != "draft_generated" || item.ReviewedBy != "" {
			t.Fatalf("generated case is not an unreviewed draft: %#v", item)
		}
		if item.CaseType == "must_answer" {
			answer++
			if len(item.ExpectedRevisionIDs) != 1 || item.ExpectedRevisionIDs[0] != next.RevisionID || len(item.ExpectedAnswerPoints) != 1 {
				t.Fatalf("must-answer draft = %#v", item)
			}
		} else {
			refuse++
			if len(item.ExpectedRevisionIDs) != 0 || len(item.ExpectedLocatorHints) != 0 || len(item.ExpectedAnswerPoints) != 0 {
				t.Fatalf("must-refuse draft has expected sources: %#v", item)
			}
		}
	}
	if answer != 1 || refuse != 3 {
		t.Fatalf("draft mix answer=%d refuse=%d", answer, refuse)
	}
	if _, err := store.FreezeOnboardingDataset(ctx, "admin", dataset.DatasetID, "freeze-drafts"); !errors.Is(err, ErrQualityConflict) {
		t.Fatalf("unreviewed drafts froze: %v", err)
	}
	var runs int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM onboarding_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("generator started runs = %d err=%v", runs, err)
	}
	pending, found, err := store.GetKnowledgeSnapshot(ctx, prepared.Snapshot.SnapshotID)
	if err != nil || !found || pending.Status != "pending_publish" {
		t.Fatalf("generator changed snapshot: %#v err=%v", pending, err)
	}
	active, _, err := store.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil || active.SnapshotID == prepared.Snapshot.SnapshotID {
		t.Fatalf("generator published snapshot: %#v err=%v", active, err)
	}
	for index, item := range drafts {
		reviewed, err := store.ReviewOnboardingCase(ctx, "admin", dataset.DatasetID, item.CaseID, fmt.Sprintf("review-%d", index), domain.OnboardingCase{})
		if err != nil || reviewed.ReviewStatus != "reviewed" || reviewed.ReviewedBy == "" {
			t.Fatalf("review = %#v err=%v", reviewed, err)
		}
	}
	frozen, err := store.FreezeOnboardingDataset(ctx, "admin", dataset.DatasetID, "freeze-reviewed")
	if err != nil || frozen.Status != "ready" {
		t.Fatalf("reviewed freeze = %#v err=%v", frozen, err)
	}

	blank := k3ReadyRevision(t, ctx, store, "blank", "sha256:blank")
	blankPrepared, err := store.PrepareKnowledgeRevisionPublish(ctx, blank.RevisionID, prepublishKey(knowledgeOperationPreparePublish, "blank-prepare"))
	if err != nil {
		t.Fatal(err)
	}
	blankDataset, err := store.CreateOnboardingDataset(ctx, "admin", "blank-dataset", "manual questions", blankPrepared.Snapshot.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GenerateOnboardingDrafts(ctx, "admin", blankDataset.DatasetID, "generate-blank"); !errors.Is(err, ErrDraftGenerationFailed) {
		t.Fatalf("blank generation error = %v", err)
	}
	if _, err := store.CreateOnboardingCase(ctx, "admin", "manual-answer", domain.OnboardingCase{DatasetID: blankDataset.DatasetID, CaseType: "must_answer", Question: "手工问题", ExpectedRevisionIDs: []string{blank.RevisionID}, ExpectedLocatorHints: []string{"手工"}, ExpectedAnswerPoints: []string{"手工答案"}, ReviewStatus: "reviewed"}); err != nil {
		t.Fatalf("manual case after generation failure: %v", err)
	}
	published, err := store.ActivateKnowledgeSnapshot(ctx, blankPrepared.Snapshot.SnapshotID, prepublishKey(knowledgeOperationActivateSnapshot, "blank-direct"), true)
	if err != nil || published.Snapshot == nil || published.Snapshot.Status != "published" || published.Snapshot.Acceptance == nil || published.Snapshot.Acceptance.DisplayLabel != "已发布 · 未验收" {
		t.Fatalf("direct publish after generation failure = %#v err=%v", published, err)
	}
	if scope, err := store.ResolveActiveKnowledgeScope(ctx); err != nil || scope.SnapshotID != published.Snapshot.SnapshotID {
		t.Fatalf("chat blocked after skipping acceptance: %#v err=%v", scope, err)
	}
}

func openPrepublishStore(t *testing.T) (context.Context, *PostgresStore) {
	t.Helper()
	baseURL := strings.TrimSpace(os.Getenv("K8R4B_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set K8R4B_TEST_DATABASE_URL to run optional pre-publish onboarding tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	schema := fmt.Sprintf("k8r4b_%d", time.Now().UTC().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteK3Identifier(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quoteK3Identifier(schema)+` CASCADE`)
	})
	store, err := NewPostgresStore(ctx, k3DatabaseURLWithSearchPath(t, baseURL, schema))
	if err != nil {
		t.Fatal(err)
	}
	return ctx, store
}

func prepublishKey(operation, key string) domain.KnowledgeMutationKey {
	return domain.KnowledgeMutationKey{ActorID: "user_admin", Operation: operation, Key: key, RequestHash: "sha256:" + operation + ":" + key}
}

func passOnboarding(t *testing.T, ctx context.Context, store *PostgresStore, snapshotID, revisionID, name string) string {
	t.Helper()
	return finishOnboarding(t, ctx, store, snapshotID, revisionID, name, true)
}

func failOnboarding(t *testing.T, ctx context.Context, store *PostgresStore, snapshotID, revisionID, name string) string {
	t.Helper()
	return finishOnboarding(t, ctx, store, snapshotID, revisionID, name, false)
}

func finishOnboarding(t *testing.T, ctx context.Context, store *PostgresStore, snapshotID, revisionID, name string, pass bool) string {
	t.Helper()
	dataset, err := store.CreateOnboardingDataset(ctx, "admin", "dataset-"+name, name, snapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOnboardingCase(ctx, "admin", "answer-"+name, domain.OnboardingCase{DatasetID: dataset.DatasetID, CaseType: "must_answer", Question: "what applies?", ExpectedRevisionIDs: []string{revisionID}, ExpectedLocatorHints: []string{"policy"}, ExpectedAnswerPoints: []string{"answer"}, ReviewStatus: "reviewed"}); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		if _, err := store.CreateOnboardingCase(ctx, "admin", fmt.Sprintf("refuse-%s-%d", name, index), domain.OnboardingCase{DatasetID: dataset.DatasetID, CaseType: "must_refuse", Question: fmt.Sprintf("out-of-scope-%s-%d", name, index), ReviewStatus: "reviewed"}); err != nil {
			t.Fatal(err)
		}
	}
	frozen, err := store.FreezeOnboardingDataset(ctx, "admin", dataset.DatasetID, "freeze-"+name)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateOnboardingRun(ctx, "admin", dataset.DatasetID, "run-"+name)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimOnboardingRun(ctx, time.Minute); err != nil || !ok {
		t.Fatalf("claim onboarding run: ok=%v err=%v", ok, err)
	}
	if !pass {
		failed, err := store.FailOnboardingRun(ctx, run.RunID, "runtime_error", "forced onboarding failure")
		if err != nil || failed.Status != "failed" {
			t.Fatalf("fail run = %#v err=%v", failed, err)
		}
		return failed.RunID
	}
	results := make([]domain.OnboardingCaseResult, 0, len(frozen.Cases))
	for _, item := range frozen.Cases {
		results = append(results, domain.OnboardingCaseResult{RunID: run.RunID, CaseID: item.CaseID, Passed: true, ObservedAnswer: "ok", ObservedCitations: []domain.Citation{}})
	}
	completed, err := store.CompleteOnboardingRun(ctx, run.RunID, "passed", results, json.RawMessage(`{"gate_passed":true}`))
	if err != nil || completed.Status != "passed" {
		t.Fatalf("complete run = %#v err=%v", completed, err)
	}
	return completed.RunID
}

func assertRetirementBlocked(t *testing.T, ctx context.Context, store *PostgresStore, documentID string) {
	t.Helper()
	readiness, err := store.GetKnowledgeRetirementReadiness(ctx, documentID)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Stage != "blocked" || !containsString(readiness.Blockers, "当前 snapshot 没有通过的 onboarding 验收") {
		t.Fatalf("retirement readiness = %#v", readiness)
	}
	if _, err := store.DisableKnowledgeDocumentAfterReadiness(ctx, documentID, prepublishKey(knowledgeOperationDisableDocument, "disable-"+documentID)); !errors.Is(err, ErrKnowledgeRetirementNotReady) {
		t.Fatalf("disable without passing run = %v", err)
	}
}

func publishEventAcceptance(t *testing.T, ctx context.Context, store *PostgresStore, snapshotID string) (string, string) {
	t.Helper()
	var status, runID sql.NullString
	if err := store.db.QueryRowContext(ctx, `
		SELECT acceptance_status, onboarding_run_id
		FROM knowledge_update_events
		WHERE snapshot_id = $1 AND event_type = 'published'
		ORDER BY occurred_at DESC, event_id DESC
		LIMIT 1`, snapshotID).Scan(&status, &runID); err != nil {
		t.Fatal(err)
	}
	return status.String, runID.String
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
