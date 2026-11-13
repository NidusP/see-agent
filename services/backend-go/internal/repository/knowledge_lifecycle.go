package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/domain"
)

var (
	ErrKnowledgeDocumentNotFound    = errors.New("knowledge document not found")
	ErrKnowledgeDocumentNotActive   = errors.New("knowledge document is not active")
	ErrKnowledgeDocumentNotDisabled = errors.New("knowledge document is not disabled")
	ErrKnowledgeRetirementNotReady  = errors.New("knowledge retirement is not ready")
	ErrDocumentHasInflightRevision  = errors.New("knowledge document has an inflight revision")
	ErrIdempotencyKeyReused         = errors.New("idempotency key reused with a different request")
)

const (
	knowledgeOperationCreateDocument   = "knowledge_document_create"
	knowledgeOperationCreateRevision   = "knowledge_revision_create"
	knowledgeOperationPublishRevision  = "knowledge_revision_publish"
	knowledgeOperationPreparePublish   = "knowledge_revision_prepare_publish"
	knowledgeOperationActivateSnapshot = "knowledge_snapshot_activate"
	knowledgeOperationDisableDocument  = "knowledge_document_disable"
	knowledgeOperationDeleteDocument   = "knowledge_document_delete"
)

func KnowledgeOperationCreateDocument() string   { return knowledgeOperationCreateDocument }
func KnowledgeOperationCreateRevision() string   { return knowledgeOperationCreateRevision }
func KnowledgeOperationPublishRevision() string  { return knowledgeOperationPublishRevision }
func KnowledgeOperationPreparePublish() string   { return knowledgeOperationPreparePublish }
func KnowledgeOperationActivateSnapshot() string { return knowledgeOperationActivateSnapshot }
func KnowledgeOperationDisableDocument() string  { return knowledgeOperationDisableDocument }
func KnowledgeOperationDeleteDocument() string   { return knowledgeOperationDeleteDocument }

func (s *PostgresStore) GetKnowledgeMutation(ctx context.Context, key domain.KnowledgeMutationKey) (json.RawMessage, bool, error) {
	if err := validateKnowledgeMutationKey(key); err != nil {
		return nil, false, err
	}
	var requestHash string
	var payload []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT request_hash, response_payload
		FROM knowledge_mutation_idempotency
		WHERE actor_id = $1 AND operation = $2 AND idempotency_key = $3`,
		key.ActorID, key.Operation, key.Key,
	).Scan(&requestHash, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if requestHash != key.RequestHash {
		return nil, false, fmt.Errorf("%w: %s", ErrIdempotencyKeyReused, key.Key)
	}
	return json.RawMessage(payload), true, nil
}

func (s *PostgresStore) CreateKnowledgeDocumentRevision(ctx context.Context, document domain.KnowledgeDocument, revision domain.KnowledgeRevision, key domain.KnowledgeMutationKey) (domain.KnowledgeRevisionUpload, error) {
	if err := validateKnowledgeMutationKey(key); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	if err := validateLifecycleRevision(document, revision, true); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	defer tx.Rollback()

	var replay domain.KnowledgeRevisionUpload
	if found, err := replayKnowledgeMutationTx(ctx, tx, key, &replay); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return domain.KnowledgeRevisionUpload{}, err
		}
		return replay, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	document.Status = "active"
	document.CreatedAt = now
	document.UpdatedAt = now
	revision.Status = "queued"
	revision.CreatedAt = now
	revision.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_documents (document_id, display_name, owner_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		document.DocumentID, document.DisplayName, document.OwnerID, document.Status, document.CreatedAt, document.UpdatedAt,
	); err != nil {
		return domain.KnowledgeRevisionUpload{}, fmt.Errorf("create knowledge document: %w", err)
	}
	if err := insertLegacyLifecycleFileTx(ctx, tx, document, revision); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	if err := insertLifecycleRevisionTx(ctx, tx, revision); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	if _, err := insertLifecycleEventTx(ctx, tx, "uploaded", document.DocumentID, revision.RevisionID, "", key.ActorID, key.Key); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	if _, err := insertLifecycleOutboxTx(ctx, tx, "knowledge.revision.uploaded", document.DocumentID, revision.RevisionID, "", map[string]string{
		"document_id": document.DocumentID,
		"revision_id": revision.RevisionID,
	}); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	delivery, err := insertLifecycleOutboxTx(ctx, tx, revisionIndexRequestedTopic, document.DocumentID, revision.RevisionID, "", map[string]string{
		"document_id": document.DocumentID,
		"revision_id": revision.RevisionID,
	})
	if err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	if err := insertLegacyLifecycleFacadeTx(ctx, tx, document, revision, delivery); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	result := domain.KnowledgeRevisionUpload{Document: document, Revision: revision, Delivery: deliverySummary(delivery)}
	if err := recordKnowledgeMutationTx(ctx, tx, key, result); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	return result, nil
}

func (s *PostgresStore) CreateKnowledgeReplacementRevision(ctx context.Context, revision domain.KnowledgeRevision, key domain.KnowledgeMutationKey) (domain.KnowledgeRevisionUpload, error) {
	if err := validateKnowledgeMutationKey(key); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	if err := validateLifecycleRevision(domain.KnowledgeDocument{}, revision, false); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	defer tx.Rollback()

	var replay domain.KnowledgeRevisionUpload
	if found, err := replayKnowledgeMutationTx(ctx, tx, key, &replay); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return domain.KnowledgeRevisionUpload{}, err
		}
		return replay, nil
	}
	document, exists, err := knowledgeDocumentTx(ctx, tx, revision.DocumentID, true)
	if err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	if !exists {
		return domain.KnowledgeRevisionUpload{}, fmt.Errorf("%w: %s", ErrKnowledgeDocumentNotFound, revision.DocumentID)
	}
	if document.Status != "active" {
		return domain.KnowledgeRevisionUpload{}, fmt.Errorf("%w: %s is %s", ErrKnowledgeDocumentNotActive, document.DocumentID, document.Status)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	revision.Status = "queued"
	revision.CreatedAt = now
	revision.UpdatedAt = now
	if err := insertLifecycleRevisionTx(ctx, tx, revision); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	if _, err := insertLifecycleEventTx(ctx, tx, "replaced", document.DocumentID, revision.RevisionID, "", key.ActorID, key.Key); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	delivery, err := insertLifecycleOutboxTx(ctx, tx, revisionIndexRequestedTopic, document.DocumentID, revision.RevisionID, "", map[string]string{
		"document_id": document.DocumentID,
		"revision_id": revision.RevisionID,
	})
	if err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	result := domain.KnowledgeRevisionUpload{Document: document, Revision: revision, Delivery: deliverySummary(delivery)}
	if err := recordKnowledgeMutationTx(ctx, tx, key, result); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeRevisionUpload{}, err
	}
	return result, nil
}

func (s *PostgresStore) ListKnowledgeDocuments(ctx context.Context, status, cursor string, limit int) (domain.KnowledgeDocumentPage, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	status = strings.TrimSpace(status)
	if status != "" && status != "active" && status != "disabled" && status != "deleted" {
		return domain.KnowledgeDocumentPage{}, fmt.Errorf("%w: invalid document status", ErrKnowledgeDocumentConflict)
	}
	query := `SELECT document_id, display_name, owner_id, status, created_at, updated_at FROM knowledge_documents`
	args := []any{}
	clauses := []string{}
	if status != "" {
		args = append(args, status)
		clauses = append(clauses, fmt.Sprintf("status = $%d", len(args)))
	}
	if strings.TrimSpace(cursor) != "" {
		args = append(args, cursor)
		clauses = append(clauses, fmt.Sprintf("document_id < $%d", len(args)))
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY document_id DESC LIMIT $%d", len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return domain.KnowledgeDocumentPage{}, err
	}
	defer rows.Close()
	items := []domain.KnowledgeDocumentListItem{}
	for rows.Next() {
		document, err := scanKnowledgeDocument(rows)
		if err != nil {
			return domain.KnowledgeDocumentPage{}, err
		}
		item := domain.KnowledgeDocumentListItem{KnowledgeDocument: document}
		if revision, found, err := s.latestKnowledgeRevision(ctx, document.DocumentID); err != nil {
			return domain.KnowledgeDocumentPage{}, err
		} else if found {
			item.CurrentRevision = &revision
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.KnowledgeDocumentPage{}, err
	}
	page := domain.KnowledgeDocumentPage{Items: items}
	if len(page.Items) > limit {
		page.NextCursor = page.Items[limit-1].DocumentID
		page.Items = page.Items[:limit]
	}
	countQuery := `SELECT COUNT(*) FROM knowledge_documents`
	countArgs := []any{}
	if status != "" {
		countQuery += ` WHERE status = $1`
		countArgs = append(countArgs, status)
	}
	if err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&page.Total); err != nil {
		return domain.KnowledgeDocumentPage{}, err
	}
	if err := s.attachDocumentPublicationState(ctx, page.Items); err != nil {
		return domain.KnowledgeDocumentPage{}, err
	}
	return page, nil
}

func (s *PostgresStore) GetKnowledgeDocument(ctx context.Context, documentID string) (domain.KnowledgeDocumentDetail, error) {
	document, found, err := s.knowledgeDocument(ctx, documentID)
	if err != nil {
		return domain.KnowledgeDocumentDetail{}, err
	}
	if !found {
		return domain.KnowledgeDocumentDetail{}, fmt.Errorf("%w: %s", ErrKnowledgeDocumentNotFound, documentID)
	}
	detail := domain.KnowledgeDocumentDetail{Document: document, Revisions: []domain.KnowledgeRevisionDetail{}}
	active, hasActive, err := s.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil {
		return domain.KnowledgeDocumentDetail{}, err
	}
	if hasActive {
		detail.ActiveSnapshotID = active.SnapshotID
	}
	rows, err := s.db.QueryContext(ctx, knowledgeRevisionSelect+` WHERE document_id = $1 ORDER BY created_at DESC, revision_id DESC`, documentID)
	if err != nil {
		return domain.KnowledgeDocumentDetail{}, err
	}
	defer rows.Close()
	for rows.Next() {
		revision, err := scanKnowledgeRevision(rows)
		if err != nil {
			return domain.KnowledgeDocumentDetail{}, err
		}
		entry := domain.KnowledgeRevisionDetail{Revision: revision, InActiveSnapshot: containsRevision(active.Members, revision.RevisionID)}
		if delivery, ok, err := s.revisionDeliverySummary(ctx, revision.RevisionID); err != nil {
			return domain.KnowledgeDocumentDetail{}, err
		} else if ok {
			entry.Delivery = &delivery
		}
		detail.Revisions = append(detail.Revisions, entry)
	}
	if err := rows.Err(); err != nil {
		return domain.KnowledgeDocumentDetail{}, err
	}
	if hasActive {
		if err := s.attachSnapshotAcceptance(ctx, &active); err != nil {
			return domain.KnowledgeDocumentDetail{}, err
		}
		for _, member := range active.Members {
			if member.DocumentID == document.DocumentID {
				detail.Acceptance = active.Acceptance
				break
			}
		}
	}
	pending, err := s.pendingSnapshotForDocument(ctx, document.DocumentID)
	if err != nil {
		return domain.KnowledgeDocumentDetail{}, err
	}
	detail.PendingSnapshot = pending
	return detail, nil
}

func (s *PostgresStore) GetKnowledgeRetirementReadiness(ctx context.Context, documentID string) (domain.KnowledgeRetirementReadiness, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeRetirementReadiness{}, err
	}
	defer tx.Rollback()
	document, found, err := knowledgeDocumentTx(ctx, tx, documentID, false)
	if err != nil {
		return domain.KnowledgeRetirementReadiness{}, err
	}
	if !found {
		return domain.KnowledgeRetirementReadiness{}, fmt.Errorf("%w: %s", ErrKnowledgeDocumentNotFound, documentID)
	}
	readiness, err := retirementReadinessTx(ctx, tx, document)
	if err != nil {
		return readiness, err
	}
	if err := tx.Commit(); err != nil {
		return readiness, err
	}
	return readiness, nil
}

func retirementReadinessTx(ctx context.Context, tx *sql.Tx, document domain.KnowledgeDocument) (domain.KnowledgeRetirementReadiness, error) {
	result := domain.KnowledgeRetirementReadiness{DocumentID: document.DocumentID, Stage: "blocked", Blockers: []string{}}
	var activeID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT snapshot_id FROM knowledge_active_snapshot WHERE singleton = TRUE FOR UPDATE`).Scan(&activeID); err != nil {
		return result, err
	}
	if !activeID.Valid || strings.TrimSpace(activeID.String) == "" {
		result.Blockers = append(result.Blockers, "没有包含新知识的 active snapshot")
		return result, nil
	}
	snapshot, err := snapshotTx(ctx, tx, activeID.String)
	if err != nil {
		return result, err
	}
	result.ActiveSnapshotID = snapshot.SnapshotID
	result.ActiveSnapshotManifestHash = snapshot.ManifestSHA256
	containsTarget, otherMembers := false, 0
	for _, member := range snapshot.Members {
		if member.DocumentID == document.DocumentID {
			containsTarget = true
		} else {
			otherMembers++
		}
	}
	var runID string
	err = tx.QueryRowContext(ctx, `SELECT run_id FROM onboarding_runs WHERE status='passed' AND knowledge_snapshot_id=$1 AND knowledge_snapshot_manifest_hash=$2 ORDER BY completed_at DESC,run_id DESC LIMIT 1`, snapshot.SnapshotID, snapshot.ManifestSHA256).Scan(&runID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if err == nil {
		result.PassingOnboardingRunID = runID
	}
	if document.Status == "active" {
		if !containsTarget {
			result.Blockers = append(result.Blockers, "目标文档不在当前 active snapshot")
		}
		if otherMembers == 0 {
			result.Blockers = append(result.Blockers, "当前 snapshot 没有可替代的新知识")
		}
		if runID == "" {
			result.Blockers = append(result.Blockers, "当前 snapshot 没有通过的 onboarding 验收")
		}
		if len(result.Blockers) == 0 {
			result.Stage = "ready_to_disable"
		}
		return result, nil
	}
	if document.Status == "disabled" {
		if containsTarget {
			result.Blockers = append(result.Blockers, "当前 active snapshot 仍包含目标文档")
		}
		if len(snapshot.Members) == 0 {
			result.Blockers = append(result.Blockers, "当前 snapshot 没有新知识")
		}
		if runID == "" {
			result.Blockers = append(result.Blockers, "new-only snapshot 没有通过的 onboarding 验收")
		}
		if len(result.Blockers) == 0 {
			result.Stage = "ready_to_delete"
		}
		return result, nil
	}
	result.Blockers = append(result.Blockers, "目标文档必须是 active 或 disabled")
	return result, nil
}

func (s *PostgresStore) PublishKnowledgeRevisionLifecycle(ctx context.Context, revisionID string, key domain.KnowledgeMutationKey, acknowledgeUnvalidated bool) (domain.KnowledgeLifecycleMutation, error) {
	return s.publishOrActivateKnowledgeRevision(ctx, revisionID, key, acknowledgeUnvalidated)
}

func (s *PostgresStore) DisableKnowledgeDocument(ctx context.Context, documentID string, key domain.KnowledgeMutationKey) (domain.KnowledgeLifecycleMutation, error) {
	return s.disableKnowledgeDocument(ctx, documentID, key, false)
}

func (s *PostgresStore) DisableKnowledgeDocumentAfterReadiness(ctx context.Context, documentID string, key domain.KnowledgeMutationKey) (domain.KnowledgeLifecycleMutation, error) {
	return s.disableKnowledgeDocument(ctx, documentID, key, true)
}

func (s *PostgresStore) disableKnowledgeDocument(ctx context.Context, documentID string, key domain.KnowledgeMutationKey, requireReadiness bool) (domain.KnowledgeLifecycleMutation, error) {
	if err := validateKnowledgeMutationKey(key); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	defer tx.Rollback()
	var replay domain.KnowledgeLifecycleMutation
	if found, err := replayKnowledgeMutationTx(ctx, tx, key, &replay); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
		return replay, nil
	}
	document, found, err := knowledgeDocumentTx(ctx, tx, documentID, true)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if !found {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("%w: %s", ErrKnowledgeDocumentNotFound, documentID)
	}
	if document.Status != "active" {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("%w: %s is %s", ErrKnowledgeDocumentNotActive, documentID, document.Status)
	}
	if requireReadiness {
		readiness, err := retirementReadinessTx(ctx, tx, document)
		if err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
		if readiness.Stage != "ready_to_disable" {
			return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("%w: %s", ErrKnowledgeRetirementNotReady, strings.Join(readiness.Blockers, "; "))
		}
	}
	var inflightRevisionID string
	err = tx.QueryRowContext(ctx, `
		SELECT revision_id
		FROM knowledge_revisions
		WHERE document_id = $1 AND status IN ('uploaded', 'queued', 'indexing')
		ORDER BY created_at DESC, revision_id DESC
		LIMIT 1
		FOR UPDATE`, documentID).Scan(&inflightRevisionID)
	if err == nil {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("%w: %s", ErrDocumentHasInflightRevision, inflightRevisionID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	var activeID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT snapshot_id FROM knowledge_active_snapshot WHERE singleton = TRUE FOR UPDATE`).Scan(&activeID); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	var successor *domain.KnowledgeSnapshot
	if activeID.Valid {
		active, err := snapshotTx(ctx, tx, activeID.String)
		if err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
		members := make([]domain.KnowledgeSnapshotMember, 0, len(active.Members))
		for _, member := range active.Members {
			if member.DocumentID != document.DocumentID {
				members = append(members, member)
			}
		}
		if len(members) == 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE knowledge_snapshots SET status = 'retired' WHERE snapshot_id = $1`, active.SnapshotID); err != nil {
				return domain.KnowledgeLifecycleMutation{}, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE knowledge_active_snapshot SET snapshot_id = NULL, lock_version = lock_version + 1 WHERE singleton = TRUE`); err != nil {
				return domain.KnowledgeLifecycleMutation{}, err
			}
		} else if len(members) != len(active.Members) {
			next, err := createSuccessorSnapshotTx(ctx, tx, active, members)
			if err != nil {
				return domain.KnowledgeLifecycleMutation{}, err
			}
			successor = &next
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_documents SET status = 'disabled', updated_at = NOW() WHERE document_id = $1`, documentID); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_revisions
		SET status = 'disabled', updated_at = NOW()
		WHERE document_id = $1 AND status IN ('ready', 'published', 'superseded')`, documentID); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	document.Status = "disabled"
	document.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	event, err := insertLifecycleEventTx(ctx, tx, "disabled", documentID, "", snapshotID(successor), key.ActorID, key.Key)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if _, err := insertLifecycleOutboxTx(ctx, tx, "knowledge.document.disabled", documentID, "", snapshotID(successor), map[string]string{"document_id": documentID}); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	result := domain.KnowledgeLifecycleMutation{Document: document, UpdateEvent: eventSummary(event)}
	if successor != nil {
		result.Snapshot = snapshotSummary(*successor)
	}
	if err := recordKnowledgeMutationTx(ctx, tx, key, result); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	return result, nil
}

func (s *PostgresStore) DeleteKnowledgeDocument(ctx context.Context, documentID string, key domain.KnowledgeMutationKey) (domain.KnowledgeLifecycleMutation, error) {
	return s.deleteKnowledgeDocument(ctx, documentID, key, false)
}

func (s *PostgresStore) DeleteKnowledgeDocumentAfterReadiness(ctx context.Context, documentID string, key domain.KnowledgeMutationKey) (domain.KnowledgeLifecycleMutation, error) {
	return s.deleteKnowledgeDocument(ctx, documentID, key, true)
}

func (s *PostgresStore) deleteKnowledgeDocument(ctx context.Context, documentID string, key domain.KnowledgeMutationKey, requireReadiness bool) (domain.KnowledgeLifecycleMutation, error) {
	if err := validateKnowledgeMutationKey(key); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	defer tx.Rollback()
	var replay domain.KnowledgeLifecycleMutation
	if found, err := replayKnowledgeMutationTx(ctx, tx, key, &replay); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
		return replay, nil
	}
	document, found, err := knowledgeDocumentTx(ctx, tx, documentID, true)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if !found {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("%w: %s", ErrKnowledgeDocumentNotFound, documentID)
	}
	if document.Status != "disabled" {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("%w: %s is %s", ErrKnowledgeDocumentNotDisabled, documentID, document.Status)
	}
	if requireReadiness {
		readiness, err := retirementReadinessTx(ctx, tx, document)
		if err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
		if readiness.Stage != "ready_to_delete" {
			return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("%w: %s", ErrKnowledgeRetirementNotReady, strings.Join(readiness.Blockers, "; "))
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_documents SET status = 'deleted', updated_at = NOW() WHERE document_id = $1`, documentID); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_revisions
		SET status = 'deleted', updated_at = NOW()
		WHERE document_id = $1 AND status <> 'deleted'`, documentID); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	document.Status = "deleted"
	document.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	event, err := insertLifecycleEventTx(ctx, tx, "deleted", documentID, "", "", key.ActorID, key.Key)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	result := domain.KnowledgeLifecycleMutation{Document: document, UpdateEvent: eventSummary(event)}
	if err := recordKnowledgeMutationTx(ctx, tx, key, result); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	return result, nil
}

func validateKnowledgeMutationKey(key domain.KnowledgeMutationKey) error {
	if strings.TrimSpace(key.ActorID) == "" || strings.TrimSpace(key.Operation) == "" || strings.TrimSpace(key.Key) == "" || strings.TrimSpace(key.RequestHash) == "" {
		return fmt.Errorf("%w: actor, operation, key and request hash are required", ErrKnowledgeDocumentConflict)
	}
	if len(key.Key) > 200 {
		return fmt.Errorf("%w: idempotency key exceeds 200 characters", ErrKnowledgeDocumentConflict)
	}
	return nil
}

func validateLifecycleRevision(document domain.KnowledgeDocument, revision domain.KnowledgeRevision, creatingDocument bool) error {
	if strings.TrimSpace(revision.RevisionID) == "" || strings.TrimSpace(revision.DocumentID) == "" || strings.TrimSpace(revision.Filename) == "" || strings.TrimSpace(revision.MIMEType) == "" || strings.TrimSpace(revision.SourceFormat) == "" || strings.TrimSpace(revision.ObjectURI) == "" || strings.TrimSpace(revision.ChecksumSHA256) == "" || strings.TrimSpace(revision.ParseVersion) == "" || strings.TrimSpace(revision.IndexVersion) == "" {
		return fmt.Errorf("%w: revision identity, file metadata, object uri, checksum and versions are required", ErrKnowledgeRevisionConflict)
	}
	if creatingDocument && (strings.TrimSpace(document.DocumentID) == "" || strings.TrimSpace(document.DisplayName) == "" || strings.TrimSpace(document.OwnerID) == "") {
		return fmt.Errorf("%w: document identity, display name and owner are required", ErrKnowledgeDocumentConflict)
	}
	return nil
}

func insertLifecycleRevisionTx(ctx context.Context, tx *sql.Tx, revision domain.KnowledgeRevision) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_revisions (
			revision_id, document_id, legacy_file_id, filename, mime_type, source_format, object_uri,
			checksum_sha256, parse_version, index_version, embedding_model, effective_at, chunk_manifest_sha256,
			status, created_at, updated_at
		) VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''), $12, NULLIF($13, ''), $14, $15, $16)`,
		revision.RevisionID, revision.DocumentID, revision.LegacyFileID, revision.Filename, revision.MIMEType, revision.SourceFormat,
		revision.ObjectURI, revision.ChecksumSHA256, revision.ParseVersion, revision.IndexVersion, revision.EmbeddingModel,
		parseTimestamp(revision.EffectiveAt), revision.ChunkManifestSHA256, revision.Status, revision.CreatedAt, revision.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create knowledge revision: %w", err)
	}
	return nil
}

func insertLegacyLifecycleFacadeTx(ctx context.Context, tx *sql.Tx, document domain.KnowledgeDocument, revision domain.KnowledgeRevision, delivery domain.KnowledgeOutboxMessage) error {
	if strings.TrimSpace(revision.LegacyFileID) == "" {
		return nil
	}
	return insertIngestionTaskTx(ctx, tx, domain.IngestionTaskSummary{
		TaskID: delivery.DeliveryID, FileID: revision.LegacyFileID, DocumentID: revision.DocumentID, RevisionID: revision.RevisionID,
		DeliveryID: delivery.DeliveryID, ChecksumSHA256: revision.ChecksumSHA256, Status: "queued",
		RequestedAt: revision.CreatedAt, UpdatedAt: revision.UpdatedAt,
	})
}

func insertLegacyLifecycleFileTx(ctx context.Context, tx *sql.Tx, document domain.KnowledgeDocument, revision domain.KnowledgeRevision) error {
	if strings.TrimSpace(revision.LegacyFileID) == "" {
		return nil
	}
	file := domain.KnowledgeFile{
		FileID: revision.LegacyFileID, Filename: revision.Filename, MIMEType: revision.MIMEType, SourceFormat: revision.SourceFormat,
		Status: "queued", UploadedBy: document.OwnerID, UploadedAt: revision.CreatedAt, ObjectURI: revision.ObjectURI,
		ParseVersion: revision.ParseVersion, IndexVersion: revision.IndexVersion,
	}
	if err := insertKnowledgeFileTx(ctx, tx, file); err != nil {
		return err
	}
	return nil
}

func insertLifecycleEventTx(ctx context.Context, tx *sql.Tx, eventType, documentID, revisionID, snapshotID, actorID, correlationID string) (domain.KnowledgeUpdateEvent, error) {
	next, err := nextCounterTx(ctx, tx, "event")
	if err != nil {
		return domain.KnowledgeUpdateEvent{}, err
	}
	event := domain.KnowledgeUpdateEvent{
		EventID: fmt.Sprintf("event_%03d", next), EventType: eventType, DocumentID: documentID, RevisionID: revisionID,
		SnapshotID: snapshotID, ActorID: actorID, CorrelationID: correlationID, OccurredAt: time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_update_events (event_id, event_type, document_id, revision_id, snapshot_id, actor_id, correlation_id, occurred_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''), $8)`,
		event.EventID, event.EventType, event.DocumentID, event.RevisionID, event.SnapshotID, event.ActorID, event.CorrelationID, event.OccurredAt,
	); err != nil {
		return domain.KnowledgeUpdateEvent{}, err
	}
	return event, nil
}

func insertLifecycleOutboxTx(ctx context.Context, tx *sql.Tx, topic, documentID, revisionID, snapshotID string, payload any) (domain.KnowledgeOutboxMessage, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return domain.KnowledgeOutboxMessage{}, err
	}
	next, err := nextCounterTx(ctx, tx, "delivery")
	if err != nil {
		return domain.KnowledgeOutboxMessage{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	message := domain.KnowledgeOutboxMessage{
		DeliveryID: fmt.Sprintf("delivery_%03d", next), Topic: topic, DocumentID: documentID, RevisionID: revisionID,
		SnapshotID: snapshotID, Payload: string(encoded), Status: "pending", AvailableAt: now, CreatedAt: now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_outbox (delivery_id, topic, document_id, revision_id, snapshot_id, payload, status, available_at, created_at, updated_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6, 'pending', $7, $8, $8)`,
		message.DeliveryID, message.Topic, message.DocumentID, message.RevisionID, message.SnapshotID, encoded, now, now,
	); err != nil {
		return domain.KnowledgeOutboxMessage{}, err
	}
	return message, nil
}

func replayKnowledgeMutationTx(ctx context.Context, tx *sql.Tx, key domain.KnowledgeMutationKey, destination any) (bool, error) {
	var hash string
	var payload []byte
	err := tx.QueryRowContext(ctx, `
		SELECT request_hash, response_payload
		FROM knowledge_mutation_idempotency
		WHERE actor_id = $1 AND operation = $2 AND idempotency_key = $3
		FOR UPDATE`, key.ActorID, key.Operation, key.Key).Scan(&hash, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if hash != key.RequestHash {
		return false, fmt.Errorf("%w: %s", ErrIdempotencyKeyReused, key.Key)
	}
	if err := json.Unmarshal(payload, destination); err != nil {
		return false, fmt.Errorf("decode idempotent knowledge mutation: %w", err)
	}
	return true, nil
}

func recordKnowledgeMutationTx(ctx context.Context, tx *sql.Tx, key domain.KnowledgeMutationKey, result any) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO knowledge_mutation_idempotency (actor_id, operation, idempotency_key, request_hash, response_payload, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())`, key.ActorID, key.Operation, key.Key, key.RequestHash, payload)
	if err == nil {
		return nil
	}
	return fmt.Errorf("record knowledge mutation: %w", err)
}

func (s *PostgresStore) recordKnowledgeMutation(ctx context.Context, key domain.KnowledgeMutationKey, result any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := recordKnowledgeMutationTx(ctx, tx, key, result); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) knowledgeDocument(ctx context.Context, documentID string) (domain.KnowledgeDocument, bool, error) {
	document, err := scanKnowledgeDocument(s.db.QueryRowContext(ctx, `
		SELECT document_id, display_name, owner_id, status, created_at, updated_at
		FROM knowledge_documents WHERE document_id = $1`, documentID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeDocument{}, false, nil
	}
	return document, err == nil, err
}

func scanKnowledgeDocument(row scanner) (domain.KnowledgeDocument, error) {
	var document domain.KnowledgeDocument
	var createdAt, updatedAt time.Time
	if err := row.Scan(&document.DocumentID, &document.DisplayName, &document.OwnerID, &document.Status, &createdAt, &updatedAt); err != nil {
		return domain.KnowledgeDocument{}, err
	}
	document.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	document.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return document, nil
}

func (s *PostgresStore) latestKnowledgeRevision(ctx context.Context, documentID string) (domain.KnowledgeRevision, bool, error) {
	revision, err := scanKnowledgeRevision(s.db.QueryRowContext(ctx, knowledgeRevisionSelect+` WHERE document_id = $1 ORDER BY created_at DESC, revision_id DESC LIMIT 1`, documentID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeRevision{}, false, nil
	}
	return revision, err == nil, err
}

func (s *PostgresStore) revisionDeliverySummary(ctx context.Context, revisionID string) (domain.RevisionDeliverySummary, bool, error) {
	var summary domain.RevisionDeliverySummary
	var lastError sql.NullString
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT delivery_id, status, attempt_count, last_error, updated_at
		FROM knowledge_outbox
		WHERE revision_id = $1 AND topic = $2
		ORDER BY created_at DESC LIMIT 1`, revisionID, revisionIndexRequestedTopic,
	).Scan(&summary.DeliveryID, &summary.Status, &summary.AttemptCount, &lastError, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RevisionDeliverySummary{}, false, nil
	}
	if err != nil {
		return domain.RevisionDeliverySummary{}, false, err
	}
	summary.LastError = nullableString(lastError)
	summary.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return summary, true, nil
}

func (s *PostgresStore) latestKnowledgeEvent(ctx context.Context, correlationID string) (domain.KnowledgeUpdateEvent, error) {
	var event domain.KnowledgeUpdateEvent
	var documentID, revisionID, snapshotID, actorID sql.NullString
	var occurredAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT event_id, event_type, document_id, revision_id, snapshot_id, actor_id, correlation_id, occurred_at
		FROM knowledge_update_events
		WHERE correlation_id = $1
		ORDER BY occurred_at DESC, event_id DESC LIMIT 1`, correlationID,
	).Scan(&event.EventID, &event.EventType, &documentID, &revisionID, &snapshotID, &actorID, &event.CorrelationID, &occurredAt)
	if err != nil {
		return domain.KnowledgeUpdateEvent{}, err
	}
	event.DocumentID = nullableString(documentID)
	event.RevisionID = nullableString(revisionID)
	event.SnapshotID = nullableString(snapshotID)
	event.ActorID = nullableString(actorID)
	event.OccurredAt = occurredAt.UTC().Format(time.RFC3339)
	return event, nil
}

func createSuccessorSnapshotTx(ctx context.Context, tx *sql.Tx, active domain.KnowledgeSnapshot, members []domain.KnowledgeSnapshotMember) (domain.KnowledgeSnapshot, error) {
	next, err := nextCounterTx(ctx, tx, "snapshot")
	if err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	snapshot := domain.KnowledgeSnapshot{
		SnapshotID: fmt.Sprintf("snap_%03d", next), ParentSnapshotID: active.SnapshotID, Status: "published",
		ManifestSHA256: snapshotManifestSHA256(members), CreatedAt: now, PublishedAt: stringPointer(now), Members: members,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_snapshots (snapshot_id, parent_snapshot_id, status, manifest_sha256, created_at, published_at)
		VALUES ($1, $2, 'published', $3, $4, $4)`, snapshot.SnapshotID, snapshot.ParentSnapshotID, snapshot.ManifestSHA256, now); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_snapshot_members (snapshot_id, revision_id, checksum_sha256) VALUES ($1, $2, $3)`, snapshot.SnapshotID, member.RevisionID, member.ChecksumSHA256); err != nil {
			return domain.KnowledgeSnapshot{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_snapshots SET status = 'retired' WHERE snapshot_id = $1`, active.SnapshotID); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_active_snapshot SET snapshot_id = $1, lock_version = lock_version + 1 WHERE singleton = TRUE`, snapshot.SnapshotID); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	return snapshot, nil
}

func deliverySummary(message domain.KnowledgeOutboxMessage) domain.RevisionDeliverySummary {
	return domain.RevisionDeliverySummary{DeliveryID: message.DeliveryID, Status: message.Status, AttemptCount: message.AttemptCount, UpdatedAt: message.CreatedAt}
}

func snapshotSummary(snapshot domain.KnowledgeSnapshot) *domain.KnowledgeSnapshotSummary {
	result := &domain.KnowledgeSnapshotSummary{
		SnapshotID: snapshot.SnapshotID, Status: snapshot.Status, ManifestSHA256: snapshot.ManifestSHA256,
		CreatedAt: snapshot.CreatedAt, Members: snapshot.Members, Acceptance: snapshot.Acceptance,
	}
	if snapshot.PublishedAt != nil {
		result.PublishedAt = *snapshot.PublishedAt
	}
	return result
}

func eventSummary(event domain.KnowledgeUpdateEvent) *domain.KnowledgeUpdateEventSummary {
	return &domain.KnowledgeUpdateEventSummary{
		EventID: event.EventID, EventType: event.EventType, DocumentID: event.DocumentID, RevisionID: event.RevisionID,
		SnapshotID: event.SnapshotID, CreatedAt: event.OccurredAt, AcceptanceStatus: event.AcceptanceStatus, OnboardingRunID: event.OnboardingRunID,
	}
}

func snapshotID(snapshot *domain.KnowledgeSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return snapshot.SnapshotID
}

var _ KnowledgeLifecycleStore = (*PostgresStore)(nil)
