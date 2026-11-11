package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/domain"
)

func validAttemptState(state string) bool {
	switch state {
	case domain.ChatExecutionAttemptAccepted, domain.ChatExecutionAttemptRunning, domain.ChatExecutionAttemptSucceeded, domain.ChatExecutionAttemptFailed, domain.ChatExecutionAttemptCancelled:
		return true
	default:
		return false
	}
}

func terminalAttemptState(state string) bool {
	return state == domain.ChatExecutionAttemptSucceeded || state == domain.ChatExecutionAttemptFailed || state == domain.ChatExecutionAttemptCancelled
}

func nextAttemptStateAllowed(from, to string) bool {
	return (from == domain.ChatExecutionAttemptAccepted && to == domain.ChatExecutionAttemptRunning) ||
		(from == domain.ChatExecutionAttemptRunning && terminalAttemptState(to))
}

func validateNewAttempt(attempt domain.ChatExecutionAttempt) error {
	if strings.TrimSpace(attempt.AttemptID) == "" || strings.TrimSpace(attempt.RequestID) == "" || strings.TrimSpace(attempt.ConversationID) == "" || strings.TrimSpace(attempt.AssistantMessageID) == "" {
		return fmt.Errorf("attempt identity is required")
	}
	if attempt.State != domain.ChatExecutionAttemptAccepted {
		return fmt.Errorf("new attempt must be accepted")
	}
	if strings.TrimSpace(attempt.ConfigVersion) == "" || strings.TrimSpace(attempt.RetrievalConfigVersion) == "" {
		return fmt.Errorf("attempt config binding is required")
	}
	return nil
}

func (s *MemoryStore) CreateChatExecutionAttempt(_ context.Context, attempt domain.ChatExecutionAttempt) (domain.ChatExecutionAttempt, error) {
	if err := validateNewAttempt(attempt); err != nil {
		return domain.ChatExecutionAttempt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.attempts[attempt.AttemptID]; exists {
		return domain.ChatExecutionAttempt{}, fmt.Errorf("attempt already exists")
	}
	for _, item := range s.attempts {
		if item.ConversationID == attempt.ConversationID && item.RequestID == attempt.RequestID {
			return domain.ChatExecutionAttempt{}, fmt.Errorf("request already has an attempt")
		}
	}
	if attempt.AcceptedAt == "" {
		attempt.AcceptedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	s.attempts[attempt.AttemptID] = attempt
	return attempt, nil
}

func (s *MemoryStore) TransitionChatExecutionAttempt(_ context.Context, attemptID, state, errorCode, errorStage string, retryable bool) (domain.ChatExecutionAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt, exists := s.attempts[attemptID]
	if !exists {
		return domain.ChatExecutionAttempt{}, fmt.Errorf("attempt not found")
	}
	if !validAttemptState(state) || !nextAttemptStateAllowed(attempt.State, state) {
		return domain.ChatExecutionAttempt{}, fmt.Errorf("invalid attempt transition %s -> %s", attempt.State, state)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	attempt.State = state
	if state == domain.ChatExecutionAttemptRunning {
		attempt.StartedAt = now
	} else {
		attempt.TerminalAt = now
		attempt.ErrorCode = strings.TrimSpace(errorCode)
		attempt.ErrorStage = strings.TrimSpace(errorStage)
		attempt.Retryable = retryable
		if (state == domain.ChatExecutionAttemptFailed || state == domain.ChatExecutionAttemptCancelled) && (attempt.ErrorCode == "" || attempt.ErrorStage == "") {
			return domain.ChatExecutionAttempt{}, fmt.Errorf("failed attempt requires error code and stage")
		}
	}
	s.attempts[attemptID] = attempt
	return attempt, nil
}

func (s *MemoryStore) GetChatExecutionAttempt(_ context.Context, attemptID string) (domain.ChatExecutionAttempt, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	attempt, found := s.attempts[attemptID]
	return attempt, found, nil
}

func (s *PostgresStore) CreateChatExecutionAttempt(ctx context.Context, attempt domain.ChatExecutionAttempt) (domain.ChatExecutionAttempt, error) {
	if err := validateNewAttempt(attempt); err != nil {
		return domain.ChatExecutionAttempt{}, err
	}
	acceptedAt := time.Now().UTC()
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO chat_execution_attempts (
			attempt_id, request_id, parent_attempt_id, conversation_id, user_message_id, assistant_message_id,
			state, config_version, retrieval_config_version, knowledge_snapshot_id, knowledge_snapshot_manifest_hash, accepted_at
		) VALUES ($1,$2,NULLIF($3,''),$4,NULLIF($5,''),$6,$7,$8,$9,NULLIF($10,''),NULLIF($11,''),$12)
		RETURNING accepted_at`,
		attempt.AttemptID, attempt.RequestID, attempt.ParentAttemptID, attempt.ConversationID, attempt.UserMessageID, attempt.AssistantMessageID,
		attempt.State, attempt.ConfigVersion, attempt.RetrievalConfigVersion, attempt.KnowledgeSnapshotID, attempt.KnowledgeSnapshotManifestHash, acceptedAt,
	).Scan(&acceptedAt); err != nil {
		return domain.ChatExecutionAttempt{}, fmt.Errorf("insert chat execution attempt: %w", err)
	}
	attempt.AcceptedAt = acceptedAt.UTC().Format(time.RFC3339Nano)
	return attempt, nil
}

func (s *PostgresStore) TransitionChatExecutionAttempt(ctx context.Context, attemptID, state, errorCode, errorStage string, retryable bool) (domain.ChatExecutionAttempt, error) {
	if !validAttemptState(state) || state == domain.ChatExecutionAttemptAccepted {
		return domain.ChatExecutionAttempt{}, fmt.Errorf("invalid target attempt state %q", state)
	}
	if (state == domain.ChatExecutionAttemptFailed || state == domain.ChatExecutionAttemptCancelled) && (strings.TrimSpace(errorCode) == "" || strings.TrimSpace(errorStage) == "") {
		return domain.ChatExecutionAttempt{}, fmt.Errorf("failed attempt requires error code and stage")
	}
	var attempt domain.ChatExecutionAttempt
	var acceptedAt time.Time
	var startedAt, terminalAt sql.NullTime
	var parentID, userMessageID, snapshotID, manifestHash, code, stage sql.NullString
	err := s.db.QueryRowContext(ctx, `
		UPDATE chat_execution_attempts
		SET state = $2,
			started_at = CASE WHEN $2 = 'running' THEN NOW() ELSE started_at END,
			terminal_at = CASE WHEN $2 IN ('succeeded','failed','cancelled') THEN NOW() ELSE terminal_at END,
			error_code = CASE WHEN $2 IN ('failed','cancelled') THEN $3 ELSE NULL END,
			error_stage = CASE WHEN $2 IN ('failed','cancelled') THEN $4 ELSE NULL END,
			retryable = CASE WHEN $2 IN ('failed','cancelled') THEN $5 ELSE FALSE END
		WHERE attempt_id = $1
		  AND (($2 = 'running' AND state = 'accepted') OR ($2 IN ('succeeded','failed','cancelled') AND state = 'running'))
		RETURNING attempt_id, request_id, parent_attempt_id, conversation_id, user_message_id, assistant_message_id, state,
			config_version, retrieval_config_version, knowledge_snapshot_id, knowledge_snapshot_manifest_hash,
			error_code, error_stage, retryable, accepted_at, started_at, terminal_at`,
		attemptID, state, strings.TrimSpace(errorCode), strings.TrimSpace(errorStage), retryable,
	).Scan(&attempt.AttemptID, &attempt.RequestID, &parentID, &attempt.ConversationID, &userMessageID, &attempt.AssistantMessageID, &attempt.State,
		&attempt.ConfigVersion, &attempt.RetrievalConfigVersion, &snapshotID, &manifestHash, &code, &stage, &attempt.Retryable, &acceptedAt, &startedAt, &terminalAt)
	if err != nil {
		return domain.ChatExecutionAttempt{}, fmt.Errorf("transition chat execution attempt: %w", err)
	}
	attempt.ParentAttemptID, attempt.UserMessageID = parentID.String, userMessageID.String
	attempt.KnowledgeSnapshotID, attempt.KnowledgeSnapshotManifestHash = snapshotID.String, manifestHash.String
	attempt.ErrorCode, attempt.ErrorStage = code.String, stage.String
	attempt.AcceptedAt = acceptedAt.UTC().Format(time.RFC3339Nano)
	if startedAt.Valid {
		attempt.StartedAt = startedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	if terminalAt.Valid {
		attempt.TerminalAt = terminalAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return attempt, nil
}

func (s *PostgresStore) GetChatExecutionAttempt(ctx context.Context, attemptID string) (domain.ChatExecutionAttempt, bool, error) {
	var attempt domain.ChatExecutionAttempt
	var acceptedAt time.Time
	var startedAt, terminalAt sql.NullTime
	var parentID, userMessageID, snapshotID, manifestHash, code, stage sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT attempt_id, request_id, parent_attempt_id, conversation_id, user_message_id, assistant_message_id, state,
			config_version, retrieval_config_version, knowledge_snapshot_id, knowledge_snapshot_manifest_hash,
			error_code, error_stage, retryable, accepted_at, started_at, terminal_at
		FROM chat_execution_attempts WHERE attempt_id = $1`, attemptID,
	).Scan(&attempt.AttemptID, &attempt.RequestID, &parentID, &attempt.ConversationID, &userMessageID, &attempt.AssistantMessageID, &attempt.State,
		&attempt.ConfigVersion, &attempt.RetrievalConfigVersion, &snapshotID, &manifestHash, &code, &stage, &attempt.Retryable, &acceptedAt, &startedAt, &terminalAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChatExecutionAttempt{}, false, nil
	}
	if err != nil {
		return domain.ChatExecutionAttempt{}, false, fmt.Errorf("get chat execution attempt: %w", err)
	}
	attempt.ParentAttemptID, attempt.UserMessageID = parentID.String, userMessageID.String
	attempt.KnowledgeSnapshotID, attempt.KnowledgeSnapshotManifestHash = snapshotID.String, manifestHash.String
	attempt.ErrorCode, attempt.ErrorStage = code.String, stage.String
	attempt.AcceptedAt = acceptedAt.UTC().Format(time.RFC3339Nano)
	if startedAt.Valid {
		attempt.StartedAt = startedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	if terminalAt.Valid {
		attempt.TerminalAt = terminalAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return attempt, true, nil
}

// completeChatExecutionAttemptTx joins a visible successful answer to its
// running attempt in the same transaction. Direct repository fixtures may not
// have created an attempt, so absence is intentionally a no-op; an existing
// non-running attempt is an integrity violation and aborts the answer write.
func completeChatExecutionAttemptTx(ctx context.Context, tx *sql.Tx, requestID string) error {
	attemptID := "attempt_" + strings.TrimSpace(requestID)
	if attemptID == "attempt_" {
		return nil
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE chat_execution_attempts
		SET state = 'succeeded', terminal_at = NOW(), error_code = NULL, error_stage = NULL, retryable = FALSE
		WHERE attempt_id = $1 AND state = 'running'`, attemptID)
	if err != nil {
		return fmt.Errorf("complete chat execution attempt: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected > 0 {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chat_execution_attempts WHERE attempt_id = $1)`, attemptID).Scan(&exists); err != nil {
		return fmt.Errorf("check chat execution attempt: %w", err)
	}
	if exists {
		return fmt.Errorf("chat execution attempt is not running")
	}
	return nil
}
