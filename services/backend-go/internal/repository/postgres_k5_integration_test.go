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

func TestPostgresK5ScopedExchangeKeepsHistoricalProvenance(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("K5_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set K5_TEST_DATABASE_URL to run the isolated Postgres K5 smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("k5_%d", time.Now().UTC().UnixNano())
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
		t.Fatalf("bootstrap K5 store: %v", err)
	}
	defer store.Close()
	if _, err := store.ResolveActiveKnowledgeScope(ctx); !errors.Is(err, ErrActiveSnapshotUnavailable) {
		t.Fatalf("empty active scope error = %v, want ErrActiveSnapshotUnavailable", err)
	}

	revisionA := k3ReadyRevision(t, ctx, store, "k5_a", "sha256:k5-a")
	snapshotA, err := store.PublishKnowledgeRevision(ctx, revisionA.RevisionID, "admin", "k5-publish-a")
	if err != nil {
		t.Fatalf("publish snapshot A: %v", err)
	}
	scopeA, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		t.Fatalf("resolve scope A: %v", err)
	}
	if scopeA.SnapshotID != snapshotA.SnapshotID || len(scopeA.RevisionIDs) != 1 || scopeA.RevisionIDs[0] != revisionA.RevisionID {
		t.Fatalf("scope A = %#v, snapshot=%#v", scopeA, snapshotA)
	}

	conversation := store.CreateConversation("K5 historical evidence")
	resultA := k5RuntimeResult(conversation.ConversationID, "msg_k5_a", scopeA, revisionA, "answer A")
	resultA.CitationAnchors = []domain.CitationAnchor{{StartRune: 0, EndRune: 8, CitationRanks: []int{1}}}
	_, assistantA, citationsA, err := store.AppendScopedRuntimeExchange(ctx, conversation.ConversationID, "", "question A", scopeA, resultA)
	if err != nil {
		t.Fatalf("append scoped exchange A: %v", err)
	}
	if assistantA.KnowledgeSnapshotID != scopeA.SnapshotID || assistantA.KnowledgeSnapshotManifestHash != scopeA.ManifestHash || assistantA.FreshnessStatus != "current" || len(citationsA) != 1 {
		t.Fatalf("assistant A provenance = %#v citations=%#v", assistantA, citationsA)
	}
	if len(assistantA.CitationAnchors) != 1 || assistantA.CitationAnchors[0].CitationRanks[0] != 1 {
		t.Fatalf("assistant A anchors = %#v", assistantA.CitationAnchors)
	}
	wrongDocument := k5RuntimeResult(conversation.ConversationID, "msg_k5_wrong_document", scopeA, revisionA, "wrong document")
	wrongDocument.Citations[0].DocumentID = "doc_k5_other"
	if _, _, _, err := store.AppendScopedRuntimeExchange(ctx, conversation.ConversationID, "", "wrong document question", scopeA, wrongDocument); !errors.Is(err, ErrKnowledgeSnapshotCitationMismatch) {
		t.Fatalf("wrong citation document error = %v, want ErrKnowledgeSnapshotCitationMismatch", err)
	}
	if _, messages, ok := store.GetConversation(conversation.ConversationID); !ok || len(messages) != 2 {
		t.Fatalf("wrong citation document must roll back exchange: %#v", messages)
	}

	revisionB := k3ReadyRevision(t, ctx, store, "k5_b", "sha256:k5-b")
	snapshotB, err := store.PublishKnowledgeRevision(ctx, revisionB.RevisionID, "admin", "k5-publish-b")
	if err != nil {
		t.Fatalf("publish snapshot B: %v", err)
	}
	scopeB, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		t.Fatalf("resolve scope B: %v", err)
	}
	if scopeB.SnapshotID != snapshotB.SnapshotID || scopeB.SnapshotID == scopeA.SnapshotID || !containsScopeRevision(scopeB, revisionB.RevisionID) {
		t.Fatalf("scope B = %#v, snapshot B=%#v", scopeB, snapshotB)
	}
	if _, _, _, err := store.AppendScopedRuntimeExchange(ctx, conversation.ConversationID, "", "question B", scopeB, k5RuntimeResult(conversation.ConversationID, "msg_k5_b", scopeB, revisionB, "answer B")); err != nil {
		t.Fatalf("append scoped exchange B: %v", err)
	}

	historical, ok := store.GetMessageSources(assistantA.MessageID)
	if !ok {
		t.Fatal("expected historical sources for answer A")
	}
	if historical.KnowledgeSnapshotID != scopeA.SnapshotID || historical.KnowledgeSnapshotManifestHash != scopeA.ManifestHash || historical.FreshnessStatus != "current" {
		t.Fatalf("historical sources were rewritten after B: %#v", historical)
	}
	if len(historical.Items) != 1 || historical.Items[0].RevisionID != revisionA.RevisionID || historical.Items[0].ChunkContentSHA256 == "" || historical.Items[0].KnowledgeSnapshotID != scopeA.SnapshotID {
		t.Fatalf("historical citation = %#v", historical.Items)
	}
	_, messages, ok := store.GetConversation(conversation.ConversationID)
	if !ok || len(messages) != 4 || messages[1].KnowledgeSnapshotID != scopeA.SnapshotID || messages[3].KnowledgeSnapshotID != scopeB.SnapshotID || len(messages[1].CitationAnchors) != 1 {
		t.Fatalf("conversation messages = %#v", messages)
	}
	reopened, err := NewPostgresStore(ctx, k3DatabaseURLWithSearchPath(t, baseURL, schema))
	if err != nil {
		t.Fatalf("reopen K5 store: %v", err)
	}
	defer reopened.Close()
	_, restoredMessages, ok := reopened.GetConversation(conversation.ConversationID)
	if !ok || len(restoredMessages) != 4 || len(restoredMessages[1].CitationAnchors) != 1 {
		t.Fatalf("reopened conversation messages = %#v", restoredMessages)
	}
}

func k5RuntimeResult(conversationID, messageID string, scope domain.RuntimeKnowledgeScope, revision domain.KnowledgeRevision, answer string) domain.ChatRuntimeResponse {
	return domain.ChatRuntimeResponse{
		RequestID:                     "req_" + messageID,
		ConversationID:                conversationID,
		MessageID:                     messageID,
		AssistantMessage:              domain.RuntimeAssistantMessage{Role: "assistant", Content: answer},
		FinishReason:                  "stop",
		CompletedAt:                   "2026-07-14T00:00:00Z",
		KnowledgeSnapshotID:           scope.SnapshotID,
		KnowledgeSnapshotManifestHash: scope.ManifestHash,
		FreshnessStatus:               "current",
		Citations: []domain.RuntimeCitation{{
			ChunkID: "chunk_" + revision.RevisionID, FileID: "file_" + revision.RevisionID, DocumentID: revision.DocumentID,
			RevisionID: revision.RevisionID, ChunkContentSHA256: "sha256:chunk-" + revision.RevisionID,
			KnowledgeSnapshotID: scope.SnapshotID, Filename: revision.Filename, Rank: 1, Score: 0.99,
			CitationLabel: "policy", SourceLocator: domain.SourceLocator{SourceFormat: "md", BlockLabel: "policy"}, Snippet: answer,
		}},
	}
}

func containsScopeRevision(scope domain.RuntimeKnowledgeScope, revisionID string) bool {
	for _, candidate := range scope.RevisionIDs {
		if candidate == revisionID {
			return true
		}
	}
	return false
}
