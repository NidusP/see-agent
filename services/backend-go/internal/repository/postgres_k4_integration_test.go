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

func TestPostgresKnowledgeLifecycleRejectsDisableWithInflightRevision(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("K4_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set K4_TEST_DATABASE_URL to run the isolated Postgres K4 smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("k4_%d", time.Now().UTC().UnixNano())
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
		t.Fatalf("bootstrap K4 store: %v", err)
	}
	defer store.Close()
	versions, err := store.ListSchemaMigrations(ctx)
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	if got, want := strings.Join(versions, ","), "001,002,003,004,005,006,007,008,009,010,011,012,013,014,015,016,017,018,019,020,021,022,023,024"; got != want {
		t.Fatalf("migration versions = %q, want %q", got, want)
	}

	createKey := k4Mutation("create", "sha256:initial")
	initial, err := store.CreateKnowledgeDocumentRevision(ctx,
		domain.KnowledgeDocument{DocumentID: "doc_k4_policy", DisplayName: "Policy", OwnerID: "user_admin"},
		k4Revision("rev_k4_initial", "doc_k4_policy", "sha256:initial"), createKey,
	)
	if err != nil || initial.Revision.Status != "queued" || initial.Delivery.Status != "pending" {
		t.Fatalf("create initial = %#v err=%v", initial, err)
	}
	if _, found, err := store.GetActivePublishedKnowledgeSnapshot(ctx); err != nil || found {
		t.Fatalf("upload must not publish snapshot: found=%v err=%v", found, err)
	}
	repeat, err := store.CreateKnowledgeDocumentRevision(ctx,
		domain.KnowledgeDocument{DocumentID: "doc_k4_policy", DisplayName: "Policy", OwnerID: "user_admin"},
		k4Revision("rev_k4_initial", "doc_k4_policy", "sha256:initial"), createKey,
	)
	if err != nil || repeat.Delivery.DeliveryID != initial.Delivery.DeliveryID {
		t.Fatalf("idempotent create = %#v err=%v", repeat, err)
	}
	if _, _, err := store.GetKnowledgeMutation(ctx, k4Mutation("create", "sha256:different")); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("idempotency mismatch error = %v", err)
	}

	if _, err := store.MarkKnowledgeRevisionReady(ctx, initial.Revision.RevisionID, "sha256:initial-chunks", "bge-m3"); err != nil {
		t.Fatalf("mark initial ready: %v", err)
	}
	published, err := store.PublishKnowledgeRevisionLifecycle(ctx, initial.Revision.RevisionID, k4Mutation("publish-initial", initial.Revision.RevisionID))
	if err != nil || published.Snapshot == nil {
		t.Fatalf("publish initial = %#v err=%v", published, err)
	}
	activeBefore, found, err := store.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil || !found || activeBefore.SnapshotID != published.Snapshot.SnapshotID {
		t.Fatalf("active initial snapshot = %#v found=%v err=%v", activeBefore, found, err)
	}

	replacement, err := store.CreateKnowledgeReplacementRevision(ctx, k4Revision("rev_k4_replacement", "doc_k4_policy", "sha256:replacement"), k4Mutation("replace", "sha256:replacement"))
	if err != nil || replacement.Revision.Status != "queued" {
		t.Fatalf("create replacement = %#v err=%v", replacement, err)
	}
	if _, err := store.DisableKnowledgeDocument(ctx, "doc_k4_policy", k4Mutation("disable-inflight", "doc_k4_policy")); !errors.Is(err, ErrDocumentHasInflightRevision) {
		t.Fatalf("disable inflight error = %v, want ErrDocumentHasInflightRevision", err)
	}
	detail, err := store.GetKnowledgeDocument(ctx, "doc_k4_policy")
	if err != nil {
		t.Fatalf("get detail after rejected disable: %v", err)
	}
	if detail.Document.Status != "active" || detail.Revisions[0].Revision.Status != "queued" {
		t.Fatalf("rejected disable changed lifecycle state: %#v", detail)
	}
	activeAfter, found, err := store.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil || !found || activeAfter.SnapshotID != activeBefore.SnapshotID {
		t.Fatalf("rejected disable changed active snapshot: %#v found=%v err=%v", activeAfter, found, err)
	}

	if _, err := store.MarkKnowledgeRevisionReady(ctx, replacement.Revision.RevisionID, "sha256:replacement-chunks", "bge-m3"); err != nil {
		t.Fatalf("mark replacement ready: %v", err)
	}
	if _, err := store.PublishKnowledgeRevisionLifecycle(ctx, replacement.Revision.RevisionID, k4Mutation("publish-replacement", replacement.Revision.RevisionID)); err != nil {
		t.Fatalf("publish replacement: %v", err)
	}
	disabled, err := store.DisableKnowledgeDocument(ctx, "doc_k4_policy", k4Mutation("disable-ready", "doc_k4_policy"))
	if err != nil || disabled.Document.Status != "disabled" || disabled.Snapshot != nil {
		t.Fatalf("disable final document = %#v err=%v", disabled, err)
	}
	if _, found, err := store.GetActivePublishedKnowledgeSnapshot(ctx); err != nil || found {
		t.Fatalf("last document disable must clear active pointer without empty snapshot: found=%v err=%v", found, err)
	}
	deleted, err := store.DeleteKnowledgeDocument(ctx, "doc_k4_policy", k4Mutation("delete", "doc_k4_policy"))
	if err != nil || deleted.Document.Status != "deleted" {
		t.Fatalf("delete disabled document = %#v err=%v", deleted, err)
	}
}

func k4Mutation(key, requestHash string) domain.KnowledgeMutationKey {
	return domain.KnowledgeMutationKey{ActorID: "user_admin", Operation: knowledgeOperationCreateDocument, Key: key, RequestHash: requestHash}
}

func k4Revision(revisionID, documentID, checksum string) domain.KnowledgeRevision {
	return domain.KnowledgeRevision{
		RevisionID: revisionID, DocumentID: documentID, Filename: revisionID + ".md", MIMEType: "text/markdown", SourceFormat: "md",
		ObjectURI: "s3://knowledge-docs/" + revisionID + ".md", ChecksumSHA256: checksum, ParseVersion: "parse_v1", IndexVersion: "index_v1",
	}
}
