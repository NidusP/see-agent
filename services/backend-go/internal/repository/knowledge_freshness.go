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
	ErrRevalidationNotFound = errors.New("revalidation not found")
	ErrRevalidationInvalid  = errors.New("revalidation invalid")
)

const maxKnowledgeFreshnessScanAttempts = 3

func (s *PostgresStore) EnsureKnowledgeFreshnessScans(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge_freshness_scans (event_id, status, available_at, created_at, updated_at)
		SELECT event_id, 'queued', NOW(), NOW(), NOW()
		FROM knowledge_update_events
		WHERE event_type IN ('published', 'disabled', 'deleted')
		ON CONFLICT (event_id) DO NOTHING`)
	return err
}

func (s *PostgresStore) ClaimKnowledgeFreshnessScan(ctx context.Context, lease time.Duration) (domain.KnowledgeFreshnessScan, domain.KnowledgeUpdateEvent, bool, error) {
	if lease <= 0 {
		lease = 30 * time.Second
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeFreshnessScan{}, domain.KnowledgeUpdateEvent{}, false, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT event_id
			FROM knowledge_freshness_scans
			WHERE (status IN ('queued', 'retry') AND available_at <= NOW())
				OR (status = 'running' AND lease_until <= NOW())
			ORDER BY available_at ASC, event_id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE knowledge_freshness_scans scan
		SET status = 'running', attempt_count = attempt_count + 1,
			lease_until = NOW() + ($1 * INTERVAL '1 millisecond'), last_error = NULL, updated_at = NOW()
		FROM candidate
		WHERE scan.event_id = candidate.event_id
		RETURNING scan.event_id, scan.status, scan.attempt_count, scan.candidate_count, scan.marked_count,
			scan.last_error, scan.updated_at, scan.completed_at`, lease.Milliseconds())
	scan, err := scanFreshnessScan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeFreshnessScan{}, domain.KnowledgeUpdateEvent{}, false, nil
	}
	if err != nil {
		return domain.KnowledgeFreshnessScan{}, domain.KnowledgeUpdateEvent{}, false, err
	}
	event, err := knowledgeUpdateEventTx(ctx, tx, scan.EventID)
	if err != nil {
		return domain.KnowledgeFreshnessScan{}, domain.KnowledgeUpdateEvent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeFreshnessScan{}, domain.KnowledgeUpdateEvent{}, false, err
	}
	return scan, event, true, nil
}

func (s *PostgresStore) CompleteKnowledgeFreshnessScan(ctx context.Context, eventID string) (domain.KnowledgeFreshnessScan, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeFreshnessScan{}, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM knowledge_freshness_scans WHERE event_id = $1 FOR UPDATE`, eventID).Scan(&status); err != nil {
		return domain.KnowledgeFreshnessScan{}, err
	}
	if status != "running" {
		return domain.KnowledgeFreshnessScan{}, fmt.Errorf("freshness scan %s is %s", eventID, status)
	}
	event, err := knowledgeUpdateEventTx(ctx, tx, eventID)
	if err != nil {
		return domain.KnowledgeFreshnessScan{}, err
	}
	if strings.TrimSpace(event.DocumentID) == "" {
		return domain.KnowledgeFreshnessScan{}, fmt.Errorf("freshness event %s has no document", eventID)
	}

	query := `
		SELECT DISTINCT message.message_id
		FROM messages message
		JOIN citations citation ON citation.message_id = message.message_id
		WHERE message.role = 'assistant'
			AND message.knowledge_snapshot_id IS NOT NULL
			AND citation.document_id = $1`
	args := []any{event.DocumentID}
	if event.EventType == "published" {
		query += ` AND citation.revision_id <> $2`
		args = append(args, event.RevisionID)
	} else if event.EventType != "disabled" && event.EventType != "deleted" {
		return domain.KnowledgeFreshnessScan{}, fmt.Errorf("unsupported freshness event %s", event.EventType)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return domain.KnowledgeFreshnessScan{}, err
	}
	defer rows.Close()
	messageIDs := []string{}
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			return domain.KnowledgeFreshnessScan{}, err
		}
		messageIDs = append(messageIDs, messageID)
	}
	if err := rows.Err(); err != nil {
		return domain.KnowledgeFreshnessScan{}, err
	}
	marked := 0
	for _, messageID := range messageIDs {
		result, err := tx.ExecContext(ctx, `UPDATE messages SET freshness_status = 'possibly_stale' WHERE message_id = $1`, messageID)
		if err != nil {
			return domain.KnowledgeFreshnessScan{}, err
		}
		count, _ := result.RowsAffected()
		marked += int(count)
		if _, err := tx.ExecContext(ctx, `UPDATE message_knowledge_provenance SET freshness_status = 'possibly_stale' WHERE message_id = $1`, messageID); err != nil {
			return domain.KnowledgeFreshnessScan{}, err
		}
	}
	row := tx.QueryRowContext(ctx, `
		UPDATE knowledge_freshness_scans
		SET status = 'completed', lease_until = NULL, candidate_count = $2, marked_count = $3,
			last_error = NULL, updated_at = NOW(), completed_at = NOW()
		WHERE event_id = $1
		RETURNING event_id, status, attempt_count, candidate_count, marked_count, last_error, updated_at, completed_at`,
		eventID, len(messageIDs), marked)
	scan, err := scanFreshnessScan(row)
	if err != nil {
		return domain.KnowledgeFreshnessScan{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeFreshnessScan{}, err
	}
	return scan, nil
}

func (s *PostgresStore) RetryKnowledgeFreshnessScan(ctx context.Context, eventID, failure string, availableAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE knowledge_freshness_scans
		SET status = CASE WHEN attempt_count >= $4 THEN 'failed' ELSE 'retry' END,
			lease_until = NULL,
			available_at = CASE WHEN attempt_count >= $4 THEN available_at ELSE $2 END,
			last_error = NULLIF($3, ''), updated_at = NOW()
		WHERE event_id = $1 AND status = 'running'`, eventID, availableAt, failure, maxKnowledgeFreshnessScanAttempts)
	return err
}

func (s *PostgresStore) ListKnowledgeUpdates(ctx context.Context, documentID, cursor string, limit int) (domain.KnowledgeUpdatePage, error) {
	if limit < 1 || limit > 100 {
		return domain.KnowledgeUpdatePage{}, fmt.Errorf("limit must be between 1 and 100")
	}
	query := `
		SELECT event.event_id, event.event_type, event.document_id, event.revision_id, event.snapshot_id, event.actor_id,
			event.correlation_id, event.occurred_at, event.acceptance_status, event.onboarding_run_id,
			scan.event_id, scan.status, scan.attempt_count,
			scan.candidate_count, scan.marked_count, scan.last_error, scan.updated_at, scan.completed_at
		FROM knowledge_update_events event
		LEFT JOIN knowledge_freshness_scans scan ON scan.event_id = event.event_id
		WHERE ($1 = '' OR event.document_id = $1)
			AND ($2 = '' OR event.event_id < $2)
		ORDER BY event.event_id DESC
		LIMIT $3`
	rows, err := s.db.QueryContext(ctx, query, strings.TrimSpace(documentID), strings.TrimSpace(cursor), limit+1)
	if err != nil {
		return domain.KnowledgeUpdatePage{}, err
	}
	defer rows.Close()
	page := domain.KnowledgeUpdatePage{Items: []domain.KnowledgeUpdateCalendarItem{}}
	for rows.Next() {
		item, err := scanKnowledgeUpdateCalendarItem(rows)
		if err != nil {
			return domain.KnowledgeUpdatePage{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.KnowledgeUpdatePage{}, err
	}
	if len(page.Items) > limit {
		page.NextCursor = page.Items[limit-1].EventID
		page.Items = page.Items[:limit]
	}
	if err := s.attachPublishedAcceptance(ctx, page.Items); err != nil {
		return domain.KnowledgeUpdatePage{}, err
	}
	return page, nil
}

func (s *PostgresStore) GetKnowledgeSnapshot(ctx context.Context, snapshotID string) (domain.KnowledgeSnapshot, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeSnapshot{}, false, err
	}
	defer tx.Rollback()
	snapshot, err := snapshotTx(ctx, tx, snapshotID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeSnapshot{}, false, nil
	}
	if err != nil {
		return domain.KnowledgeSnapshot{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeSnapshot{}, false, err
	}
	if err := s.attachSnapshotAcceptance(ctx, &snapshot); err != nil {
		return domain.KnowledgeSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func (s *PostgresStore) CreateRevalidationRun(ctx context.Context, request domain.RevalidationRequest) (domain.RevalidationRun, error) {
	if err := validateRevalidationRequest(request); err != nil {
		return domain.RevalidationRun{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	defer tx.Rollback()
	existing, found, err := revalidationRunByRequesterKeyTx(ctx, tx, request.RequesterID, request.IdempotencyKey)
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	if found {
		if existing.ConversationID != request.ConversationID || existing.OriginalMessageID != request.OriginalMessageID || existing.Reason != request.Reason {
			return domain.RevalidationRun{}, fmt.Errorf("%w: %s", ErrIdempotencyKeyReused, request.IdempotencyKey)
		}
		if err := attachRevalidationResultTx(ctx, tx, &existing); err != nil {
			return domain.RevalidationRun{}, err
		}
		if err := tx.Commit(); err != nil {
			return domain.RevalidationRun{}, err
		}
		return existing, nil
	}

	if err := validateStoredScopeTx(ctx, tx, request.LatestScope); err != nil {
		return domain.RevalidationRun{}, err
	}
	var originalAnswer, originalSnapshotID, originalManifest, status string
	err = tx.QueryRowContext(ctx, `
		SELECT content, COALESCE(knowledge_snapshot_id, ''), COALESCE(knowledge_snapshot_manifest_hash, ''), status
		FROM messages
		WHERE message_id = $1 AND conversation_id = $2 AND role = 'assistant'
		FOR UPDATE`, request.OriginalMessageID, request.ConversationID).Scan(&originalAnswer, &originalSnapshotID, &originalManifest, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RevalidationRun{}, fmt.Errorf("%w: assistant message was not found", ErrRevalidationInvalid)
	}
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	if status != "completed" || strings.TrimSpace(originalSnapshotID) == "" || strings.TrimSpace(originalManifest) == "" {
		return domain.RevalidationRun{}, fmt.Errorf("%w: target does not have K5 provenance", ErrRevalidationInvalid)
	}
	citationRevisionIDs, err := citationRevisionIDsTx(ctx, tx, request.OriginalMessageID)
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	relatedEventIDs, err := relatedUpdateEventIDsTx(ctx, tx, request.OriginalMessageID)
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	next, err := nextCounterTx(ctx, tx, "revalidation")
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	now := time.Now().UTC()
	run := domain.RevalidationRun{
		RevalidationID: fmt.Sprintf("reval_%03d", next), ConversationID: request.ConversationID, OriginalMessageID: request.OriginalMessageID,
		Status: "queued", Trigger: "manual", Reason: request.Reason, LatestSnapshotID: request.LatestScope.SnapshotID,
		LatestSnapshotManifestHash: request.LatestScope.ManifestHash, RelatedUpdateEventIDs: relatedEventIDs, RequestedAt: now.Format(time.RFC3339),
		RequesterID: request.RequesterID, IdempotencyKey: request.IdempotencyKey, OriginalAnswer: originalAnswer,
		OriginalSnapshotID: originalSnapshotID, OriginalSnapshotManifestHash: originalManifest, OriginalCitationRevisionIDs: citationRevisionIDs,
	}
	relatedJSON, _ := json.Marshal(run.RelatedUpdateEventIDs)
	citationJSON, _ := json.Marshal(run.OriginalCitationRevisionIDs)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO answer_revalidation_runs (
			revalidation_id, conversation_id, original_message_id, knowledge_snapshot_id, status, requested_at,
			requester_id, idempotency_key, trigger, reason, latest_snapshot_id, latest_snapshot_manifest_sha256,
			related_update_event_ids, original_answer, original_snapshot_id, original_snapshot_manifest_sha256,
			original_citation_revision_ids, available_at, updated_at
		) VALUES ($1, $2, $3, $4, 'queued', $5, $6, $7, 'manual', NULLIF($8, ''), $4, $9, $10, $11, $12, $13, $14, $5, $5)`,
		run.RevalidationID, run.ConversationID, run.OriginalMessageID, run.LatestSnapshotID, now, run.RequesterID, run.IdempotencyKey,
		run.Reason, run.LatestSnapshotManifestHash, relatedJSON, run.OriginalAnswer, run.OriginalSnapshotID, run.OriginalSnapshotManifestHash, citationJSON,
	)
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.RevalidationRun{}, err
	}
	return run, nil
}

func (s *PostgresStore) GetRevalidationRun(ctx context.Context, conversationID, revalidationID string) (domain.RevalidationRun, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RevalidationRun{}, false, err
	}
	defer tx.Rollback()
	run, found, err := revalidationRunByIDTx(ctx, tx, conversationID, revalidationID, false)
	if err != nil || !found {
		return run, found, err
	}
	if err := attachRevalidationResultTx(ctx, tx, &run); err != nil {
		return domain.RevalidationRun{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.RevalidationRun{}, false, err
	}
	return run, true, nil
}

func (s *PostgresStore) ClaimRevalidationRun(ctx context.Context, lease time.Duration) (domain.RevalidationRun, bool, error) {
	if lease <= 0 {
		lease = 30 * time.Second
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RevalidationRun{}, false, err
	}
	defer tx.Rollback()
	var runID string
	err = tx.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT revalidation_id FROM answer_revalidation_runs
			WHERE (status = 'queued' AND available_at <= NOW())
				OR (status = 'running' AND lease_until <= NOW())
			ORDER BY requested_at ASC, revalidation_id ASC
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE answer_revalidation_runs run
		SET status = 'running', attempt_count = attempt_count + 1, lease_until = NOW() + ($1 * INTERVAL '1 millisecond'),
			last_error_code = NULL, last_error_message = NULL, updated_at = NOW()
		FROM candidate WHERE run.revalidation_id = candidate.revalidation_id
		RETURNING run.revalidation_id`, lease.Milliseconds()).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RevalidationRun{}, false, nil
	}
	if err != nil {
		return domain.RevalidationRun{}, false, err
	}
	run, found, err := revalidationRunByIDTx(ctx, tx, "", runID, false)
	if err != nil || !found {
		return domain.RevalidationRun{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.RevalidationRun{}, false, err
	}
	return run, true, nil
}

func (s *PostgresStore) CompleteRevalidationRun(ctx context.Context, runID string, response domain.ChatRuntimeResponse, outcome string) (domain.RevalidationRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	defer tx.Rollback()
	run, found, err := revalidationRunByIDTx(ctx, tx, "", runID, true)
	if err != nil || !found {
		return domain.RevalidationRun{}, revalidationNotFound(err, runID)
	}
	if run.Status != "running" {
		return domain.RevalidationRun{}, fmt.Errorf("%w: run is %s", ErrRevalidationInvalid, run.Status)
	}
	scope, err := scopeForSnapshotTx(ctx, tx, run.LatestSnapshotID, run.LatestSnapshotManifestHash)
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	if response.KnowledgeSnapshotID != scope.SnapshotID || response.KnowledgeSnapshotManifestHash != scope.ManifestHash || response.FreshnessStatus != "revalidated" {
		return domain.RevalidationRun{}, fmt.Errorf("%w: runtime response scope mismatch", ErrKnowledgeSnapshotCitationMismatch)
	}
	if err := validateSnapshotCitationAuthorityTx(ctx, tx, scope, response.Citations, nil); err != nil {
		return domain.RevalidationRun{}, err
	}
	if !validRevalidationOutcome(outcome) {
		return domain.RevalidationRun{}, fmt.Errorf("%w: invalid outcome", ErrRevalidationInvalid)
	}
	citations := revalidationCitations(run.OriginalMessageID, scope.SnapshotID, response.Citations)
	citationJSON, err := json.Marshal(citations)
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	completedAt := time.Now().UTC()
	content := strings.TrimSpace(response.AssistantMessage.Content)
	if content == "" {
		content = "Unable to determine whether the historical answer remains valid."
		outcome = "insufficient_evidence"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO answer_revalidation_results (
			revalidation_id, content, knowledge_snapshot_id, knowledge_snapshot_manifest_sha256, outcome, citations, completed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		run.RevalidationID, content, scope.SnapshotID, scope.ManifestHash, outcome, citationJSON, completedAt,
	); err != nil {
		return domain.RevalidationRun{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE answer_revalidation_runs
		SET status = 'completed', lease_until = NULL, completed_at = $2, outcome = $3,
			last_error_code = NULL, last_error_message = NULL, updated_at = $2
		WHERE revalidation_id = $1`, run.RevalidationID, completedAt, outcome); err != nil {
		return domain.RevalidationRun{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET freshness_status = 'revalidated' WHERE message_id = $1`, run.OriginalMessageID); err != nil {
		return domain.RevalidationRun{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE message_knowledge_provenance SET freshness_status = 'revalidated' WHERE message_id = $1`, run.OriginalMessageID); err != nil {
		return domain.RevalidationRun{}, err
	}
	run.Status, run.Outcome = "completed", outcome
	value := completedAt.Format(time.RFC3339)
	run.CompletedAt = value
	run.Result = &domain.RevalidationResult{RevalidationID: run.RevalidationID, Content: content, KnowledgeSnapshotID: scope.SnapshotID, KnowledgeSnapshotManifestHash: scope.ManifestHash, Outcome: outcome, Citations: citations, CompletedAt: value}
	if err := tx.Commit(); err != nil {
		return domain.RevalidationRun{}, err
	}
	return run, nil
}

func (s *PostgresStore) FailRevalidationRun(ctx context.Context, runID, code, message string) (domain.RevalidationRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RevalidationRun{}, err
	}
	defer tx.Rollback()
	run, found, err := revalidationRunByIDTx(ctx, tx, "", runID, true)
	if err != nil || !found {
		return domain.RevalidationRun{}, revalidationNotFound(err, runID)
	}
	if run.Status != "running" {
		return domain.RevalidationRun{}, fmt.Errorf("%w: run is %s", ErrRevalidationInvalid, run.Status)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE answer_revalidation_runs
		SET status = 'failed', lease_until = NULL, last_error_code = $2, last_error_message = $3, updated_at = NOW()
		WHERE revalidation_id = $1`, runID, code, message); err != nil {
		return domain.RevalidationRun{}, err
	}
	run.Status = "failed"
	run.Error = &domain.RevalidationError{Code: code, Message: message, RequestID: runID}
	if err := tx.Commit(); err != nil {
		return domain.RevalidationRun{}, err
	}
	return run, nil
}

func scanFreshnessScan(row scanner) (domain.KnowledgeFreshnessScan, error) {
	var scan domain.KnowledgeFreshnessScan
	var lastError sql.NullString
	var updatedAt time.Time
	var completedAt sql.NullTime
	if err := row.Scan(&scan.EventID, &scan.Status, &scan.AttemptCount, &scan.CandidateCount, &scan.MarkedCount, &lastError, &updatedAt, &completedAt); err != nil {
		return domain.KnowledgeFreshnessScan{}, err
	}
	scan.LastError = nullableString(lastError)
	scan.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	if completedAt.Valid {
		scan.CompletedAt = completedAt.Time.UTC().Format(time.RFC3339)
	}
	return scan, nil
}

func knowledgeUpdateEventTx(ctx context.Context, tx *sql.Tx, eventID string) (domain.KnowledgeUpdateEvent, error) {
	var event domain.KnowledgeUpdateEvent
	var documentID, revisionID, snapshotID, actorID sql.NullString
	var occurredAt time.Time
	err := tx.QueryRowContext(ctx, `
		SELECT event_id, event_type, document_id, revision_id, snapshot_id, actor_id, correlation_id, occurred_at
		FROM knowledge_update_events WHERE event_id = $1`, eventID,
	).Scan(&event.EventID, &event.EventType, &documentID, &revisionID, &snapshotID, &actorID, &event.CorrelationID, &occurredAt)
	if err != nil {
		return domain.KnowledgeUpdateEvent{}, err
	}
	event.DocumentID, event.RevisionID, event.SnapshotID, event.ActorID = nullableString(documentID), nullableString(revisionID), nullableString(snapshotID), nullableString(actorID)
	event.OccurredAt = occurredAt.UTC().Format(time.RFC3339)
	return event, nil
}

func scanKnowledgeUpdateCalendarItem(row scanner) (domain.KnowledgeUpdateCalendarItem, error) {
	var item domain.KnowledgeUpdateCalendarItem
	var documentID, revisionID, snapshotID, actorID, acceptanceStatus, onboardingRunID sql.NullString
	var occurredAt time.Time
	var scanEventID, scanStatus, scanLastError sql.NullString
	var scanAttempt, scanCandidates, scanMarked sql.NullInt64
	var scanUpdated, scanCompleted sql.NullTime
	if err := row.Scan(&item.EventID, &item.EventType, &documentID, &revisionID, &snapshotID, &actorID, &item.CorrelationID, &occurredAt,
		&acceptanceStatus, &onboardingRunID,
		&scanEventID, &scanStatus, &scanAttempt, &scanCandidates, &scanMarked, &scanLastError, &scanUpdated, &scanCompleted); err != nil {
		return domain.KnowledgeUpdateCalendarItem{}, err
	}
	item.DocumentID, item.RevisionID, item.SnapshotID, item.ActorID = nullableString(documentID), nullableString(revisionID), nullableString(snapshotID), nullableString(actorID)
	item.AcceptanceStatus = nullableString(acceptanceStatus)
	item.OnboardingRunID = nullableString(onboardingRunID)
	item.OccurredAt = occurredAt.UTC().Format(time.RFC3339)
	if scanEventID.Valid {
		scan := domain.KnowledgeFreshnessScan{EventID: scanEventID.String, Status: scanStatus.String, AttemptCount: int(scanAttempt.Int64), CandidateCount: int(scanCandidates.Int64), MarkedCount: int(scanMarked.Int64), LastError: nullableString(scanLastError)}
		if scanUpdated.Valid {
			scan.UpdatedAt = scanUpdated.Time.UTC().Format(time.RFC3339)
		}
		if scanCompleted.Valid {
			scan.CompletedAt = scanCompleted.Time.UTC().Format(time.RFC3339)
		}
		item.FreshnessScan = &scan
	}
	return item, nil
}

func validateRevalidationRequest(request domain.RevalidationRequest) error {
	if strings.TrimSpace(request.RequesterID) == "" || strings.TrimSpace(request.ConversationID) == "" || strings.TrimSpace(request.OriginalMessageID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return fmt.Errorf("%w: requester, conversation, message and idempotency key are required", ErrRevalidationInvalid)
	}
	if len(request.IdempotencyKey) > 200 || len(request.Reason) > 500 {
		return fmt.Errorf("%w: idempotency key or reason is too long", ErrRevalidationInvalid)
	}
	if strings.TrimSpace(request.LatestScope.SnapshotID) == "" || strings.TrimSpace(request.LatestScope.ManifestHash) == "" || len(request.LatestScope.RevisionIDs) == 0 {
		return fmt.Errorf("%w: latest scope is required", ErrRevalidationInvalid)
	}
	return nil
}

func validateStoredScopeTx(ctx context.Context, tx *sql.Tx, scope domain.RuntimeKnowledgeScope) error {
	var manifest string
	if err := tx.QueryRowContext(ctx, `SELECT manifest_sha256 FROM knowledge_snapshots WHERE snapshot_id = $1`, scope.SnapshotID).Scan(&manifest); err != nil {
		return fmt.Errorf("%w: latest snapshot is unavailable", ErrRevalidationInvalid)
	}
	if manifest != scope.ManifestHash {
		return fmt.Errorf("%w: latest snapshot manifest mismatch", ErrRevalidationInvalid)
	}
	return nil
}

func citationRevisionIDsTx(ctx context.Context, tx *sql.Tx, messageID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT revision_id FROM citations WHERE message_id = $1 AND revision_id IS NOT NULL ORDER BY revision_id`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	return items, rows.Err()
}

func relatedUpdateEventIDsTx(ctx context.Context, tx *sql.Tx, messageID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT event.event_id
		FROM knowledge_update_events event
		JOIN citations citation ON citation.message_id = $1
		JOIN messages message ON message.message_id = citation.message_id
		WHERE event.event_type IN ('published', 'disabled', 'deleted')
			AND event.occurred_at > COALESCE(message.answered_at, message.created_at)
			AND ((event.event_type = 'published' AND event.document_id = citation.document_id AND event.revision_id <> citation.revision_id)
				OR (event.event_type IN ('disabled', 'deleted') AND event.document_id = citation.document_id))
		ORDER BY event.event_id`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	return items, rows.Err()
}

const revalidationRunColumns = `
	revalidation_id, conversation_id, original_message_id, status, trigger, COALESCE(reason, ''),
	COALESCE(latest_snapshot_id, ''), COALESCE(latest_snapshot_manifest_sha256, ''), related_update_event_ids,
	requested_at, completed_at, COALESCE(outcome, ''), COALESCE(last_error_code, ''), COALESCE(last_error_message, ''),
	requester_id, idempotency_key, COALESCE(original_answer, ''), COALESCE(original_snapshot_id, ''),
	COALESCE(original_snapshot_manifest_sha256, ''), original_citation_revision_ids`

func revalidationRunByRequesterKeyTx(ctx context.Context, tx *sql.Tx, requesterID, key string) (domain.RevalidationRun, bool, error) {
	run, err := scanRevalidationRun(tx.QueryRowContext(ctx, `SELECT `+revalidationRunColumns+` FROM answer_revalidation_runs WHERE requester_id = $1 AND idempotency_key = $2 FOR UPDATE`, requesterID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RevalidationRun{}, false, nil
	}
	return run, err == nil, err
}

func revalidationRunByIDTx(ctx context.Context, tx *sql.Tx, conversationID, runID string, lock bool) (domain.RevalidationRun, bool, error) {
	query := `SELECT ` + revalidationRunColumns + ` FROM answer_revalidation_runs WHERE revalidation_id = $1`
	args := []any{runID}
	if conversationID != "" {
		query += ` AND conversation_id = $2`
		args = append(args, conversationID)
	}
	if lock {
		query += ` FOR UPDATE`
	}
	run, err := scanRevalidationRun(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RevalidationRun{}, false, nil
	}
	return run, err == nil, err
}

func scanRevalidationRun(row scanner) (domain.RevalidationRun, error) {
	var run domain.RevalidationRun
	var related, originalCitations []byte
	var requested time.Time
	var completed sql.NullTime
	var errorCode, errorMessage string
	if err := row.Scan(&run.RevalidationID, &run.ConversationID, &run.OriginalMessageID, &run.Status, &run.Trigger, &run.Reason,
		&run.LatestSnapshotID, &run.LatestSnapshotManifestHash, &related, &requested, &completed, &run.Outcome, &errorCode, &errorMessage,
		&run.RequesterID, &run.IdempotencyKey, &run.OriginalAnswer, &run.OriginalSnapshotID, &run.OriginalSnapshotManifestHash, &originalCitations); err != nil {
		return domain.RevalidationRun{}, err
	}
	_ = json.Unmarshal(related, &run.RelatedUpdateEventIDs)
	_ = json.Unmarshal(originalCitations, &run.OriginalCitationRevisionIDs)
	if run.RelatedUpdateEventIDs == nil {
		run.RelatedUpdateEventIDs = []string{}
	}
	if run.OriginalCitationRevisionIDs == nil {
		run.OriginalCitationRevisionIDs = []string{}
	}
	run.RequestedAt = requested.UTC().Format(time.RFC3339)
	if completed.Valid {
		run.CompletedAt = completed.Time.UTC().Format(time.RFC3339)
	}
	if errorCode != "" || errorMessage != "" {
		run.Error = &domain.RevalidationError{Code: errorCode, Message: errorMessage, RequestID: run.RevalidationID}
	}
	return run, nil
}

func attachRevalidationResultTx(ctx context.Context, tx *sql.Tx, run *domain.RevalidationRun) error {
	var result domain.RevalidationResult
	var citations []byte
	var completed time.Time
	err := tx.QueryRowContext(ctx, `
		SELECT revalidation_id, content, knowledge_snapshot_id, knowledge_snapshot_manifest_sha256, outcome, citations, completed_at
		FROM answer_revalidation_results WHERE revalidation_id = $1`, run.RevalidationID,
	).Scan(&result.RevalidationID, &result.Content, &result.KnowledgeSnapshotID, &result.KnowledgeSnapshotManifestHash, &result.Outcome, &citations, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(citations, &result.Citations); err != nil {
		return err
	}
	result.CompletedAt = completed.UTC().Format(time.RFC3339)
	run.Result = &result
	return nil
}

func scopeForSnapshotTx(ctx context.Context, tx *sql.Tx, snapshotID, manifest string) (domain.RuntimeKnowledgeScope, error) {
	snapshot, err := snapshotTx(ctx, tx, snapshotID)
	if err != nil {
		return domain.RuntimeKnowledgeScope{}, err
	}
	if snapshot.ManifestSHA256 != manifest || len(snapshot.Members) == 0 {
		return domain.RuntimeKnowledgeScope{}, fmt.Errorf("%w: revalidation snapshot mismatch", ErrKnowledgeSnapshotCitationMismatch)
	}
	revisionIDs := make([]string, 0, len(snapshot.Members))
	for _, member := range snapshot.Members {
		revisionIDs = append(revisionIDs, member.RevisionID)
	}
	return domain.RuntimeKnowledgeScope{SnapshotID: snapshot.SnapshotID, ManifestHash: snapshot.ManifestSHA256, RevisionIDs: revisionIDs, ResolvedAt: time.Now().UTC().Format(time.RFC3339)}, nil
}

func revalidationCitations(messageID, snapshotID string, source []domain.RuntimeCitation) []domain.Citation {
	items := make([]domain.Citation, 0, len(source))
	for index, item := range source {
		rank := item.Rank
		if rank < 1 {
			rank = index + 1
		}
		label := strings.TrimSpace(item.CitationLabel)
		if label == "" {
			label = fmt.Sprintf("source-%d", rank)
		}
		items = append(items, domain.Citation{MessageID: messageID, ChunkID: item.ChunkID, FileID: item.FileID, DocumentID: item.DocumentID, RevisionID: item.RevisionID, ChunkContentSHA256: item.ChunkContentSHA256, KnowledgeSnapshotID: snapshotID, Filename: item.Filename, Rank: rank, Score: item.Score, CitationLabel: label, SourceLocator: item.SourceLocator, Snippet: item.Snippet})
	}
	return items
}

func validRevalidationOutcome(value string) bool {
	return value == "still_valid" || value == "needs_update" || value == "insufficient_evidence"
}

func revalidationNotFound(err error, runID string) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: %s", ErrRevalidationNotFound, runID)
}

var _ KnowledgeFreshnessStore = (*PostgresStore)(nil)
