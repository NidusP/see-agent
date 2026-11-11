package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
)

func TestPostgresK6FreshnessAndRevalidationLifecycle(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("K6_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set K6_TEST_DATABASE_URL to run the isolated Postgres K6 smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("k6_%d", time.Now().UTC().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteK3Identifier(schema)); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	defer func() {
		if _, err := adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quoteK3Identifier(schema)+` CASCADE`); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	}()
	store, err := NewPostgresStore(ctx, k3DatabaseURLWithSearchPath(t, baseURL, schema))
	if err != nil {
		t.Fatalf("bootstrap K6 store: %v", err)
	}
	defer store.Close()
	versions, err := store.ListSchemaMigrations(ctx)
	if err != nil || strings.Join(versions, ",") != "001,002,003,004,005,006,007,008,009,010,011,012,013,014,015,016,017,018,019,020,021,022,023,024" {
		t.Fatalf("migration versions = %#v err=%v", versions, err)
	}

	revisionA1 := k6ReadyRevision(t, ctx, store, "doc_k6_a", "rev_k6_a1")
	if _, err := store.PublishKnowledgeRevision(ctx, revisionA1.RevisionID, "admin", "k6-a1"); err != nil {
		t.Fatalf("publish A1: %v", err)
	}
	revisionB1 := k6ReadyRevision(t, ctx, store, "doc_k6_b", "rev_k6_b1")
	if _, err := store.PublishKnowledgeRevision(ctx, revisionB1.RevisionID, "admin", "k6-b1"); err != nil {
		t.Fatalf("publish B1: %v", err)
	}
	scopeBefore, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		t.Fatalf("resolve pre-replacement scope: %v", err)
	}
	conversation := store.CreateConversation("K6 evidence")
	_, answerA, _, err := store.AppendScopedRuntimeExchange(ctx, conversation.ConversationID, "", "question A", scopeBefore, k6RuntimeResponse(conversation.ConversationID, "msg_k6_a", scopeBefore, revisionA1, "old answer A"))
	if err != nil {
		t.Fatalf("append answer A: %v", err)
	}
	_, answerB, _, err := store.AppendScopedRuntimeExchange(ctx, conversation.ConversationID, "", "question B", scopeBefore, k6RuntimeResponse(conversation.ConversationID, "msg_k6_b", scopeBefore, revisionB1, "old answer B"))
	if err != nil {
		t.Fatalf("append answer B: %v", err)
	}

	revisionA2 := k6ReadyRevision(t, ctx, store, "doc_k6_a", "rev_k6_a2")
	if _, err := store.PublishKnowledgeRevision(ctx, revisionA2.RevisionID, "admin", "k6-a2"); err != nil {
		t.Fatalf("publish A2: %v", err)
	}
	k6RunAllFreshnessScans(t, ctx, store)
	_, messages, ok := store.GetConversation(conversation.ConversationID)
	if !ok {
		t.Fatal("conversation disappeared")
	}
	if k6Message(t, messages, answerA.MessageID).FreshnessStatus != "possibly_stale" || k6Message(t, messages, answerB.MessageID).FreshnessStatus != "current" {
		t.Fatalf("replacement scan must only mark intersecting answer: %#v", messages)
	}

	disableKey := domain.KnowledgeMutationKey{ActorID: "admin", Operation: KnowledgeOperationDisableDocument(), Key: "k6-disable-b", RequestHash: "sha256:k6-disable-b"}
	if _, err := store.DisableKnowledgeDocument(ctx, "doc_k6_b", disableKey); err != nil {
		t.Fatalf("disable B: %v", err)
	}
	k6RunAllFreshnessScans(t, ctx, store)
	_, messages, _ = store.GetConversation(conversation.ConversationID)
	if k6Message(t, messages, answerB.MessageID).FreshnessStatus != "possibly_stale" {
		t.Fatalf("disable scan must mark all answers citing B: %#v", messages)
	}
	deleteKey := domain.KnowledgeMutationKey{ActorID: "admin", Operation: KnowledgeOperationDeleteDocument(), Key: "k6-delete-b", RequestHash: "sha256:k6-delete-b"}
	if _, err := store.DeleteKnowledgeDocument(ctx, "doc_k6_b", deleteKey); err != nil {
		t.Fatalf("delete B: %v", err)
	}
	k6RunAllFreshnessScans(t, ctx, store)

	latestScope, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		t.Fatalf("resolve latest scope: %v", err)
	}
	request := domain.RevalidationRequest{RequesterID: "user", ConversationID: conversation.ConversationID, OriginalMessageID: answerA.MessageID, IdempotencyKey: "k6-revalidate-a", Reason: "policy changed", LatestScope: latestScope}
	run, err := store.CreateRevalidationRun(ctx, request)
	if err != nil {
		t.Fatalf("create revalidation: %v", err)
	}
	replayed, err := store.CreateRevalidationRun(ctx, request)
	if err != nil || replayed.RevalidationID != run.RevalidationID {
		t.Fatalf("revalidation replay = %#v err=%v", replayed, err)
	}
	request.Reason = "different request"
	if _, err := store.CreateRevalidationRun(ctx, request); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("revalidation idempotency conflict = %v", err)
	}
	claimed, claimedOK, err := store.ClaimRevalidationRun(ctx, time.Second)
	if err != nil || !claimedOK || claimed.RevalidationID != run.RevalidationID {
		t.Fatalf("claim revalidation = %#v ok=%v err=%v", claimed, claimedOK, err)
	}
	completed, err := store.CompleteRevalidationRun(ctx, run.RevalidationID, k6RuntimeResponse(conversation.ConversationID, run.RevalidationID, latestScope, revisionA2, "new independent result"), "needs_update")
	if err != nil || completed.Result == nil || completed.Result.Content != "new independent result" {
		t.Fatalf("complete revalidation = %#v err=%v", completed, err)
	}
	_, messages, _ = store.GetConversation(conversation.ConversationID)
	if old := k6Message(t, messages, answerA.MessageID); old.Content != "old answer A" || old.FreshnessStatus != "revalidated" {
		t.Fatalf("revalidation rewrote historical answer: %#v", old)
	}

	badRun, err := store.CreateRevalidationRun(ctx, domain.RevalidationRequest{RequesterID: "user", ConversationID: conversation.ConversationID, OriginalMessageID: answerB.MessageID, IdempotencyKey: "k6-revalidate-b", LatestScope: latestScope})
	if err != nil {
		t.Fatalf("create bad-scope revalidation: %v", err)
	}
	if _, ok, err := store.ClaimRevalidationRun(ctx, time.Second); err != nil || !ok {
		t.Fatalf("claim bad-scope run: ok=%v err=%v", ok, err)
	}
	badResponse := k6RuntimeResponse(conversation.ConversationID, badRun.RevalidationID, latestScope, revisionA2, "must not persist")
	badResponse.Citations[0].DocumentID = "doc_wrong"
	if _, err := store.CompleteRevalidationRun(ctx, badRun.RevalidationID, badResponse, "still_valid"); !errors.Is(err, ErrKnowledgeSnapshotCitationMismatch) {
		t.Fatalf("scope mismatch completion = %v", err)
	}
	if _, err := store.FailRevalidationRun(ctx, badRun.RevalidationID, "snapshot_scope_violation", "wrong document"); err != nil {
		t.Fatalf("fail bad-scope run: %v", err)
	}
	badRead, found, err := store.GetRevalidationRun(ctx, conversation.ConversationID, badRun.RevalidationID)
	if err != nil || !found || badRead.Status != "failed" || badRead.Result != nil {
		t.Fatalf("bad-scope run must have no result: %#v found=%v err=%v", badRead, found, err)
	}

	revisionA3 := k6ReadyRevision(t, ctx, store, "doc_k6_a", "rev_k6_a3")
	if _, err := store.PublishKnowledgeRevision(ctx, revisionA3.RevisionID, "admin", "k6-a3-retry"); err != nil {
		t.Fatalf("publish A3: %v", err)
	}
	failureEvent, err := store.latestKnowledgeEvent(ctx, "k6-a3-retry")
	if err != nil {
		t.Fatalf("read failure event: %v", err)
	}
	if err := store.EnsureKnowledgeFreshnessScans(ctx); err != nil {
		t.Fatalf("ensure failure scan: %v", err)
	}
	for attempt := 1; attempt <= maxKnowledgeFreshnessScanAttempts; attempt++ {
		scan, _, claimed, err := store.ClaimKnowledgeFreshnessScan(ctx, time.Second)
		if err != nil || !claimed || scan.EventID != failureEvent.EventID {
			t.Fatalf("claim retry attempt %d = %#v claimed=%v err=%v", attempt, scan, claimed, err)
		}
		if err := store.RetryKnowledgeFreshnessScan(ctx, scan.EventID, "simulated freshness failure", time.Now().UTC().Add(-time.Millisecond)); err != nil {
			t.Fatalf("retry freshness attempt %d: %v", attempt, err)
		}
	}
	page, err := store.ListKnowledgeUpdates(ctx, "doc_k6_a", "", 20)
	if err != nil {
		t.Fatalf("list calendar after failure: %v", err)
	}
	failed := false
	for _, item := range page.Items {
		if item.EventID == failureEvent.EventID && item.FreshnessScan != nil && item.FreshnessScan.Status == "failed" && item.FreshnessScan.LastError == "simulated freshness failure" {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("calendar did not expose failed scan: %#v", page)
	}
}

func k6ReadyRevision(t *testing.T, ctx context.Context, store *PostgresStore, documentID, revisionID string) domain.KnowledgeRevision {
	t.Helper()
	if _, found, err := store.knowledgeDocument(ctx, documentID); err != nil {
		t.Fatalf("find document %s: %v", documentID, err)
	} else if !found {
		if _, err := store.CreateKnowledgeDocument(ctx, domain.KnowledgeDocument{DocumentID: documentID, DisplayName: documentID, OwnerID: "admin"}); err != nil {
			t.Fatalf("create document %s: %v", documentID, err)
		}
	}
	revision, err := store.CreateKnowledgeRevision(ctx, domain.KnowledgeRevision{RevisionID: revisionID, DocumentID: documentID, Filename: revisionID + ".md", MIMEType: "text/markdown", SourceFormat: "md", ObjectURI: "s3://knowledge/" + revisionID, ChecksumSHA256: "sha256:" + revisionID, ParseVersion: "parse_v1", IndexVersion: "index_v1"})
	if err != nil {
		t.Fatalf("create revision %s: %v", revisionID, err)
	}
	if _, err := store.QueueKnowledgeRevision(ctx, revision.RevisionID); err != nil {
		t.Fatalf("queue revision %s: %v", revisionID, err)
	}
	ready, err := store.MarkKnowledgeRevisionReady(ctx, revision.RevisionID, "sha256:chunks-"+revisionID, "bge-m3")
	if err != nil {
		t.Fatalf("ready revision %s: %v", revisionID, err)
	}
	return ready
}

func k6RuntimeResponse(conversationID, messageID string, scope domain.RuntimeKnowledgeScope, revision domain.KnowledgeRevision, content string) domain.ChatRuntimeResponse {
	return domain.ChatRuntimeResponse{RequestID: "req_" + messageID, ConversationID: conversationID, MessageID: messageID, AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: content}, FinishReason: "stop", CompletedAt: time.Now().UTC().Format(time.RFC3339), KnowledgeSnapshotID: scope.SnapshotID, KnowledgeSnapshotManifestHash: scope.ManifestHash, FreshnessStatus: "revalidated", RevalidationOutcome: "needs_update", Citations: []domain.RuntimeCitation{{ChunkID: "chunk_" + revision.RevisionID, FileID: "file_" + revision.RevisionID, DocumentID: revision.DocumentID, RevisionID: revision.RevisionID, ChunkContentSHA256: "sha256:chunk-" + revision.RevisionID, KnowledgeSnapshotID: scope.SnapshotID, Filename: revision.Filename, Rank: 1, Score: 0.99, CitationLabel: "policy", SourceLocator: domain.SourceLocator{SourceFormat: "md", BlockLabel: "policy"}, Snippet: content}}}
}

func k6RunAllFreshnessScans(t *testing.T, ctx context.Context, store *PostgresStore) {
	t.Helper()
	if err := store.EnsureKnowledgeFreshnessScans(ctx); err != nil {
		t.Fatalf("ensure freshness scans: %v", err)
	}
	for {
		scan, _, claimed, err := store.ClaimKnowledgeFreshnessScan(ctx, time.Second)
		if err != nil {
			t.Fatalf("claim freshness scan: %v", err)
		}
		if !claimed {
			return
		}
		if _, err := store.CompleteKnowledgeFreshnessScan(ctx, scan.EventID); err != nil {
			t.Fatalf("complete freshness scan %s: %v", scan.EventID, err)
		}
	}
}

func k6Message(t *testing.T, messages []domain.Message, messageID string) domain.Message {
	t.Helper()
	for _, message := range messages {
		if message.MessageID == messageID {
			return message
		}
	}
	t.Fatalf("message %s not found in %#v", messageID, messages)
	return domain.Message{}
}
