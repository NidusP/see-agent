package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"see-agent/services/backend-go/internal/domain"
)

var (
	ErrUnvalidatedAcknowledgementRequired = errors.New("unvalidated publish acknowledgement is required")
	ErrKnowledgeSnapshotNotFound          = errors.New("knowledge snapshot not found")
	ErrSnapshotNotPending                 = errors.New("knowledge snapshot is not pending publish")
	ErrStalePendingSnapshot               = errors.New("pending snapshot no longer matches the active knowledge")
)

// UnvalidatedAcknowledgementError is the displayable refusal when a system
// administrator tries to publish without the required unvalidated confirmation.
type UnvalidatedAcknowledgementError struct {
	Message string
}

func (e UnvalidatedAcknowledgementError) Error() string { return e.Message }

func (e UnvalidatedAcknowledgementError) Unwrap() error {
	return ErrUnvalidatedAcknowledgementRequired
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func deriveKnowledgeAcceptance(ctx context.Context, q rowQueryer, snapshotID, manifest, snapshotStatus string) (domain.KnowledgeAcceptance, error) {
	acceptance := domain.KnowledgeAcceptance{Status: "unvalidated"}
	var passedID string
	err := q.QueryRowContext(ctx, `
		SELECT run_id
		FROM onboarding_runs
		WHERE knowledge_snapshot_id = $1
			AND knowledge_snapshot_manifest_hash = $2
			AND status = 'passed'
		ORDER BY completed_at DESC NULLS LAST, run_id DESC
		LIMIT 1`, snapshotID, manifest).Scan(&passedID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return acceptance, err
	}
	if err == nil {
		acceptance.Status = "accepted"
		acceptance.OnboardingRunID = passedID
	} else {
		var runID, status string
		err = q.QueryRowContext(ctx, `
			SELECT run_id, status
			FROM onboarding_runs
			WHERE knowledge_snapshot_id = $1
				AND knowledge_snapshot_manifest_hash = $2
				AND status IN ('passed', 'failed')
			ORDER BY completed_at DESC NULLS LAST, run_id DESC
			LIMIT 1`, snapshotID, manifest).Scan(&runID, &status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return acceptance, err
		}
		if err == nil && status == "failed" {
			acceptance.Status = "failed"
			acceptance.OnboardingRunID = runID
		}
	}
	acceptance.DisplayLabel = domain.AcceptanceDisplayLabel(snapshotStatus, acceptance.Status)
	acceptance.RequiresUnvalidatedAcknowledgement = acceptance.Status != "accepted" && snapshotStatus == "pending_publish"
	return acceptance, nil
}

func acknowledgementRequired(acceptance domain.KnowledgeAcceptance) error {
	if !acceptance.RequiresUnvalidatedAcknowledgement {
		return nil
	}
	if acceptance.Status == "failed" {
		return UnvalidatedAcknowledgementError{Message: "发布后这份知识会立刻服务用户，但验收未通过"}
	}
	return UnvalidatedAcknowledgementError{Message: "直接发布需要确认尚未完成上线验收"}
}

func ensureRevisionPublishable(revision domain.KnowledgeRevision) error {
	if revision.Status != "ready" {
		return fmt.Errorf("%w: %s is %s", ErrRevisionNotReady, revision.RevisionID, revision.Status)
	}
	if revision.ChecksumSHA256 == "" || revision.ChunkManifestSHA256 == "" {
		return fmt.Errorf("%w: %s", ErrRevisionManifestMismatch, revision.RevisionID)
	}
	if revision.EffectiveAt != nil {
		effectiveAt, err := time.Parse(time.RFC3339, *revision.EffectiveAt)
		if err != nil {
			return fmt.Errorf("%w: invalid effective_at", ErrRevisionNotEffective)
		}
		if effectiveAt.After(time.Now().UTC()) {
			return fmt.Errorf("%w: %s", ErrRevisionNotEffective, revision.RevisionID)
		}
	}
	return nil
}

func successorMembers(active *domain.KnowledgeSnapshot, revision domain.KnowledgeRevision) []domain.KnowledgeSnapshotMember {
	members := []domain.KnowledgeSnapshotMember{}
	if active != nil {
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
	return members
}

func retireOtherPendingSnapshotsTx(ctx context.Context, tx *sql.Tx, keepID string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE knowledge_snapshots
		SET status = 'retired'
		WHERE status = 'pending_publish' AND snapshot_id <> $1`, keepID)
	return err
}

func lockActiveSnapshotTx(ctx context.Context, tx *sql.Tx) (sql.NullString, error) {
	var activeSnapshotID sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT snapshot_id FROM knowledge_active_snapshot WHERE singleton = TRUE FOR UPDATE`).Scan(&activeSnapshotID)
	return activeSnapshotID, err
}

func publishedActiveSnapshotTx(ctx context.Context, tx *sql.Tx, activeSnapshotID sql.NullString) (*domain.KnowledgeSnapshot, error) {
	if !activeSnapshotID.Valid || activeSnapshotID.String == "" {
		return nil, nil
	}
	active, err := snapshotTx(ctx, tx, activeSnapshotID.String)
	if err != nil {
		return nil, err
	}
	if active.Status != "published" {
		return nil, fmt.Errorf("%w: pointer targets %s", ErrActiveSnapshotUnavailable, active.Status)
	}
	return &active, nil
}

func findMatchingPendingTx(ctx context.Context, tx *sql.Tx, parentID, manifest, revisionID string) (domain.KnowledgeSnapshot, bool, error) {
	var snapshotID string
	err := tx.QueryRowContext(ctx, `
		SELECT snapshot_id
		FROM knowledge_snapshots
		WHERE status = 'pending_publish'
			AND manifest_sha256 = $1
			AND parent_snapshot_id IS NOT DISTINCT FROM NULLIF($2, '')
		ORDER BY created_at, snapshot_id
		LIMIT 1`, manifest, parentID).Scan(&snapshotID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeSnapshot{}, false, nil
	}
	if err != nil {
		return domain.KnowledgeSnapshot{}, false, err
	}
	snapshot, err := snapshotTx(ctx, tx, snapshotID)
	if err != nil {
		return domain.KnowledgeSnapshot{}, false, err
	}
	if !containsRevision(snapshot.Members, revisionID) {
		return domain.KnowledgeSnapshot{}, false, nil
	}
	return snapshot, true, nil
}

func (s *PostgresStore) PrepareKnowledgeRevisionPublish(ctx context.Context, revisionID string, key domain.KnowledgeMutationKey) (domain.KnowledgeLifecycleMutation, error) {
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
	snapshot, revision, err := preparePendingSnapshotTx(ctx, tx, revisionID)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	document, found, err := knowledgeDocumentTx(ctx, tx, revision.DocumentID, false)
	if err != nil || !found {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("prepare document lookup: %w", err)
	}
	result := lifecycleMutation(document, snapshot, domain.KnowledgeUpdateEvent{})
	if err := recordKnowledgeMutationTx(ctx, tx, key, result); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	return result, nil
}

func preparePendingSnapshotTx(ctx context.Context, tx *sql.Tx, revisionID string) (domain.KnowledgeSnapshot, domain.KnowledgeRevision, error) {
	activeID, err := lockActiveSnapshotTx(ctx, tx)
	if err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeRevision{}, err
	}
	revision, exists, err := knowledgeRevisionTx(ctx, tx, revisionID, true)
	if err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeRevision{}, err
	}
	if !exists {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeRevision{}, fmt.Errorf("%w: %s", ErrRevisionNotFound, revisionID)
	}
	if err := ensureRevisionPublishable(revision); err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeRevision{}, err
	}
	active, err := publishedActiveSnapshotTx(ctx, tx, activeID)
	if err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeRevision{}, err
	}
	members := successorMembers(active, revision)
	manifest := snapshotManifestSHA256(members)
	parentID := ""
	if active != nil {
		parentID = active.SnapshotID
	}
	if pending, found, err := findMatchingPendingTx(ctx, tx, parentID, manifest, revision.RevisionID); err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeRevision{}, err
	} else if found {
		if err := retireOtherPendingSnapshotsTx(ctx, tx, pending.SnapshotID); err != nil {
			return domain.KnowledgeSnapshot{}, domain.KnowledgeRevision{}, err
		}
		if err := attachAcceptanceTx(ctx, tx, &pending); err != nil {
			return domain.KnowledgeSnapshot{}, domain.KnowledgeRevision{}, err
		}
		return pending, revision, nil
	}
	if err := retireOtherPendingSnapshotsTx(ctx, tx, ""); err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeRevision{}, err
	}
	snapshot, err := insertSnapshotShellTx(ctx, tx, parentID, "pending_publish", manifest, members)
	if err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeRevision{}, err
	}
	if err := attachAcceptanceTx(ctx, tx, &snapshot); err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeRevision{}, err
	}
	return snapshot, revision, nil
}

func insertSnapshotShellTx(ctx context.Context, tx *sql.Tx, parentID, finalStatus, manifest string, members []domain.KnowledgeSnapshotMember) (domain.KnowledgeSnapshot, error) {
	nextSnapshot, err := nextCounterTx(ctx, tx, "snapshot")
	if err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	snapshotID := fmt.Sprintf("snap_%03d", nextSnapshot)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_snapshots (snapshot_id, parent_snapshot_id, status, manifest_sha256, created_at)
		VALUES ($1, NULLIF($2, ''), 'building', $3, $4)`, snapshotID, parentID, manifest, now); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO knowledge_snapshot_members (snapshot_id, revision_id, checksum_sha256)
			VALUES ($1, $2, $3)`, snapshotID, member.RevisionID, member.ChecksumSHA256); err != nil {
			return domain.KnowledgeSnapshot{}, err
		}
	}
	if finalStatus == "published" {
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_snapshots SET status = 'published', published_at = $2 WHERE snapshot_id = $1`, snapshotID, now); err != nil {
			return domain.KnowledgeSnapshot{}, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_snapshots SET status = $2 WHERE snapshot_id = $1`, snapshotID, finalStatus); err != nil {
			return domain.KnowledgeSnapshot{}, err
		}
	}
	return snapshotTx(ctx, tx, snapshotID)
}

func (s *PostgresStore) ActivateKnowledgeSnapshot(ctx context.Context, snapshotID string, key domain.KnowledgeMutationKey, acknowledgeUnvalidated bool) (domain.KnowledgeLifecycleMutation, error) {
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
	if _, err := lockActiveSnapshotTx(ctx, tx); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	snapshot, err := snapshotTx(ctx, tx, snapshotID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("%w: %s", ErrKnowledgeSnapshotNotFound, snapshotID)
	}
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if snapshot.Status != "pending_publish" {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("%w: %s is %s", ErrSnapshotNotPending, snapshotID, snapshot.Status)
	}
	result, err := activatePendingTx(ctx, tx, snapshot, key.ActorID, key.Key, acknowledgeUnvalidated)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if err := recordKnowledgeMutationTx(ctx, tx, key, result); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if s.publishHook != nil {
		if err := s.publishHook(publishHookBeforeCommit); err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	return result, nil
}

func (s *PostgresStore) publishOrActivateKnowledgeRevision(ctx context.Context, revisionID string, key domain.KnowledgeMutationKey, acknowledgeUnvalidated bool) (domain.KnowledgeLifecycleMutation, error) {
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
	activeID, err := lockActiveSnapshotTx(ctx, tx)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	revision, exists, err := knowledgeRevisionTx(ctx, tx, revisionID, true)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if !exists {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("%w: %s", ErrRevisionNotFound, revisionID)
	}
	if revision.Status == "published" && activeID.Valid {
		active, err := snapshotTx(ctx, tx, activeID.String)
		if err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
		if active.Status == "published" && containsRevision(active.Members, revisionID) {
			if err := attachAcceptanceTx(ctx, tx, &active); err != nil {
				return domain.KnowledgeLifecycleMutation{}, err
			}
			document, found, err := knowledgeDocumentTx(ctx, tx, revision.DocumentID, false)
			if err != nil || !found {
				return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("published document lookup: %w", err)
			}
			event, _, err := latestPublishedEventForRevisionTx(ctx, tx, revisionID)
			if err != nil {
				return domain.KnowledgeLifecycleMutation{}, err
			}
			result := lifecycleMutation(document, active, event)
			if err := recordKnowledgeMutationTx(ctx, tx, key, result); err != nil {
				return domain.KnowledgeLifecycleMutation{}, err
			}
			if err := tx.Commit(); err != nil {
				return domain.KnowledgeLifecycleMutation{}, err
			}
			return result, nil
		}
	}
	if err := ensureRevisionPublishable(revision); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	active, err := publishedActiveSnapshotTx(ctx, tx, activeID)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	members := successorMembers(active, revision)
	manifest := snapshotManifestSHA256(members)
	parentID := ""
	if active != nil {
		parentID = active.SnapshotID
	}
	if pending, found, err := findMatchingPendingTx(ctx, tx, parentID, manifest, revision.RevisionID); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	} else if found {
		result, err := activatePendingTx(ctx, tx, pending, key.ActorID, key.Key, acknowledgeUnvalidated)
		if err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
		if err := recordKnowledgeMutationTx(ctx, tx, key, result); err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
		if s.publishHook != nil {
			if err := s.publishHook(publishHookBeforeCommit); err != nil {
				return domain.KnowledgeLifecycleMutation{}, err
			}
		}
		if err := tx.Commit(); err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
		return result, nil
	}
	if !acknowledgeUnvalidated {
		return domain.KnowledgeLifecycleMutation{}, UnvalidatedAcknowledgementError{Message: "直接发布需要确认尚未完成上线验收"}
	}
	snapshot, event, err := publishReadyRevisionTx(ctx, tx, revision, active, key.ActorID, key.Key)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	document, found, err := knowledgeDocumentTx(ctx, tx, revision.DocumentID, false)
	if err != nil || !found {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("published document lookup: %w", err)
	}
	result := lifecycleMutation(document, snapshot, event)
	if err := recordKnowledgeMutationTx(ctx, tx, key, result); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if s.publishHook != nil {
		if err := s.publishHook(publishHookBeforeCommit); err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	return result, nil
}

func activatePendingTx(ctx context.Context, tx *sql.Tx, snapshot domain.KnowledgeSnapshot, actorID, correlationID string, acknowledgeUnvalidated bool) (domain.KnowledgeLifecycleMutation, error) {
	activeID, err := lockActiveSnapshotTx(ctx, tx)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	currentParent := ""
	if activeID.Valid {
		currentParent = activeID.String
	}
	if snapshot.ParentSnapshotID != currentParent {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("%w: %s", ErrStalePendingSnapshot, snapshot.SnapshotID)
	}
	if _, err := publishedActiveSnapshotTx(ctx, tx, activeID); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	fresh, err := snapshotTx(ctx, tx, snapshot.SnapshotID)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if fresh.Status != "pending_publish" {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("%w: %s is %s", ErrSnapshotNotPending, fresh.SnapshotID, fresh.Status)
	}
	primary, readyRevisions, err := readyRevisionsInSnapshotTx(ctx, tx, fresh)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	acceptance, err := deriveKnowledgeAcceptance(ctx, tx, fresh.SnapshotID, fresh.ManifestSHA256, fresh.Status)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if !acknowledgeUnvalidated {
		if err := acknowledgementRequired(acceptance); err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
	}
	for _, revision := range readyRevisions {
		if _, err := tx.ExecContext(ctx, `
			UPDATE knowledge_revisions
			SET status = 'superseded', updated_at = NOW()
			WHERE document_id = $1 AND revision_id <> $2 AND status = 'published'`, revision.DocumentID, revision.RevisionID); err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_revisions SET status = 'published', updated_at = NOW() WHERE revision_id = $1`, revision.RevisionID); err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if currentParent != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_snapshots SET status = 'retired' WHERE snapshot_id = $1`, currentParent); err != nil {
			return domain.KnowledgeLifecycleMutation{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_snapshots SET status = 'published', published_at = $2 WHERE snapshot_id = $1`, fresh.SnapshotID, now); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_active_snapshot SET snapshot_id = $1, lock_version = lock_version + 1 WHERE singleton = TRUE`, fresh.SnapshotID); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if err := retireOtherPendingSnapshotsTx(ctx, tx, fresh.SnapshotID); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	acceptance.DisplayLabel = domain.AcceptanceDisplayLabel("published", acceptance.Status)
	acceptance.RequiresUnvalidatedAcknowledgement = false
	event, err := insertKnowledgePublishEventTx(ctx, tx, primary.DocumentID, primary.RevisionID, fresh.SnapshotID, actorID, correlationID, acceptance.Status, acceptance.OnboardingRunID)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	if err := insertKnowledgeOutboxTx(ctx, tx, "knowledge.snapshot.published", primary.DocumentID, primary.RevisionID, fresh.SnapshotID, map[string]string{
		"snapshot_id":     fresh.SnapshotID,
		"revision_id":     primary.RevisionID,
		"manifest_sha256": fresh.ManifestSHA256,
	}); err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	published, err := snapshotTx(ctx, tx, fresh.SnapshotID)
	if err != nil {
		return domain.KnowledgeLifecycleMutation{}, err
	}
	published.Acceptance = &acceptance
	document, found, err := knowledgeDocumentTx(ctx, tx, primary.DocumentID, false)
	if err != nil || !found {
		return domain.KnowledgeLifecycleMutation{}, fmt.Errorf("activated document lookup: %w", err)
	}
	return lifecycleMutation(document, published, event), nil
}

func readyRevisionsInSnapshotTx(ctx context.Context, tx *sql.Tx, snapshot domain.KnowledgeSnapshot) (domain.KnowledgeRevision, []domain.KnowledgeRevision, error) {
	ready := []domain.KnowledgeRevision{}
	for _, member := range snapshot.Members {
		revision, exists, err := knowledgeRevisionTx(ctx, tx, member.RevisionID, true)
		if err != nil {
			return domain.KnowledgeRevision{}, nil, err
		}
		if !exists {
			return domain.KnowledgeRevision{}, nil, fmt.Errorf("%w: %s", ErrRevisionNotFound, member.RevisionID)
		}
		if revision.Status == "ready" {
			if err := ensureRevisionPublishable(revision); err != nil {
				return domain.KnowledgeRevision{}, nil, err
			}
			ready = append(ready, revision)
		}
	}
	if len(ready) == 0 {
		return domain.KnowledgeRevision{}, nil, fmt.Errorf("%w: snapshot %s has no ready revision", ErrRevisionNotReady, snapshot.SnapshotID)
	}
	return ready[len(ready)-1], ready, nil
}

func (s *PostgresStore) PublishKnowledgeRevision(ctx context.Context, revisionID, actorID, correlationID string) (domain.KnowledgeSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	defer tx.Rollback()
	activeID, err := lockActiveSnapshotTx(ctx, tx)
	if err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	revision, exists, err := knowledgeRevisionTx(ctx, tx, revisionID, true)
	if err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	if !exists {
		return domain.KnowledgeSnapshot{}, fmt.Errorf("%w: %s", ErrRevisionNotFound, revisionID)
	}
	active, err := publishedActiveSnapshotTx(ctx, tx, activeID)
	if err != nil && revision.Status != "published" {
		return domain.KnowledgeSnapshot{}, err
	}
	if revision.Status == "published" && active != nil && containsRevision(active.Members, revisionID) {
		if err := attachAcceptanceTx(ctx, tx, active); err != nil {
			return domain.KnowledgeSnapshot{}, err
		}
		if err := tx.Commit(); err != nil {
			return domain.KnowledgeSnapshot{}, err
		}
		return *active, nil
	}
	if err := ensureRevisionPublishable(revision); err != nil {
		return domain.KnowledgeSnapshot{}, err
	}
	if active == nil && activeID.Valid && activeID.String != "" {
		return domain.KnowledgeSnapshot{}, fmt.Errorf("%w: pointer targets unavailable snapshot", ErrActiveSnapshotUnavailable)
	}
	snapshot, _, err := publishReadyRevisionTx(ctx, tx, revision, active, actorID, correlationID)
	if err != nil {
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
	return snapshot, nil
}

func publishReadyRevisionTx(ctx context.Context, tx *sql.Tx, revision domain.KnowledgeRevision, active *domain.KnowledgeSnapshot, actorID, correlationID string) (domain.KnowledgeSnapshot, domain.KnowledgeUpdateEvent, error) {
	if err := retireOtherPendingSnapshotsTx(ctx, tx, ""); err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeUpdateEvent{}, err
	}
	members := successorMembers(active, revision)
	manifest := snapshotManifestSHA256(members)
	parentID := ""
	if active != nil {
		parentID = active.SnapshotID
	}
	snapshot, err := insertSnapshotShellTx(ctx, tx, parentID, "published", manifest, members)
	if err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeUpdateEvent{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_revisions
		SET status = 'superseded', updated_at = NOW()
		WHERE document_id = $1 AND revision_id <> $2 AND status = 'published'`, revision.DocumentID, revision.RevisionID); err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeUpdateEvent{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_revisions SET status = 'published', updated_at = NOW() WHERE revision_id = $1`, revision.RevisionID); err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeUpdateEvent{}, err
	}
	if active != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_snapshots SET status = 'retired' WHERE snapshot_id = $1`, active.SnapshotID); err != nil {
			return domain.KnowledgeSnapshot{}, domain.KnowledgeUpdateEvent{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE knowledge_active_snapshot SET snapshot_id = $1, lock_version = lock_version + 1 WHERE singleton = TRUE`, snapshot.SnapshotID); err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeUpdateEvent{}, err
	}
	acceptance, err := deriveKnowledgeAcceptance(ctx, tx, snapshot.SnapshotID, snapshot.ManifestSHA256, "published")
	if err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeUpdateEvent{}, err
	}
	event, err := insertKnowledgePublishEventTx(ctx, tx, revision.DocumentID, revision.RevisionID, snapshot.SnapshotID, actorID, correlationID, acceptance.Status, acceptance.OnboardingRunID)
	if err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeUpdateEvent{}, err
	}
	if err := insertKnowledgeOutboxTx(ctx, tx, "knowledge.snapshot.published", revision.DocumentID, revision.RevisionID, snapshot.SnapshotID, map[string]string{
		"snapshot_id":     snapshot.SnapshotID,
		"revision_id":     revision.RevisionID,
		"manifest_sha256": snapshot.ManifestSHA256,
	}); err != nil {
		return domain.KnowledgeSnapshot{}, domain.KnowledgeUpdateEvent{}, err
	}
	snapshot.Acceptance = &acceptance
	return snapshot, event, nil
}

func insertKnowledgePublishEventTx(ctx context.Context, tx *sql.Tx, documentID, revisionID, snapshotID, actorID, correlationID, acceptanceStatus, runID string) (domain.KnowledgeUpdateEvent, error) {
	next, err := nextCounterTx(ctx, tx, "event")
	if err != nil {
		return domain.KnowledgeUpdateEvent{}, err
	}
	event := domain.KnowledgeUpdateEvent{
		EventID:          fmt.Sprintf("event_%03d", next),
		EventType:        "published",
		DocumentID:       documentID,
		RevisionID:       revisionID,
		SnapshotID:       snapshotID,
		ActorID:          actorID,
		CorrelationID:    correlationID,
		AcceptanceStatus: acceptanceStatus,
		OnboardingRunID:  runID,
		OccurredAt:       time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_update_events (
			event_id, event_type, document_id, revision_id, snapshot_id, actor_id, correlation_id, occurred_at, acceptance_status, onboarding_run_id
		) VALUES ($1, 'published', NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7, NULLIF($8, ''), NULLIF($9, ''))`,
		event.EventID, event.DocumentID, event.RevisionID, event.SnapshotID, event.ActorID, event.CorrelationID, event.OccurredAt, event.AcceptanceStatus, event.OnboardingRunID,
	); err != nil {
		return domain.KnowledgeUpdateEvent{}, err
	}
	return event, nil
}

func latestPublishedEventForRevisionTx(ctx context.Context, tx *sql.Tx, revisionID string) (domain.KnowledgeUpdateEvent, bool, error) {
	var event domain.KnowledgeUpdateEvent
	var documentID, snapshotID, actorID, correlationID, acceptanceStatus, runID sql.NullString
	var occurredAt time.Time
	err := tx.QueryRowContext(ctx, `
		SELECT event_id, event_type, document_id, revision_id, snapshot_id, actor_id, correlation_id, occurred_at,
			acceptance_status, onboarding_run_id
		FROM knowledge_update_events
		WHERE revision_id = $1 AND event_type = 'published'
		ORDER BY occurred_at DESC, event_id DESC
		LIMIT 1`, revisionID).Scan(&event.EventID, &event.EventType, &documentID, &event.RevisionID, &snapshotID, &actorID, &correlationID, &occurredAt, &acceptanceStatus, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KnowledgeUpdateEvent{}, false, nil
	}
	if err != nil {
		return domain.KnowledgeUpdateEvent{}, false, err
	}
	event.DocumentID = nullableString(documentID)
	event.SnapshotID = nullableString(snapshotID)
	event.ActorID = nullableString(actorID)
	event.CorrelationID = nullableString(correlationID)
	event.AcceptanceStatus = nullableString(acceptanceStatus)
	event.OnboardingRunID = nullableString(runID)
	event.OccurredAt = occurredAt.UTC().Format(time.RFC3339)
	return event, true, nil
}

func attachAcceptanceTx(ctx context.Context, tx *sql.Tx, snapshot *domain.KnowledgeSnapshot) error {
	acceptance, err := deriveKnowledgeAcceptance(ctx, tx, snapshot.SnapshotID, snapshot.ManifestSHA256, snapshot.Status)
	if err != nil {
		return err
	}
	snapshot.Acceptance = &acceptance
	return nil
}

func lifecycleMutation(document domain.KnowledgeDocument, snapshot domain.KnowledgeSnapshot, event domain.KnowledgeUpdateEvent) domain.KnowledgeLifecycleMutation {
	result := domain.KnowledgeLifecycleMutation{Document: document, Snapshot: snapshotSummary(snapshot)}
	if event.EventID != "" {
		result.UpdateEvent = eventSummary(event)
	}
	return result
}

func (s *PostgresStore) attachSnapshotAcceptance(ctx context.Context, snapshot *domain.KnowledgeSnapshot) error {
	if snapshot == nil || snapshot.SnapshotID == "" {
		return nil
	}
	acceptance, err := deriveKnowledgeAcceptance(ctx, s.db, snapshot.SnapshotID, snapshot.ManifestSHA256, snapshot.Status)
	if err != nil {
		return err
	}
	snapshot.Acceptance = &acceptance
	return nil
}

func (s *PostgresStore) pendingSnapshotForDocument(ctx context.Context, documentID string) (*domain.KnowledgeSnapshot, error) {
	var snapshotID string
	err := s.db.QueryRowContext(ctx, `
		SELECT s.snapshot_id
		FROM knowledge_snapshots s
		JOIN knowledge_snapshot_members m ON m.snapshot_id = s.snapshot_id
		JOIN knowledge_revisions r ON r.revision_id = m.revision_id
		WHERE s.status = 'pending_publish' AND r.document_id = $1
		ORDER BY s.created_at DESC, s.snapshot_id DESC
		LIMIT 1`, documentID).Scan(&snapshotID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	snapshot, err := snapshotTx(ctx, tx, snapshotID)
	if err != nil {
		return nil, err
	}
	if err := attachAcceptanceTx(ctx, tx, &snapshot); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (s *PostgresStore) attachPublishedAcceptance(ctx context.Context, items []domain.KnowledgeUpdateCalendarItem) error {
	for index := range items {
		item := &items[index]
		if item.EventType != "published" || item.SnapshotID == "" {
			continue
		}
		var manifest, status string
		err := s.db.QueryRowContext(ctx, `SELECT manifest_sha256, status FROM knowledge_snapshots WHERE snapshot_id = $1`, item.SnapshotID).Scan(&manifest, &status)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		acceptance, err := deriveKnowledgeAcceptance(ctx, s.db, item.SnapshotID, manifest, status)
		if err != nil {
			return err
		}
		item.Acceptance = &acceptance
	}
	return nil
}

func (s *PostgresStore) attachDocumentPublicationState(ctx context.Context, items []domain.KnowledgeDocumentListItem) error {
	active, hasActive, err := s.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil {
		return err
	}
	if hasActive {
		if err := s.attachSnapshotAcceptance(ctx, &active); err != nil {
			return err
		}
	}
	for index := range items {
		if hasActive {
			for _, member := range active.Members {
				if member.DocumentID == items[index].DocumentID {
					items[index].Acceptance = active.Acceptance
					break
				}
			}
		}
		pending, err := s.pendingSnapshotForDocument(ctx, items[index].DocumentID)
		if err != nil {
			return err
		}
		items[index].PendingSnapshot = pending
	}
	return nil
}
