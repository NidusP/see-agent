package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
)

func TestEvalReportUsesTestProviderMarksOnlyExplicitTestEvidence(t *testing.T) {
	if !evalReportUsesTestProvider(json.RawMessage(`{"samples":[{"execution":{"generation":{"provider":"test_injected"}}}]}`)) {
		t.Fatal("test provider report was not marked invalid for quality")
	}
	if evalReportUsesTestProvider(json.RawMessage(`{"samples":[{"execution":{"generation":{"provider":"openai_compatible"}}}]}`)) {
		t.Fatal("real provider report was incorrectly marked invalid for quality")
	}
}

func TestValidateQualityReportAssociation(t *testing.T) {
	experiment := domain.QualityExperiment{
		ExperimentID:                    "qexp_001",
		BaselineConfigVersion:           "cfg_baseline",
		BaselineRetrievalConfigVersion:  "rcfg_baseline",
		CandidateConfigVersion:          "cfg_candidate",
		CandidateRetrievalConfigVersion: "rcfg_candidate",
		KnowledgeSnapshotID:             "snap_001",
		KnowledgeSnapshotManifestHash:   "sha256:manifest",
		DatasetID:                       "v1-q1",
		DatasetSHA256:                   "sha256:dataset",
		Conditions:                      json.RawMessage(`{"candidate_gate_id":"a1-q1-candidate-v1"}`),
		ConditionsSHA256:                "sha256:conditions",
	}

	tests := []struct {
		name    string
		role    string
		mutate  func(map[string]any)
		wantErr bool
	}{
		{
			name: "rejects false report role",
			role: "candidate",
			mutate: func(payload map[string]any) {
				payload["evaluation_role"] = "baseline"
			},
			wantErr: true,
		},
		{
			name: "rejects missing candidate report eligibility",
			role: "candidate",
			mutate: func(payload map[string]any) {
				delete(payload, "quality_eligible")
			},
			wantErr: true,
		},
		{
			name: "rejects missing candidate provider eligibility",
			role: "candidate",
			mutate: func(payload map[string]any) {
				delete(payload, "provider_evidence")
			},
			wantErr: true,
		},
		{
			name: "rejects weak provider eligibility",
			role: "candidate",
			mutate: func(payload map[string]any) {
				payload["provider_evidence"] = map[string]any{"quality_eligible": false}
			},
			wantErr: true,
		},
		{
			name: "rejects failed candidate gate",
			role: "candidate",
			mutate: func(payload map[string]any) {
				payload["quality_gate"] = map[string]any{"gate_id": "a1-q1-candidate-v1", "passed": false}
			},
			wantErr: true,
		},
		{
			name: "rejects wrong conditions hash",
			role: "candidate",
			mutate: func(payload map[string]any) {
				payload["quality_experiment"].(map[string]any)["conditions_sha256"] = "sha256:wrong"
			},
			wantErr: true,
		},
		{
			name: "rejects missing baseline report comparison eligibility",
			role: "baseline",
			mutate: func(payload map[string]any) {
				delete(payload, "comparison_eligible")
			},
			wantErr: true,
		},
		{
			name: "rejects missing baseline provider comparison eligibility",
			role: "baseline",
			mutate: func(payload map[string]any) {
				delete(payload, "provider_evidence")
			},
			wantErr: true,
		},
		{name: "accepts valid candidate association", role: "candidate"},
		{name: "accepts valid baseline association", role: "baseline"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := q1ReportPayload("report-"+strings.ReplaceAll(test.name, " ", "-"), experiment, test.role, true, experiment.DatasetSHA256)
			if test.mutate != nil {
				test.mutate(payload)
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			config, retrieval := experiment.BaselineConfigVersion, experiment.BaselineRetrievalConfigVersion
			if test.role == "candidate" {
				config, retrieval = experiment.CandidateConfigVersion, experiment.CandidateRetrievalConfigVersion
			}
			report := domain.EvalReport{EvalReportRegistration: domain.EvalReportRegistration{DatasetID: experiment.DatasetID, ConfigVersion: config, RetrievalConfigVersion: retrieval, Report: raw}}
			err = validateQualityReportAssociation(report, experiment, test.role)
			if test.wantErr && !errors.Is(err, ErrQualityConflict) {
				t.Fatalf("validateQualityReportAssociation() error = %v, want quality conflict", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("validateQualityReportAssociation() error = %v", err)
			}
		})
	}
}

func TestValidateQualityReportAssociationPreservesGenericQ1Compatibility(t *testing.T) {
	experiment := domain.QualityExperiment{
		BaselineConfigVersion:           "cfg_baseline",
		BaselineRetrievalConfigVersion:  "rcfg_baseline",
		CandidateConfigVersion:          "cfg_candidate",
		CandidateRetrievalConfigVersion: "rcfg_candidate",
		KnowledgeSnapshotID:             "snap_001",
		KnowledgeSnapshotManifestHash:   "sha256:manifest",
		DatasetID:                       "generic-q1",
		DatasetSHA256:                   "sha256:dataset",
		Conditions:                      json.RawMessage(`{"top_k":3,"rerank_enabled":false}`),
		ConditionsSHA256:                "sha256:conditions",
	}
	raw := json.RawMessage(`{"dataset_sha256":"sha256:dataset","knowledge_scope":{"snapshot_id":"snap_001","manifest_hash":"sha256:manifest"},"quality_experiment":{"conditions_sha256":"sha256:conditions"}}`)

	for _, test := range []struct {
		name      string
		role      string
		config    string
		retrieval string
	}{
		{name: "baseline", role: "baseline", config: experiment.BaselineConfigVersion, retrieval: experiment.BaselineRetrievalConfigVersion},
		{name: "candidate", role: "candidate", config: experiment.CandidateConfigVersion, retrieval: experiment.CandidateRetrievalConfigVersion},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := domain.EvalReport{EvalReportRegistration: domain.EvalReportRegistration{DatasetID: experiment.DatasetID, ConfigVersion: test.config, RetrievalConfigVersion: test.retrieval, Report: raw}}
			if err := validateQualityReportAssociation(report, experiment, test.role); err != nil {
				t.Fatalf("generic Q1 association error = %v", err)
			}
		})
	}
}

func TestValidA1DispositionFeedback(t *testing.T) {
	tests := []struct {
		name        string
		feedback    domain.AnswerFeedback
		status      string
		intent      string
		disposition string
		want        bool
	}{
		{name: "should clarify grounded answer", feedback: domain.AnswerFeedback{Kind: "should_clarify", Comment: "缺少适用地点"}, status: "completed", intent: "knowledge_query", disposition: "grounded_answer", want: true},
		{name: "should clarify insufficient evidence", feedback: domain.AnswerFeedback{Kind: "should_clarify", Comment: "问题范围没有明确"}, status: "completed", intent: "knowledge_query", disposition: "insufficient_evidence", want: true},
		{name: "unnecessary clarification", feedback: domain.AnswerFeedback{Kind: "unnecessary_clarification", Comment: "上下文已经指明差旅报销"}, status: "completed", intent: "knowledge_query", disposition: "clarification_required", want: true},
		{name: "incomplete coverage partial answer", feedback: domain.AnswerFeedback{Kind: "incomplete_coverage", Comment: "遗漏第三项已知规则"}, status: "completed", intent: "knowledge_query", disposition: "partial_answer", want: true},
		{name: "new feedback rejects wrong disposition", feedback: domain.AnswerFeedback{Kind: "unnecessary_clarification", Comment: "不该追问"}, status: "completed", intent: "knowledge_query", disposition: "grounded_answer", want: false},
		{name: "new feedback rejects blank comment", feedback: domain.AnswerFeedback{Kind: "should_clarify", Comment: "  "}, status: "completed", intent: "knowledge_query", disposition: "grounded_answer", want: false},
		{name: "new feedback rejects citation rank", feedback: domain.AnswerFeedback{Kind: "incomplete_coverage", Comment: "遗漏规则", ExpectedCitationRanks: []int{1}}, status: "completed", intent: "knowledge_query", disposition: "grounded_answer", want: false},
		{name: "new feedback rejects controlled response", feedback: domain.AnswerFeedback{Kind: "should_clarify", Comment: "错误分类"}, status: "completed", intent: "capability_help", disposition: "controlled_response", want: false},
		{name: "new feedback rejects failed message", feedback: domain.AnswerFeedback{Kind: "should_clarify", Comment: "错误分类"}, status: "failed", intent: "knowledge_query", disposition: "grounded_answer", want: false},
		{name: "existing feedback remains compatible", feedback: domain.AnswerFeedback{Kind: "wrong_evidence", ExpectedCitationRanks: []int{1}}, status: "completed", intent: "knowledge_query", disposition: "grounded_answer", want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validA1DispositionFeedback(test.feedback, test.status, test.intent, test.disposition); got != test.want {
				t.Fatalf("validA1DispositionFeedback(%#v, %q, %q, %q) = %v, want %v", test.feedback, test.status, test.intent, test.disposition, got, test.want)
			}
		})
	}
}

func TestPostgresQualityOperationsReleaseLifecycle(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("Q1_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set Q1_TEST_DATABASE_URL to run the isolated Postgres Q1 smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("q1_%d", time.Now().UTC().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteK3Identifier(schema)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quoteK3Identifier(schema)+` CASCADE`)
	}()
	store, err := NewPostgresStore(ctx, k3DatabaseURLWithSearchPath(t, baseURL, schema))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	versions, err := store.ListSchemaMigrations(ctx)
	if err != nil || strings.Join(versions, ",") != "001,002,003,004,005,006,007,008,009,010,011,012,013,014,015,016,017,018,019,020,021,022,023,024,025" {
		t.Fatalf("migrations=%v err=%v", versions, err)
	}

	baselineAgent, baselineRetrieval := store.ActiveConfig(), store.ActiveRetrievalConfig()
	if baselineRetrieval.RetrievalStrategy != domain.RetrievalStrategyNativeDenseV1 {
		t.Fatalf("bootstrap retrieval strategy = %q, want %q", baselineRetrieval.RetrievalStrategy, domain.RetrievalStrategyNativeDenseV1)
	}
	releases, err := store.ListQualityReleases(ctx, 10)
	if err != nil || len(releases.Items) != 1 {
		t.Fatalf("bootstrap releases=%#v err=%v", releases, err)
	}
	bootstrap := releases.Items[0]
	candidateAgent := store.CreateConfig("candidate-model", "prompt_v2", "concise", "", .2, 1, 1024)
	candidateRetrieval := store.CreateRetrievalConfig(domain.RetrievalStrategyNativeDenseV1, "qwen3-embedding:0.6b", 3, false, 0.15)
	if got := store.ActiveConfig(); got.ConfigVersion != baselineAgent.ConfigVersion {
		t.Fatalf("candidate config became active: %#v", got)
	}
	if got := store.ActiveRetrievalConfig(); got.RetrievalConfigVersion != baselineRetrieval.RetrievalConfigVersion {
		t.Fatalf("candidate retrieval became active: %#v", got)
	}
	if created := store.CreateOwnedConversation("user_admin", "candidate isolated"); created.ConfigVersion != baselineAgent.ConfigVersion || created.RetrievalConfigVersion != baselineRetrieval.RetrievalConfigVersion {
		t.Fatalf("candidate changed new conversation: %#v", created)
	}

	revision := k3ReadyRevision(t, ctx, store, "q1", "sha256:q1")
	if _, err := store.PublishKnowledgeRevision(ctx, revision.RevisionID, "admin", "q1"); err != nil {
		t.Fatal(err)
	}
	scope, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conversation := store.CreateOwnedConversation("user_admin", "feedback evidence")
	_, assistant, _, err := store.AppendScopedRuntimeExchange(ctx, conversation.ConversationID, "", "question", scope, k5RuntimeResult(conversation.ConversationID, "q1_message", scope, revision, "answer"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAnswerFeedback(ctx, "admin", "q1-feedback-invalid", domain.AnswerFeedback{MessageID: assistant.MessageID, Kind: "wrong_evidence", ExpectedCitationRanks: []int{2}}); !errors.Is(err, ErrQualityInvalid) {
		t.Fatalf("missing citation rank error=%v", err)
	}
	feedback, err := store.CreateAnswerFeedback(ctx, "admin", "q1-feedback-valid", domain.AnswerFeedback{MessageID: assistant.MessageID, Kind: "wrong_evidence", ExpectedCitationRanks: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	a1Conversation := store.CreateOwnedConversation("user_admin", "a1 feedback evidence")
	_, a1Assistant, _, err := store.AppendScopedRuntimeExchange(ctx, a1Conversation.ConversationID, "", "question with missing location", scope, a1GroundedResult(a1Conversation.ConversationID, "q1_a1_message", scope, revision, "answer"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAnswerFeedback(ctx, "admin", "q1-a1-feedback-valid", domain.AnswerFeedback{MessageID: a1Assistant.MessageID, Kind: "should_clarify", Comment: "location changes the answer"}); err != nil {
		t.Fatalf("valid should_clarify feedback: %v", err)
	}
	if _, err = store.CreateAnswerFeedback(ctx, "admin", "q1-a1-feedback-empty", domain.AnswerFeedback{MessageID: a1Assistant.MessageID, Kind: "should_clarify", Comment: " "}); !errors.Is(err, ErrQualityInvalid) {
		t.Fatalf("blank should_clarify comment error=%v", err)
	}
	if _, err = store.CreateAnswerFeedback(ctx, "admin", "q1-a1-feedback-disposition", domain.AnswerFeedback{MessageID: a1Assistant.MessageID, Kind: "unnecessary_clarification", Comment: "already answerable"}); !errors.Is(err, ErrQualityInvalid) {
		t.Fatalf("wrong a1 feedback disposition error=%v", err)
	}

	create := func(key string) domain.QualityExperiment {
		x, err := store.CreateQualityExperiment(ctx, "admin", key, domain.CreateQualityExperimentRequest{BaselineConfigVersion: baselineAgent.ConfigVersion, BaselineRetrievalConfigVersion: baselineRetrieval.RetrievalConfigVersion, CandidateConfigVersion: candidateAgent.ConfigVersion, CandidateRetrievalConfigVersion: candidateRetrieval.RetrievalConfigVersion, KnowledgeSnapshotID: scope.SnapshotID, KnowledgeSnapshotManifestHash: scope.ManifestHash, DatasetID: "q1-fixture", DatasetSHA256: "sha256:q1dataset", Conditions: json.RawMessage(`{"candidate_gate_id":"a1-q1-candidate-v1","top_k":3,"rerank_enabled":false}`), Acceptance: json.RawMessage(`{"gate":"required"}`)})
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	experiment := create("q1-experiment-failing")
	if _, err = store.AttachQualityExperimentFeedback(ctx, "admin", experiment.ExperimentID, "q1-attach", feedback.FeedbackID); err != nil {
		t.Fatal(err)
	}
	experiment, err = store.FreezeQualityExperiment(ctx, "admin", experiment.ExperimentID, "q1-freeze")
	if err != nil || experiment.Status != "evaluating" {
		t.Fatalf("freeze=%#v err=%v", experiment, err)
	}
	mismatch := q1Report("q1-mismatch", baselineAgent.ConfigVersion, baselineRetrieval.RetrievalConfigVersion, experiment, "baseline", true, "sha256:wrong")
	if _, err = store.RegisterEvalReport(ctx, "q1-mismatch", mismatch); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RegisterQualityExperimentReport(ctx, "admin", experiment.ExperimentID, "q1-link-mismatch", "q1-mismatch", "baseline"); !errors.Is(err, ErrQualityConflict) {
		t.Fatalf("mismatch accepted: %v", err)
	}
	failedGate := q1Report("q1-failed-gate", candidateAgent.ConfigVersion, candidateRetrieval.RetrievalConfigVersion, experiment, "candidate", false, experiment.DatasetSHA256)
	if _, err = store.RegisterEvalReport(ctx, "q1-failed-gate", failedGate); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RegisterQualityExperimentReport(ctx, "admin", experiment.ExperimentID, "q1-link-failed-gate", failedGate.ReportID, "candidate"); !errors.Is(err, ErrQualityConflict) {
		t.Fatalf("failed candidate gate accepted: %v", err)
	}

	publishExperiment := create("q1-experiment-publish")
	publishExperiment, err = store.FreezeQualityExperiment(ctx, "admin", publishExperiment.ExperimentID, "q1-freeze-publish")
	if err != nil {
		t.Fatal(err)
	}
	q1LinkReports(t, ctx, store, publishExperiment, baselineAgent, baselineRetrieval, candidateAgent, candidateRetrieval, true)
	if _, err = store.ApproveQualityExperiment(ctx, "admin", publishExperiment.ExperimentID, "q1-approve-publish", "gate passed"); err != nil {
		t.Fatal(err)
	}
	published, err := store.PublishQualityExperiment(ctx, "admin", publishExperiment.ExperimentID, "q1-publish")
	if err != nil {
		t.Fatal(err)
	}
	if got := store.CreateOwnedConversation("user_admin", "published"); got.ConfigVersion != candidateAgent.ConfigVersion || got.RetrievalConfigVersion != candidateRetrieval.RetrievalConfigVersion {
		t.Fatalf("published pair not bound: %#v", got)
	}
	rolled, err := store.RollbackQualityRelease(ctx, "admin", bootstrap.ReleaseID, "q1-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if rolled.PreviousReleaseID != published.ReleaseID {
		t.Fatalf("rollback lineage=%#v", rolled)
	}
	if got := store.CreateOwnedConversation("user_admin", "rolled back"); got.ConfigVersion != baselineAgent.ConfigVersion || got.RetrievalConfigVersion != baselineRetrieval.RetrievalConfigVersion {
		t.Fatalf("rollback pair not bound: %#v", got)
	}
	finalReleases, err := store.ListQualityReleases(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, release := range finalReleases.Items {
		status[release.ReleaseID] = release.Status
	}
	if status[bootstrap.ReleaseID] != "published" || status[published.ReleaseID] != "published" {
		t.Fatalf("historical releases mutated: %#v", status)
	}
}

func q1Report(id, config, retrieval string, experiment domain.QualityExperiment, role string, passed bool, datasetSHA string) domain.EvalReportRegistration {
	payload, _ := json.Marshal(q1ReportPayload(id, experiment, role, passed, datasetSHA))
	return domain.EvalReportRegistration{ReportID: id, DatasetID: experiment.DatasetID, ModelID: "llama3.2:latest", ConfigVersion: config, RetrievalConfigVersion: retrieval, GenerationConfig: json.RawMessage(`{}`), Metrics: json.RawMessage(`{}`), CompletedAt: "2026-07-17T00:00:00Z", Report: payload}
}

func q1ReportPayload(id string, experiment domain.QualityExperiment, role string, passed bool, datasetSHA string) map[string]any {
	payload := map[string]any{
		"report_id":          id,
		"mode":               "runtime-remote",
		"evaluation_role":    role,
		"dataset_id":         experiment.DatasetID,
		"dataset_sha256":     datasetSHA,
		"knowledge_scope":    map[string]string{"snapshot_id": experiment.KnowledgeSnapshotID, "manifest_hash": experiment.KnowledgeSnapshotManifestHash},
		"quality_experiment": map[string]any{"experiment_id": experiment.ExperimentID, "role": role, "conditions_sha256": experiment.ConditionsSHA256},
		"provider_evidence":  map[string]any{},
	}
	if role == "candidate" {
		payload["quality_eligible"] = true
		payload["provider_evidence"] = map[string]any{"quality_eligible": true}
		payload["quality_gate"] = map[string]any{"gate_id": "a1-q1-candidate-v1", "passed": passed}
	} else {
		payload["comparison_eligible"] = true
		payload["provider_evidence"] = map[string]any{"comparison_eligible": true}
	}
	return payload
}

func q1LinkReports(t *testing.T, ctx context.Context, store *PostgresStore, experiment domain.QualityExperiment, baseline domain.AgentConfig, baselineRetrieval domain.RetrievalConfig, candidate domain.AgentConfig, candidateRetrieval domain.RetrievalConfig, passed bool) {
	t.Helper()
	base := q1Report("base-"+experiment.ExperimentID, baseline.ConfigVersion, baselineRetrieval.RetrievalConfigVersion, experiment, "baseline", true, experiment.DatasetSHA256)
	if _, err := store.RegisterEvalReport(ctx, "idem-"+base.ReportID, base); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterQualityExperimentReport(ctx, "admin", experiment.ExperimentID, "link-"+base.ReportID, base.ReportID, "baseline"); err != nil {
		t.Fatal(err)
	}
	candidateReport := q1Report("candidate-"+experiment.ExperimentID, candidate.ConfigVersion, candidateRetrieval.RetrievalConfigVersion, experiment, "candidate", passed, experiment.DatasetSHA256)
	if _, err := store.RegisterEvalReport(ctx, "idem-"+candidateReport.ReportID, candidateReport); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterQualityExperimentReport(ctx, "admin", experiment.ExperimentID, "link-"+candidateReport.ReportID, candidateReport.ReportID, "candidate"); err != nil {
		t.Fatal(err)
	}
}
