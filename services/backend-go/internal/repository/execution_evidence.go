package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/domain"
)

func (s *PostgresStore) GetMessageExecutionEvidence(ctx context.Context, messageID string) (domain.MessageExecutionEvidence, bool, error) {
	item, err := scanMessageExecutionEvidence(s.db.QueryRowContext(ctx, executionEvidenceSelect+` WHERE evidence.message_id = $1`, messageID))
	if err == nil {
		if sources, found := s.GetMessageSources(messageID); found {
			item.KnowledgeSnapshotManifestHash = sources.KnowledgeSnapshotManifestHash
			item.FreshnessStatus = sources.FreshnessStatus
			item.Citations = sources.Items
		}
		candidates, candidateErr := s.listRetrievalCandidates(ctx, messageID)
		if candidateErr != nil {
			return domain.MessageExecutionEvidence{}, false, candidateErr
		}
		item.RetrievalCandidates = candidates
		return item, true, nil
	}
	if err == sql.ErrNoRows {
		return domain.MessageExecutionEvidence{}, false, nil
	}
	return domain.MessageExecutionEvidence{}, false, err
}

func (s *PostgresStore) ListMessageExecutionEvidence(ctx context.Context, conversationID, snapshotID, revisionID, cursor string, limit int) (domain.MessageExecutionEvidencePage, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	where := []string{"1 = 1"}
	args := []any{}
	add := func(clause, value string) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if strings.TrimSpace(conversationID) != "" {
		add("evidence.conversation_id = $%d", conversationID)
	}
	if strings.TrimSpace(snapshotID) != "" {
		add("evidence.knowledge_snapshot_id = $%d", snapshotID)
	}
	if strings.TrimSpace(revisionID) != "" {
		args = append(args, revisionID)
		index := len(args)
		where = append(where, fmt.Sprintf("(EXISTS (SELECT 1 FROM citations c WHERE c.message_id = evidence.message_id AND c.revision_id = $%d) OR EXISTS (SELECT 1 FROM message_retrieval_candidates candidate WHERE candidate.message_id = evidence.message_id AND candidate.revision_id = $%d))", index, index))
	}
	if strings.TrimSpace(cursor) != "" {
		add("evidence.message_id < $%d", cursor)
	}
	args = append(args, limit+1)
	query := executionEvidenceSelect + ` WHERE ` + strings.Join(where, " AND ") + ` ORDER BY evidence.completed_at DESC, evidence.message_id DESC LIMIT $` + fmt.Sprint(len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return domain.MessageExecutionEvidencePage{}, err
	}
	defer rows.Close()
	items := []domain.MessageExecutionEvidence{}
	for rows.Next() {
		item, err := scanMessageExecutionEvidence(rows)
		if err != nil {
			return domain.MessageExecutionEvidencePage{}, err
		}
		candidates, candidateErr := s.listRetrievalCandidates(ctx, item.MessageID)
		if candidateErr != nil {
			return domain.MessageExecutionEvidencePage{}, candidateErr
		}
		item.RetrievalCandidates = candidates
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.MessageExecutionEvidencePage{}, err
	}
	page := domain.MessageExecutionEvidencePage{Items: items}
	if len(items) > limit {
		page.NextCursor = items[limit-1].MessageID
		page.Items = items[:limit]
	}
	return page, nil
}

func (s *PostgresStore) listRetrievalCandidates(ctx context.Context, messageID string) ([]domain.RuntimeRetrievalCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT chunk_id, file_id, document_id, revision_id, chunk_content_sha256, knowledge_snapshot_id, rank, score, source_locator
		FROM message_retrieval_candidates
		WHERE message_id = $1
		ORDER BY rank ASC`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.RuntimeRetrievalCandidate{}
	for rows.Next() {
		var item domain.RuntimeRetrievalCandidate
		var locator []byte
		if err := rows.Scan(&item.ChunkID, &item.FileID, &item.DocumentID, &item.RevisionID, &item.ChunkContentSHA256, &item.KnowledgeSnapshotID, &item.Rank, &item.Score, &locator); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(locator, &item.SourceLocator); err != nil {
			return nil, fmt.Errorf("decode retrieval candidate locator: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const executionEvidenceSelect = `
	SELECT evidence.message_id, evidence.conversation_id, evidence.request_id,
		evidence.config_version, evidence.retrieval_config_version, evidence.knowledge_snapshot_id,
		evidence.model_id, evidence.generation_config, evidence.retrieval_config, evidence.usage,
		evidence.refusal, evidence.finish_reason, evidence.completed_at, evidence.intent,
		evidence.retrieval_performed, evidence.generation_performed, evidence.knowledge_used,
		evidence.answer_disposition, evidence.answer_policy_version, evidence.policy_evaluation_performed, evidence.answer_generation_performed,
		evidence.clarification, evidence.coverage, evidence.clarification_resolution, evidence.resolved_query,
		evidence.execution_observation, evidence.used_citation_ranks, evidence.context_resolution,
		evidence.legacy_fallback, evidence.invalid_for_quality
	FROM message_execution_evidence evidence`

func scanMessageExecutionEvidence(row scanner) (domain.MessageExecutionEvidence, error) {
	var item domain.MessageExecutionEvidence
	var completedAt time.Time
	var usage []byte
	var execution []byte
	var answerDisposition sql.NullString
	var answerPolicyVersion sql.NullString
	var policyEvaluation sql.NullBool
	var answerGeneration sql.NullBool
	var clarification, coverage, resolution []byte
	var resolvedQuery sql.NullString
	var usedRanks []byte
	var contextResolution []byte
	var snapshotID sql.NullString
	if err := row.Scan(&item.MessageID, &item.ConversationID, &item.RequestID, &item.ConfigVersion,
		&item.RetrievalConfigVersion, &snapshotID, &item.ModelID, &item.GenerationConfig,
		&item.RetrievalConfig, &usage, &item.Refusal, &item.FinishReason, &completedAt, &item.Intent,
		&item.RetrievalPerformed, &item.GenerationPerformed, &item.KnowledgeUsed,
		&answerDisposition, &answerPolicyVersion, &policyEvaluation, &answerGeneration,
		&clarification, &coverage, &resolution, &resolvedQuery,
		&execution, &usedRanks, &contextResolution,
		&item.LegacyFallback, &item.InvalidForQuality); err != nil {
		return domain.MessageExecutionEvidence{}, err
	}
	if len(usage) > 0 {
		item.Usage = usage
	}
	item.CompletedAt = completedAt.UTC().Format(time.RFC3339)
	item.KnowledgeSnapshotID = nullableString(snapshotID)
	item.AnswerDisposition = nullableString(answerDisposition)
	item.AnswerPolicyVersion = nullableString(answerPolicyVersion)
	if policyEvaluation.Valid {
		value := policyEvaluation.Bool
		item.PolicyEvaluationPerformed = &value
	}
	if answerGeneration.Valid {
		value := answerGeneration.Bool
		item.AnswerGenerationPerformed = &value
	}
	if len(clarification) > 0 {
		item.Clarification = &domain.RuntimeClarification{}
		if err := json.Unmarshal(clarification, item.Clarification); err != nil {
			return domain.MessageExecutionEvidence{}, fmt.Errorf("decode clarification evidence: %w", err)
		}
	}
	if len(coverage) > 0 {
		item.Coverage = &domain.RuntimeCoverage{}
		if err := json.Unmarshal(coverage, item.Coverage); err != nil {
			return domain.MessageExecutionEvidence{}, fmt.Errorf("decode coverage evidence: %w", err)
		}
	}
	if len(resolution) > 0 {
		item.Resolution = &domain.RuntimeClarificationResolution{}
		if err := json.Unmarshal(resolution, item.Resolution); err != nil {
			return domain.MessageExecutionEvidence{}, fmt.Errorf("decode clarification resolution: %w", err)
		}
	}
	item.ResolvedQuery = nullableString(resolvedQuery)
	if len(execution) > 0 {
		item.Execution = execution
	}
	if len(usedRanks) > 0 {
		if err := json.Unmarshal(usedRanks, &item.UsedCitationRanks); err != nil {
			return domain.MessageExecutionEvidence{}, fmt.Errorf("decode used citation ranks: %w", err)
		}
	}
	if len(contextResolution) > 0 {
		item.ContextResolution = contextResolution
	}
	return item, nil
}

var _ ExecutionEvidenceStore = (*PostgresStore)(nil)
