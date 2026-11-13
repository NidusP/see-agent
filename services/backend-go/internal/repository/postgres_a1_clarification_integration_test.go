package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
)

func TestPostgresA1ClarificationLifecycleAndEvidence(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("A1_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set A1_TEST_DATABASE_URL to run the isolated Postgres A1 smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()
	if err := adminDB.PingContext(ctx); err != nil {
		t.Fatalf("ping admin database: %v", err)
	}
	schema := fmt.Sprintf("a1_%d", time.Now().UTC().UnixNano())
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
		t.Fatalf("bootstrap A1 store: %v", err)
	}
	defer store.Close()
	versions, err := store.ListSchemaMigrations(ctx)
	if err != nil || strings.Join(versions, ",") != "001,002,003,004,005,006,007,008,009,010,011,012,013,014,015,016,017,018,019,020,021,022,023,024,025,026" {
		t.Fatalf("migration versions = %#v err=%v", versions, err)
	}
	policyConfig := store.CreateConfig("a1-model", "prompt_a1", "concise", domain.AnswerPolicyV1, .2, 1, 1024)
	if restored, found := store.GetConfig(policyConfig.ConfigVersion); !found || restored.AnswerPolicyVersion != domain.AnswerPolicyV1 {
		t.Fatalf("persisted answer policy config = %#v found=%v", restored, found)
	}

	revision := k3ReadyRevision(t, ctx, store, "a1", "sha256:a1")
	if _, err := store.PublishKnowledgeRevision(ctx, revision.RevisionID, "admin", "a1-publish"); err != nil {
		t.Fatalf("publish A1 revision: %v", err)
	}
	scope, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		t.Fatalf("resolve A1 scope: %v", err)
	}

	t.Run("resolved and stale write rollback", func(t *testing.T) {
		conversation := store.CreateConversation("A1 resolved")
		_, clarificationMessage, _, err := store.AppendScopedRuntimeExchangeWithPending(
			ctx, conversation.ConversationID, "", "总结差旅报销的规则", scope, nil,
			a1ClarificationResult(conversation.ConversationID, "msg_a1_initial", scope, "你需要查询哪个制度版本？"),
		)
		if err != nil {
			t.Fatalf("persist initial clarification: %v", err)
		}
		if clarificationMessage.ClarificationID == "" || clarificationMessage.KnowledgeSnapshotID != "" || clarificationMessage.KnowledgeUsed {
			t.Fatalf("clarification message projection = %#v", clarificationMessage)
		}
		if _, found := store.GetMessageSources(clarificationMessage.MessageID); found {
			t.Fatal("clarification must not expose a user source entry")
		}
		pending, found, err := store.GetPendingClarification(ctx, conversation.ConversationID)
		if err != nil || !found || pending.ClarificationID != clarificationMessage.ClarificationID || pending.StateVersion != 1 || pending.TurnCount != 1 {
			t.Fatalf("initial pending clarification = %#v found=%v err=%v", pending, found, err)
		}
		evidence, found, err := store.GetMessageExecutionEvidence(ctx, clarificationMessage.MessageID)
		if err != nil || !found || evidence.AnswerDisposition != "clarification_required" || evidence.Clarification == nil || evidence.PolicyEvaluationPerformed == nil || !*evidence.PolicyEvaluationPerformed || evidence.AnswerGenerationPerformed == nil || *evidence.AnswerGenerationPerformed || len(evidence.RetrievalCandidates) != 0 {
			t.Fatalf("clarification evidence = %#v found=%v err=%v", evidence, found, err)
		}

		resolved := a1GroundedResult(conversation.ConversationID, "msg_a1_resolved", scope, revision, "2025 年制度下，提交期限为十天。")
		resolved.Resolution = a1Resolution(pending, "resolved", "2025 年差旅报销提交期限")
		resolved.ResolvedQuery = resolved.Resolution.ResolvedQuery
		_, answer, citations, err := store.AppendScopedRuntimeExchangeWithPending(ctx, conversation.ConversationID, "", "2025 年制度", scope, &pending, resolved)
		if err != nil {
			t.Fatalf("persist resolved clarification: %v", err)
		}
		if answer.ClarificationID != "" || answer.KnowledgeSnapshotID != scope.SnapshotID || len(citations) != 1 {
			t.Fatalf("resolved answer projection = %#v citations=%#v", answer, citations)
		}
		if _, found, err := store.GetPendingClarification(ctx, conversation.ConversationID); err != nil || found {
			t.Fatalf("resolved clarification must not remain pending: found=%v err=%v", found, err)
		}
		sources, found := store.GetMessageSources(answer.MessageID)
		if !found || len(sources.Items) != 1 || sources.Items[0].Rank != 1 {
			t.Fatalf("resolved answer sources = %#v found=%v", sources, found)
		}
		evidence, found, err = store.GetMessageExecutionEvidence(ctx, answer.MessageID)
		if err != nil || !found || evidence.Resolution == nil || evidence.ResolvedQuery != "2025 年差旅报销提交期限" || len(evidence.RetrievalCandidates) != 1 || len(evidence.UsedCitationRanks) != 1 || evidence.UsedCitationRanks[0] != 1 || len(evidence.Citations) != 1 {
			t.Fatalf("resolved answer evidence = %#v found=%v err=%v", evidence, found, err)
		}

		stale := a1GroundedResult(conversation.ConversationID, "msg_a1_stale", scope, revision, "stale answer")
		stale.Resolution = a1Resolution(pending, "resolved", "stale query")
		stale.ResolvedQuery = stale.Resolution.ResolvedQuery
		if _, _, _, err := store.AppendScopedRuntimeExchangeWithPending(ctx, conversation.ConversationID, "", "stale update", scope, &pending, stale); !errors.Is(err, ErrClarificationStateConflict) {
			t.Fatalf("stale clarification write error = %v", err)
		}
		_, messages, found := store.GetConversation(conversation.ConversationID)
		if !found || len(messages) != 4 {
			t.Fatalf("stale clarification write must roll back all messages: %#v", messages)
		}
		status, stateVersion, turns := a1ClarificationStatus(t, ctx, store, pending.ClarificationID)
		if status != "resolved" || stateVersion != 2 || turns != 1 {
			t.Fatalf("resolved clarification state = status=%q version=%d turns=%d", status, stateVersion, turns)
		}
	})

	t.Run("needs more expires after the bounded second clarification", func(t *testing.T) {
		conversation := store.CreateConversation("A1 expiry")
		_, initial, _, err := store.AppendScopedRuntimeExchangeWithPending(
			ctx, conversation.ConversationID, "", "解释差旅制度", scope, nil,
			a1ClarificationResult(conversation.ConversationID, "msg_a1_expire_initial", scope, "你要查询哪个制度版本？"),
		)
		if err != nil {
			t.Fatalf("persist expiry initial clarification: %v", err)
		}
		pending, found, err := store.GetPendingClarification(ctx, conversation.ConversationID)
		if err != nil || !found {
			t.Fatalf("read expiry pending clarification: %#v found=%v err=%v", pending, found, err)
		}
		more := a1ClarificationResult(conversation.ConversationID, "msg_a1_expire_more", scope, "请确认是国内还是国际差旅。")
		more.Resolution = a1Resolution(pending, "needs_more", "")
		if _, assistant, _, err := store.AppendScopedRuntimeExchangeWithPending(ctx, conversation.ConversationID, "", "报销", scope, &pending, more); err != nil || assistant.ClarificationID != initial.ClarificationID {
			t.Fatalf("persist first needs_more = %#v err=%v", assistant, err)
		}
		pending, found, err = store.GetPendingClarification(ctx, conversation.ConversationID)
		if err != nil || !found || pending.StateVersion != 2 || pending.TurnCount != 2 {
			t.Fatalf("updated pending clarification = %#v found=%v err=%v", pending, found, err)
		}
		expire := a1ClarificationResult(conversation.ConversationID, "msg_a1_expire_terminal", scope, "请用完整问题重新描述。")
		expire.Resolution = a1Resolution(pending, "needs_more", "")
		if _, _, _, err := store.AppendScopedRuntimeExchangeWithPending(ctx, conversation.ConversationID, "", "还是报销", scope, &pending, expire); err != nil {
			t.Fatalf("persist terminal needs_more: %v", err)
		}
		if _, found, err := store.GetPendingClarification(ctx, conversation.ConversationID); err != nil || found {
			t.Fatalf("expired clarification must not remain pending: found=%v err=%v", found, err)
		}
		status, stateVersion, turns := a1ClarificationStatus(t, ctx, store, initial.ClarificationID)
		if status != "expired" || stateVersion != 3 || turns != 2 {
			t.Fatalf("expired clarification state = status=%q version=%d turns=%d", status, stateVersion, turns)
		}
	})

	t.Run("independent question abandons the pending clarification", func(t *testing.T) {
		conversation := store.CreateConversation("A1 abandoned")
		_, initial, _, err := store.AppendScopedRuntimeExchangeWithPending(
			ctx, conversation.ConversationID, "", "说明差旅制度", scope, nil,
			a1ClarificationResult(conversation.ConversationID, "msg_a1_abandon_initial", scope, "你要查询哪个版本？"),
		)
		if err != nil {
			t.Fatalf("persist abandon initial clarification: %v", err)
		}
		pending, found, err := store.GetPendingClarification(ctx, conversation.ConversationID)
		if err != nil || !found {
			t.Fatalf("read abandon pending clarification: %#v found=%v err=%v", pending, found, err)
		}
		abandoned := a1InsufficientEvidenceResult(conversation.ConversationID, "msg_a1_abandoned", scope, revision)
		abandoned.Resolution = a1Resolution(pending, "abandoned", "")
		if _, answer, citations, err := store.AppendScopedRuntimeExchangeWithPending(ctx, conversation.ConversationID, "", "请问采购审批流程", scope, &pending, abandoned); err != nil || answer.ClarificationID != "" || len(citations) != 0 {
			t.Fatalf("persist abandoned clarification answer = %#v citations=%#v err=%v", answer, citations, err)
		}
		if _, found, err := store.GetPendingClarification(ctx, conversation.ConversationID); err != nil || found {
			t.Fatalf("abandoned clarification must not remain pending: found=%v err=%v", found, err)
		}
		status, stateVersion, turns := a1ClarificationStatus(t, ctx, store, initial.ClarificationID)
		if status != "abandoned" || stateVersion != 2 || turns != 1 {
			t.Fatalf("abandoned clarification state = status=%q version=%d turns=%d", status, stateVersion, turns)
		}
	})
}

func a1ClarificationResult(conversationID, messageID string, scope domain.RuntimeKnowledgeScope, question string) domain.ChatRuntimeResponse {
	policy := true
	notGenerated := false
	return domain.ChatRuntimeResponse{
		RequestID: messageID + "_request", ConversationID: conversationID, MessageID: messageID,
		AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: question},
		FinishReason:     "stop", CompletedAt: "2026-08-02T00:00:00Z", Intent: "knowledge_query",
		AnswerDisposition: "clarification_required", AnswerPolicyVersion: domain.AnswerPolicyV1,
		PolicyEvaluationPerformed: &policy, AnswerGenerationPerformed: &notGenerated,
		Clarification: &domain.RuntimeClarification{MissingDimensions: []string{"policy_version"}, Question: question},
		Execution: &domain.RuntimeExecutionObservation{
			Policy:     "performed",
			Retrieval:  domain.RuntimeRetrievalObservation{Status: "not_run"},
			Generation: domain.RuntimeGenerationObservation{Status: "not_run"},
		},
		KnowledgeSnapshotID: scope.SnapshotID, KnowledgeSnapshotManifestHash: scope.ManifestHash,
	}
}

func a1GroundedResult(conversationID, messageID string, scope domain.RuntimeKnowledgeScope, revision domain.KnowledgeRevision, answer string) domain.ChatRuntimeResponse {
	result := k5RuntimeResult(conversationID, messageID, scope, revision, answer)
	policy := true
	candidateCount := 1
	citation := result.Citations[0]
	result.Intent = "knowledge_query"
	result.AnswerDisposition = "grounded_answer"
	result.AnswerPolicyVersion = domain.AnswerPolicyV1
	result.PolicyEvaluationPerformed = &policy
	result.AnswerGenerationPerformed = &policy
	result.CitationAnchors = []domain.CitationAnchor{{StartRune: 0, EndRune: 1, CitationRanks: []int{1}}}
	result.RetrievalCandidates = []domain.RuntimeRetrievalCandidate{{
		ChunkID: citation.ChunkID, FileID: citation.FileID, DocumentID: citation.DocumentID, RevisionID: citation.RevisionID,
		ChunkContentSHA256: citation.ChunkContentSHA256, KnowledgeSnapshotID: citation.KnowledgeSnapshotID,
		Rank: citation.Rank, Score: citation.Score, SourceLocator: citation.SourceLocator,
	}}
	result.UsedCitationRanks = []int{citation.Rank}
	result.RetrievalPerformed = true
	result.GenerationPerformed = true
	result.KnowledgeUsed = true
	result.Execution = &domain.RuntimeExecutionObservation{
		Policy:     "performed",
		Retrieval:  domain.RuntimeRetrievalObservation{Status: "performed", Strategy: "native_dense_v1", EmbeddingModel: "bge-m3", Collection: "a1", CandidateCount: &candidateCount},
		Generation: domain.RuntimeGenerationObservation{Status: "performed", Provider: "test_provider", ModelID: "a1-model"},
	}
	return result
}

func a1InsufficientEvidenceResult(conversationID, messageID string, scope domain.RuntimeKnowledgeScope, revision domain.KnowledgeRevision) domain.ChatRuntimeResponse {
	result := a1GroundedResult(conversationID, messageID, scope, revision, "当前知识不足以回答。")
	notGenerated := false
	result.AnswerDisposition = "insufficient_evidence"
	result.Citations = nil
	result.CitationAnchors = nil
	result.UsedCitationRanks = nil
	result.Refusal = true
	result.AnswerGenerationPerformed = &notGenerated
	result.GenerationPerformed = false
	result.KnowledgeUsed = false
	result.Execution.Generation = domain.RuntimeGenerationObservation{Status: "not_run"}
	return result
}

func a1Resolution(pending domain.RuntimePendingClarification, action, resolvedQuery string) *domain.RuntimeClarificationResolution {
	return &domain.RuntimeClarificationResolution{
		PendingClarificationID: pending.ClarificationID,
		PendingStateVersion:    pending.StateVersion,
		Action:                 action,
		ResolvedQuery:          resolvedQuery,
	}
}

func a1ClarificationStatus(t *testing.T, ctx context.Context, store *PostgresStore, clarificationID string) (string, int, int) {
	t.Helper()
	var status string
	var stateVersion, turns int
	if err := store.db.QueryRowContext(ctx, `
		SELECT status, state_version, turn_count
		FROM conversation_clarifications
		WHERE clarification_id = $1`, clarificationID).Scan(&status, &stateVersion, &turns); err != nil {
		t.Fatalf("read clarification %q: %v", clarificationID, err)
	}
	return status, stateVersion, turns
}
