package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"see-agent/services/backend-go/internal/domain"
)

var ErrEvalReportConflict = errors.New("eval report registration conflict")

func (s *PostgresStore) RegisterEvalReport(ctx context.Context, idempotencyKey string, registration domain.EvalReportRegistration) (domain.EvalReport, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	requestHash, err := evalReportRequestHash(registration.Report)
	if err != nil {
		return domain.EvalReport{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.EvalReport{}, err
	}
	defer tx.Rollback()

	existing, found, err := evalReportTx(ctx, tx, idempotencyKey, registration.ReportID, true)
	if err != nil {
		return domain.EvalReport{}, err
	}
	if found {
		var existingHash string
		if err := tx.QueryRowContext(ctx, `SELECT request_hash FROM eval_reports WHERE report_id = $1`, existing.ReportID).Scan(&existingHash); err != nil {
			return domain.EvalReport{}, err
		}
		if existingHash != requestHash {
			return domain.EvalReport{}, fmt.Errorf("%w: key or report_id already represents different content", ErrEvalReportConflict)
		}
		if err := tx.Commit(); err != nil {
			return domain.EvalReport{}, err
		}
		return existing, nil
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO eval_reports (
			report_id, idempotency_key, request_hash, dataset_id, model_id, config_version,
			retrieval_config_version, generation_config, metrics, report, completed_at, invalid_for_quality
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10::jsonb, $11::timestamptz, $12)
		ON CONFLICT DO NOTHING`,
		registration.ReportID, idempotencyKey, requestHash, registration.DatasetID, registration.ModelID,
		registration.ConfigVersion, registration.RetrievalConfigVersion, registration.GenerationConfig,
		registration.Metrics, registration.Report, registration.CompletedAt, evalReportUsesTestProvider(registration.Report),
	)
	if err != nil {
		return domain.EvalReport{}, err
	}
	report, found, err := evalReportTx(ctx, tx, idempotencyKey, registration.ReportID, false)
	if err != nil {
		return domain.EvalReport{}, err
	}
	if !found {
		return domain.EvalReport{}, errors.New("registered eval report was not readable")
	}
	var storedHash string
	if err := tx.QueryRowContext(ctx, `SELECT request_hash FROM eval_reports WHERE report_id = $1`, report.ReportID).Scan(&storedHash); err != nil {
		return domain.EvalReport{}, err
	}
	if storedHash != requestHash {
		return domain.EvalReport{}, fmt.Errorf("%w: key or report_id already represents different content", ErrEvalReportConflict)
	}
	if err := tx.Commit(); err != nil {
		return domain.EvalReport{}, err
	}
	return report, nil
}

func evalReportRequestHash(report json.RawMessage) (string, error) {
	var value any
	if err := json.Unmarshal(report, &value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(canonical)), nil
}

func (s *PostgresStore) GetEvalReport(ctx context.Context, reportID string) (domain.EvalReport, bool, error) {
	report, err := scanEvalReport(s.db.QueryRowContext(ctx, evalReportSelect+` WHERE report_id = $1`, reportID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.EvalReport{}, false, nil
	}
	return report, err == nil, err
}

func (s *PostgresStore) ListEvalReports(ctx context.Context, configVersion, retrievalConfigVersion, cursor string, limit int) (domain.EvalReportPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	where := []string{"1 = 1"}
	args := []any{}
	add := func(clause, value string) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if strings.TrimSpace(configVersion) != "" {
		add("config_version = $%d", configVersion)
	}
	if strings.TrimSpace(retrievalConfigVersion) != "" {
		add("retrieval_config_version = $%d", retrievalConfigVersion)
	}
	if strings.TrimSpace(cursor) != "" {
		add("report_id < $%d", cursor)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, evalReportSelect+` WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC, report_id DESC LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return domain.EvalReportPage{}, err
	}
	defer rows.Close()
	items := []domain.EvalReport{}
	for rows.Next() {
		item, err := scanEvalReport(rows)
		if err != nil {
			return domain.EvalReportPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.EvalReportPage{}, err
	}
	page := domain.EvalReportPage{Items: items}
	if len(items) > limit {
		page.NextCursor = items[limit-1].ReportID
		page.Items = items[:limit]
	}
	return page, nil
}

func (s *PostgresStore) ListKnowledgeDeliveryEvidence(ctx context.Context, status, documentID, revisionID, cursor string, limit int) (domain.KnowledgeDeliveryEvidencePage, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	where := []string{"1 = 1"}
	args := []any{}
	add := func(clause, value string) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if strings.TrimSpace(status) != "" {
		add("status = $%d", status)
	}
	if strings.TrimSpace(documentID) != "" {
		add("document_id = $%d", documentID)
	}
	if strings.TrimSpace(revisionID) != "" {
		add("revision_id = $%d", revisionID)
	}
	if strings.TrimSpace(cursor) != "" {
		add("delivery_id < $%d", cursor)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, deliveryEvidenceSelect+` WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC, delivery_id DESC LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return domain.KnowledgeDeliveryEvidencePage{}, err
	}
	defer rows.Close()
	items := []domain.KnowledgeDeliveryEvidence{}
	for rows.Next() {
		item, err := scanDeliveryEvidence(rows)
		if err != nil {
			return domain.KnowledgeDeliveryEvidencePage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.KnowledgeDeliveryEvidencePage{}, err
	}
	page := domain.KnowledgeDeliveryEvidencePage{Items: items}
	if len(items) > limit {
		page.NextCursor = items[limit-1].DeliveryID
		page.Items = items[:limit]
	}
	return page, nil
}

const evalReportSelect = `
	SELECT report_id, dataset_id, model_id, config_version, retrieval_config_version,
		generation_config, metrics, completed_at, report, created_at, legacy_fallback, invalid_for_quality
	FROM eval_reports`

func evalReportTx(ctx context.Context, tx *sql.Tx, idempotencyKey, reportID string, lock bool) (domain.EvalReport, bool, error) {
	query := evalReportSelect + ` WHERE idempotency_key = $1 OR report_id = $2 ORDER BY (idempotency_key = $1) DESC LIMIT 1`
	if lock {
		query += ` FOR UPDATE`
	}
	report, err := scanEvalReport(tx.QueryRowContext(ctx, query, idempotencyKey, reportID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.EvalReport{}, false, nil
	}
	return report, err == nil, err
}

func scanEvalReport(row scanner) (domain.EvalReport, error) {
	var report domain.EvalReport
	var completedAt, createdAt time.Time
	if err := row.Scan(&report.ReportID, &report.DatasetID, &report.ModelID, &report.ConfigVersion,
		&report.RetrievalConfigVersion, &report.GenerationConfig, &report.Metrics, &completedAt,
		&report.Report, &createdAt, &report.LegacyFallback, &report.InvalidForQuality); err != nil {
		return domain.EvalReport{}, err
	}
	report.CompletedAt = completedAt.UTC().Format(time.RFC3339)
	report.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	return report, nil
}

func evalReportUsesTestProvider(payload json.RawMessage) bool {
	var value any
	if json.Unmarshal(payload, &value) != nil {
		return true
	}
	serialized, _ := json.Marshal(value)
	valueText := strings.ToLower(string(serialized))
	return strings.Contains(valueText, "test_provider") || strings.Contains(valueText, "test_injected")
}

const deliveryEvidenceSelect = `
	SELECT delivery_id, topic, document_id, revision_id, snapshot_id, status, attempt_count,
		available_at, stream_message_id, lease_until, last_error, completed_at, created_at, updated_at
	FROM knowledge_outbox`

func scanDeliveryEvidence(row scanner) (domain.KnowledgeDeliveryEvidence, error) {
	var item domain.KnowledgeDeliveryEvidence
	var documentID, revisionID, snapshotID, streamID, lastError sql.NullString
	var availableAt, createdAt, updatedAt time.Time
	var leaseUntil, completedAt sql.NullTime
	if err := row.Scan(&item.DeliveryID, &item.Topic, &documentID, &revisionID, &snapshotID, &item.Status,
		&item.AttemptCount, &availableAt, &streamID, &leaseUntil, &lastError, &completedAt,
		&createdAt, &updatedAt); err != nil {
		return domain.KnowledgeDeliveryEvidence{}, err
	}
	item.DocumentID = nullableString(documentID)
	item.RevisionID = nullableString(revisionID)
	item.SnapshotID = nullableString(snapshotID)
	item.StreamMessageID = nullableString(streamID)
	item.LastError = nullableString(lastError)
	item.AvailableAt = availableAt.UTC().Format(time.RFC3339)
	item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	item.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	if leaseUntil.Valid {
		value := leaseUntil.Time.UTC().Format(time.RFC3339)
		item.LeaseUntil = &value
	}
	if completedAt.Valid {
		value := completedAt.Time.UTC().Format(time.RFC3339)
		item.CompletedAt = &value
	}
	return item, nil
}
