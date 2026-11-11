package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/domain"
)

var (
	ErrKnowledgeDocumentConflict = errors.New("knowledge document conflict")
	ErrKnowledgeRevisionConflict = errors.New("knowledge revision conflict")
	ErrRevisionNotFound          = errors.New("knowledge revision not found")
	ErrRevisionNotReady          = errors.New("knowledge revision not ready")
	ErrRevisionManifestMismatch  = errors.New("knowledge revision manifest mismatch")
	ErrRevisionNotEffective      = errors.New("knowledge revision not effective")
	ErrActiveSnapshotUnavailable = errors.New("active knowledge snapshot unavailable")
)

const publishHookBeforeCommit = "before_publish_commit"

func (s *PostgresStore) CreateKnowledgeDocument(ctx context.Context, document domain.KnowledgeDocument) (domain.KnowledgeDocument, error) {
	document.DocumentID = strings.TrimSpace(document.DocumentID)
	document.DisplayName = strings.TrimSpace(document.DisplayName)
	document.OwnerID = strings.TrimSpace(document.OwnerID)
	if document.DocumentID == "" || document.DisplayName == "" || document.OwnerID == "" {
		return domain.KnowledgeDocument{}, fmt.Errorf("%w: document_id, display_name and owner_id are required", ErrKnowledgeDocumentConflict)
	}
	if document.Status == "" {
		document.Status = "active"
	}
	if document.Status != "active" && document.Status != "disabled" && document.Status != "deleted" {
		return domain.KnowledgeDocument{}, fmt.Errorf("%w: invalid document status %q", ErrKnowledgeDocumentConflict, document.Status)
	}
	now := time.Now().UTC()
	if document.CreatedAt == "" {
		document.CreatedAt = now.Format(time.RFC3339)
	}
	if document.UpdatedAt == "" {
		document.UpdatedAt = document.CreatedAt
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeDocument{}, err
	}
	defer tx.Rollback()
	existing, exists, err := knowledgeDocumentTx(ctx, tx, document.DocumentID, true)
	if err != nil {
		return domain.KnowledgeDocument{}, err
	}
	if exists {
		if existing.DisplayName != document.DisplayName || existing.OwnerID != document.OwnerID {
			return domain.KnowledgeDocument{}, fmt.Errorf("%w: %s", ErrKnowledgeDocumentConflict, document.DocumentID)
		}
		if err := tx.Commit(); err != nil {
			return domain.KnowledgeDocument{}, err
		}
		return existing, nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_documents (document_id, display_name, owner_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		document.DocumentID, document.DisplayName, document.OwnerID, document.Status, document.CreatedAt, document.UpdatedAt); err != nil {
		return domain.KnowledgeDocument{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeDocument{}, err
	}
	return document, nil
}

func (s *PostgresStore) CreateKnowledgeRevision(ctx context.Context, revision domain.KnowledgeRevision) (domain.KnowledgeRevision, error) {
	revision.RevisionID = strings.TrimSpace(revision.RevisionID)
	revision.DocumentID = strings.TrimSpace(revision.DocumentID)
	revision.ChecksumSHA256 = strings.TrimSpace(revision.ChecksumSHA256)
	if revision.RevisionID == "" || revision.DocumentID == "" || revision.ChecksumSHA256 == "" || strings.TrimSpace(revision.ObjectURI) == "" {
		return domain.KnowledgeRevision{}, fmt.Errorf("%w: revision_id, document_id, object_uri and checksum_sha256 are required", ErrKnowledgeRevisionConflict)
	}
	if revision.Status == "" {
		revision.Status = "uploaded"
	}
	if revision.Status != "uploaded" {
		return domain.KnowledgeRevision{}, fmt.Errorf("%w: new revision must start uploaded", ErrKnowledgeRevisionConflict)
	}
	now := time.Now().UTC()
	if revision.CreatedAt == "" {
		revision.CreatedAt = now.Format(time.RFC3339)
	}
	if revision.UpdatedAt == "" {
		revision.UpdatedAt = revision.CreatedAt
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeRevision{}, err
	}
	defer tx.Rollback()
	if _, exists, err := knowledgeDocumentTx(ctx, tx, revision.DocumentID, false); err != nil {
		return domain.KnowledgeRevision{}, err
	} else if !exists {
		return domain.KnowledgeRevision{}, fmt.Errorf("%w: document %s", ErrKnowledgeRevisionConflict, revision.DocumentID)
	}
	existing, exists, err := knowledgeRevisionTx(ctx, tx, revision.RevisionID, true)
	if err != nil {
		return domain.KnowledgeRevision{}, err
	}
	if exists {
		if existing.DocumentID != revision.DocumentID || existing.ChecksumSHA256 != revision.ChecksumSHA256 || existing.ObjectURI != revision.ObjectURI {
			return domain.KnowledgeRevision{}, fmt.Errorf("%w: %s", ErrKnowledgeRevisionConflict, revision.RevisionID)
		}
		if err := tx.Commit(); err != nil {
			return domain.KnowledgeRevision{}, err
		}
		return existing, nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_revisions (
			revision_id, document_id, legacy_file_id, filename, mime_type, source_format, object_uri,
			checksum_sha256, parse_version, index_version, embedding_model, effective_at, chunk_manifest_sha256,
			status, created_at, updated_at
		) VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''), $12, NULLIF($13, ''), $14, $15, $16)`,
		revision.RevisionID, revision.DocumentID, revision.LegacyFileID, revision.Filename, revision.MIMEType, revision.SourceFormat,
		revision.ObjectURI, revision.ChecksumSHA256, revision.ParseVersion, revision.IndexVersion, revision.EmbeddingModel,
		parseTimestamp(revision.EffectiveAt), revision.ChunkManifestSHA256, revision.Status, revision.CreatedAt, revision.UpdatedAt); err != nil {
		return domain.KnowledgeRevision{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeRevision{}, err
	}
	return revision, nil
}

func (s *PostgresStore) QueueKnowledgeRevision(ctx context.Context, revisionID string) (domain.KnowledgeRevision, error) {
	return s.transitionRevision(ctx, revisionID, []string{"uploaded", "failed"}, "queued", func(tx *sql.Tx, revision domain.KnowledgeRevision) error {
		return insertKnowledgeOutboxTx(ctx, tx, "knowledge.revision.index_requested", revision.DocumentID, revision.RevisionID, "", map[string]string{
			"document_id": revision.DocumentID,
			"revision_id": revision.RevisionID,
		})
	})
}

func (s *PostgresStore) MarkKnowledgeRevisionReady(ctx context.Context, revisionID, chunkManifestSHA256, embeddingModel string) (domain.KnowledgeRevision, error) {
	chunkManifestSHA256 = strings.TrimSpace(chunkManifestSHA256)
	if chunkManifestSHA256 == "" {
		return domain.KnowledgeRevision{}, fmt.Errorf("%w: chunk manifest is required", ErrRevisionManifestMismatch)
	}
	return s.transitionRevision(ctx, revisionID, []string{"queued", "indexing"}, "ready", func(tx *sql.Tx, revision domain.KnowledgeRevision) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE knowledge_revisions
			SET chunk_manifest_sha256 = $2, embedding_model = NULLIF($3, ''), updated_at = NOW()
			WHERE revision_id = $1`, revision.RevisionID, chunkManifestSHA256, embeddingModel); err != nil {
			return err
		}
		return insertKnowledgeOutboxTx(ctx, tx, "knowledge.revision.ready", revision.DocumentID, revision.RevisionID, "", map[string]string{
			"document_id": revision.DocumentID,
			"revision_id": revision.RevisionID,
		})
	})
}

func (s *PostgresStore) transitionRevision(ctx context.Context, revisionID string, allowed []string, target string, sideEffect func(*sql.Tx, domain.KnowledgeRevision) error) (domain.KnowledgeRevision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeRevision{}, err
	}
	defer tx.Rollback()
	revision, exists, err := knowledgeRevisionTx(ctx, tx, revisionID, true)
	if err != nil {
		return domain.KnowledgeRevision{}, err
	}
	if !exists {
		return domain.KnowledgeRevision{}, fmt.Errorf("%w: %s", ErrRevisionNotFound, revisionID)
	}
	if !containsStatus(allowed, revision.Status) {
		return domain.KnowledgeRevision{}, fmt.Errorf("%w: %s is %s", ErrRevisionNotReady, revisionID, revision.Status)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_revisions SET status = $2, updated_at = NOW() WHERE revision_id = $1`, revisionID, target); err != nil {
		return domain.KnowledgeRevision{}, err
	}
	if err := sideEffect(tx, revision); err != nil {
		return domain.KnowledgeRevision{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeRevision{}, err
	}
	return s.getKnowledgeRevision(ctx, revisionID)
}

func (s *PostgresStore) PublishKnowledgeRevision(ctx context.Context, revisionID, actorID, correlationID string) (domain.KnowledgeSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	defer tx.Rollback()

	var activeSnapshotID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT snapshot_id FROM knowledge_active_snapshot WHERE singleton = TRUE FOR UPDATE`).Scan(&activeSnapshotID); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	revision, exists, err := knowledgeRevisionTx(ctx, tx, revisionID, true)
	if err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	if !exists {
		return domain.KnowledgeSnapshot{}, fmt.Errorf("%w: %s", ErrRevisionNotFound, revisionID)
	}
	if revision.Status == "published" && activeSnapshotID.Valid {
		snapshot, err := snapshotTx(ctx, tx, activeSnapshotID.String)
		if err != nil {
			return domain.KnowledgeSnapshot{}, err
		}
		if containsRevision(snapshot.Members, revisionID) {
			if err := tx.Commit(); err != nil {
				return domain.KnowledgeSnapshot{}, err
			}
			return snapshot, nil
		}
	}
	if revision.Status != "ready" {
		return domain.KnowledgeSnapshot{}, fmt.Errorf("%w: %s is %s", ErrRevisionNotReady, revisionID, revision.Status)
	}
	if revision.ChecksumSHA256 == "" || revision.ChunkManifestSHA256 == "" {
		return domain.KnowledgeSnapshot{}, fmt.Errorf("%w: %s", ErrRevisionManifestMismatch, revisionID)
	}
	if revision.EffectiveAt != nil {
		effectiveAt, err := time.Parse(time.RFC3339, *revision.EffectiveAt)
		if err != nil {
			return domain.KnowledgeSnapshot{}, fmt.Errorf("%w: invalid effective_at", ErrRevisionNotEffective)
		}
		if effectiveAt.After(time.Now().UTC()) {
			return domain.KnowledgeSnapshot{}, fmt.Errorf("%w: %s", ErrRevisionNotEffective, revisionID)
		}
	}

	members := []domain.KnowledgeSnapshotMember{}
	parentSnapshotID := ""
	if activeSnapshotID.Valid {
		parentSnapshotID = activeSnapshotID.String
		active, err := snapshotTx(ctx, tx, activeSnapshotID.String)
		if err != nil {
			return domain.KnowledgeSnapshot{}, err
		}
		if active.Status != "published" {
			return domain.KnowledgeSnapshot{}, fmt.Errorf("%w: pointer targets %s", ErrActiveSnapshotUnavailable, active.Status)
		}
		for _, member := range active.Members {
			if member.DocumentID != revision.DocumentID {
				members = append(members, member)
			}
		}
	}
	members = append(members, domain.KnowledgeSnapshotMember{
		RevisionID:     revision.RevisionID,
		DocumentID:     revision.DocumentID,
		ChecksumSHA256: revision.ChecksumSHA256,
	})
	manifestSHA256 := snapshotManifestSHA256(members)
	nextSnapshot, err := nextCounterTx(ctx, tx, "snapshot")
	if err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	snapshotID := fmt.Sprintf("snap_%03d", nextSnapshot)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_snapshots (snapshot_id, parent_snapshot_id, status, manifest_sha256, created_at)
		VALUES ($1, NULLIF($2, ''), 'building', $3, $4)`, snapshotID, parentSnapshotID, manifestSHA256, now); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO knowledge_snapshot_members (snapshot_id, revision_id, checksum_sha256)
			VALUES ($1, $2, $3)`, snapshotID, member.RevisionID, member.ChecksumSHA256); err != nil {
			return domain.KnowledgeSnapshot{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_revisions
		SET status = 'superseded', updated_at = NOW()
		WHERE document_id = $1 AND revision_id <> $2 AND status = 'published'`, revision.DocumentID, revision.RevisionID); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_revisions SET status = 'published', updated_at = NOW() WHERE revision_id = $1`, revision.RevisionID); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	if activeSnapshotID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_snapshots SET status = 'retired' WHERE snapshot_id = $1`, activeSnapshotID.String); err != nil {
			return domain.KnowledgeSnapshot{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_snapshots SET status = 'published', published_at = $2 WHERE snapshot_id = $1`, snapshotID, now); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_active_snapshot SET snapshot_id = $1, lock_version = lock_version + 1 WHERE singleton = TRUE`, snapshotID); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	if err := insertKnowledgeUpdateEventTx(ctx, tx, "published", revision.DocumentID, revision.RevisionID, snapshotID, actorID, correlationID); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	if err := insertKnowledgeOutboxTx(ctx, tx, "knowledge.snapshot.published", revision.DocumentID, revision.RevisionID, snapshotID, map[string]string{
		"snapshot_id":     snapshotID,
		"revision_id":     revision.RevisionID,
		"manifest_sha256": manifestSHA256,
	}); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	if s.publishHook != nil {
		if err := s.publishHook(publishHookBeforeCommit); err != nil {
			return domain.KnowledgeSnapshot{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	return domain.KnowledgeSnapshot{
		SnapshotID:       snapshotID,
		ParentSnapshotID: parentSnapshotID,
		Status:           "published",
		ManifestSHA256:   manifestSHA256,
		CreatedAt:        now,
		PublishedAt:      stringPointer(now),
		Members:          members,
	}, nil
}

func (s *PostgresStore) GetActivePublishedKnowledgeSnapshot(ctx context.Context) (domain.KnowledgeSnapshot, bool, error) {
	var snapshotID sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT snapshot_id FROM knowledge_active_snapshot WHERE singleton = TRUE`).Scan(&snapshotID); err != nil {
		return domain.KnowledgeSnapshot{}, false, err
	}
	if !snapshotID.Valid {
		return domain.KnowledgeSnapshot{}, false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeSnapshot{}, false, err
	}
	defer tx.Rollback()
	snapshot, err := snapshotTx(ctx, tx, snapshotID.String)
	if err != nil {
		return domain.KnowledgeSnapshot{}, false, err
	}
	if snapshot.Status != "published" {
		return domain.KnowledgeSnapshot{}, false, fmt.Errorf("%w: %s", ErrActiveSnapshotUnavailable, snapshotID.String)
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func (s *PostgresStore) ListSchemaMigrations(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := []string{}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s *PostgresStore) GetLegacyKnowledgeRevision(ctx context.Context, fileID string) (domain.KnowledgeRevision, bool, error) {
	row := s.db.QueryRowContext(ctx, knowledgeRevisionSelect+` WHERE legacy_file_id = $1`, fileID)
	revision, err := scanKnowledgeRevision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeRevision{}, false, nil
	}
	if err != nil {
		return domain.KnowledgeRevision{}, false, err
	}
	return revision, true, nil
}

func (s *PostgresStore) getKnowledgeRevision(ctx context.Context, revisionID string) (domain.KnowledgeRevision, error) {
	row := s.db.QueryRowContext(ctx, knowledgeRevisionSelect+` WHERE revision_id = $1`, revisionID)
	return scanKnowledgeRevision(row)
}

func knowledgeDocumentTx(ctx context.Context, tx *sql.Tx, documentID string, lock bool) (domain.KnowledgeDocument, bool, error) {
	query := `SELECT document_id, display_name, owner_id, status, created_at, updated_at FROM knowledge_documents WHERE document_id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	var document domain.KnowledgeDocument
	var createdAt, updatedAt time.Time
	err := tx.QueryRowContext(ctx, query, documentID).Scan(&document.DocumentID, &document.DisplayName, &document.OwnerID, &document.Status, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeDocument{}, false, nil
	}
	if err != nil {
		return domain.KnowledgeDocument{}, false, err
	}
	document.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	document.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return document, true, nil
}

const knowledgeRevisionSelect = `
	SELECT revision_id, document_id, legacy_file_id, filename, mime_type, source_format, object_uri,
		checksum_sha256, parse_version, index_version, embedding_model, effective_at, chunk_manifest_sha256,
		status, created_at, updated_at
	FROM knowledge_revisions`

func knowledgeRevisionTx(ctx context.Context, tx *sql.Tx, revisionID string, lock bool) (domain.KnowledgeRevision, bool, error) {
	query := knowledgeRevisionSelect + ` WHERE revision_id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	revision, err := scanKnowledgeRevision(tx.QueryRowContext(ctx, query, revisionID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeRevision{}, false, nil
	}
	return revision, err == nil, err
}

func scanKnowledgeRevision(row scanner) (domain.KnowledgeRevision, error) {
	var revision domain.KnowledgeRevision
	var legacyFileID, checksum, embeddingModel, manifest sql.NullString
	var effectiveAt sql.NullTime
	var createdAt, updatedAt time.Time
	err := row.Scan(
		&revision.RevisionID, &revision.DocumentID, &legacyFileID, &revision.Filename, &revision.MIMEType, &revision.SourceFormat,
		&revision.ObjectURI, &checksum, &revision.ParseVersion, &revision.IndexVersion, &embeddingModel, &effectiveAt, &manifest,
		&revision.Status, &createdAt, &updatedAt,
	)
	if err != nil {
		return domain.KnowledgeRevision{}, err
	}
	revision.LegacyFileID = nullableString(legacyFileID)
	revision.ChecksumSHA256 = nullableString(checksum)
	revision.EmbeddingModel = nullableString(embeddingModel)
	revision.ChunkManifestSHA256 = nullableString(manifest)
	if effectiveAt.Valid {
		value := effectiveAt.Time.UTC().Format(time.RFC3339)
		revision.EffectiveAt = &value
	}
	revision.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	revision.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return revision, nil
}

func snapshotTx(ctx context.Context, tx *sql.Tx, snapshotID string) (domain.KnowledgeSnapshot, error) {
	var snapshot domain.KnowledgeSnapshot
	var parentID sql.NullString
	var publishedAt sql.NullTime
	var createdAt time.Time
	if err := tx.QueryRowContext(ctx, `
		SELECT snapshot_id, parent_snapshot_id, status, manifest_sha256, created_at, published_at
		FROM knowledge_snapshots WHERE snapshot_id = $1`, snapshotID).Scan(
		&snapshot.SnapshotID, &parentID, &snapshot.Status, &snapshot.ManifestSHA256, &createdAt, &publishedAt,
	); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	snapshot.ParentSnapshotID = nullableString(parentID)
	snapshot.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	if publishedAt.Valid {
		value := publishedAt.Time.UTC().Format(time.RFC3339)
		snapshot.PublishedAt = &value
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT m.snapshot_id, m.revision_id, r.document_id, m.checksum_sha256
		FROM knowledge_snapshot_members m
		JOIN knowledge_revisions r ON r.revision_id = m.revision_id
		WHERE m.snapshot_id = $1
		ORDER BY m.revision_id`, snapshotID)
	if err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var member domain.KnowledgeSnapshotMember
		if err := rows.Scan(&member.SnapshotID, &member.RevisionID, &member.DocumentID, &member.ChecksumSHA256); err != nil {
			return domain.KnowledgeSnapshot{}, err
		}
		snapshot.Members = append(snapshot.Members, member)
	}
	if err := rows.Err(); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	return snapshot, nil
}

func insertKnowledgeUpdateEventTx(ctx context.Context, tx *sql.Tx, eventType, documentID, revisionID, snapshotID, actorID, correlationID string) error {
	next, err := nextCounterTx(ctx, tx, "event")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO knowledge_update_events (event_id, event_type, document_id, revision_id, snapshot_id, actor_id, correlation_id, occurred_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''), NOW())`,
		fmt.Sprintf("event_%03d", next), eventType, documentID, revisionID, snapshotID, actorID, correlationID)
	return err
}

func insertKnowledgeOutboxTx(ctx context.Context, tx *sql.Tx, topic, documentID, revisionID, snapshotID string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	next, err := nextCounterTx(ctx, tx, "delivery")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO knowledge_outbox (delivery_id, topic, document_id, revision_id, snapshot_id, payload, status, available_at, created_at, updated_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6, 'pending', NOW(), NOW(), NOW())`,
		fmt.Sprintf("delivery_%03d", next), topic, documentID, revisionID, snapshotID, encoded)
	return err
}

func snapshotManifestSHA256(members []domain.KnowledgeSnapshotMember) string {
	ordered := append([]domain.KnowledgeSnapshotMember(nil), members...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].RevisionID < ordered[j].RevisionID })
	builder := strings.Builder{}
	for _, member := range ordered {
		fmt.Fprintf(&builder, "%s:%s\n", member.RevisionID, member.ChecksumSHA256)
	}
	digest := sha256.Sum256([]byte(builder.String()))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func containsStatus(statuses []string, target string) bool {
	for _, status := range statuses {
		if status == target {
			return true
		}
	}
	return false
}

func containsRevision(members []domain.KnowledgeSnapshotMember, revisionID string) bool {
	for _, member := range members {
		if member.RevisionID == revisionID {
			return true
		}
	}
	return false
}

func parseTimestamp(value *string) any {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return *value
}

func nullableString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func stringPointer(value string) *string {
	return &value
}

var _ KnowledgeEvolutionStore = (*PostgresStore)(nil)
