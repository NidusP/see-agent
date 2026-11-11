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
	ErrKnowledgeSnapshotCitationMismatch = errors.New("knowledge snapshot citation mismatch")
	ErrClarificationStateConflict        = errors.New("clarification state conflict")
)

// ResolveActiveKnowledgeScope freezes the active published snapshot once for a
// chat request. It intentionally never reads the pointer again during answer
// persistence, so a concurrent publish can only affect the next request.
func (s *PostgresStore) ResolveActiveKnowledgeScope(ctx context.Context) (domain.RuntimeKnowledgeScope, error) {
	snapshot, found, err := s.GetActivePublishedKnowledgeSnapshot(ctx)
	if err != nil {
		return domain.RuntimeKnowledgeScope{}, fmt.Errorf("read active snapshot: %w", err)
	}
	if !found {
		return domain.RuntimeKnowledgeScope{}, fmt.Errorf("%w: no active published snapshot", ErrActiveSnapshotUnavailable)
	}
	if snapshot.Status != "published" || strings.TrimSpace(snapshot.ManifestSHA256) == "" || len(snapshot.Members) == 0 {
		return domain.RuntimeKnowledgeScope{}, fmt.Errorf("%w: active snapshot is incomplete", ErrActiveSnapshotUnavailable)
	}
	if snapshot.ManifestSHA256 != snapshotManifestSHA256(snapshot.Members) {
		return domain.RuntimeKnowledgeScope{}, fmt.Errorf("%w: active snapshot manifest does not match members", ErrActiveSnapshotUnavailable)
	}

	revisionIDs := make([]string, 0, len(snapshot.Members))
	seen := make(map[string]struct{}, len(snapshot.Members))
	for _, member := range snapshot.Members {
		revisionID := strings.TrimSpace(member.RevisionID)
		if revisionID == "" {
			return domain.RuntimeKnowledgeScope{}, fmt.Errorf("%w: active snapshot has empty revision member", ErrActiveSnapshotUnavailable)
		}
		if _, exists := seen[revisionID]; exists {
			return domain.RuntimeKnowledgeScope{}, fmt.Errorf("%w: active snapshot has duplicate revision member", ErrActiveSnapshotUnavailable)
		}
		seen[revisionID] = struct{}{}
		revisionIDs = append(revisionIDs, revisionID)
	}

	return domain.RuntimeKnowledgeScope{
		SnapshotID:   snapshot.SnapshotID,
		ManifestHash: snapshot.ManifestSHA256,
		RevisionIDs:  revisionIDs,
		ResolvedAt:   time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// AppendScopedRuntimeExchange writes a K5 response and all evidence together.
// The caller has already checked Runtime identity and scope membership; this
// method only uses the passed immutable scope and never consults the active
// snapshot pointer.
func (s *PostgresStore) AppendScopedRuntimeExchange(ctx context.Context, conversationID, userMessageID, content string, scope domain.RuntimeKnowledgeScope, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, error) {
	return s.appendScopedRuntimeExchange(ctx, conversationID, userMessageID, content, scope, nil, result)
}

// AppendScopedRuntimeExchangeWithPending atomically persists an A1 response
// against the exact pending state supplied to Runtime before its model call.
func (s *PostgresStore) AppendScopedRuntimeExchangeWithPending(ctx context.Context, conversationID, userMessageID, content string, scope domain.RuntimeKnowledgeScope, expectedPending *domain.RuntimePendingClarification, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, error) {
	return s.appendScopedRuntimeExchange(ctx, conversationID, userMessageID, content, scope, expectedPending, result)
}

func (s *PostgresStore) appendScopedRuntimeExchange(ctx context.Context, conversationID, userMessageID, content string, scope domain.RuntimeKnowledgeScope, expectedPending *domain.RuntimePendingClarification, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, error) {
	if strings.TrimSpace(scope.SnapshotID) == "" || strings.TrimSpace(scope.ManifestHash) == "" || len(scope.RevisionIDs) == 0 {
		return domain.Message{}, domain.Message{}, nil, fmt.Errorf("%w: scope is incomplete", ErrActiveSnapshotUnavailable)
	}
	if strings.TrimSpace(result.MessageID) == "" {
		return domain.Message{}, domain.Message{}, nil, fmt.Errorf("runtime result message_id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}
	defer tx.Rollback()

	conversation, err := conversationForUpdateTx(ctx, tx, conversationID)
	if err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}
	if err := validateSnapshotCitationAuthorityTx(ctx, tx, scope, result.Citations, result.RetrievalCandidates); err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}
	persistedAt := time.Now().UTC().Truncate(time.Microsecond)
	now := persistedAt.Format(time.RFC3339Nano)
	userMessageID = strings.TrimSpace(userMessageID)
	if userMessageID == "" {
		next, err := nextCounterTx(ctx, tx, "message")
		if err != nil {
			return domain.Message{}, domain.Message{}, nil, err
		}
		userMessageID = fmt.Sprintf("msg_%03d", next)
	}
	userMessage := domain.Message{
		MessageID:      userMessageID,
		ConversationID: conversationID,
		Role:           "user",
		Content:        content,
		Status:         "completed",
		CreatedAt:      now,
		Intent:         normalizedIntent(result.Intent),
		KnowledgeUsed:  false,
	}
	if err := insertMessageTx(ctx, tx, userMessage); err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}

	finishReason := strings.TrimSpace(result.FinishReason)
	assistantContent := strings.TrimSpace(result.AssistantMessage.Content)
	answeredAt := coalesceTimestamp(result.CompletedAt)
	assistantCreatedAt := persistedAt.Add(time.Microsecond).Format(time.RFC3339Nano)
	persistUserProvenance := !(isA1PolicyRecord(result) && result.AnswerDisposition == "clarification_required")
	clarificationID, err := prepareClarificationPersistenceTx(ctx, tx, conversationID, expectedPending, result)
	if err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}
	assistantMessage := domain.Message{
		MessageID:                     result.MessageID,
		ConversationID:                conversationID,
		Role:                          "assistant",
		Content:                       assistantContent,
		Status:                        "completed",
		CreatedAt:                     assistantCreatedAt,
		FinishReason:                  &finishReason,
		CitationsCount:                len(result.Citations),
		KnowledgeSnapshotID:           conditionalString(persistUserProvenance, scope.SnapshotID),
		KnowledgeSnapshotManifestHash: conditionalString(persistUserProvenance, scope.ManifestHash),
		AnsweredAt:                    &answeredAt,
		FreshnessStatus:               conditionalString(persistUserProvenance, "current"),
		CitationAnchors:               result.CitationAnchors,
		Intent:                        normalizedIntent(result.Intent),
		KnowledgeUsed:                 result.KnowledgeUsed,
		ClarificationID:               clarificationID,
	}
	applyA1MessageProjection(&assistantMessage, result)
	if err := insertMessageTx(ctx, tx, assistantMessage); err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}
	if persistUserProvenance {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_knowledge_provenance (
				message_id, knowledge_snapshot_id, knowledge_snapshot_manifest_sha256, answered_at, freshness_status
			) VALUES ($1, $2, $3, $4::timestamptz, 'current')`,
			assistantMessage.MessageID, scope.SnapshotID, scope.ManifestHash, answeredAt,
		); err != nil {
			return domain.Message{}, domain.Message{}, nil, err
		}
	}

	citations := make([]domain.Citation, 0, len(result.Citations))
	for _, item := range result.Citations {
		citation := domain.Citation{
			MessageID:           assistantMessage.MessageID,
			ChunkID:             item.ChunkID,
			FileID:              item.FileID,
			DocumentID:          item.DocumentID,
			RevisionID:          item.RevisionID,
			ChunkContentSHA256:  item.ChunkContentSHA256,
			KnowledgeSnapshotID: scope.SnapshotID,
			Filename:            item.Filename,
			Rank:                item.Rank,
			Score:               item.Score,
			CitationLabel:       item.CitationLabel,
			SourceLocator:       item.SourceLocator,
			Snippet:             item.Snippet,
		}
		if err := insertCitationTx(ctx, tx, citation); err != nil {
			return domain.Message{}, domain.Message{}, nil, err
		}
		citations = append(citations, citation)
	}
	if err := insertCitationAnchorsTx(ctx, tx, assistantMessage.MessageID, result.CitationAnchors); err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}
	if err := insertExecutionEvidenceTx(ctx, tx, conversation, assistantMessage, &scope, result); err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}
	if err := insertRetrievalCandidatesTx(ctx, tx, assistantMessage.MessageID, result.RetrievalCandidates); err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}
	if err := applyClarificationPersistenceTx(ctx, tx, expectedPending, result, userMessage, assistantMessage, persistedAt); err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}
	applyAutomaticConversationTitle(&conversation, content)
	conversation.UpdatedAt = now
	if err := updateConversationTitleAndTimestampTx(ctx, tx, conversation); err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}
	if err := completeChatExecutionAttemptTx(ctx, tx, result.RequestID); err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Message{}, domain.Message{}, nil, err
	}
	return userMessage, assistantMessage, citations, nil
}

func (s *PostgresStore) GetPendingClarification(ctx context.Context, conversationID string) (domain.RuntimePendingClarification, bool, error) {
	var item domain.RuntimePendingClarification
	var missingDimensions []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT clarification_id, state_version, original_user_message_id, original_query, missing_dimensions, question, turn_count
		FROM conversation_clarifications
		WHERE conversation_id = $1 AND status = 'pending'`, conversationID).Scan(
		&item.ClarificationID, &item.StateVersion, &item.OriginalUserMessageID, &item.OriginalQuery,
		&missingDimensions, &item.Question, &item.TurnCount,
	)
	if err == sql.ErrNoRows {
		return domain.RuntimePendingClarification{}, false, nil
	}
	if err != nil {
		return domain.RuntimePendingClarification{}, false, err
	}
	if err := json.Unmarshal(missingDimensions, &item.MissingDimensions); err != nil {
		return domain.RuntimePendingClarification{}, false, fmt.Errorf("decode pending clarification dimensions: %w", err)
	}
	return item, true, nil
}

func isA1PolicyRecord(result domain.ChatRuntimeResponse) bool {
	return result.PolicyEvaluationPerformed != nil
}

func conditionalString(condition bool, value string) string {
	if condition {
		return value
	}
	return ""
}

func prepareClarificationPersistenceTx(ctx context.Context, tx *sql.Tx, conversationID string, expected *domain.RuntimePendingClarification, result domain.ChatRuntimeResponse) (string, error) {
	if !isA1PolicyRecord(result) {
		if expected != nil {
			return "", fmt.Errorf("%w: legacy Runtime response omitted policy result", ErrClarificationStateConflict)
		}
		return "", nil
	}
	current, found, err := pendingClarificationForUpdateTx(ctx, tx, conversationID)
	if err != nil {
		return "", err
	}
	if expected == nil {
		if found || result.Resolution != nil {
			return "", fmt.Errorf("%w: request had no matching pending clarification", ErrClarificationStateConflict)
		}
		if result.Clarification == nil {
			return "", nil
		}
		return nextClarificationIDTx(ctx, tx)
	}
	if !found || current.ClarificationID != expected.ClarificationID || current.StateVersion != expected.StateVersion {
		return "", fmt.Errorf("%w: pending clarification changed before writeback", ErrClarificationStateConflict)
	}
	resolution := result.Resolution
	if resolution == nil || resolution.PendingClarificationID != expected.ClarificationID || resolution.PendingStateVersion != expected.StateVersion {
		return "", fmt.Errorf("%w: Runtime resolution does not match request pending state", ErrClarificationStateConflict)
	}
	switch resolution.Action {
	case "resolved":
		if result.Clarification != nil {
			return "", fmt.Errorf("%w: resolved clarification cannot create another question", ErrClarificationStateConflict)
		}
		return "", nil
	case "needs_more":
		if result.Clarification == nil {
			return "", fmt.Errorf("%w: needs_more requires clarification content", ErrClarificationStateConflict)
		}
		return expected.ClarificationID, nil
	case "abandoned":
		if result.Clarification == nil {
			return "", nil
		}
		return nextClarificationIDTx(ctx, tx)
	default:
		return "", fmt.Errorf("%w: unsupported clarification resolution action", ErrClarificationStateConflict)
	}
}

func pendingClarificationForUpdateTx(ctx context.Context, tx *sql.Tx, conversationID string) (domain.RuntimePendingClarification, bool, error) {
	var item domain.RuntimePendingClarification
	var missingDimensions []byte
	err := tx.QueryRowContext(ctx, `
		SELECT clarification_id, state_version, original_user_message_id, original_query, missing_dimensions, question, turn_count
		FROM conversation_clarifications
		WHERE conversation_id = $1 AND status = 'pending'
		FOR UPDATE`, conversationID).Scan(
		&item.ClarificationID, &item.StateVersion, &item.OriginalUserMessageID, &item.OriginalQuery,
		&missingDimensions, &item.Question, &item.TurnCount,
	)
	if err == sql.ErrNoRows {
		return domain.RuntimePendingClarification{}, false, nil
	}
	if err != nil {
		return domain.RuntimePendingClarification{}, false, err
	}
	if err := json.Unmarshal(missingDimensions, &item.MissingDimensions); err != nil {
		return domain.RuntimePendingClarification{}, false, fmt.Errorf("decode pending clarification dimensions: %w", err)
	}
	return item, true, nil
}

func nextClarificationIDTx(ctx context.Context, tx *sql.Tx) (string, error) {
	next, err := nextCounterTx(ctx, tx, "clarification")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("clrf_%03d", next), nil
}

func applyClarificationPersistenceTx(ctx context.Context, tx *sql.Tx, expected *domain.RuntimePendingClarification, result domain.ChatRuntimeResponse, user, assistant domain.Message, persistedAt time.Time) error {
	if !isA1PolicyRecord(result) {
		return nil
	}
	now := persistedAt.Format(time.RFC3339Nano)
	if expected == nil {
		if result.Clarification == nil {
			return nil
		}
		return insertPendingClarificationTx(ctx, tx, assistant.ClarificationID, user, assistant, result.Clarification, now)
	}
	resolution := result.Resolution
	if resolution == nil {
		return fmt.Errorf("%w: pending request has no Runtime resolution", ErrClarificationStateConflict)
	}
	var resultWrite sql.Result
	var err error
	switch resolution.Action {
	case "resolved":
		resultWrite, err = tx.ExecContext(ctx, `
			UPDATE conversation_clarifications
			SET status = 'resolved', state_version = state_version + 1, resolution_user_message_id = $4,
				resolved_query = $5, terminal_reason = 'resolved', updated_at = $6::timestamptz, resolved_at = $6::timestamptz
			WHERE clarification_id = $1 AND conversation_id = $2 AND status = 'pending' AND state_version = $3`,
			expected.ClarificationID, user.ConversationID, expected.StateVersion, user.MessageID, resolution.ResolvedQuery, now)
	case "abandoned":
		resultWrite, err = tx.ExecContext(ctx, `
			UPDATE conversation_clarifications
			SET status = 'abandoned', state_version = state_version + 1, resolution_user_message_id = $4,
				terminal_reason = 'abandoned', updated_at = $5::timestamptz, resolved_at = $5::timestamptz
			WHERE clarification_id = $1 AND conversation_id = $2 AND status = 'pending' AND state_version = $3`,
			expected.ClarificationID, user.ConversationID, expected.StateVersion, user.MessageID, now)
	case "needs_more":
		if expected.TurnCount >= 2 {
			resultWrite, err = tx.ExecContext(ctx, `
				UPDATE conversation_clarifications
				SET status = 'expired', state_version = state_version + 1, resolution_user_message_id = $4,
					terminal_reason = 'clarification_turn_limit', updated_at = $5::timestamptz, resolved_at = $5::timestamptz
				WHERE clarification_id = $1 AND conversation_id = $2 AND status = 'pending' AND state_version = $3`,
				expected.ClarificationID, user.ConversationID, expected.StateVersion, user.MessageID, now)
		} else {
			if result.Clarification == nil {
				return fmt.Errorf("%w: needs_more omitted clarification content", ErrClarificationStateConflict)
			}
			dimensions, marshalErr := json.Marshal(result.Clarification.MissingDimensions)
			if marshalErr != nil {
				return marshalErr
			}
			resultWrite, err = tx.ExecContext(ctx, `
				UPDATE conversation_clarifications
				SET assistant_message_id = $4, missing_dimensions = $5::jsonb, question = $6,
					state_version = state_version + 1, turn_count = turn_count + 1, resolution_user_message_id = $7,
					updated_at = $8::timestamptz
				WHERE clarification_id = $1 AND conversation_id = $2 AND status = 'pending' AND state_version = $3`,
				expected.ClarificationID, user.ConversationID, expected.StateVersion, assistant.MessageID, dimensions, result.Clarification.Question, user.MessageID, now)
		}
	default:
		return fmt.Errorf("%w: unsupported clarification resolution action", ErrClarificationStateConflict)
	}
	if err != nil {
		return err
	}
	rows, err := resultWrite.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%w: pending clarification no longer matches", ErrClarificationStateConflict)
	}
	if resolution.Action == "abandoned" && result.Clarification != nil {
		return insertPendingClarificationTx(ctx, tx, assistant.ClarificationID, user, assistant, result.Clarification, now)
	}
	return nil
}

func insertPendingClarificationTx(ctx context.Context, tx *sql.Tx, clarificationID string, user, assistant domain.Message, clarification *domain.RuntimeClarification, now string) error {
	if strings.TrimSpace(clarificationID) == "" || clarification == nil || len(clarification.MissingDimensions) == 0 || strings.TrimSpace(clarification.Question) == "" {
		return fmt.Errorf("%w: incomplete new clarification", ErrClarificationStateConflict)
	}
	dimensions, err := json.Marshal(clarification.MissingDimensions)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO conversation_clarifications (
			clarification_id, conversation_id, original_user_message_id, original_query, assistant_message_id,
			missing_dimensions, question, status, state_version, turn_count, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, 'pending', 1, 1, $8::timestamptz, $8::timestamptz)`,
		clarificationID, user.ConversationID, user.MessageID, user.Content, assistant.MessageID, dimensions, clarification.Question, now)
	return err
}

func insertRetrievalCandidatesTx(ctx context.Context, tx *sql.Tx, messageID string, candidates []domain.RuntimeRetrievalCandidate) error {
	for _, candidate := range candidates {
		locator, err := json.Marshal(candidate.SourceLocator)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO message_retrieval_candidates (
				message_id, rank, chunk_id, file_id, document_id, revision_id, chunk_content_sha256,
				knowledge_snapshot_id, score, source_locator
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)`,
			messageID, candidate.Rank, candidate.ChunkID, candidate.FileID, candidate.DocumentID,
			candidate.RevisionID, candidate.ChunkContentSHA256, candidate.KnowledgeSnapshotID,
			candidate.Score, locator)
		if err != nil {
			return err
		}
	}
	return nil
}

// insertExecutionEvidenceTx is deliberately called from the K5 message
// transaction. A visible scoped answer therefore cannot be separated from
// the immutable runtime/config observation that explains it.
func insertExecutionEvidenceTx(ctx context.Context, tx *sql.Tx, conversation domain.Conversation, assistant domain.Message, scope *domain.RuntimeKnowledgeScope, result domain.ChatRuntimeResponse) error {
	var agent domain.AgentConfig
	if err := scanAgentConfig(tx.QueryRowContext(ctx, `
		SELECT config_version, model_id, system_prompt_version, response_style, temperature, top_p, max_output_tokens, COALESCE(answer_policy_version, 'legacy_unknown'), created_at
		FROM agent_configs WHERE config_version = $1`, conversation.ConfigVersion), &agent); err != nil {
		return fmt.Errorf("read execution agent config: %w", err)
	}
	var retrieval domain.RetrievalConfig
	if err := scanRetrievalConfig(tx.QueryRowContext(ctx, `
		SELECT retrieval_config_version, retrieval_strategy, embedding_model, top_k, rerank_enabled, evidence_min_score, created_at
		FROM retrieval_configs WHERE retrieval_config_version = $1`, conversation.RetrievalConfigVersion), &retrieval); err != nil {
		return fmt.Errorf("read execution retrieval config: %w", err)
	}
	generationConfig, err := json.Marshal(map[string]any{
		"system_prompt_version": agent.SystemPromptVersion,
		"answer_policy_version": agent.AnswerPolicyVersion,
		"response_style":        agent.ResponseStyle,
		"temperature":           agent.Temperature,
		"top_p":                 agent.TopP,
		"max_output_tokens":     agent.MaxOutputTokens,
	})
	if err != nil {
		return err
	}
	retrievalConfig, err := json.Marshal(map[string]any{
		"retrieval_strategy": retrieval.RetrievalStrategy,
		"embedding_model":    retrieval.EmbeddingModel,
		"top_k":              retrieval.TopK,
		"rerank_enabled":     retrieval.RerankEnabled,
		"evidence_min_score": retrieval.EvidenceMinScore,
	})
	if err != nil {
		return err
	}
	var usage any
	if result.Usage != nil {
		usage = result.Usage
	}
	usagePayload, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	var execution any
	if result.Execution != nil {
		execution = result.Execution
	}
	executionPayload, err := json.Marshal(execution)
	if err != nil {
		return err
	}
	var contextResolution any
	if result.ContextResolution != nil {
		contextResolution = result.ContextResolution
	}
	contextResolutionPayload, err := json.Marshal(contextResolution)
	if err != nil {
		return err
	}
	usedRanksPayload, err := json.Marshal(result.UsedCitationRanks)
	if err != nil {
		return err
	}
	clarificationPayload, err := marshalOptionalJSON(result.Clarification)
	if err != nil {
		return err
	}
	coveragePayload, err := marshalOptionalJSON(result.Coverage)
	if err != nil {
		return err
	}
	resolutionPayload, err := marshalOptionalJSON(result.Resolution)
	if err != nil {
		return err
	}
	answerDisposition := ""
	answerPolicyVersion := ""
	if result.PolicyEvaluationPerformed != nil {
		answerDisposition = result.AnswerDisposition
		answerPolicyVersion = result.AnswerPolicyVersion
	}
	finishReason := ""
	if assistant.FinishReason != nil {
		finishReason = *assistant.FinishReason
	}
	completedAt := ""
	if assistant.AnsweredAt != nil {
		completedAt = *assistant.AnsweredAt
	}
	var snapshotID any
	if scope != nil && strings.TrimSpace(scope.SnapshotID) != "" {
		snapshotID = scope.SnapshotID
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO message_execution_evidence (
			message_id, conversation_id, request_id, config_version, retrieval_config_version,
			knowledge_snapshot_id, model_id, generation_config, retrieval_config, usage,
			refusal, finish_reason, completed_at, intent, retrieval_performed, generation_performed, knowledge_used,
			answer_disposition, answer_policy_version, policy_evaluation_performed, answer_generation_performed,
			clarification, coverage, clarification_resolution, resolved_query,
			execution_observation, used_citation_ranks, context_resolution
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, NULLIF($10::jsonb, 'null'::jsonb), $11, $12, $13::timestamptz, $14, $15, $16, $17,
			NULLIF($18, ''), NULLIF($19, ''), $20, $21, NULLIF($22::jsonb, 'null'::jsonb), NULLIF($23::jsonb, 'null'::jsonb), NULLIF($24::jsonb, 'null'::jsonb), NULLIF($25, ''),
			NULLIF($26::jsonb, 'null'::jsonb), $27::jsonb, NULLIF($28::jsonb, 'null'::jsonb))`,
		assistant.MessageID, conversation.ConversationID, result.RequestID, conversation.ConfigVersion,
		conversation.RetrievalConfigVersion, snapshotID, conversation.ModelID, generationConfig,
		retrievalConfig, usagePayload, result.Refusal, finishReason, completedAt, normalizedIntent(result.Intent),
		result.RetrievalPerformed, result.GenerationPerformed, result.KnowledgeUsed,
		answerDisposition, answerPolicyVersion, result.PolicyEvaluationPerformed, result.AnswerGenerationPerformed,
		clarificationPayload, coveragePayload, resolutionPayload, result.ResolvedQuery,
		executionPayload, usedRanksPayload, contextResolutionPayload,
	)
	return err
}

func applyA1MessageProjection(message *domain.Message, result domain.ChatRuntimeResponse) {
	if result.PolicyEvaluationPerformed == nil {
		return
	}
	message.AnswerDisposition = result.AnswerDisposition
	message.AnswerPolicyVersion = result.AnswerPolicyVersion
	message.PolicyEvaluationPerformed = result.PolicyEvaluationPerformed
	message.AnswerGenerationPerformed = result.AnswerGenerationPerformed
	message.Coverage = result.Coverage
}

func marshalOptionalJSON(value any) ([]byte, error) {
	if value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(value)
}

// AppendControlledRuntimeExchange persists a non-knowledge answer and its
// execution evidence in one transaction. It intentionally writes no snapshot,
// provenance, citation or anchor rows.
func (s *PostgresStore) AppendControlledRuntimeExchange(ctx context.Context, conversationID, userMessageID, content string, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Message{}, domain.Message{}, err
	}
	defer tx.Rollback()
	conversation, err := conversationForUpdateTx(ctx, tx, conversationID)
	if err != nil {
		return domain.Message{}, domain.Message{}, err
	}
	persistedAt := time.Now().UTC().Truncate(time.Microsecond)
	userMessageID = strings.TrimSpace(userMessageID)
	if userMessageID == "" {
		next, err := nextCounterTx(ctx, tx, "message")
		if err != nil {
			return domain.Message{}, domain.Message{}, err
		}
		userMessageID = fmt.Sprintf("msg_%03d", next)
	}
	intent := normalizedIntent(result.Intent)
	user := domain.Message{MessageID: userMessageID, ConversationID: conversationID, Role: "user", Content: content, Status: "completed", CreatedAt: persistedAt.Format(time.RFC3339Nano), Intent: intent, KnowledgeUsed: false}
	if err := insertMessageTx(ctx, tx, user); err != nil {
		return domain.Message{}, domain.Message{}, err
	}
	finishReason := strings.TrimSpace(result.FinishReason)
	if finishReason == "" {
		finishReason = "stop"
	}
	answeredAt := coalesceTimestamp(result.CompletedAt)
	assistant := domain.Message{
		MessageID: result.MessageID, ConversationID: conversationID, Role: "assistant",
		Content: strings.TrimSpace(result.AssistantMessage.Content), Status: "completed",
		CreatedAt: persistedAt.Add(time.Microsecond).Format(time.RFC3339Nano), FinishReason: &finishReason,
		AnsweredAt: &answeredAt, Intent: intent, KnowledgeUsed: false,
	}
	applyA1MessageProjection(&assistant, result)
	if assistant.Content == "" {
		return domain.Message{}, domain.Message{}, errors.New("controlled runtime answer is empty")
	}
	if err := insertMessageTx(ctx, tx, assistant); err != nil {
		return domain.Message{}, domain.Message{}, err
	}
	if err := insertExecutionEvidenceTx(ctx, tx, conversation, assistant, nil, result); err != nil {
		return domain.Message{}, domain.Message{}, err
	}
	applyAutomaticConversationTitle(&conversation, content)
	conversation.UpdatedAt = persistedAt.Format(time.RFC3339Nano)
	if err := updateConversationTitleAndTimestampTx(ctx, tx, conversation); err != nil {
		return domain.Message{}, domain.Message{}, err
	}
	if err := completeChatExecutionAttemptTx(ctx, tx, result.RequestID); err != nil {
		return domain.Message{}, domain.Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Message{}, domain.Message{}, err
	}
	return user, assistant, nil
}

// validateSnapshotCitationAuthorityTx protects against a Runtime response that
// pairs an allowed revision id with a document id from another logical source.
// Retired snapshots remain valid here: a request may have frozen A while a
// concurrent publish promotes B, and A is still the historical evidence.
func validateSnapshotCitationAuthorityTx(ctx context.Context, tx *sql.Tx, scope domain.RuntimeKnowledgeScope, citations []domain.RuntimeCitation, candidates []domain.RuntimeRetrievalCandidate) error {
	var manifestHash string
	if err := tx.QueryRowContext(ctx, `
		SELECT manifest_sha256
		FROM knowledge_snapshots
		WHERE snapshot_id = $1`, scope.SnapshotID).Scan(&manifestHash); err != nil {
		return fmt.Errorf("%w: snapshot %q is not readable: %v", ErrKnowledgeSnapshotCitationMismatch, scope.SnapshotID, err)
	}
	if manifestHash != scope.ManifestHash {
		return fmt.Errorf("%w: snapshot manifest changed", ErrKnowledgeSnapshotCitationMismatch)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT member.revision_id, revision.document_id
		FROM knowledge_snapshot_members member
		JOIN knowledge_revisions revision ON revision.revision_id = member.revision_id
		WHERE member.snapshot_id = $1`, scope.SnapshotID)
	if err != nil {
		return err
	}
	defer rows.Close()
	authoritativeDocuments := make(map[string]string, len(scope.RevisionIDs))
	for rows.Next() {
		var revisionID, documentID string
		if err := rows.Scan(&revisionID, &documentID); err != nil {
			return err
		}
		authoritativeDocuments[revisionID] = documentID
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(authoritativeDocuments) == 0 {
		return fmt.Errorf("%w: snapshot has no members", ErrKnowledgeSnapshotCitationMismatch)
	}
	for _, revisionID := range scope.RevisionIDs {
		if _, ok := authoritativeDocuments[revisionID]; !ok {
			return fmt.Errorf("%w: request scope revision %q is not a snapshot member", ErrKnowledgeSnapshotCitationMismatch, revisionID)
		}
	}
	for _, citation := range citations {
		documentID, ok := authoritativeDocuments[citation.RevisionID]
		if !ok || documentID != citation.DocumentID {
			return fmt.Errorf("%w: citation document %q does not belong to revision %q", ErrKnowledgeSnapshotCitationMismatch, citation.DocumentID, citation.RevisionID)
		}
	}
	seenRanks := make(map[int]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.Rank < 1 || strings.TrimSpace(candidate.ChunkID) == "" || strings.TrimSpace(candidate.FileID) == "" || strings.TrimSpace(candidate.DocumentID) == "" || strings.TrimSpace(candidate.RevisionID) == "" || strings.TrimSpace(candidate.ChunkContentSHA256) == "" || candidate.KnowledgeSnapshotID != scope.SnapshotID {
			return fmt.Errorf("%w: retrieval candidate has incomplete scoped identity", ErrKnowledgeSnapshotCitationMismatch)
		}
		if _, duplicate := seenRanks[candidate.Rank]; duplicate {
			return fmt.Errorf("%w: retrieval candidate rank %d is duplicated", ErrKnowledgeSnapshotCitationMismatch, candidate.Rank)
		}
		seenRanks[candidate.Rank] = struct{}{}
		documentID, ok := authoritativeDocuments[candidate.RevisionID]
		if !ok || documentID != candidate.DocumentID {
			return fmt.Errorf("%w: retrieval candidate document %q does not belong to revision %q", ErrKnowledgeSnapshotCitationMismatch, candidate.DocumentID, candidate.RevisionID)
		}
	}
	return nil
}

var _ KnowledgeScopedChatStore = (*PostgresStore)(nil)
