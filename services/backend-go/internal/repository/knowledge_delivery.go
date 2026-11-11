package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/taskqueue"
)

var (
	ErrKnowledgeDeliveryNotFound         = errors.New("knowledge delivery not found")
	ErrKnowledgeDeliveryNotDispatchable  = errors.New("knowledge delivery is not dispatchable")
	ErrKnowledgeDeliveryIdentityMismatch = errors.New("knowledge delivery identity mismatch")
	ErrKnowledgeDeliveryLeaseActive      = errors.New("knowledge delivery lease is active")
)

const revisionIndexRequestedTopic = "knowledge.revision.index_requested"

// RelayKnowledgeRevisionOutbox projects the authoritative outbox into the K3
// stream. XADD happens before the database transition: a process crash after
// XADD merely repeats the same outbox-idempotent stream add on the next relay.
func RelayKnowledgeRevisionOutbox(ctx context.Context, store KnowledgeDeliveryStore, stream taskqueue.RevisionStream, limit int) (int, error) {
	if stream == nil {
		return 0, errors.New("revision stream is required")
	}
	deliveries, err := store.ListPendingKnowledgeRevisionDeliveries(ctx, limit)
	if err != nil {
		return 0, err
	}
	streamed := 0
	for _, delivery := range deliveries {
		messageID, err := stream.Add(ctx, taskqueue.RevisionStreamEvent{
			OutboxID:       delivery.DeliveryID,
			DeliveryID:     delivery.DeliveryID,
			DocumentID:     delivery.DocumentID,
			RevisionID:     delivery.RevisionID,
			ChecksumSHA256: delivery.Revision.ChecksumSHA256,
			Attempt:        delivery.AttemptCount,
		})
		if err != nil {
			return streamed, fmt.Errorf("stream delivery %s: %w", delivery.DeliveryID, err)
		}
		if _, err := store.MarkKnowledgeDeliveryStreamed(ctx, delivery.DeliveryID, messageID); err != nil {
			return streamed, fmt.Errorf("record streamed delivery %s: %w", delivery.DeliveryID, err)
		}
		streamed++
	}
	return streamed, nil
}

func (s *PostgresStore) ListPendingKnowledgeRevisionDeliveries(ctx context.Context, limit int) ([]KnowledgeDelivery, error) {
	if limit < 1 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, knowledgeDeliverySelect+`
		WHERE o.topic = $1
		  AND o.status IN ('pending', 'retry')
		  AND o.available_at <= NOW()
		ORDER BY o.available_at ASC, o.created_at ASC
		LIMIT $2`, revisionIndexRequestedTopic, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending knowledge deliveries: %w", err)
	}
	defer rows.Close()
	deliveries := []KnowledgeDelivery{}
	for rows.Next() {
		delivery, err := scanKnowledgeDelivery(rows)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func (s *PostgresStore) MarkKnowledgeDeliveryStreamed(ctx context.Context, deliveryID, streamMessageID string) (KnowledgeDelivery, error) {
	deliveryID = strings.TrimSpace(deliveryID)
	streamMessageID = strings.TrimSpace(streamMessageID)
	if deliveryID == "" || streamMessageID == "" {
		return KnowledgeDelivery{}, fmt.Errorf("%w: delivery_id and stream message id are required", ErrKnowledgeDeliveryIdentityMismatch)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeDelivery{}, err
	}
	defer tx.Rollback()
	delivery, exists, err := knowledgeDeliveryTx(ctx, tx, deliveryID, true)
	if err != nil {
		return KnowledgeDelivery{}, err
	}
	if !exists {
		return KnowledgeDelivery{}, fmt.Errorf("%w: %s", ErrKnowledgeDeliveryNotFound, deliveryID)
	}
	if delivery.Status == "streamed" && delivery.StreamMessageID == streamMessageID {
		if err := tx.Commit(); err != nil {
			return KnowledgeDelivery{}, err
		}
		return delivery, nil
	}
	if delivery.Status != "pending" && delivery.Status != "retry" {
		return KnowledgeDelivery{}, fmt.Errorf("%w: %s is %s", ErrKnowledgeDeliveryNotDispatchable, deliveryID, delivery.Status)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_outbox
		SET status = 'streamed', stream_message_id = $2, lease_until = NULL, updated_at = NOW()
		WHERE delivery_id = $1`, deliveryID, streamMessageID); err != nil {
		return KnowledgeDelivery{}, err
	}
	delivery, _, err = knowledgeDeliveryTx(ctx, tx, deliveryID, false)
	if err != nil {
		return KnowledgeDelivery{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeDelivery{}, err
	}
	return delivery, nil
}

func (s *PostgresStore) ClaimKnowledgeRevisionDelivery(ctx context.Context, deliveryID, streamMessageID string, leaseFor time.Duration) (KnowledgeDelivery, bool, error) {
	if leaseFor <= 0 {
		return KnowledgeDelivery{}, false, errors.New("knowledge delivery lease duration must be positive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeDelivery{}, false, err
	}
	defer tx.Rollback()
	delivery, exists, err := knowledgeDeliveryTx(ctx, tx, deliveryID, true)
	if err != nil {
		return KnowledgeDelivery{}, false, err
	}
	if !exists {
		return KnowledgeDelivery{}, false, fmt.Errorf("%w: %s", ErrKnowledgeDeliveryNotFound, deliveryID)
	}
	if delivery.StreamMessageID != streamMessageID {
		if err := tx.Commit(); err != nil {
			return KnowledgeDelivery{}, false, err
		}
		return delivery, false, nil
	}
	if delivery.Status == "delivered" || delivery.Status == "dead_letter" || delivery.Status == "pending" || delivery.Status == "retry" {
		if err := tx.Commit(); err != nil {
			return KnowledgeDelivery{}, false, err
		}
		return delivery, false, nil
	}
	if delivery.Status != "streamed" && delivery.Status != "indexing" {
		return KnowledgeDelivery{}, false, fmt.Errorf("%w: %s is %s", ErrKnowledgeDeliveryNotDispatchable, deliveryID, delivery.Status)
	}
	now := time.Now().UTC()
	if delivery.Status == "indexing" && delivery.LeaseUntil != nil && delivery.LeaseUntil.After(now) {
		return KnowledgeDelivery{}, false, fmt.Errorf("%w: %s", ErrKnowledgeDeliveryLeaseActive, deliveryID)
	}

	revision, exists, err := knowledgeRevisionTx(ctx, tx, delivery.RevisionID, true)
	if err != nil {
		return KnowledgeDelivery{}, false, err
	}
	if !exists || revision.DocumentID != delivery.DocumentID || revision.ChecksumSHA256 == "" {
		return KnowledgeDelivery{}, false, fmt.Errorf("%w: outbox revision identity", ErrKnowledgeDeliveryIdentityMismatch)
	}
	switch revision.Status {
	case "queued":
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_revisions SET status = 'indexing', updated_at = NOW() WHERE revision_id = $1`, revision.RevisionID); err != nil {
			return KnowledgeDelivery{}, false, err
		}
	case "indexing":
		// A reclaimed pending message safely resumes the same immutable revision.
	case "ready", "published", "superseded":
		// K3 can encounter an outbox event created before a process crash after
		// the Runtime writeback. It is already safe and must not be re-indexed.
		if _, err := tx.ExecContext(ctx, `
			UPDATE knowledge_outbox
			SET status = 'delivered', lease_until = NULL, completed_at = COALESCE(completed_at, NOW()), updated_at = NOW()
			WHERE delivery_id = $1`, deliveryID); err != nil {
			return KnowledgeDelivery{}, false, err
		}
		delivery, _, err = knowledgeDeliveryTx(ctx, tx, deliveryID, false)
		if err != nil {
			return KnowledgeDelivery{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return KnowledgeDelivery{}, false, err
		}
		return delivery, false, nil
	default:
		return KnowledgeDelivery{}, false, fmt.Errorf("%w: revision %s is %s", ErrKnowledgeDeliveryNotDispatchable, revision.RevisionID, revision.Status)
	}
	leaseUntil := now.Add(leaseFor)
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_outbox
		SET status = 'indexing', attempt_count = attempt_count + 1, lease_until = $2, updated_at = NOW()
		WHERE delivery_id = $1`, deliveryID, leaseUntil); err != nil {
		return KnowledgeDelivery{}, false, err
	}
	delivery, _, err = knowledgeDeliveryTx(ctx, tx, deliveryID, false)
	if err != nil {
		return KnowledgeDelivery{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeDelivery{}, false, err
	}
	return delivery, true, nil
}

func (s *PostgresStore) CompleteKnowledgeRevisionDelivery(ctx context.Context, streamMessageID string, result KnowledgeDeliveryResult) (KnowledgeDelivery, error) {
	if err := validateKnowledgeDeliveryResult(result); err != nil {
		return KnowledgeDelivery{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeDelivery{}, err
	}
	defer tx.Rollback()
	delivery, exists, err := knowledgeDeliveryTx(ctx, tx, result.DeliveryID, true)
	if err != nil {
		return KnowledgeDelivery{}, err
	}
	if !exists {
		return KnowledgeDelivery{}, fmt.Errorf("%w: %s", ErrKnowledgeDeliveryNotFound, result.DeliveryID)
	}
	if delivery.StreamMessageID != strings.TrimSpace(streamMessageID) || delivery.DocumentID != result.DocumentID || delivery.RevisionID != result.RevisionID || delivery.Revision.ChecksumSHA256 != result.ChecksumSHA256 {
		return KnowledgeDelivery{}, fmt.Errorf("%w: runtime result does not match claimed delivery", ErrKnowledgeDeliveryIdentityMismatch)
	}
	if delivery.Status == "delivered" {
		if err := tx.Commit(); err != nil {
			return KnowledgeDelivery{}, err
		}
		return delivery, nil
	}
	if delivery.Status != "indexing" || delivery.Revision.Status != "indexing" {
		return KnowledgeDelivery{}, fmt.Errorf("%w: delivery=%s revision=%s", ErrKnowledgeDeliveryNotDispatchable, delivery.Status, delivery.Revision.Status)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_revisions
		SET status = 'ready', chunk_manifest_sha256 = $2, embedding_model = $3, updated_at = NOW()
		WHERE revision_id = $1`, result.RevisionID, result.ChunkManifestSHA256, result.EmbeddingModel); err != nil {
		return KnowledgeDelivery{}, err
	}
	if err := insertKnowledgeOutboxTx(ctx, tx, "knowledge.revision.ready", delivery.DocumentID, delivery.RevisionID, "", map[string]string{
		"document_id": delivery.DocumentID,
		"revision_id": delivery.RevisionID,
	}); err != nil {
		return KnowledgeDelivery{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_outbox
		SET status = 'delivered', lease_until = NULL, last_error = NULL, completed_at = NOW(), updated_at = NOW()
		WHERE delivery_id = $1`, delivery.DeliveryID); err != nil {
		return KnowledgeDelivery{}, err
	}
	delivery, _, err = knowledgeDeliveryTx(ctx, tx, delivery.DeliveryID, false)
	if err != nil {
		return KnowledgeDelivery{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeDelivery{}, err
	}
	return delivery, nil
}

func (s *PostgresStore) ScheduleKnowledgeRevisionDeliveryRetry(ctx context.Context, deliveryID, streamMessageID, failure string, availableAt time.Time) (KnowledgeDelivery, error) {
	return s.finishKnowledgeRevisionDeliveryFailure(ctx, deliveryID, streamMessageID, failure, "retry", availableAt)
}

func (s *PostgresStore) DeadLetterKnowledgeRevisionDelivery(ctx context.Context, deliveryID, streamMessageID, failure string) (KnowledgeDelivery, error) {
	return s.finishKnowledgeRevisionDeliveryFailure(ctx, deliveryID, streamMessageID, failure, "dead_letter", time.Now().UTC())
}

func (s *PostgresStore) finishKnowledgeRevisionDeliveryFailure(ctx context.Context, deliveryID, streamMessageID, failure, targetStatus string, availableAt time.Time) (KnowledgeDelivery, error) {
	deliveryID = strings.TrimSpace(deliveryID)
	streamMessageID = strings.TrimSpace(streamMessageID)
	failure = strings.TrimSpace(failure)
	if deliveryID == "" || streamMessageID == "" || failure == "" {
		return KnowledgeDelivery{}, fmt.Errorf("%w: delivery_id, stream message id and failure are required", ErrKnowledgeDeliveryIdentityMismatch)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgeDelivery{}, err
	}
	defer tx.Rollback()
	delivery, exists, err := knowledgeDeliveryTx(ctx, tx, deliveryID, true)
	if err != nil {
		return KnowledgeDelivery{}, err
	}
	if !exists {
		return KnowledgeDelivery{}, fmt.Errorf("%w: %s", ErrKnowledgeDeliveryNotFound, deliveryID)
	}
	if delivery.StreamMessageID != streamMessageID {
		return KnowledgeDelivery{}, fmt.Errorf("%w: stale stream message", ErrKnowledgeDeliveryIdentityMismatch)
	}
	if delivery.Status == targetStatus {
		if err := tx.Commit(); err != nil {
			return KnowledgeDelivery{}, err
		}
		return delivery, nil
	}
	if delivery.Status != "indexing" {
		return KnowledgeDelivery{}, fmt.Errorf("%w: %s is %s", ErrKnowledgeDeliveryNotDispatchable, deliveryID, delivery.Status)
	}
	if targetStatus == "dead_letter" {
		revision, exists, err := knowledgeRevisionTx(ctx, tx, delivery.RevisionID, true)
		if err != nil {
			return KnowledgeDelivery{}, err
		}
		if !exists || revision.DocumentID != delivery.DocumentID || revision.Status != "indexing" {
			return KnowledgeDelivery{}, fmt.Errorf("%w: cannot fail revision", ErrKnowledgeDeliveryNotDispatchable)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_revisions SET status = 'failed', updated_at = NOW() WHERE revision_id = $1`, revision.RevisionID); err != nil {
			return KnowledgeDelivery{}, err
		}
	}
	completedAt := "NULL"
	if targetStatus == "dead_letter" {
		completedAt = "NOW()"
	}
	query := `UPDATE knowledge_outbox SET status = $2, lease_until = NULL, last_error = $3, available_at = $4, updated_at = NOW(), completed_at = ` + completedAt + ` WHERE delivery_id = $1`
	if _, err := tx.ExecContext(ctx, query, deliveryID, targetStatus, failure, availableAt.UTC()); err != nil {
		return KnowledgeDelivery{}, err
	}
	delivery, _, err = knowledgeDeliveryTx(ctx, tx, deliveryID, false)
	if err != nil {
		return KnowledgeDelivery{}, err
	}
	if err := tx.Commit(); err != nil {
		return KnowledgeDelivery{}, err
	}
	return delivery, nil
}

const knowledgeDeliverySelect = `
	SELECT
		o.delivery_id, o.topic, o.document_id, o.revision_id, o.snapshot_id, o.status, o.attempt_count,
		o.available_at, o.stream_message_id, o.lease_until, o.last_error, o.completed_at,
		r.revision_id, r.document_id, r.legacy_file_id, r.filename, r.mime_type, r.source_format, r.object_uri,
		r.checksum_sha256, r.parse_version, r.index_version, r.embedding_model, r.effective_at, r.chunk_manifest_sha256,
		r.status, r.created_at, r.updated_at
	FROM knowledge_outbox o
	JOIN knowledge_revisions r ON r.revision_id = o.revision_id`

func knowledgeDeliveryTx(ctx context.Context, tx *sql.Tx, deliveryID string, lock bool) (KnowledgeDelivery, bool, error) {
	query := knowledgeDeliverySelect + ` WHERE o.delivery_id = $1`
	if lock {
		query += ` FOR UPDATE OF o, r`
	}
	delivery, err := scanKnowledgeDelivery(tx.QueryRowContext(ctx, query, deliveryID))
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeDelivery{}, false, nil
	}
	return delivery, err == nil, err
}

func scanKnowledgeDelivery(row scanner) (KnowledgeDelivery, error) {
	var delivery KnowledgeDelivery
	var documentID, revisionID, snapshotID, streamMessageID, lastError sql.NullString
	var leaseUntil, completedAt sql.NullTime
	var legacyFileID, checksum, embeddingModel, manifest sql.NullString
	var effectiveAt sql.NullTime
	var revisionCreatedAt, revisionUpdatedAt time.Time
	err := row.Scan(
		&delivery.DeliveryID, &delivery.Topic, &documentID, &revisionID, &snapshotID, &delivery.Status, &delivery.AttemptCount,
		&delivery.AvailableAt, &streamMessageID, &leaseUntil, &lastError, &completedAt,
		&delivery.Revision.RevisionID, &delivery.Revision.DocumentID, &legacyFileID, &delivery.Revision.Filename, &delivery.Revision.MIMEType,
		&delivery.Revision.SourceFormat, &delivery.Revision.ObjectURI, &checksum, &delivery.Revision.ParseVersion, &delivery.Revision.IndexVersion,
		&embeddingModel, &effectiveAt, &manifest, &delivery.Revision.Status, &revisionCreatedAt, &revisionUpdatedAt,
	)
	if err != nil {
		return KnowledgeDelivery{}, err
	}
	delivery.DocumentID = nullableString(documentID)
	delivery.RevisionID = nullableString(revisionID)
	delivery.SnapshotID = nullableString(snapshotID)
	delivery.StreamMessageID = nullableString(streamMessageID)
	delivery.LastError = nullableString(lastError)
	delivery.AvailableAt = delivery.AvailableAt.UTC()
	if leaseUntil.Valid {
		value := leaseUntil.Time.UTC()
		delivery.LeaseUntil = &value
	}
	if completedAt.Valid {
		value := completedAt.Time.UTC()
		delivery.CompletedAt = &value
	}
	delivery.Revision.LegacyFileID = nullableString(legacyFileID)
	delivery.Revision.ChecksumSHA256 = nullableString(checksum)
	delivery.Revision.EmbeddingModel = nullableString(embeddingModel)
	delivery.Revision.ChunkManifestSHA256 = nullableString(manifest)
	if effectiveAt.Valid {
		value := effectiveAt.Time.UTC().Format(time.RFC3339)
		delivery.Revision.EffectiveAt = &value
	}
	delivery.Revision.CreatedAt = revisionCreatedAt.UTC().Format(time.RFC3339)
	delivery.Revision.UpdatedAt = revisionUpdatedAt.UTC().Format(time.RFC3339)
	return delivery, nil
}

func validateKnowledgeDeliveryResult(result KnowledgeDeliveryResult) error {
	if strings.TrimSpace(result.DeliveryID) == "" || strings.TrimSpace(result.DocumentID) == "" || strings.TrimSpace(result.RevisionID) == "" || strings.TrimSpace(result.ChecksumSHA256) == "" || strings.TrimSpace(result.ChunkManifestSHA256) == "" || strings.TrimSpace(result.EmbeddingModel) == "" {
		return fmt.Errorf("%w: delivery, document, revision, checksum, manifest and embedding model are required", ErrKnowledgeDeliveryIdentityMismatch)
	}
	return nil
}

var _ KnowledgeDeliveryStore = (*PostgresStore)(nil)
