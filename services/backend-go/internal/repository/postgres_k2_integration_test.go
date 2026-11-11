package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
)

func TestPostgresKnowledgeEvolution(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("K2_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set K2_TEST_DATABASE_URL to run the isolated Postgres K2 smoke")
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

	schema := fmt.Sprintf("k2_%d", time.Now().UTC().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteIdentifier(schema)); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	defer func() {
		if _, err := adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quoteIdentifier(schema)+` CASCADE`); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	}()

	store, err := NewPostgresStore(ctx, databaseURLWithSearchPath(t, baseURL, schema))
	if err != nil {
		t.Fatalf("bootstrap isolated store: %v", err)
	}
	defer store.Close()

	versions, err := store.ListSchemaMigrations(ctx)
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	if got, want := strings.Join(versions, ","), "001,002,003,004,005,006,007,008,009,010,011,012,013,014,015,016,017,018,019,020,021,022,023,024"; got != want {
		t.Fatalf("migration versions = %q, want %q", got, want)
	}
	legacy, ok, err := store.GetLegacyKnowledgeRevision(ctx, "file_001")
	if err != nil || !ok {
		t.Fatalf("legacy revision = %#v, ok=%v, err=%v", legacy, ok, err)
	}
	if legacy.Status != "legacy_unpublished" || legacy.ChecksumSHA256 != "" {
		t.Fatalf("legacy revision = %#v, want legacy_unpublished without checksum", legacy)
	}
	secondStore, err := NewPostgresStore(ctx, databaseURLWithSearchPath(t, baseURL, schema))
	if err != nil {
		t.Fatalf("repeat isolated bootstrap: %v", err)
	}
	if err := secondStore.Close(); err != nil {
		t.Fatalf("close repeat isolated store: %v", err)
	}
	var legacyCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_revisions WHERE legacy_file_id = 'file_001'`).Scan(&legacyCount); err != nil {
		t.Fatalf("count legacy revisions: %v", err)
	}
	if legacyCount != 1 {
		t.Fatalf("legacy revision count = %d, want 1 after repeat bootstrap", legacyCount)
	}

	first := readyRevision(t, ctx, store, "travel", "001", "sha256:travel-v1")
	firstSnapshot, err := store.PublishKnowledgeRevision(ctx, first.RevisionID, "admin", "publish-first")
	if err != nil {
		t.Fatalf("publish first revision: %v", err)
	}
	if firstSnapshot.Status != "published" || len(firstSnapshot.Members) != 1 {
		t.Fatalf("first snapshot = %#v", firstSnapshot)
	}
	duplicateSnapshot, err := store.PublishKnowledgeRevision(ctx, first.RevisionID, "admin", "publish-first-retry")
	if err != nil {
		t.Fatalf("repeat first publication: %v", err)
	}
	if duplicateSnapshot.SnapshotID != firstSnapshot.SnapshotID {
		t.Fatalf("repeat publication snapshot = %q, want %q", duplicateSnapshot.SnapshotID, firstSnapshot.SnapshotID)
	}

	notReadyDocument := createDocument(t, ctx, store, "not-ready")
	notReady, err := store.CreateKnowledgeRevision(ctx, domain.KnowledgeRevision{
		RevisionID:     "rev_not_ready_001",
		DocumentID:     notReadyDocument.DocumentID,
		Filename:       "not-ready.md",
		MIMEType:       "text/markdown",
		SourceFormat:   "md",
		ObjectURI:      "s3://knowledge-docs/not-ready.md",
		ChecksumSHA256: "sha256:not-ready",
		ParseVersion:   "parse_v2",
		IndexVersion:   "index_v2",
	})
	if err != nil {
		t.Fatalf("create not-ready revision: %v", err)
	}
	if _, err := store.PublishKnowledgeRevision(ctx, notReady.RevisionID, "admin", "publish-not-ready"); !errors.Is(err, ErrRevisionNotReady) {
		t.Fatalf("publish non-ready error = %v, want ErrRevisionNotReady", err)
	}
	active, ok, err := store.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil || !ok || active.SnapshotID != firstSnapshot.SnapshotID {
		t.Fatalf("active snapshot changed after rejected publish: %#v ok=%v err=%v", active, ok, err)
	}

	futureDocument := createDocument(t, ctx, store, "future")
	futureAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	futureRevision, err := store.CreateKnowledgeRevision(ctx, domain.KnowledgeRevision{
		RevisionID:     "rev_future_001",
		DocumentID:     futureDocument.DocumentID,
		Filename:       "future.md",
		MIMEType:       "text/markdown",
		SourceFormat:   "md",
		ObjectURI:      "s3://knowledge-docs/future.md",
		ChecksumSHA256: "sha256:future",
		ParseVersion:   "parse_v2",
		IndexVersion:   "index_v2",
		EffectiveAt:    &futureAt,
	})
	if err != nil {
		t.Fatalf("create future revision: %v", err)
	}
	if _, err := store.QueueKnowledgeRevision(ctx, futureRevision.RevisionID); err != nil {
		t.Fatalf("queue future revision: %v", err)
	}
	if _, err := store.MarkKnowledgeRevisionReady(ctx, futureRevision.RevisionID, "sha256:chunks-future", "bge-m3"); err != nil {
		t.Fatalf("mark future revision ready: %v", err)
	}
	if _, err := store.PublishKnowledgeRevision(ctx, futureRevision.RevisionID, "admin", "publish-future"); !errors.Is(err, ErrRevisionNotEffective) {
		t.Fatalf("future publish error = %v, want ErrRevisionNotEffective", err)
	}
	active, ok, err = store.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil || !ok || active.SnapshotID != firstSnapshot.SnapshotID {
		t.Fatalf("active snapshot changed after future publish: %#v ok=%v err=%v", active, ok, err)
	}

	manifestDocument := createDocument(t, ctx, store, "manifest")
	manifestRevision, err := store.CreateKnowledgeRevision(ctx, domain.KnowledgeRevision{
		RevisionID:     "rev_manifest_001",
		DocumentID:     manifestDocument.DocumentID,
		Filename:       "manifest.md",
		MIMEType:       "text/markdown",
		SourceFormat:   "md",
		ObjectURI:      "s3://knowledge-docs/manifest.md",
		ChecksumSHA256: "sha256:manifest",
		ParseVersion:   "parse_v2",
		IndexVersion:   "index_v2",
	})
	if err != nil {
		t.Fatalf("create manifest revision: %v", err)
	}
	if _, err := store.QueueKnowledgeRevision(ctx, manifestRevision.RevisionID); err != nil {
		t.Fatalf("queue manifest revision: %v", err)
	}
	if _, err := store.MarkKnowledgeRevisionReady(ctx, manifestRevision.RevisionID, "", "bge-m3"); !errors.Is(err, ErrRevisionManifestMismatch) {
		t.Fatalf("empty manifest error = %v, want ErrRevisionManifestMismatch", err)
	}

	rollback := readyRevision(t, ctx, store, "rollback", "001", "sha256:rollback")
	store.publishHook = func(stage string) error {
		if stage == publishHookBeforeCommit {
			return errors.New("forced publish rollback")
		}
		return nil
	}
	if _, err := store.PublishKnowledgeRevision(ctx, rollback.RevisionID, "admin", "publish-rollback"); err == nil {
		t.Fatal("forced publish rollback unexpectedly succeeded")
	}
	store.publishHook = nil
	active, ok, err = store.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil || !ok || active.SnapshotID != firstSnapshot.SnapshotID {
		t.Fatalf("active snapshot changed after rollback: %#v ok=%v err=%v", active, ok, err)
	}

	left := readyRevision(t, ctx, store, "concurrent-left", "001", "sha256:left")
	right := readyRevision(t, ctx, store, "concurrent-right", "001", "sha256:right")
	errs := make(chan error, 2)
	var group sync.WaitGroup
	for _, revisionID := range []string{left.RevisionID, right.RevisionID} {
		group.Add(1)
		go func(id string) {
			defer group.Done()
			_, err := store.PublishKnowledgeRevision(ctx, id, "admin", "publish-concurrent-"+id)
			errs <- err
		}(revisionID)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent publish: %v", err)
		}
	}
	active, ok, err = store.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil || !ok {
		t.Fatalf("read active snapshot after concurrent publish: %#v ok=%v err=%v", active, ok, err)
	}
	if !containsRevision(active.Members, first.RevisionID) || !containsRevision(active.Members, left.RevisionID) || !containsRevision(active.Members, right.RevisionID) {
		t.Fatalf("active members lost a published revision: %#v", active.Members)
	}
	var outboxCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_outbox WHERE topic = 'knowledge.snapshot.published'`).Scan(&outboxCount); err != nil {
		t.Fatalf("count publication outbox rows: %v", err)
	}
	if outboxCount != 3 {
		t.Fatalf("publication outbox count = %d, want 3", outboxCount)
	}
}

func createDocument(t *testing.T, ctx context.Context, store *PostgresStore, suffix string) domain.KnowledgeDocument {
	t.Helper()
	document, err := store.CreateKnowledgeDocument(ctx, domain.KnowledgeDocument{
		DocumentID:  "doc_" + strings.ReplaceAll(suffix, "-", "_"),
		DisplayName: "Document " + suffix,
		OwnerID:     "admin",
	})
	if err != nil {
		t.Fatalf("create document %s: %v", suffix, err)
	}
	return document
}

func readyRevision(t *testing.T, ctx context.Context, store *PostgresStore, documentSuffix, revisionSuffix, checksum string) domain.KnowledgeRevision {
	t.Helper()
	document := createDocument(t, ctx, store, documentSuffix)
	revision, err := store.CreateKnowledgeRevision(ctx, domain.KnowledgeRevision{
		RevisionID:     "rev_" + strings.ReplaceAll(documentSuffix, "-", "_") + "_" + revisionSuffix,
		DocumentID:     document.DocumentID,
		Filename:       documentSuffix + ".md",
		MIMEType:       "text/markdown",
		SourceFormat:   "md",
		ObjectURI:      "s3://knowledge-docs/" + documentSuffix + ".md",
		ChecksumSHA256: checksum,
		ParseVersion:   "parse_v2",
		IndexVersion:   "index_v2",
	})
	if err != nil {
		t.Fatalf("create revision %s: %v", documentSuffix, err)
	}
	if _, err := store.QueueKnowledgeRevision(ctx, revision.RevisionID); err != nil {
		t.Fatalf("queue revision %s: %v", documentSuffix, err)
	}
	ready, err := store.MarkKnowledgeRevisionReady(ctx, revision.RevisionID, "sha256:chunks-"+documentSuffix, "bge-m3")
	if err != nil {
		t.Fatalf("mark revision ready %s: %v", documentSuffix, err)
	}
	return ready
}

func databaseURLWithSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse K2_TEST_DATABASE_URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
