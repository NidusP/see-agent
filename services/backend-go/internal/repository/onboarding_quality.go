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

const onboardingDatasetSelect = `SELECT dataset_id,name,status,knowledge_snapshot_id,knowledge_snapshot_manifest_hash,cases_sha256,created_by,created_at,frozen_at FROM onboarding_datasets`
const onboardingCaseSelect = `SELECT case_id,dataset_id,case_type,question,expected_revision_ids,expected_locator_hints,expected_answer_points,review_status,reviewed_by,created_at,updated_at FROM onboarding_cases`
const onboardingRunSelect = `SELECT run_id,dataset_id,status,knowledge_snapshot_id,knowledge_snapshot_manifest_hash,config_version,retrieval_config_version,cases_sha256,metrics,error_code,error_message,created_at,started_at,completed_at FROM onboarding_runs`

func (s *PostgresStore) CreateOnboardingDataset(ctx context.Context, actor, key, name, snapshotID string) (domain.OnboardingDataset, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(snapshotID) == "" {
		return domain.OnboardingDataset{}, ErrQualityInvalid
	}
	hash, _, err := canonicalHash(map[string]string{"actor": actor, "name": name, "snapshot_id": snapshotID})
	if err != nil {
		return domain.OnboardingDataset{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.OnboardingDataset{}, err
	}
	defer tx.Rollback()
	var oldHash string
	var old json.RawMessage
	err = tx.QueryRowContext(ctx, `SELECT request_hash,response FROM onboarding_operation_requests WHERE idempotency_key=$1 FOR UPDATE`, key).Scan(&oldHash, &old)
	if err == nil {
		if oldHash != hash {
			return domain.OnboardingDataset{}, fmt.Errorf("%w: idempotency key reused", ErrQualityConflict)
		}
		var saved domain.OnboardingDataset
		if json.Unmarshal(old, &saved) != nil {
			return saved, ErrQualityConflict
		}
		_ = tx.Commit()
		return saved, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.OnboardingDataset{}, err
	}
	snapshot, err := snapshotTx(ctx, tx, snapshotID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OnboardingDataset{}, ErrQualityNotFound
	}
	if err != nil {
		return domain.OnboardingDataset{}, err
	}
	if snapshot.Status != "published" {
		return domain.OnboardingDataset{}, fmt.Errorf("%w: snapshot is not published", ErrQualityConflict)
	}
	n, err := nextCounterTx(ctx, tx, "onboarding_dataset")
	if err != nil {
		return domain.OnboardingDataset{}, err
	}
	x := domain.OnboardingDataset{DatasetID: fmt.Sprintf("ond_%03d", n), Name: strings.TrimSpace(name), Status: "draft", KnowledgeSnapshotID: snapshot.SnapshotID, KnowledgeSnapshotManifestHash: snapshot.ManifestSHA256, CreatedBy: actor, CreatedAt: qnow()}
	_, err = tx.ExecContext(ctx, `INSERT INTO onboarding_datasets(dataset_id,name,status,knowledge_snapshot_id,knowledge_snapshot_manifest_hash,created_by,idempotency_key,request_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::timestamptz)`, x.DatasetID, x.Name, x.Status, x.KnowledgeSnapshotID, x.KnowledgeSnapshotManifestHash, x.CreatedBy, key, hash, x.CreatedAt)
	if err != nil {
		return x, err
	}
	response, _ := json.Marshal(x)
	_, err = tx.ExecContext(ctx, `INSERT INTO onboarding_operation_requests(idempotency_key,operation,resource_id,request_hash,response) VALUES($1,'create_dataset',$2,$3,$4::jsonb)`, key, x.DatasetID, hash, response)
	if err != nil {
		return x, err
	}
	if err = tx.Commit(); err != nil {
		return x, err
	}
	return x, nil
}

func (s *PostgresStore) ListOnboardingDatasets(ctx context.Context, limit int) (domain.OnboardingDatasetPage, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, onboardingDatasetSelect+` ORDER BY created_at DESC,dataset_id DESC LIMIT $1`, limit)
	if err != nil {
		return domain.OnboardingDatasetPage{}, err
	}
	defer rows.Close()
	p := domain.OnboardingDatasetPage{Items: []domain.OnboardingDataset{}}
	for rows.Next() {
		x, err := scanOnboardingDataset(rows)
		if err != nil {
			return p, err
		}
		p.Items = append(p.Items, x)
	}
	return p, rows.Err()
}

func (s *PostgresStore) GetOnboardingDataset(ctx context.Context, id string) (domain.OnboardingDataset, bool, error) {
	x, err := scanOnboardingDataset(s.db.QueryRowContext(ctx, onboardingDatasetSelect+` WHERE dataset_id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return x, false, nil
	}
	if err != nil {
		return x, false, err
	}
	cases, err := onboardingCases(ctx, s.db, id)
	if err != nil {
		return x, false, err
	}
	x.Cases = cases
	return x, true, nil
}

func (s *PostgresStore) CreateOnboardingCase(ctx context.Context, actor, key string, input domain.OnboardingCase) (domain.OnboardingCase, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(input.DatasetID) == "" || strings.TrimSpace(input.Question) == "" || !domain.IsOnboardingCaseType(input.CaseType) || (input.ReviewStatus != "draft" && input.ReviewStatus != "reviewed") {
		return domain.OnboardingCase{}, ErrQualityInvalid
	}
	if err := validateOnboardingCaseTruth(input); err != nil {
		return domain.OnboardingCase{}, err
	}
	hash, _, err := canonicalHash(map[string]any{"actor": actor, "dataset_id": input.DatasetID, "case_type": input.CaseType, "question": input.Question, "expected_revision_ids": input.ExpectedRevisionIDs, "expected_locator_hints": input.ExpectedLocatorHints, "expected_answer_points": input.ExpectedAnswerPoints, "review_status": input.ReviewStatus})
	if err != nil {
		return domain.OnboardingCase{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.OnboardingCase{}, err
	}
	defer tx.Rollback()
	if saved, found, err := onboardingCaseOperationTx(ctx, tx, key, hash); err != nil {
		return domain.OnboardingCase{}, err
	} else if found {
		_ = tx.Commit()
		return saved, nil
	}
	dataset, found, err := onboardingDatasetTx(ctx, tx, input.DatasetID, true)
	if err != nil {
		return domain.OnboardingCase{}, err
	}
	if !found {
		return domain.OnboardingCase{}, ErrQualityNotFound
	}
	if dataset.Status != "draft" {
		return domain.OnboardingCase{}, fmt.Errorf("%w: dataset is frozen", ErrQualityConflict)
	}
	if err = validateOnboardingCaseSnapshotTx(ctx, tx, dataset.KnowledgeSnapshotID, input); err != nil {
		return domain.OnboardingCase{}, err
	}
	n, err := nextCounterTx(ctx, tx, "onboarding_case")
	if err != nil {
		return domain.OnboardingCase{}, err
	}
	input.CaseID = fmt.Sprintf("onc_%03d", n)
	if input.ReviewStatus == "reviewed" {
		input.ReviewedBy = actor
	}
	input.CreatedAt, input.UpdatedAt = qnow(), qnow()
	revisions, _ := json.Marshal(input.ExpectedRevisionIDs)
	locators, _ := json.Marshal(input.ExpectedLocatorHints)
	answers, _ := json.Marshal(input.ExpectedAnswerPoints)
	_, err = tx.ExecContext(ctx, `INSERT INTO onboarding_cases(case_id,dataset_id,case_type,question,expected_revision_ids,expected_locator_hints,expected_answer_points,review_status,reviewed_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7::jsonb,$8,$9,$10::timestamptz,$11::timestamptz)`, input.CaseID, input.DatasetID, input.CaseType, input.Question, revisions, locators, answers, input.ReviewStatus, input.ReviewedBy, input.CreatedAt, input.UpdatedAt)
	if err != nil {
		return input, err
	}
	response, _ := json.Marshal(input)
	_, err = tx.ExecContext(ctx, `INSERT INTO onboarding_operation_requests(idempotency_key,operation,resource_id,request_hash,response) VALUES($1,'create_case',$2,$3,$4::jsonb)`, key, input.CaseID, hash, response)
	if err != nil {
		return input, err
	}
	if err = tx.Commit(); err != nil {
		return input, err
	}
	return input, nil
}

func (s *PostgresStore) FreezeOnboardingDataset(ctx context.Context, actor, id, key string) (domain.OnboardingDataset, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(id) == "" || strings.TrimSpace(key) == "" {
		return domain.OnboardingDataset{}, ErrQualityInvalid
	}
	hash, _, _ := canonicalHash(map[string]string{"actor": actor, "dataset_id": id, "operation": "freeze"})
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.OnboardingDataset{}, err
	}
	defer tx.Rollback()
	if saved, found, err := onboardingDatasetOperationTx(ctx, tx, key, hash); err != nil {
		return domain.OnboardingDataset{}, err
	} else if found {
		_ = tx.Commit()
		return saved, nil
	}
	x, found, err := onboardingDatasetTx(ctx, tx, id, true)
	if err != nil {
		return x, err
	}
	if !found {
		return x, ErrQualityNotFound
	}
	if x.Status != "draft" {
		return x, fmt.Errorf("%w: dataset is not draft", ErrQualityConflict)
	}
	cases, err := onboardingCases(ctx, tx, id)
	if err != nil {
		return x, err
	}
	answer, refuse := 0, 0
	for _, c := range cases {
		if c.ReviewStatus != "reviewed" {
			return x, fmt.Errorf("%w: every case must be reviewed", ErrQualityConflict)
		}
		if c.CaseType == "must_answer" {
			answer++
		} else {
			refuse++
		}
	}
	if answer < 1 || refuse < 3 {
		return x, fmt.Errorf("%w: requires at least one must-answer and three must-refuse cases", ErrQualityConflict)
	}
	caseHash, _, err := canonicalHash(cases)
	if err != nil {
		return x, err
	}
	now := qnow()
	_, err = tx.ExecContext(ctx, `UPDATE onboarding_datasets SET status='ready',cases_sha256=$2,frozen_at=$3::timestamptz WHERE dataset_id=$1`, id, caseHash, now)
	if err != nil {
		return x, err
	}
	x.Status = "ready"
	x.CasesSHA256 = caseHash
	x.FrozenAt = &now
	x.Cases = cases
	response, _ := json.Marshal(x)
	_, err = tx.ExecContext(ctx, `INSERT INTO onboarding_operation_requests(idempotency_key,operation,resource_id,request_hash,response) VALUES($1,'freeze_dataset',$2,$3,$4::jsonb)`, key, id, hash, response)
	if err != nil {
		return x, err
	}
	if err = tx.Commit(); err != nil {
		return x, err
	}
	return x, nil
}

func (s *PostgresStore) CreateOnboardingRun(ctx context.Context, actor, id, key string) (domain.OnboardingRun, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(id) == "" || strings.TrimSpace(key) == "" {
		return domain.OnboardingRun{}, ErrQualityInvalid
	}
	hash, _, _ := canonicalHash(map[string]string{"actor": actor, "dataset_id": id, "operation": "create_run"})
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	defer tx.Rollback()
	if saved, found, err := onboardingRunOperationTx(ctx, tx, key, hash); err != nil {
		return domain.OnboardingRun{}, err
	} else if found {
		_ = tx.Commit()
		return saved, nil
	}
	dataset, found, err := onboardingDatasetTx(ctx, tx, id, true)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	if !found {
		return domain.OnboardingRun{}, ErrQualityNotFound
	}
	if dataset.Status != "ready" {
		return domain.OnboardingRun{}, fmt.Errorf("%w: dataset is not ready", ErrQualityConflict)
	}
	snapshot, err := snapshotTx(ctx, tx, dataset.KnowledgeSnapshotID)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	if snapshot.Status != "published" || snapshot.ManifestSHA256 != dataset.KnowledgeSnapshotManifestHash {
		return domain.OnboardingRun{}, fmt.Errorf("%w: snapshot identity is no longer publishable", ErrQualityConflict)
	}
	var configVersion, retrievalVersion string
	err = tx.QueryRowContext(ctx, `SELECT release.config_version,release.retrieval_config_version FROM quality_active_release pointer JOIN quality_releases release ON release.release_id=pointer.release_id WHERE pointer.singleton=TRUE FOR SHARE`).Scan(&configVersion, &retrievalVersion)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	n, err := nextCounterTx(ctx, tx, "onboarding_run")
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	x := domain.OnboardingRun{RunID: fmt.Sprintf("onr_%03d", n), DatasetID: id, Status: "queued", KnowledgeSnapshotID: dataset.KnowledgeSnapshotID, KnowledgeSnapshotManifestHash: dataset.KnowledgeSnapshotManifestHash, ConfigVersion: configVersion, RetrievalConfigVersion: retrievalVersion, CasesSHA256: dataset.CasesSHA256, Metrics: json.RawMessage(`{}`), CreatedAt: qnow()}
	_, err = tx.ExecContext(ctx, `INSERT INTO onboarding_runs(run_id,dataset_id,status,knowledge_snapshot_id,knowledge_snapshot_manifest_hash,config_version,retrieval_config_version,cases_sha256,idempotency_key,request_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::timestamptz)`, x.RunID, x.DatasetID, x.Status, x.KnowledgeSnapshotID, x.KnowledgeSnapshotManifestHash, x.ConfigVersion, x.RetrievalConfigVersion, x.CasesSHA256, key, hash, x.CreatedAt)
	if err != nil {
		return x, err
	}
	response, _ := json.Marshal(x)
	_, err = tx.ExecContext(ctx, `INSERT INTO onboarding_operation_requests(idempotency_key,operation,resource_id,request_hash,response) VALUES($1,'create_run',$2,$3,$4::jsonb)`, key, x.RunID, hash, response)
	if err != nil {
		return x, err
	}
	if err = tx.Commit(); err != nil {
		return x, err
	}
	return x, nil
}

func (s *PostgresStore) GetOnboardingRun(ctx context.Context, id string) (domain.OnboardingRun, bool, error) {
	x, err := scanOnboardingRun(s.db.QueryRowContext(ctx, onboardingRunSelect+` WHERE run_id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return x, false, nil
	}
	if err != nil {
		return x, false, err
	}
	results, err := onboardingRunResults(ctx, s.db, id)
	if err != nil {
		return x, false, err
	}
	x.Results = results
	return x, true, nil
}

func (s *PostgresStore) ClaimOnboardingRun(ctx context.Context, lease time.Duration) (domain.OnboardingRun, bool, error) {
	if lease <= 0 {
		return domain.OnboardingRun{}, false, ErrQualityInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.OnboardingRun{}, false, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, onboardingRunSelect+` WHERE (status='queued' OR (status='running' AND lease_until < NOW())) ORDER BY created_at,run_id FOR UPDATE SKIP LOCKED LIMIT 1`)
	x, err := scanOnboardingRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OnboardingRun{}, false, tx.Commit()
	}
	if err != nil {
		return domain.OnboardingRun{}, false, err
	}
	now := time.Now().UTC()
	leaseUntil := now.Add(lease)
	_, err = tx.ExecContext(ctx, `UPDATE onboarding_runs SET status='running',started_at=COALESCE(started_at,$2::timestamptz),lease_until=$3::timestamptz,error_code='',error_message='' WHERE run_id=$1`, x.RunID, now.Format(time.RFC3339), leaseUntil.Format(time.RFC3339))
	if err != nil {
		return x, false, err
	}
	x.Status = "running"
	if x.StartedAt == nil {
		started := now.Format(time.RFC3339)
		x.StartedAt = &started
	}
	if err = tx.Commit(); err != nil {
		return x, false, err
	}
	return x, true, nil
}

func (s *PostgresStore) CompleteOnboardingRun(ctx context.Context, id, status string, results []domain.OnboardingCaseResult, metrics json.RawMessage) (domain.OnboardingRun, error) {
	if (status != "passed" && status != "failed") || !json.Valid(metrics) {
		return domain.OnboardingRun{}, ErrQualityInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	defer tx.Rollback()
	x, err := scanOnboardingRun(tx.QueryRowContext(ctx, onboardingRunSelect+` WHERE run_id=$1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OnboardingRun{}, ErrQualityNotFound
	}
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	if x.Status != "running" {
		return domain.OnboardingRun{}, fmt.Errorf("%w: run is not running", ErrQualityConflict)
	}
	var expectedCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM onboarding_cases WHERE dataset_id=$1`, x.DatasetID).Scan(&expectedCount); err != nil {
		return domain.OnboardingRun{}, err
	}
	if len(results) != expectedCount {
		return domain.OnboardingRun{}, fmt.Errorf("%w: result count does not match frozen cases", ErrQualityInvalid)
	}
	seenCases := make(map[string]struct{}, len(results))
	for _, result := range results {
		if result.RunID != id || strings.TrimSpace(result.CaseID) == "" {
			return domain.OnboardingRun{}, ErrQualityInvalid
		}
		if _, duplicate := seenCases[result.CaseID]; duplicate {
			return domain.OnboardingRun{}, ErrQualityInvalid
		}
		seenCases[result.CaseID] = struct{}{}
		var belongs bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM onboarding_cases WHERE case_id=$1 AND dataset_id=$2)`, result.CaseID, x.DatasetID).Scan(&belongs); err != nil || !belongs {
			if err != nil {
				return domain.OnboardingRun{}, err
			}
			return domain.OnboardingRun{}, ErrQualityInvalid
		}
		citations, err := json.Marshal(result.ObservedCitations)
		if err != nil {
			return domain.OnboardingRun{}, err
		}
		completedAt := result.CompletedAt
		if completedAt == "" {
			completedAt = qnow()
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO onboarding_case_results(run_id,case_id,passed,refusal,observed_answer,observed_citations,expected_revision_hit,expected_locator_hit,answer_point_hit_rate,failure_code,completed_at) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9,$10,$11::timestamptz)`, result.RunID, result.CaseID, result.Passed, result.Refusal, result.ObservedAnswer, citations, result.ExpectedRevisionHit, result.ExpectedLocatorHit, result.AnswerPointHitRate, result.FailureCode, completedAt)
		if err != nil {
			return domain.OnboardingRun{}, err
		}
	}
	now := qnow()
	_, err = tx.ExecContext(ctx, `UPDATE onboarding_runs SET status=$2,metrics=$3::jsonb,completed_at=$4::timestamptz,lease_until=NULL WHERE run_id=$1`, id, status, metrics, now)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.OnboardingRun{}, err
	}
	return s.onboardingRunWithResults(ctx, id)
}

func (s *PostgresStore) FailOnboardingRun(ctx context.Context, id, code, message string) (domain.OnboardingRun, error) {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(message) == "" {
		return domain.OnboardingRun{}, ErrQualityInvalid
	}
	now := qnow()
	result, err := s.db.ExecContext(ctx, `UPDATE onboarding_runs SET status='failed',error_code=$2,error_message=$3,completed_at=$4::timestamptz,lease_until=NULL WHERE run_id=$1 AND status='running'`, id, code, message, now)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return domain.OnboardingRun{}, fmt.Errorf("%w: run is not running", ErrQualityConflict)
	}
	return s.onboardingRunWithResults(ctx, id)
}

func (s *PostgresStore) onboardingRunWithResults(ctx context.Context, id string) (domain.OnboardingRun, error) {
	x, found, err := s.GetOnboardingRun(ctx, id)
	if err != nil {
		return x, err
	}
	if !found {
		return x, ErrQualityNotFound
	}
	return x, nil
}

func onboardingDatasetTx(ctx context.Context, tx *sql.Tx, id string, lock bool) (domain.OnboardingDataset, bool, error) {
	q := onboardingDatasetSelect + ` WHERE dataset_id=$1`
	if lock {
		q += ` FOR UPDATE`
	}
	x, err := scanOnboardingDataset(tx.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return x, false, nil
	}
	return x, err == nil, err
}
func onboardingCases(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, id string) ([]domain.OnboardingCase, error) {
	rows, err := q.QueryContext(ctx, onboardingCaseSelect+` WHERE dataset_id=$1 ORDER BY created_at,case_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.OnboardingCase{}
	for rows.Next() {
		x, err := scanOnboardingCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func scanOnboardingDataset(row scanner) (domain.OnboardingDataset, error) {
	var x domain.OnboardingDataset
	var created time.Time
	var frozen sql.NullTime
	err := row.Scan(&x.DatasetID, &x.Name, &x.Status, &x.KnowledgeSnapshotID, &x.KnowledgeSnapshotManifestHash, &x.CasesSHA256, &x.CreatedBy, &created, &frozen)
	if err != nil {
		return x, err
	}
	x.CreatedAt = created.UTC().Format(time.RFC3339)
	if frozen.Valid {
		v := frozen.Time.UTC().Format(time.RFC3339)
		x.FrozenAt = &v
	}
	return x, nil
}
func scanOnboardingCase(row scanner) (domain.OnboardingCase, error) {
	var x domain.OnboardingCase
	var revisions, locators, answers []byte
	var created, updated time.Time
	err := row.Scan(&x.CaseID, &x.DatasetID, &x.CaseType, &x.Question, &revisions, &locators, &answers, &x.ReviewStatus, &x.ReviewedBy, &created, &updated)
	if err != nil {
		return x, err
	}
	if json.Unmarshal(revisions, &x.ExpectedRevisionIDs) != nil || json.Unmarshal(locators, &x.ExpectedLocatorHints) != nil || json.Unmarshal(answers, &x.ExpectedAnswerPoints) != nil {
		return x, ErrQualityInvalid
	}
	x.CreatedAt = created.UTC().Format(time.RFC3339)
	x.UpdatedAt = updated.UTC().Format(time.RFC3339)
	return x, nil
}
func scanOnboardingRun(row scanner) (domain.OnboardingRun, error) {
	var x domain.OnboardingRun
	var created time.Time
	var started, completed sql.NullTime
	var code, message string
	err := row.Scan(&x.RunID, &x.DatasetID, &x.Status, &x.KnowledgeSnapshotID, &x.KnowledgeSnapshotManifestHash, &x.ConfigVersion, &x.RetrievalConfigVersion, &x.CasesSHA256, &x.Metrics, &code, &message, &created, &started, &completed)
	if err != nil {
		return x, err
	}
	x.CreatedAt = created.UTC().Format(time.RFC3339)
	if started.Valid {
		v := started.Time.UTC().Format(time.RFC3339)
		x.StartedAt = &v
	}
	if completed.Valid {
		v := completed.Time.UTC().Format(time.RFC3339)
		x.CompletedAt = &v
	}
	if code != "" {
		x.Error = &domain.OnboardingRunError{Code: code, Message: message}
	}
	return x, nil
}
func onboardingRunResults(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, runID string) ([]domain.OnboardingCaseResult, error) {
	rows, err := q.QueryContext(ctx, `SELECT run_id,case_id,passed,refusal,observed_answer,observed_citations,expected_revision_hit,expected_locator_hit,answer_point_hit_rate,failure_code,completed_at FROM onboarding_case_results WHERE run_id=$1 ORDER BY case_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.OnboardingCaseResult{}
	for rows.Next() {
		var x domain.OnboardingCaseResult
		var citations []byte
		var completed time.Time
		if err := rows.Scan(&x.RunID, &x.CaseID, &x.Passed, &x.Refusal, &x.ObservedAnswer, &citations, &x.ExpectedRevisionHit, &x.ExpectedLocatorHit, &x.AnswerPointHitRate, &x.FailureCode, &completed); err != nil {
			return nil, err
		}
		if json.Unmarshal(citations, &x.ObservedCitations) != nil {
			return nil, ErrQualityInvalid
		}
		x.CompletedAt = completed.UTC().Format(time.RFC3339)
		items = append(items, x)
	}
	return items, rows.Err()
}
func validateOnboardingCaseTruth(x domain.OnboardingCase) error {
	if x.CaseType == "must_refuse" {
		if len(x.ExpectedRevisionIDs) > 0 || len(x.ExpectedLocatorHints) > 0 || len(x.ExpectedAnswerPoints) > 0 {
			return ErrQualityInvalid
		}
		return nil
	}
	if len(x.ExpectedRevisionIDs) == 0 || len(x.ExpectedLocatorHints) == 0 || len(x.ExpectedAnswerPoints) == 0 {
		return ErrQualityInvalid
	}
	return nil
}
func validateOnboardingCaseSnapshotTx(ctx context.Context, tx *sql.Tx, snapshotID string, x domain.OnboardingCase) error {
	for _, revisionID := range x.ExpectedRevisionIDs {
		var found bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM knowledge_snapshot_members WHERE snapshot_id=$1 AND revision_id=$2)`, snapshotID, revisionID).Scan(&found)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: expected revision does not belong to dataset snapshot", ErrQualityInvalid)
		}
	}
	return nil
}
func onboardingCaseOperationTx(ctx context.Context, tx *sql.Tx, key, hash string) (domain.OnboardingCase, bool, error) {
	var oldHash string
	var raw json.RawMessage
	err := tx.QueryRowContext(ctx, `SELECT request_hash,response FROM onboarding_operation_requests WHERE idempotency_key=$1 FOR UPDATE`, key).Scan(&oldHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OnboardingCase{}, false, nil
	}
	if err != nil {
		return domain.OnboardingCase{}, false, err
	}
	if oldHash != hash {
		return domain.OnboardingCase{}, false, fmt.Errorf("%w: idempotency key reused", ErrQualityConflict)
	}
	var x domain.OnboardingCase
	if json.Unmarshal(raw, &x) != nil {
		return x, false, ErrQualityConflict
	}
	return x, true, nil
}
func onboardingDatasetOperationTx(ctx context.Context, tx *sql.Tx, key, hash string) (domain.OnboardingDataset, bool, error) {
	var oldHash string
	var raw json.RawMessage
	err := tx.QueryRowContext(ctx, `SELECT request_hash,response FROM onboarding_operation_requests WHERE idempotency_key=$1 FOR UPDATE`, key).Scan(&oldHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OnboardingDataset{}, false, nil
	}
	if err != nil {
		return domain.OnboardingDataset{}, false, err
	}
	if oldHash != hash {
		return domain.OnboardingDataset{}, false, fmt.Errorf("%w: idempotency key reused", ErrQualityConflict)
	}
	var x domain.OnboardingDataset
	if json.Unmarshal(raw, &x) != nil {
		return x, false, ErrQualityConflict
	}
	return x, true, nil
}
func onboardingRunOperationTx(ctx context.Context, tx *sql.Tx, key, hash string) (domain.OnboardingRun, bool, error) {
	var oldHash string
	var raw json.RawMessage
	err := tx.QueryRowContext(ctx, `SELECT request_hash,response FROM onboarding_operation_requests WHERE idempotency_key=$1 FOR UPDATE`, key).Scan(&oldHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OnboardingRun{}, false, nil
	}
	if err != nil {
		return domain.OnboardingRun{}, false, err
	}
	if oldHash != hash {
		return domain.OnboardingRun{}, false, fmt.Errorf("%w: idempotency key reused", ErrQualityConflict)
	}
	var x domain.OnboardingRun
	if json.Unmarshal(raw, &x) != nil {
		return x, false, ErrQualityConflict
	}
	return x, true, nil
}
