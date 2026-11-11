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

var (
	ErrQualityUnavailable = errors.New("quality operations require PostgreSQL")
	ErrQualityNotFound    = errors.New("quality operation resource not found")
	ErrQualityConflict    = errors.New("quality operation conflict")
	ErrQualityInvalid     = errors.New("quality operation invalid")
)

func (s *PostgresStore) ensureBootstrapQualityRelease(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM quality_active_release WHERE singleton = TRUE)`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit()
	}
	config, err := activeConfigTx(ctx, tx)
	if err != nil {
		return fmt.Errorf("bootstrap quality release config: %w", err)
	}
	retrieval, err := activeRetrievalTx(ctx, tx)
	if err != nil {
		return fmt.Errorf("bootstrap quality release retrieval: %w", err)
	}
	// bootstrap runs before counter reconciliation; a fixed one-time id avoids
	// coupling first-start release seeding to an as-yet-uninitialized counter.
	releaseID := "qrel_bootstrap"
	_, err = tx.ExecContext(ctx, `INSERT INTO quality_releases (release_id,status,config_version,retrieval_config_version,created_by) VALUES ($1,'published',$2,$3,'system_bootstrap')`, releaseID, config.ConfigVersion, retrieval.RetrievalConfigVersion)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO quality_active_release(singleton,release_id) VALUES(TRUE,$1)`, releaseID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO quality_release_events(event_id,release_id,event_type,actor_id) VALUES($1,$2,'bootstrap','system_bootstrap')`, "qrevt_bootstrap_"+releaseID, releaseID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func canonicalHash(v any) (string, []byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b)), b, nil
}
func qnow() string { return time.Now().UTC().Format(time.RFC3339) }
func qualityJSON(raw json.RawMessage) (json.RawMessage, string, error) {
	var v any
	if !json.Valid(raw) {
		return nil, "", ErrQualityInvalid
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, "", err
	}
	hash, b, err := canonicalHash(v)
	return json.RawMessage(b), hash, err
}

func (s *PostgresStore) CreateAnswerFeedback(ctx context.Context, actorID, key string, input domain.AnswerFeedback) (domain.AnswerFeedback, error) {
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(input.MessageID) == "" || !domain.IsAnswerFeedbackKind(input.Kind) {
		return domain.AnswerFeedback{}, ErrQualityInvalid
	}
	for _, rank := range input.ExpectedCitationRanks {
		if rank < 1 {
			return domain.AnswerFeedback{}, ErrQualityInvalid
		}
	}
	hash, _, err := canonicalHash(map[string]any{"message_id": input.MessageID, "kind": input.Kind, "comment": input.Comment, "corrected_answer": input.CorrectedAnswer, "expected_citation_ranks": input.ExpectedCitationRanks})
	if err != nil {
		return domain.AnswerFeedback{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AnswerFeedback{}, err
	}
	defer tx.Rollback()
	if existing, found, err := answerFeedbackByKeyTx(ctx, tx, key); err != nil {
		return domain.AnswerFeedback{}, err
	} else if found {
		var old string
		_ = tx.QueryRowContext(ctx, `SELECT request_hash FROM answer_feedback WHERE feedback_id=$1`, existing.FeedbackID).Scan(&old)
		if old != hash {
			return domain.AnswerFeedback{}, fmt.Errorf("%w: idempotency key reused", ErrQualityConflict)
		}
		_ = tx.Commit()
		return existing, nil
	}
	var conversationID, role, status string
	err = tx.QueryRowContext(ctx, `SELECT conversation_id,role,status FROM messages WHERE message_id=$1`, input.MessageID).Scan(&conversationID, &role, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AnswerFeedback{}, ErrQualityNotFound
	}
	if err != nil {
		return domain.AnswerFeedback{}, err
	}
	if role != "assistant" {
		return domain.AnswerFeedback{}, ErrQualityInvalid
	}
	var intent string
	var answerDisposition sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT intent,answer_disposition FROM message_execution_evidence WHERE message_id=$1`, input.MessageID).Scan(&intent, &answerDisposition)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AnswerFeedback{}, ErrQualityInvalid
	}
	if err != nil {
		return domain.AnswerFeedback{}, err
	}
	if !validA1DispositionFeedback(input, status, intent, answerDisposition.String) {
		return domain.AnswerFeedback{}, ErrQualityInvalid
	}
	for _, rank := range input.ExpectedCitationRanks {
		var found bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM citations WHERE message_id=$1 AND rank=$2)`, input.MessageID, rank).Scan(&found); err != nil {
			return domain.AnswerFeedback{}, err
		}
		if !found {
			return domain.AnswerFeedback{}, ErrQualityInvalid
		}
	}
	n, err := nextCounterTx(ctx, tx, "quality_feedback")
	if err != nil {
		return domain.AnswerFeedback{}, err
	}
	input.FeedbackID = fmt.Sprintf("qfb_%03d", n)
	input.ActorID = actorID
	input.ConversationID = conversationID
	input.CreatedAt = qnow()
	ranks, _ := json.Marshal(input.ExpectedCitationRanks)
	_, err = tx.ExecContext(ctx, `INSERT INTO answer_feedback(feedback_id,message_id,conversation_id,actor_id,kind,comment,corrected_answer,expected_citation_ranks,idempotency_key,request_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11::timestamptz)`, input.FeedbackID, input.MessageID, input.ConversationID, input.ActorID, input.Kind, input.Comment, input.CorrectedAnswer, ranks, key, hash, input.CreatedAt)
	if err != nil {
		return domain.AnswerFeedback{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.AnswerFeedback{}, err
	}
	return input, nil
}

func validA1DispositionFeedback(input domain.AnswerFeedback, messageStatus, intent, answerDisposition string) bool {
	if !domain.IsA1DispositionFeedbackKind(input.Kind) {
		return true
	}
	if messageStatus != "completed" || intent != "knowledge_query" || strings.TrimSpace(input.Comment) == "" || len(input.ExpectedCitationRanks) != 0 {
		return false
	}
	switch input.Kind {
	case "should_clarify":
		return answerDisposition == "grounded_answer" || answerDisposition == "partial_answer" || answerDisposition == "insufficient_evidence"
	case "unnecessary_clarification":
		return answerDisposition == "clarification_required"
	case "incomplete_coverage":
		return answerDisposition == "grounded_answer" || answerDisposition == "partial_answer"
	default:
		return false
	}
}

func (s *PostgresStore) ListAnswerFeedback(ctx context.Context, messageID, kind string, limit int) ([]domain.AnswerFeedback, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	q := answerFeedbackSelect + ` WHERE ($1='' OR message_id=$1) AND ($2='' OR kind=$2) ORDER BY created_at DESC,feedback_id DESC LIMIT $3`
	rows, err := s.db.QueryContext(ctx, q, messageID, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.AnswerFeedback{}
	for rows.Next() {
		x, err := scanAnswerFeedback(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

const answerFeedbackSelect = `SELECT feedback_id,message_id,conversation_id,actor_id,kind,comment,corrected_answer,expected_citation_ranks,created_at FROM answer_feedback`

func answerFeedbackByKeyTx(ctx context.Context, tx *sql.Tx, key string) (domain.AnswerFeedback, bool, error) {
	x, err := scanAnswerFeedback(tx.QueryRowContext(ctx, answerFeedbackSelect+` WHERE idempotency_key=$1 FOR UPDATE`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AnswerFeedback{}, false, nil
	}
	return x, err == nil, err
}
func scanAnswerFeedback(row scanner) (domain.AnswerFeedback, error) {
	var x domain.AnswerFeedback
	var ranks []byte
	var created time.Time
	err := row.Scan(&x.FeedbackID, &x.MessageID, &x.ConversationID, &x.ActorID, &x.Kind, &x.Comment, &x.CorrectedAnswer, &ranks, &created)
	if err != nil {
		return x, err
	}
	if err = json.Unmarshal(ranks, &x.ExpectedCitationRanks); err != nil {
		return x, err
	}
	x.CreatedAt = created.UTC().Format(time.RFC3339)
	return x, nil
}

func (s *PostgresStore) CreateQualityExperiment(ctx context.Context, actor, key string, in domain.CreateQualityExperimentRequest) (domain.QualityExperiment, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(in.BaselineConfigVersion) == "" || strings.TrimSpace(in.BaselineRetrievalConfigVersion) == "" || strings.TrimSpace(in.CandidateConfigVersion) == "" || strings.TrimSpace(in.CandidateRetrievalConfigVersion) == "" || strings.TrimSpace(in.KnowledgeSnapshotID) == "" || strings.TrimSpace(in.KnowledgeSnapshotManifestHash) == "" || strings.TrimSpace(in.DatasetID) == "" || strings.TrimSpace(in.DatasetSHA256) == "" {
		return domain.QualityExperiment{}, ErrQualityInvalid
	}
	conditions, conditionsHash, err := qualityJSON(in.Conditions)
	if err != nil {
		return domain.QualityExperiment{}, ErrQualityInvalid
	}
	acceptance, _, err := qualityJSON(in.Acceptance)
	if err != nil {
		return domain.QualityExperiment{}, ErrQualityInvalid
	}
	hash, _, _ := canonicalHash(map[string]any{"input": in, "conditions": json.RawMessage(conditions), "acceptance": json.RawMessage(acceptance)})
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.QualityExperiment{}, err
	}
	defer tx.Rollback()
	if x, found, err := qualityExperimentByKeyTx(ctx, tx, key); err != nil {
		return x, err
	} else if found {
		var old string
		_ = tx.QueryRowContext(ctx, `SELECT request_hash FROM quality_experiments WHERE experiment_id=$1`, x.ExperimentID).Scan(&old)
		if old != hash {
			return x, fmt.Errorf("%w: idempotency key reused", ErrQualityConflict)
		}
		_ = tx.Commit()
		return x, nil
	}
	for _, pair := range [][2]string{{"agent_configs", in.BaselineConfigVersion}, {"retrieval_configs", in.BaselineRetrievalConfigVersion}, {"agent_configs", in.CandidateConfigVersion}, {"retrieval_configs", in.CandidateRetrievalConfigVersion}} {
		var ok bool
		column := "config_version"
		if pair[0] == "retrieval_configs" {
			column = "retrieval_config_version"
		}
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE %s=$1)`, pair[0], column), pair[1]).Scan(&ok); err != nil {
			return domain.QualityExperiment{}, err
		}
		if !ok {
			return domain.QualityExperiment{}, ErrQualityInvalid
		}
	}
	n, err := nextCounterTx(ctx, tx, "quality_experiment")
	if err != nil {
		return domain.QualityExperiment{}, err
	}
	x := domain.QualityExperiment{ExperimentID: fmt.Sprintf("qexp_%03d", n), Status: "draft", CreatedBy: actor, BaselineConfigVersion: in.BaselineConfigVersion, BaselineRetrievalConfigVersion: in.BaselineRetrievalConfigVersion, CandidateConfigVersion: in.CandidateConfigVersion, CandidateRetrievalConfigVersion: in.CandidateRetrievalConfigVersion, KnowledgeSnapshotID: in.KnowledgeSnapshotID, KnowledgeSnapshotManifestHash: in.KnowledgeSnapshotManifestHash, DatasetID: in.DatasetID, DatasetSHA256: in.DatasetSHA256, Conditions: conditions, ConditionsSHA256: conditionsHash, Acceptance: acceptance, CreatedAt: qnow()}
	_, err = tx.ExecContext(ctx, `INSERT INTO quality_experiments(experiment_id,status,created_by,baseline_config_version,baseline_retrieval_config_version,candidate_config_version,candidate_retrieval_config_version,knowledge_snapshot_id,knowledge_snapshot_manifest_hash,dataset_id,dataset_sha256,conditions,conditions_sha256,acceptance,idempotency_key,request_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,$14::jsonb,$15,$16,$17::timestamptz)`, x.ExperimentID, x.Status, x.CreatedBy, x.BaselineConfigVersion, x.BaselineRetrievalConfigVersion, x.CandidateConfigVersion, x.CandidateRetrievalConfigVersion, x.KnowledgeSnapshotID, x.KnowledgeSnapshotManifestHash, x.DatasetID, x.DatasetSHA256, x.Conditions, x.ConditionsSHA256, x.Acceptance, key, hash, x.CreatedAt)
	if err != nil {
		return x, err
	}
	if err = tx.Commit(); err != nil {
		return x, err
	}
	return x, nil
}

const qualityExperimentSelect = `SELECT experiment_id,status,created_by,baseline_config_version,baseline_retrieval_config_version,candidate_config_version,candidate_retrieval_config_version,knowledge_snapshot_id,knowledge_snapshot_manifest_hash,dataset_id,dataset_sha256,conditions,conditions_sha256,acceptance,COALESCE(baseline_report_id,''),COALESCE(candidate_report_id,''),decision_reason,COALESCE(release_id,''),created_at,frozen_at,approved_at,published_at FROM quality_experiments`

func scanQualityExperiment(row scanner) (domain.QualityExperiment, error) {
	var x domain.QualityExperiment
	var created time.Time
	var frozen, approved, published sql.NullTime
	err := row.Scan(&x.ExperimentID, &x.Status, &x.CreatedBy, &x.BaselineConfigVersion, &x.BaselineRetrievalConfigVersion, &x.CandidateConfigVersion, &x.CandidateRetrievalConfigVersion, &x.KnowledgeSnapshotID, &x.KnowledgeSnapshotManifestHash, &x.DatasetID, &x.DatasetSHA256, &x.Conditions, &x.ConditionsSHA256, &x.Acceptance, &x.BaselineReportID, &x.CandidateReportID, &x.DecisionReason, &x.ReleaseID, &created, &frozen, &approved, &published)
	if err != nil {
		return x, err
	}
	x.CreatedAt = created.UTC().Format(time.RFC3339)
	for _, p := range []struct {
		n sql.NullTime
		o **string
	}{{frozen, &x.FrozenAt}, {approved, &x.ApprovedAt}, {published, &x.PublishedAt}} {
		if p.n.Valid {
			v := p.n.Time.UTC().Format(time.RFC3339)
			*p.o = &v
		}
	}
	return x, nil
}
func qualityExperimentTx(ctx context.Context, tx *sql.Tx, id string, lock bool) (domain.QualityExperiment, bool, error) {
	q := qualityExperimentSelect + ` WHERE experiment_id=$1`
	if lock {
		q += ` FOR UPDATE`
	}
	x, err := scanQualityExperiment(tx.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return x, false, nil
	}
	if err == nil {
		x.FeedbackIDs, _ = feedbackIDsTx(ctx, tx, id)
	}
	return x, err == nil, err
}
func qualityExperimentByKeyTx(ctx context.Context, tx *sql.Tx, key string) (domain.QualityExperiment, bool, error) {
	x, err := scanQualityExperiment(tx.QueryRowContext(ctx, qualityExperimentSelect+` WHERE idempotency_key=$1 FOR UPDATE`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return x, false, nil
	}
	return x, err == nil, err
}
func feedbackIDsTx(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT feedback_id FROM quality_experiment_feedback WHERE experiment_id=$1 ORDER BY feedback_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var o []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		o = append(o, v)
	}
	return o, rows.Err()
}
func (s *PostgresStore) GetQualityExperiment(ctx context.Context, id string) (domain.QualityExperiment, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.QualityExperiment{}, false, err
	}
	defer tx.Rollback()
	x, ok, err := qualityExperimentTx(ctx, tx, id, false)
	if err == nil {
		err = tx.Commit()
	}
	return x, ok, err
}
func (s *PostgresStore) ListQualityExperiments(ctx context.Context, status string, limit int) (domain.QualityExperimentPage, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, qualityExperimentSelect+` WHERE ($1='' OR status=$1) ORDER BY created_at DESC,experiment_id DESC LIMIT $2`, status, limit)
	if err != nil {
		return domain.QualityExperimentPage{}, err
	}
	defer rows.Close()
	out := domain.QualityExperimentPage{Items: []domain.QualityExperiment{}}
	for rows.Next() {
		x, err := scanQualityExperiment(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, x)
	}
	return out, rows.Err()
}

func (s *PostgresStore) transitionExperiment(ctx context.Context, id, key string, allowed map[string]bool, actor string, apply func(*sql.Tx, *domain.QualityExperiment) error) (domain.QualityExperiment, error) {
	if strings.TrimSpace(key) == "" {
		return domain.QualityExperiment{}, ErrQualityInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.QualityExperiment{}, err
	}
	defer tx.Rollback()
	var previous json.RawMessage
	err = tx.QueryRowContext(ctx, `SELECT response FROM quality_operation_requests WHERE idempotency_key=$1 FOR UPDATE`, key).Scan(&previous)
	if err == nil {
		var saved domain.QualityExperiment
		if json.Unmarshal(previous, &saved) != nil {
			return domain.QualityExperiment{}, ErrQualityConflict
		}
		_ = tx.Commit()
		return saved, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.QualityExperiment{}, err
	}
	x, ok, err := qualityExperimentTx(ctx, tx, id, true)
	if err != nil {
		return x, err
	}
	if !ok {
		return x, ErrQualityNotFound
	}
	if !allowed[x.Status] {
		return x, fmt.Errorf("%w: illegal state %s", ErrQualityConflict, x.Status)
	}
	if err = apply(tx, &x); err != nil {
		return x, err
	}
	response, _ := json.Marshal(x)
	hash, _, _ := canonicalHash(map[string]string{"resource": id, "actor": actor})
	_, err = tx.ExecContext(ctx, `INSERT INTO quality_operation_requests(idempotency_key,operation,resource_id,request_hash,response) VALUES($1,'experiment_transition',$2,$3,$4::jsonb)`, key, id, hash, response)
	if err != nil {
		return x, err
	}
	if err = tx.Commit(); err != nil {
		return x, err
	}
	fresh, found, freshErr := s.GetQualityExperiment(ctx, id)
	if freshErr != nil {
		return x, freshErr
	}
	if !found {
		return x, ErrQualityNotFound
	}
	return fresh, nil
}

func (s *PostgresStore) AttachQualityExperimentFeedback(ctx context.Context, actor, id, key, feedbackID string) (domain.QualityExperiment, error) {
	return s.transitionExperiment(ctx, id, key, map[string]bool{"draft": true, "collecting_feedback": true}, actor, func(tx *sql.Tx, x *domain.QualityExperiment) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM answer_feedback WHERE feedback_id=$1)`, feedbackID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrQualityNotFound
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO quality_experiment_feedback(experiment_id,feedback_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, x.ExperimentID, feedbackID); err != nil {
			return err
		}
		if x.Status == "draft" {
			_, err := tx.ExecContext(ctx, `UPDATE quality_experiments SET status='collecting_feedback' WHERE experiment_id=$1`, x.ExperimentID)
			x.Status = "collecting_feedback"
			return err
		}
		return nil
	})
}
func (s *PostgresStore) FreezeQualityExperiment(ctx context.Context, actor, id, key string) (domain.QualityExperiment, error) {
	return s.transitionExperiment(ctx, id, key, map[string]bool{"draft": true, "collecting_feedback": true}, actor, func(tx *sql.Tx, x *domain.QualityExperiment) error {
		now := time.Now().UTC()
		_, err := tx.ExecContext(ctx, `UPDATE quality_experiments SET status='evaluating', frozen_at=$2 WHERE experiment_id=$1`, x.ExperimentID, now)
		x.Status = "evaluating"
		v := now.Format(time.RFC3339)
		x.FrozenAt = &v
		return err
	})
}

type qualityReportEvidence struct {
	Mode               string `json:"mode"`
	EvaluationRole     string `json:"evaluation_role"`
	QualityEligible    *bool  `json:"quality_eligible"`
	ComparisonEligible *bool  `json:"comparison_eligible"`
	DatasetSHA256      string `json:"dataset_sha256"`
	KnowledgeScope     struct {
		SnapshotID   string `json:"snapshot_id"`
		ManifestHash string `json:"manifest_hash"`
	} `json:"knowledge_scope"`
	QualityExperiment struct {
		ExperimentID     string `json:"experiment_id"`
		Role             string `json:"role"`
		ConditionsSHA256 string `json:"conditions_sha256"`
	} `json:"quality_experiment"`
	ProviderEvidence struct {
		QualityEligible    *bool `json:"quality_eligible"`
		ComparisonEligible *bool `json:"comparison_eligible"`
	} `json:"provider_evidence"`
	QualityGate struct {
		GateID string `json:"gate_id"`
		Passed *bool  `json:"passed"`
	} `json:"quality_gate"`
}

func parseQualityReportEvidence(report domain.EvalReport) (qualityReportEvidence, error) {
	var evidence qualityReportEvidence
	if json.Unmarshal(report.Report, &evidence) != nil {
		return qualityReportEvidence{}, ErrQualityInvalid
	}
	return evidence, nil
}

func reportEvidenceRole(evidence qualityReportEvidence) (string, bool) {
	role := strings.TrimSpace(evidence.EvaluationRole)
	embeddedRole := strings.TrimSpace(evidence.QualityExperiment.Role)
	if role == "" {
		role = embeddedRole
	}
	return role, role != "" && (embeddedRole == "" || embeddedRole == role)
}

func reportEvidenceEligibility(topLevel, provider *bool) bool {
	return topLevel != nil && *topLevel && provider != nil && *provider
}

func candidateGateID(conditions json.RawMessage) string {
	var raw struct {
		CandidateGateID string `json:"candidate_gate_id"`
	}
	if json.Unmarshal(conditions, &raw) != nil {
		return ""
	}
	return strings.TrimSpace(raw.CandidateGateID)
}

func validateQualityReportAssociation(report domain.EvalReport, experiment domain.QualityExperiment, role string) error {
	evidence, err := parseQualityReportEvidence(report)
	if err != nil {
		return fmt.Errorf("%w: report evidence is invalid", ErrQualityConflict)
	}
	wantConfig, wantRetrieval := experiment.BaselineConfigVersion, experiment.BaselineRetrievalConfigVersion
	if role == "candidate" {
		wantConfig, wantRetrieval = experiment.CandidateConfigVersion, experiment.CandidateRetrievalConfigVersion
	}
	if report.DatasetID != experiment.DatasetID || report.ConfigVersion != wantConfig || report.RetrievalConfigVersion != wantRetrieval || evidence.KnowledgeScope.SnapshotID != experiment.KnowledgeSnapshotID || evidence.KnowledgeScope.ManifestHash != experiment.KnowledgeSnapshotManifestHash || evidence.QualityExperiment.ConditionsSHA256 != experiment.ConditionsSHA256 || evidence.DatasetSHA256 != experiment.DatasetSHA256 {
		return fmt.Errorf("%w: report identity does not match frozen experiment", ErrQualityConflict)
	}
	wantGateID := candidateGateID(experiment.Conditions)
	if wantGateID == "" {
		return nil
	}
	reportRole, roleValid := reportEvidenceRole(evidence)
	if !roleValid || reportRole != role {
		return fmt.Errorf("%w: report role does not match association role", ErrQualityConflict)
	}
	if evidence.Mode != "runtime-remote" {
		return fmt.Errorf("%w: report must be runtime-remote", ErrQualityConflict)
	}
	if evidence.QualityExperiment.ExperimentID != "" && evidence.QualityExperiment.ExperimentID != experiment.ExperimentID {
		return fmt.Errorf("%w: report experiment does not match association", ErrQualityConflict)
	}

	if role == "baseline" {
		if !reportEvidenceEligibility(evidence.ComparisonEligible, evidence.ProviderEvidence.ComparisonEligible) {
			return fmt.Errorf("%w: baseline report is not comparison eligible", ErrQualityConflict)
		}
		return nil
	}
	if !reportEvidenceEligibility(evidence.QualityEligible, evidence.ProviderEvidence.QualityEligible) {
		return fmt.Errorf("%w: candidate report is not quality eligible", ErrQualityConflict)
	}
	if strings.TrimSpace(evidence.QualityGate.GateID) != wantGateID || evidence.QualityGate.Passed == nil || !*evidence.QualityGate.Passed {
		return fmt.Errorf("%w: candidate quality gate has not passed", ErrQualityConflict)
	}
	return nil
}

func reportGatePassed(report domain.EvalReport) bool {
	var value map[string]any
	if json.Unmarshal(report.Report, &value) != nil {
		return false
	}
	for _, key := range []string{"gate_passed", "quality_gate_passed"} {
		if passed, ok := value[key].(bool); ok && passed {
			return true
		}
	}
	for _, key := range []string{"gate", "quality_gate"} {
		if gate, ok := value[key].(map[string]any); ok {
			if passed, ok := gate["passed"].(bool); ok && passed {
				return true
			}
		}
	}
	return false
}

func (s *PostgresStore) RegisterQualityExperimentReport(ctx context.Context, actor, id, key, reportID, role string) (domain.QualityExperiment, error) {
	if role != "baseline" && role != "candidate" || strings.TrimSpace(reportID) == "" {
		return domain.QualityExperiment{}, ErrQualityInvalid
	}
	return s.transitionExperiment(ctx, id, key, map[string]bool{"evaluating": true}, actor, func(tx *sql.Tx, x *domain.QualityExperiment) error {
		report, err := scanEvalReport(tx.QueryRowContext(ctx, evalReportSelect+` WHERE report_id=$1`, reportID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrQualityNotFound
		}
		if err != nil {
			return err
		}
		if report.LegacyFallback || report.InvalidForQuality {
			return fmt.Errorf("%w: report is not eligible for quality approval", ErrQualityConflict)
		}
		if err := validateQualityReportAssociation(report, *x, role); err != nil {
			return err
		}
		column := "baseline_report_id"
		if role == "candidate" {
			column = "candidate_report_id"
		}
		_, err = tx.ExecContext(ctx, `UPDATE quality_experiments SET `+column+`=$2 WHERE experiment_id=$1`, x.ExperimentID, reportID)
		if err != nil {
			return err
		}
		if role == "baseline" {
			x.BaselineReportID = reportID
		} else {
			x.CandidateReportID = reportID
		}
		if x.BaselineReportID != "" && x.CandidateReportID != "" {
			_, err = tx.ExecContext(ctx, `UPDATE quality_experiments SET status='review_ready' WHERE experiment_id=$1`, x.ExperimentID)
			x.Status = "review_ready"
		}
		return err
	})
}
func (s *PostgresStore) ApproveQualityExperiment(ctx context.Context, actor, id, key, reason string) (domain.QualityExperiment, error) {
	return s.transitionExperiment(ctx, id, key, map[string]bool{"review_ready": true}, actor, func(tx *sql.Tx, x *domain.QualityExperiment) error {
		report, err := scanEvalReport(tx.QueryRowContext(ctx, evalReportSelect+` WHERE report_id=$1`, x.CandidateReportID))
		if err != nil {
			return fmt.Errorf("%w: candidate report unavailable", ErrQualityConflict)
		}
		if report.LegacyFallback || report.InvalidForQuality {
			return fmt.Errorf("%w: candidate report is not eligible for quality approval", ErrQualityConflict)
		}
		if err := validateQualityReportAssociation(report, *x, "candidate"); err != nil {
			return err
		}
		if candidateGateID(x.Conditions) == "" && !reportGatePassed(report) {
			return fmt.Errorf("%w: candidate gate has not passed", ErrQualityConflict)
		}
		now := time.Now().UTC()
		_, err = tx.ExecContext(ctx, `UPDATE quality_experiments SET status='approved',decision_reason=$2,approved_at=$3 WHERE experiment_id=$1`, x.ExperimentID, reason, now)
		x.Status = "approved"
		x.DecisionReason = reason
		v := now.Format(time.RFC3339)
		x.ApprovedAt = &v
		return err
	})
}

func scanQualityRelease(row scanner) (domain.QualityRelease, error) {
	var x domain.QualityRelease
	var created time.Time
	var experiment, previous sql.NullString
	err := row.Scan(&x.ReleaseID, &x.Status, &x.ConfigVersion, &x.RetrievalConfigVersion, &experiment, &previous, &x.CreatedBy, &created)
	if err != nil {
		return x, err
	}
	x.ExperimentID = nullableString(experiment)
	x.PreviousReleaseID = nullableString(previous)
	x.CreatedAt = created.UTC().Format(time.RFC3339)
	return x, nil
}

const qualityReleaseSelect = `SELECT release_id,status,config_version,retrieval_config_version,experiment_id,previous_release_id,created_by,created_at FROM quality_releases`

func (s *PostgresStore) PublishQualityExperiment(ctx context.Context, actor, id, key string) (domain.QualityRelease, error) {
	if strings.TrimSpace(key) == "" {
		return domain.QualityRelease{}, ErrQualityInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.QualityRelease{}, err
	}
	defer tx.Rollback()
	var previousResponse json.RawMessage
	err = tx.QueryRowContext(ctx, `SELECT response FROM quality_operation_requests WHERE idempotency_key=$1 FOR UPDATE`, key).Scan(&previousResponse)
	if err == nil {
		var saved domain.QualityRelease
		if json.Unmarshal(previousResponse, &saved) != nil {
			return saved, ErrQualityConflict
		}
		_ = tx.Commit()
		return saved, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.QualityRelease{}, err
	}
	x, found, err := qualityExperimentTx(ctx, tx, id, true)
	if err != nil {
		return domain.QualityRelease{}, err
	}
	if !found {
		return domain.QualityRelease{}, ErrQualityNotFound
	}
	if x.Status != "approved" {
		return domain.QualityRelease{}, fmt.Errorf("%w: experiment is not approved", ErrQualityConflict)
	}
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT release_id FROM quality_active_release WHERE singleton=TRUE FOR UPDATE`).Scan(&previous)
	if err != nil {
		return domain.QualityRelease{}, err
	}
	n, err := nextCounterTx(ctx, tx, "quality_release")
	if err != nil {
		return domain.QualityRelease{}, err
	}
	release := domain.QualityRelease{ReleaseID: fmt.Sprintf("qrel_%03d", n), Status: "published", ConfigVersion: x.CandidateConfigVersion, RetrievalConfigVersion: x.CandidateRetrievalConfigVersion, ExperimentID: x.ExperimentID, PreviousReleaseID: previous, CreatedBy: actor, CreatedAt: qnow()}
	_, err = tx.ExecContext(ctx, `INSERT INTO quality_releases(release_id,status,config_version,retrieval_config_version,experiment_id,previous_release_id,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8::timestamptz)`, release.ReleaseID, release.Status, release.ConfigVersion, release.RetrievalConfigVersion, release.ExperimentID, release.PreviousReleaseID, release.CreatedBy, release.CreatedAt)
	if err != nil {
		return domain.QualityRelease{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE quality_active_release SET release_id=$1,lock_version=lock_version+1 WHERE singleton=TRUE`, release.ReleaseID)
	if err != nil {
		return domain.QualityRelease{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE quality_experiments SET status='published',release_id=$2,published_at=NOW() WHERE experiment_id=$1`, x.ExperimentID, release.ReleaseID)
	if err != nil {
		return domain.QualityRelease{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO quality_release_events(event_id,release_id,event_type,actor_id) VALUES($1,$2,'publish',$3)`, `qrevt_publish_`+release.ReleaseID, release.ReleaseID, actor)
	if err != nil {
		return domain.QualityRelease{}, err
	}
	response, _ := json.Marshal(release)
	hash, _, _ := canonicalHash(map[string]string{"resource": id, "actor": actor})
	_, err = tx.ExecContext(ctx, `INSERT INTO quality_operation_requests(idempotency_key,operation,resource_id,request_hash,response) VALUES($1,'publish',$2,$3,$4::jsonb)`, key, id, hash, response)
	if err != nil {
		return domain.QualityRelease{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.QualityRelease{}, err
	}
	return release, nil
}
func (s *PostgresStore) ListQualityReleases(ctx context.Context, limit int) (domain.QualityReleasePage, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, qualityReleaseSelect+` ORDER BY created_at DESC,release_id DESC LIMIT $1`, limit)
	if err != nil {
		return domain.QualityReleasePage{}, err
	}
	defer rows.Close()
	p := domain.QualityReleasePage{Items: []domain.QualityRelease{}}
	for rows.Next() {
		x, err := scanQualityRelease(rows)
		if err != nil {
			return p, err
		}
		p.Items = append(p.Items, x)
	}
	return p, rows.Err()
}
func (s *PostgresStore) RollbackQualityRelease(ctx context.Context, actor, id, key string) (domain.QualityRelease, error) {
	if strings.TrimSpace(key) == "" {
		return domain.QualityRelease{}, ErrQualityInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.QualityRelease{}, err
	}
	defer tx.Rollback()
	var previousResponse json.RawMessage
	err = tx.QueryRowContext(ctx, `SELECT response FROM quality_operation_requests WHERE idempotency_key=$1 FOR UPDATE`, key).Scan(&previousResponse)
	if err == nil {
		var saved domain.QualityRelease
		if json.Unmarshal(previousResponse, &saved) != nil {
			return saved, ErrQualityConflict
		}
		_ = tx.Commit()
		return saved, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.QualityRelease{}, err
	}
	target, err := scanQualityRelease(tx.QueryRowContext(ctx, qualityReleaseSelect+` WHERE release_id=$1 FOR SHARE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.QualityRelease{}, ErrQualityNotFound
	}
	if err != nil {
		return domain.QualityRelease{}, err
	}
	var previous string
	if err = tx.QueryRowContext(ctx, `SELECT release_id FROM quality_active_release WHERE singleton=TRUE FOR UPDATE`).Scan(&previous); err != nil {
		return domain.QualityRelease{}, err
	}
	n, err := nextCounterTx(ctx, tx, "quality_release")
	if err != nil {
		return domain.QualityRelease{}, err
	}
	release := domain.QualityRelease{ReleaseID: fmt.Sprintf("qrel_%03d", n), Status: "published", ConfigVersion: target.ConfigVersion, RetrievalConfigVersion: target.RetrievalConfigVersion, PreviousReleaseID: previous, CreatedBy: actor, CreatedAt: qnow()}
	_, err = tx.ExecContext(ctx, `INSERT INTO quality_releases(release_id,status,config_version,retrieval_config_version,previous_release_id,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7::timestamptz)`, release.ReleaseID, release.Status, release.ConfigVersion, release.RetrievalConfigVersion, release.PreviousReleaseID, actor, release.CreatedAt)
	if err != nil {
		return release, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE quality_active_release SET release_id=$1,lock_version=lock_version+1 WHERE singleton=TRUE`, release.ReleaseID)
	if err != nil {
		return release, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO quality_release_events(event_id,release_id,event_type,actor_id) VALUES($1,$2,'rollback',$3)`, `qrevt_rollback_`+release.ReleaseID, release.ReleaseID, actor)
	if err != nil {
		return release, err
	}
	response, _ := json.Marshal(release)
	hash, _, _ := canonicalHash(map[string]string{"resource": id, "actor": actor})
	_, err = tx.ExecContext(ctx, `INSERT INTO quality_operation_requests(idempotency_key,operation,resource_id,request_hash,response) VALUES($1,'rollback',$2,$3,$4::jsonb)`, key, id, hash, response)
	if err != nil {
		return release, err
	}
	if err = tx.Commit(); err != nil {
		return release, err
	}
	return release, nil
}
