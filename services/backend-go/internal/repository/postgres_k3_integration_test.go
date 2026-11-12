package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/taskqueue"
)

func TestPostgresKnowledgeDeliveryLifecycle(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("K3_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set K3_TEST_DATABASE_URL to run the isolated Postgres K3 smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()
	if err := adminDB.PingContext(ctx); err != nil {
		t.Fatalf("ping admin database: %v", err)
	}
	schema := fmt.Sprintf("k3_%d", time.Now().UTC().UnixNano())
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
		t.Fatalf("bootstrap isolated store: %v", err)
	}
	defer store.Close()
	versions, err := store.ListSchemaMigrations(ctx)
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	if got, want := strings.Join(versions, ","), "001,002,003,004,005,006,007,008,009,010,011,012,013,014,015,016,017,018,019,020,021,022,023,024,025,026"; got != want {
		t.Fatalf("migration versions = %q, want %q", got, want)
	}

	activeRevision := k3ReadyRevision(t, ctx, store, "active", "sha256:active")
	activeSnapshot, err := store.PublishKnowledgeRevision(ctx, activeRevision.RevisionID, "admin", "k3-active")
	if err != nil {
		t.Fatalf("publish active revision: %v", err)
	}

	stream := &k3RecordingStream{}
	success := k3QueuedRevision(t, ctx, store, "success", "sha256:success")
	if streamed, err := RelayKnowledgeRevisionOutbox(ctx, store, stream, 10); err != nil || streamed != 2 {
		t.Fatalf("relay success revision: streamed=%d err=%v", streamed, err)
	}
	activeMessage := stream.find(t, activeRevision.RevisionID)
	if _, claimed, err := store.ClaimKnowledgeRevisionDelivery(ctx, activeMessage.DeliveryID, activeMessage.StreamMessageID, time.Second); err != nil || claimed {
		t.Fatalf("claim stale ready delivery: claimed=%v err=%v", claimed, err)
	}
	successMessage := stream.find(t, success.RevisionID)
	claimed, ok, err := store.ClaimKnowledgeRevisionDelivery(ctx, successMessage.DeliveryID, successMessage.StreamMessageID, time.Second)
	if err != nil || !ok || claimed.Status != "indexing" || claimed.AttemptCount != 1 {
		t.Fatalf("claim success delivery = %#v ok=%v err=%v", claimed, ok, err)
	}
	completed, err := store.CompleteKnowledgeRevisionDelivery(ctx, successMessage.StreamMessageID, KnowledgeDeliveryResult{
		DeliveryID:          claimed.DeliveryID,
		DocumentID:          claimed.DocumentID,
		RevisionID:          claimed.RevisionID,
		ChecksumSHA256:      claimed.Revision.ChecksumSHA256,
		ChunkManifestSHA256: "sha256:success-chunks",
		EmbeddingModel:      "bge-m3",
	})
	if err != nil || completed.Status != "delivered" || completed.Revision.Status != "ready" {
		t.Fatalf("complete success delivery = %#v err=%v", completed, err)
	}

	failure := k3QueuedRevision(t, ctx, store, "failure", "sha256:failure")
	stream.messages = nil
	if streamed, err := RelayKnowledgeRevisionOutbox(ctx, store, stream, 10); err != nil || streamed != 1 {
		t.Fatalf("relay failure revision: streamed=%d err=%v", streamed, err)
	}
	failureMessage := stream.find(t, failure.RevisionID)
	claimed, ok, err = store.ClaimKnowledgeRevisionDelivery(ctx, failureMessage.DeliveryID, failureMessage.StreamMessageID, time.Second)
	if err != nil || !ok {
		t.Fatalf("claim failure delivery: %#v ok=%v err=%v", claimed, ok, err)
	}
	_, err = store.CompleteKnowledgeRevisionDelivery(ctx, failureMessage.StreamMessageID, KnowledgeDeliveryResult{
		DeliveryID:          claimed.DeliveryID,
		DocumentID:          claimed.DocumentID,
		RevisionID:          claimed.RevisionID,
		ChecksumSHA256:      "sha256:wrong",
		ChunkManifestSHA256: "sha256:wrong-chunks",
		EmbeddingModel:      "bge-m3",
	})
	if !errors.Is(err, ErrKnowledgeDeliveryIdentityMismatch) {
		t.Fatalf("complete mismatched identity error = %v", err)
	}
	dead, err := store.DeadLetterKnowledgeRevisionDelivery(ctx, claimed.DeliveryID, failureMessage.StreamMessageID, "runtime identity mismatch")
	if err != nil || dead.Status != "dead_letter" || dead.Revision.Status != "failed" {
		t.Fatalf("dead-letter delivery = %#v err=%v", dead, err)
	}

	retry := k3QueuedRevision(t, ctx, store, "retry", "sha256:retry")
	stream.messages = nil
	if streamed, err := RelayKnowledgeRevisionOutbox(ctx, store, stream, 10); err != nil || streamed != 1 {
		t.Fatalf("relay retry revision: streamed=%d err=%v", streamed, err)
	}
	retryMessage := stream.find(t, retry.RevisionID)
	claimed, ok, err = store.ClaimKnowledgeRevisionDelivery(ctx, retryMessage.DeliveryID, retryMessage.StreamMessageID, time.Second)
	if err != nil || !ok || claimed.AttemptCount != 1 {
		t.Fatalf("claim retry delivery = %#v ok=%v err=%v", claimed, ok, err)
	}
	if _, err := store.ScheduleKnowledgeRevisionDeliveryRetry(ctx, claimed.DeliveryID, retryMessage.StreamMessageID, "runtime timeout", time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatalf("schedule retry: %v", err)
	}
	stream.messages = nil
	if streamed, err := RelayKnowledgeRevisionOutbox(ctx, store, stream, 10); err != nil || streamed != 1 {
		t.Fatalf("relay scheduled retry: streamed=%d err=%v", streamed, err)
	}
	retryMessage = stream.find(t, retry.RevisionID)
	if retryMessage.Attempt != 1 || retryMessage.StreamMessageID == claimed.StreamMessageID {
		t.Fatalf("retry stream message = %#v, prior=%s", retryMessage, claimed.StreamMessageID)
	}
	active, found, err := store.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil || !found || active.SnapshotID != activeSnapshot.SnapshotID {
		t.Fatalf("active snapshot changed during K3 failure: %#v found=%v err=%v", active, found, err)
	}
}

func k3QueuedRevision(t *testing.T, ctx context.Context, store *PostgresStore, suffix, checksum string) domain.KnowledgeRevision {
	t.Helper()
	document, err := store.CreateKnowledgeDocument(ctx, domain.KnowledgeDocument{
		DocumentID:  "doc_k3_" + suffix,
		DisplayName: "K3 " + suffix,
		OwnerID:     "admin",
	})
	if err != nil {
		t.Fatalf("create document %s: %v", suffix, err)
	}
	revision, err := store.CreateKnowledgeRevision(ctx, domain.KnowledgeRevision{
		RevisionID:     "rev_k3_" + suffix,
		DocumentID:     document.DocumentID,
		Filename:       suffix + ".md",
		MIMEType:       "text/markdown",
		SourceFormat:   "md",
		ObjectURI:      "s3://knowledge-docs/k3/" + suffix + ".md",
		ChecksumSHA256: checksum,
		ParseVersion:   "parse_v1",
		IndexVersion:   "index_v1",
	})
	if err != nil {
		t.Fatalf("create revision %s: %v", suffix, err)
	}
	queued, err := store.QueueKnowledgeRevision(ctx, revision.RevisionID)
	if err != nil {
		t.Fatalf("queue revision %s: %v", suffix, err)
	}
	return queued
}

func k3ReadyRevision(t *testing.T, ctx context.Context, store *PostgresStore, suffix, checksum string) domain.KnowledgeRevision {
	t.Helper()
	queued := k3QueuedRevision(t, ctx, store, suffix, checksum)
	ready, err := store.MarkKnowledgeRevisionReady(ctx, queued.RevisionID, "sha256:"+suffix+"-chunks", "bge-m3")
	if err != nil {
		t.Fatalf("mark ready %s: %v", suffix, err)
	}
	return ready
}

func k3DatabaseURLWithSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse K3_TEST_DATABASE_URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func quoteK3Identifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

type k3RecordingStream struct {
	messages []taskqueue.RevisionStreamMessage
	nextID   int
}

func (*k3RecordingStream) EnsureGroup(context.Context) error { return nil }
func (s *k3RecordingStream) Add(_ context.Context, event taskqueue.RevisionStreamEvent) (string, error) {
	s.nextID++
	messageID := fmt.Sprintf("stream-%d", s.nextID)
	s.messages = append(s.messages, taskqueue.RevisionStreamMessage{StreamMessageID: messageID, RevisionStreamEvent: event})
	return messageID, nil
}
func (*k3RecordingStream) Read(context.Context, int64, time.Duration) ([]taskqueue.RevisionStreamMessage, error) {
	return nil, nil
}
func (*k3RecordingStream) Reclaim(context.Context, time.Duration, int64) ([]taskqueue.RevisionStreamMessage, error) {
	return nil, nil
}
func (*k3RecordingStream) Ack(context.Context, ...string) error { return nil }
func (*k3RecordingStream) DeadLetter(context.Context, taskqueue.RevisionStreamEvent, string, string) (string, error) {
	return "dlq-1", nil
}
func (*k3RecordingStream) Close() error { return nil }
func (s *k3RecordingStream) find(t *testing.T, revisionID string) taskqueue.RevisionStreamMessage {
	t.Helper()
	for _, message := range s.messages {
		if message.RevisionID == revisionID {
			return message
		}
	}
	t.Fatalf("revision %s not found in stream messages %#v", revisionID, s.messages)
	return taskqueue.RevisionStreamMessage{}
}
