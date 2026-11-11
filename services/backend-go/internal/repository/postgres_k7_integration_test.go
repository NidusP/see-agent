package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
)

func TestPostgresK7EvidenceAndEvalRegistration(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("K7_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set K7_TEST_DATABASE_URL to run the isolated Postgres K7 smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("k7_%d", time.Now().UTC().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteK3Identifier(schema)); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	defer func() {
		if _, err := adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quoteK3Identifier(schema)+` CASCADE`); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	}()

	store, err := NewPostgresStore(ctx, k3DatabaseURLWithSearchPath(t, baseURL, schema))
	if err != nil {
		t.Fatalf("bootstrap K7 store: %v", err)
	}
	defer store.Close()
	versions, err := store.ListSchemaMigrations(ctx)
	if err != nil || strings.Join(versions, ",") != "001,002,003,004,005,006,007,008,009,010,011,012,013,014,015,016,017,018,019,020,021,022,023,024" {
		t.Fatalf("migration versions = %#v err=%v", versions, err)
	}

	revision := k3ReadyRevision(t, ctx, store, "k7", "sha256:k7")
	if _, err := store.PublishKnowledgeRevision(ctx, revision.RevisionID, "admin", "k7-publish"); err != nil {
		t.Fatalf("publish K7 revision: %v", err)
	}
	scope, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		t.Fatalf("resolve K7 scope: %v", err)
	}
	conversation := store.CreateConversation("K7 evidence")
	result := k5RuntimeResult(conversation.ConversationID, "msg_k7", scope, revision, "K7 answer")
	result.Usage = &domain.RuntimeTokenUsage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14}
	result.ContextResolution = &domain.RuntimeContextResolution{
		Kind:              "standalone",
		BasisMessageIDs:   []string{},
		ResolverPerformed: false,
	}
	_, assistant, _, err := store.AppendScopedRuntimeExchange(ctx, conversation.ConversationID, "", "K7 question", scope, result)
	if err != nil {
		t.Fatalf("append K7 exchange: %v", err)
	}
	evidence, found, err := store.GetMessageExecutionEvidence(ctx, assistant.MessageID)
	if err != nil || !found {
		t.Fatalf("get K7 evidence = %#v found=%v err=%v", evidence, found, err)
	}
	if evidence.ConfigVersion != conversation.ConfigVersion || evidence.RetrievalConfigVersion != conversation.RetrievalConfigVersion || evidence.KnowledgeSnapshotID != scope.SnapshotID || len(evidence.Usage) == 0 {
		t.Fatalf("K7 evidence = %#v", evidence)
	}
	var contextResolution domain.RuntimeContextResolution
	if err := json.Unmarshal(evidence.ContextResolution, &contextResolution); err != nil || contextResolution.Kind != "standalone" || contextResolution.ResolverPerformed {
		t.Fatalf("context resolution evidence = %#v err=%v", contextResolution, err)
	}
	var retrievalEvidence map[string]any
	if err := json.Unmarshal(evidence.RetrievalConfig, &retrievalEvidence); err != nil || retrievalEvidence["retrieval_strategy"] != domain.HistoricalRetrievalStrategyLexicalCompatV1 || retrievalEvidence["evidence_min_score"] != 0.15 {
		t.Fatalf("retrieval strategy evidence = %#v err=%v", retrievalEvidence, err)
	}
	page, err := store.ListMessageExecutionEvidence(ctx, conversation.ConversationID, scope.SnapshotID, revision.RevisionID, "", 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].MessageID != assistant.MessageID {
		t.Fatalf("list K7 evidence = %#v err=%v", page, err)
	}
	deliveries, err := store.ListKnowledgeDeliveryEvidence(ctx, "pending", revision.DocumentID, revision.RevisionID, "", 20)
	if err != nil || len(deliveries.Items) == 0 {
		t.Fatalf("list K7 delivery evidence = %#v err=%v", deliveries, err)
	}

	rawReport := json.RawMessage(`{"report_id":"eval_k7","dataset_id":"baseline-v1","model_id":"llama3.2:latest","config_version":"cfg_001","retrieval_config_version":"rcfg_001","generation_config":{"temperature":0.2,"top_p":1,"max_output_tokens":1024},"metrics":{"citation_accuracy":0.97},"completed_at":"2026-07-15T01:02:03Z"}`)
	registration := domain.EvalReportRegistration{
		ReportID: "eval_k7", DatasetID: "baseline-v1", ModelID: "llama3.2:latest",
		ConfigVersion: "cfg_001", RetrievalConfigVersion: "rcfg_001",
		GenerationConfig: json.RawMessage(`{"temperature":0.2,"top_p":1,"max_output_tokens":1024}`),
		Metrics:          json.RawMessage(`{"citation_accuracy":0.97}`), CompletedAt: "2026-07-15T01:02:03Z", Report: rawReport,
	}
	registered, err := store.RegisterEvalReport(ctx, "idem-eval-k7", registration)
	if err != nil || registered.ReportID != registration.ReportID || !jsonSemanticallyEqual(t, registered.Report, rawReport) {
		t.Fatalf("register eval report = %#v err=%v", registered, err)
	}
	repeated, err := store.RegisterEvalReport(ctx, "idem-eval-k7", registration)
	if err != nil || repeated.ReportID != registered.ReportID || repeated.CreatedAt != registered.CreatedAt {
		t.Fatalf("repeat eval registration = %#v err=%v", repeated, err)
	}
	reordered := registration
	reordered.Report = json.RawMessage(`{ "metrics": { "citation_accuracy": 0.97 }, "report_id": "eval_k7", "dataset_id": "baseline-v1", "model_id": "llama3.2:latest", "config_version": "cfg_001", "retrieval_config_version": "rcfg_001", "generation_config": { "temperature": 0.2, "top_p": 1, "max_output_tokens": 1024 }, "completed_at": "2026-07-15T01:02:03Z" }`)
	if _, err := store.RegisterEvalReport(ctx, "idem-eval-k7", reordered); err != nil {
		t.Fatalf("semantic-equivalent eval registration: %v", err)
	}
	conflict := registration
	conflict.Report = json.RawMessage(`{"report_id":"eval_k7","metrics":{"citation_accuracy":0.1}}`)
	if _, err := store.RegisterEvalReport(ctx, "idem-eval-k7", conflict); !errors.Is(err, ErrEvalReportConflict) {
		t.Fatalf("conflicting eval registration error = %v", err)
	}
	reportPage, err := store.ListEvalReports(ctx, registration.ConfigVersion, registration.RetrievalConfigVersion, "", 20)
	if err != nil || len(reportPage.Items) != 1 || reportPage.Items[0].ReportID != registration.ReportID {
		t.Fatalf("list eval reports = %#v err=%v", reportPage, err)
	}

	controlledResult := domain.ChatRuntimeResponse{
		RequestID: "req_c2_greeting", ConversationID: conversation.ConversationID, MessageID: "msg_c2_greeting",
		AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "你好！我主要根据已发布的内部知识回答制度、流程和业务问题。你想查询什么？"},
		Citations:        []domain.RuntimeCitation{}, Refusal: false, FinishReason: "stop", CompletedAt: time.Now().UTC().Format(time.RFC3339),
		Intent: "social_greeting", RetrievalPerformed: false, GenerationPerformed: false, KnowledgeUsed: false,
		Usage: &domain.RuntimeTokenUsage{PromptTokens: 0, CompletionTokens: 12, TotalTokens: 12},
		ContextResolution: &domain.RuntimeContextResolution{
			Intent:              "social_greeting",
			Route:               "controlled_reply",
			Kind:                "standalone",
			BasisMessageIDs:     []string{},
			RouterPolicyVersion: "conversation_router_v1",
			ResolverProvider:    "test_router",
			ResolverModelID:     "test-router-v1",
			ResolverPerformed:   true,
			Confidence:          0.95,
		},
	}
	_, controlledAssistant, err := store.AppendControlledRuntimeExchange(ctx, conversation.ConversationID, "", "你好", controlledResult)
	if err != nil {
		t.Fatalf("append controlled exchange: %v", err)
	}
	controlledEvidence, found, err := store.GetMessageExecutionEvidence(ctx, controlledAssistant.MessageID)
	if err != nil || !found || controlledEvidence.Intent != "social_greeting" || controlledEvidence.KnowledgeSnapshotID != "" || controlledEvidence.RetrievalPerformed || controlledEvidence.GenerationPerformed || controlledEvidence.KnowledgeUsed {
		t.Fatalf("controlled evidence = %#v found=%v err=%v", controlledEvidence, found, err)
	}
	if controlledAssistant.KnowledgeUsed || controlledAssistant.KnowledgeSnapshotID != "" || controlledAssistant.CitationsCount != 0 {
		t.Fatalf("controlled assistant invented knowledge provenance: %#v", controlledAssistant)
	}
	var controlledContextResolution domain.RuntimeContextResolution
	if err := json.Unmarshal(controlledEvidence.ContextResolution, &controlledContextResolution); err != nil || controlledContextResolution.RouterPolicyVersion != "conversation_router_v1" {
		t.Fatalf("controlled context resolution evidence = %#v err=%v", controlledContextResolution, err)
	}

	if _, err := store.db.ExecContext(ctx, `DROP TABLE message_execution_evidence`); err != nil {
		t.Fatalf("drop execution evidence to inject persistence failure: %v", err)
	}
	failingResult := k5RuntimeResult(conversation.ConversationID, "msg_k7_rollback", scope, revision, "must roll back")
	if _, _, _, err := store.AppendScopedRuntimeExchange(ctx, conversation.ConversationID, "", "rollback question", scope, failingResult); err == nil {
		t.Fatal("expected execution evidence failure to roll back scoped exchange")
	}
	_, messages, found := store.GetConversation(conversation.ConversationID)
	if !found || len(messages) != 4 {
		t.Fatalf("failed evidence insert must not leave partial messages: %#v", messages)
	}
}

func jsonSemanticallyEqual(t *testing.T, left, right json.RawMessage) bool {
	t.Helper()
	var leftValue, rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		t.Fatalf("decode left JSON: %v", err)
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		t.Fatalf("decode right JSON: %v", err)
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
