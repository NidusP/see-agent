package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/repository"
)

func TestServerK6CalendarAndRuntimeRevalidation(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("K6_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set K6_TEST_DATABASE_URL to run the Server K6 smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("k6_server_%d", time.Now().UTC().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteK5ServerIdentifier(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	defer func() {
		if _, err := adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quoteK5ServerIdentifier(schema)+` CASCADE`); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	}()
	databaseURL := serverK4DatabaseURLWithSearchPath(t, baseURL, schema)

	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/run" {
			http.NotFound(w, r)
			return
		}
		var request domain.ChatRuntimeRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.AnswerMode != "revalidate" || request.KnowledgeScope == nil || request.ResponseOverrides == nil || request.ResponseOverrides.RetrievalStrategy != domain.HistoricalRetrievalStrategyLexicalCompatV1 {
			http.Error(w, "revalidation scope and retrieval strategy required", http.StatusBadRequest)
			return
		}
		revisionID := request.KnowledgeScope.RevisionIDs[len(request.KnowledgeScope.RevisionIDs)-1]
		documentID := "doc_k6_server_a"
		response := domain.ChatRuntimeResponse{
			RequestID: request.RequestID, ConversationID: request.ConversationID, MessageID: request.MessageID,
			AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "runtime revalidation result"},
			FinishReason:     "stop", CompletedAt: time.Now().UTC().Format(time.RFC3339), KnowledgeSnapshotID: request.KnowledgeScope.SnapshotID,
			KnowledgeSnapshotManifestHash: request.KnowledgeScope.ManifestHash, FreshnessStatus: "revalidated", RevalidationOutcome: "needs_update",
			Citations: []domain.RuntimeCitation{{ChunkID: "chunk_" + revisionID, FileID: "file_" + revisionID, DocumentID: documentID, RevisionID: revisionID, ChunkContentSHA256: "sha256:chunk-" + revisionID, KnowledgeSnapshotID: request.KnowledgeScope.SnapshotID, Filename: "policy.md", Rank: 1, Score: 0.99, CitationLabel: "policy", SourceLocator: domain.SourceLocator{SourceFormat: "md", BlockLabel: "policy"}, Snippet: "new evidence"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer runtimeServer.Close()

	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("REDIS_URL", "")
	t.Setenv("K3_DELIVERY_ENABLED", "false")
	t.Setenv("AI_RUNTIME_BASE_URL", runtimeServer.URL)
	t.Setenv("AUTH_SESSION_STORE", "memory")
	t.Setenv("BACKEND_UPLOAD_DIR", t.TempDir())
	t.Setenv("SEE_AGENT_ADMIN_USERNAME", "admin")
	t.Setenv("SEE_AGENT_ADMIN_PASSWORD", "admin123")
	t.Setenv("SEE_AGENT_USER_USERNAME", "user")
	t.Setenv("SEE_AGENT_USER_PASSWORD", "user123")

	store, err := repository.NewPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open setup store: %v", err)
	}
	defer store.Close()
	revisionA1 := k6ServerReadyRevision(t, ctx, store, "rev_k6_server_a1")
	if _, err := store.PublishKnowledgeRevision(ctx, revisionA1.RevisionID, "admin", "k6-server-a1"); err != nil {
		t.Fatalf("publish A1: %v", err)
	}
	scopeA1, err := store.ResolveActiveKnowledgeScope(ctx)
	if err != nil {
		t.Fatalf("resolve A1 scope: %v", err)
	}

	backend := New("127.0.0.1:0")
	defer backend.Shutdown(context.Background())
	api := httptest.NewServer(backend.Handler())
	defer api.Close()
	user := loginK4Client(t, api.URL, "user", "user123")
	admin := loginK4Client(t, api.URL, "admin", "admin123")
	conversationID := k5ServerConversation(t, user, api.URL)
	_, original, _, err := store.AppendScopedRuntimeExchange(ctx, conversationID, "", "historical question", scopeA1, k6ServerHistoricalResponse(conversationID, "msg_k6_server_original", scopeA1, revisionA1, "historical answer"))
	if err != nil {
		t.Fatalf("append historical answer: %v", err)
	}

	revisionA2 := k6ServerReadyRevision(t, ctx, store, "rev_k6_server_a2")
	snapshotA2, err := store.PublishKnowledgeRevision(ctx, revisionA2.RevisionID, "admin", "k6-server-a2")
	if err != nil {
		t.Fatalf("publish A2: %v", err)
	}

	assertStatus(t, http.MethodGet, api.URL+"/api/kb/updates", nil, nil, http.StatusUnauthorized)
	response, err := user.Get(api.URL + "/api/kb/updates")
	if err != nil {
		t.Fatalf("user updates: %v", err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("user updates = %d", response.StatusCode)
	}
	response.Body.Close()
	response, err = admin.Get(api.URL + "/api/kb/updates")
	if err != nil {
		t.Fatalf("admin updates: %v", err)
	}
	updates := k4Payload(t, response)
	if response.StatusCode != http.StatusOK || len(updates["items"].([]any)) == 0 {
		t.Fatalf("admin updates = %d %#v", response.StatusCode, updates)
	}
	response, err = user.Get(api.URL + "/api/kb/snapshots/" + snapshotA2.SnapshotID)
	if err != nil {
		t.Fatalf("user snapshot: %v", err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("user snapshot = %d", response.StatusCode)
	}
	response.Body.Close()
	response, err = admin.Get(api.URL + "/api/kb/snapshots/" + snapshotA2.SnapshotID)
	if err != nil {
		t.Fatalf("admin snapshot: %v", err)
	}
	snapshotPayload := k4Payload(t, response)
	if response.StatusCode != http.StatusOK || k4NestedString(t, snapshotPayload, "snapshot", "snapshot_id") != snapshotA2.SnapshotID {
		t.Fatalf("admin snapshot = %d %#v", response.StatusCode, snapshotPayload)
	}

	revalidateRequest, _ := http.NewRequest(http.MethodPost, api.URL+"/api/chat/conversations/"+conversationID+"/messages/"+original.MessageID+"/revalidate", bytes.NewBufferString(`{"reason":"policy changed"}`))
	revalidateRequest.Header.Set("Content-Type", "application/json")
	revalidateRequest.Header.Set("Idempotency-Key", "k6-http-revalidate")
	response, err = user.Do(revalidateRequest)
	if err != nil {
		t.Fatalf("enqueue revalidation: %v", err)
	}
	payload := k4Payload(t, response)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("enqueue revalidation = %d %#v", response.StatusCode, payload)
	}
	runID := k4NestedString(t, payload, "revalidation", "revalidation_id")
	if runID == "" {
		t.Fatalf("missing run id: %#v", payload)
	}

	runPayload := k6WaitForRun(t, user, api.URL, conversationID, runID)
	run := runPayload["revalidation"].(map[string]any)
	if run["status"] != "completed" || run["outcome"] != "needs_update" {
		t.Fatalf("runtime revalidation was not persisted: %#v", runPayload)
	}
	result, _ := run["result"].(map[string]any)
	if result["content"] != "runtime revalidation result" || result["outcome"] != "needs_update" {
		t.Fatalf("wrong independent result: %#v", result)
	}
	_, messages, _ := store.GetConversation(conversationID)
	for _, message := range messages {
		if message.MessageID == original.MessageID && (message.Content != "historical answer" || message.FreshnessStatus != "revalidated") {
			t.Fatalf("original message was incorrectly rewritten: %#v", message)
		}
	}
}

func k6ServerReadyRevision(t *testing.T, ctx context.Context, store *repository.PostgresStore, revisionID string) domain.KnowledgeRevision {
	t.Helper()
	documentID := "doc_k6_server_a"
	if _, err := store.GetKnowledgeDocument(ctx, documentID); errors.Is(err, repository.ErrKnowledgeDocumentNotFound) {
		if _, err := store.CreateKnowledgeDocument(ctx, domain.KnowledgeDocument{DocumentID: documentID, DisplayName: "K6 policy", OwnerID: "admin"}); err != nil {
			t.Fatalf("create document: %v", err)
		}
	} else if err != nil {
		t.Fatalf("get document: %v", err)
	}
	_, err := store.CreateKnowledgeRevision(ctx, domain.KnowledgeRevision{RevisionID: revisionID, DocumentID: documentID, Filename: "policy.md", MIMEType: "text/markdown", SourceFormat: "md", ObjectURI: "s3://knowledge/" + revisionID, ChecksumSHA256: "sha256:" + revisionID, ParseVersion: "parse_v1", IndexVersion: "index_v1"})
	if err != nil {
		t.Fatalf("create revision: %v", err)
	}
	if _, err := store.QueueKnowledgeRevision(ctx, revisionID); err != nil {
		t.Fatalf("queue revision: %v", err)
	}
	ready, err := store.MarkKnowledgeRevisionReady(ctx, revisionID, "sha256:chunks-"+revisionID, "bge-m3")
	if err != nil {
		t.Fatalf("ready revision: %v", err)
	}
	return ready
}

func k6ServerHistoricalResponse(conversationID, messageID string, scope domain.RuntimeKnowledgeScope, revision domain.KnowledgeRevision, content string) domain.ChatRuntimeResponse {
	return domain.ChatRuntimeResponse{RequestID: "req_" + messageID, ConversationID: conversationID, MessageID: messageID, AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: content}, FinishReason: "stop", CompletedAt: time.Now().UTC().Format(time.RFC3339), KnowledgeSnapshotID: scope.SnapshotID, KnowledgeSnapshotManifestHash: scope.ManifestHash, FreshnessStatus: "current", Citations: []domain.RuntimeCitation{{ChunkID: "chunk_" + revision.RevisionID, FileID: "file_" + revision.RevisionID, DocumentID: revision.DocumentID, RevisionID: revision.RevisionID, ChunkContentSHA256: "sha256:chunk-" + revision.RevisionID, KnowledgeSnapshotID: scope.SnapshotID, Filename: revision.Filename, Rank: 1, Score: 0.99, CitationLabel: "policy", SourceLocator: domain.SourceLocator{SourceFormat: "md", BlockLabel: "policy"}, Snippet: content}}}
}

func k6WaitForRun(t *testing.T, client *http.Client, apiURL, conversationID, runID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(apiURL + "/api/chat/conversations/" + conversationID + "/revalidations/" + runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		payload := k4Payload(t, response)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("get run status = %d %#v", response.StatusCode, payload)
		}
		run, _ := payload["revalidation"].(map[string]any)
		if run["status"] == "completed" || run["status"] == "failed" {
			return payload
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for K6 revalidation")
	return nil
}
