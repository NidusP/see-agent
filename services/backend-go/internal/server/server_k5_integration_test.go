package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
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

func TestServerK5SnapshotScopeAndProvenance(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("K5_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set K5_TEST_DATABASE_URL to run the Server K5 smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("k5_server_%d", time.Now().UTC().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteK5ServerIdentifier(schema)); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	defer func() {
		if _, err := adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quoteK5ServerIdentifier(schema)+` CASCADE`); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	}()
	databaseURL := serverK4DatabaseURLWithSearchPath(t, baseURL, schema)

	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request domain.ChatRuntimeRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.KnowledgeScope == nil {
			http.Error(w, "knowledge scope is required", http.StatusBadRequest)
			return
		}
		response := k5ServerRuntimeResponse(request, strings.Contains(request.UserQuery, "scope violation"), strings.Contains(request.UserQuery, "document violation"))
		switch r.URL.Path {
		case "/v1/chat/run":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		case "/v1/chat/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			delta, _ := json.Marshal(domain.ChatRuntimeStreamEvent{Event: "message.delta", RequestID: request.RequestID, MessageID: request.MessageID, Delta: "scoped "})
			completed, _ := json.Marshal(map[string]any{"chat_runtime_response": response})
			_, _ = fmt.Fprintf(w, "event: message.delta\ndata: %s\n\n", delta)
			_, _ = fmt.Fprintf(w, "event: message.completed\ndata: %s\n\n", completed)
		default:
			http.NotFound(w, r)
		}
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

	backend := New("127.0.0.1:0")
	defer backend.Shutdown(context.Background())
	api := httptest.NewServer(backend.Handler())
	defer api.Close()
	client := loginK4Client(t, api.URL, "user", "user123")
	conversationID := k5ServerConversation(t, client, api.URL)

	status, payload := k5ServerPostMessage(t, client, api.URL, conversationID, "no active snapshot", false)
	if status != http.StatusServiceUnavailable || k4PayloadErrorCode(payload) != "knowledge_snapshot_unavailable" {
		t.Fatalf("missing snapshot response = %d %#v", status, payload)
	}

	store, err := repository.NewPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open verification store: %v", err)
	}
	defer store.Close()
	if _, messages, ok := store.GetConversation(conversationID); !ok || len(messages) != 0 {
		t.Fatalf("missing snapshot must not persist exchange: %#v", messages)
	}

	revisionA := k5ServerReadyRevision(t, ctx, store, "a")
	snapshotA, err := store.PublishKnowledgeRevision(ctx, revisionA.RevisionID, "admin", "k5-server-a")
	if err != nil {
		t.Fatalf("publish A: %v", err)
	}
	status, payload = k5ServerPostMessage(t, client, api.URL, conversationID, "question A", false)
	if status != http.StatusOK || k4NestedString(t, payload, "assistant_message", "knowledge_snapshot_id") != snapshotA.SnapshotID {
		t.Fatalf("answer A = %d %#v", status, payload)
	}
	assistantAID := k4NestedString(t, payload, "assistant_message", "message_id")

	revisionB := k5ServerReadyRevision(t, ctx, store, "b")
	snapshotB, err := store.PublishKnowledgeRevision(ctx, revisionB.RevisionID, "admin", "k5-server-b")
	if err != nil {
		t.Fatalf("publish B: %v", err)
	}
	status, payload = k5ServerPostMessage(t, client, api.URL, conversationID, "question B", false)
	if status != http.StatusOK || k4NestedString(t, payload, "assistant_message", "knowledge_snapshot_id") != snapshotB.SnapshotID {
		t.Fatalf("answer B = %d %#v", status, payload)
	}

	sourcesResponse, err := client.Get(api.URL + "/api/chat/conversations/" + conversationID + "/sources/" + assistantAID)
	if err != nil {
		t.Fatalf("get historical sources: %v", err)
	}
	sourcesPayload := k4Payload(t, sourcesResponse)
	if sourcesResponse.StatusCode != http.StatusOK || k4NestedString(t, sourcesPayload, "", "knowledge_snapshot_id") != "" {
		// The helper only reads nested fields; the direct assertion below checks the root.
		t.Fatalf("historical sources response = %d %#v", sourcesResponse.StatusCode, sourcesPayload)
	}
	if snapshotID, _ := sourcesPayload["knowledge_snapshot_id"].(string); snapshotID != snapshotA.SnapshotID {
		t.Fatalf("historical sources snapshot = %q, want %q", snapshotID, snapshotA.SnapshotID)
	}
	items, _ := sourcesPayload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("historical sources items = %#v", items)
	}

	status, payload = k5ServerPostMessage(t, client, api.URL, conversationID, "scope violation", false)
	if status != http.StatusServiceUnavailable || k4PayloadErrorCode(payload) != "snapshot_scope_violation" {
		t.Fatalf("scope violation response = %d %#v", status, payload)
	}
	if _, messages, ok := store.GetConversation(conversationID); !ok || len(messages) != 4 {
		t.Fatalf("scope violation must not persist exchange: %#v", messages)
	}
	status, payload = k5ServerPostMessage(t, client, api.URL, conversationID, "document violation", false)
	if status != http.StatusServiceUnavailable || k4PayloadErrorCode(payload) != "snapshot_scope_violation" {
		t.Fatalf("document violation response = %d %#v", status, payload)
	}
	if _, messages, ok := store.GetConversation(conversationID); !ok || len(messages) != 4 {
		t.Fatalf("document violation must not persist exchange: %#v", messages)
	}

	request, err := http.NewRequest(http.MethodPost, api.URL+"/api/chat/conversations/"+conversationID+"/messages", bytes.NewBufferString(`{"content":"stream B","stream":true}`))
	if err != nil {
		t.Fatalf("new stream request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	streamResponse, err := client.Do(request)
	if err != nil {
		t.Fatalf("stream scoped answer: %v", err)
	}
	streamBody, readErr := io.ReadAll(streamResponse.Body)
	streamResponse.Body.Close()
	if readErr != nil {
		t.Fatalf("read stream response: %v", readErr)
	}
	if streamResponse.StatusCode != http.StatusOK || !strings.Contains(string(streamBody), "event: message.completed") || strings.Contains(string(streamBody), "snapshot_scope_violation") {
		t.Fatalf("stream scoped answer = %d %s", streamResponse.StatusCode, streamBody)
	}
	_, messages, ok := store.GetConversation(conversationID)
	if !ok || len(messages) != 6 || messages[5].KnowledgeSnapshotID != snapshotB.SnapshotID {
		t.Fatalf("stream exchange provenance = %#v", messages)
	}
}

func k5ServerConversation(t *testing.T, client *http.Client, apiURL string) string {
	t.Helper()
	response, err := client.Post(apiURL+"/api/chat/conversations", "application/json", strings.NewReader(`{"title":"K5"}`))
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	payload := k4Payload(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create conversation = %d %#v", response.StatusCode, payload)
	}
	return k4NestedString(t, payload, "conversation", "conversation_id")
}

func k5ServerPostMessage(t *testing.T, client *http.Client, apiURL, conversationID, content string, stream bool) (int, map[string]any) {
	t.Helper()
	response, err := client.Post(apiURL+"/api/chat/conversations/"+conversationID+"/messages", "application/json", strings.NewReader(fmt.Sprintf(`{"content":%q,"stream":%t}`, content, stream)))
	if err != nil {
		t.Fatalf("post message: %v", err)
	}
	status := response.StatusCode
	return status, k4Payload(t, response)
}

func k5ServerReadyRevision(t *testing.T, ctx context.Context, store *repository.PostgresStore, suffix string) domain.KnowledgeRevision {
	t.Helper()
	documentID := "doc_k5_server_" + suffix
	if _, err := store.CreateKnowledgeDocument(ctx, domain.KnowledgeDocument{DocumentID: documentID, DisplayName: "K5 " + suffix, OwnerID: "admin"}); err != nil {
		t.Fatalf("create document %s: %v", suffix, err)
	}
	revision, err := store.CreateKnowledgeRevision(ctx, domain.KnowledgeRevision{
		RevisionID: "rev_k5_server_" + suffix, DocumentID: documentID, Filename: suffix + ".md", MIMEType: "text/markdown", SourceFormat: "md",
		ObjectURI: "s3://knowledge-docs/k5/" + suffix + ".md", ChecksumSHA256: "sha256:k5-server-" + suffix, ParseVersion: "parse_v1", IndexVersion: "index_v1",
	})
	if err != nil {
		t.Fatalf("create revision %s: %v", suffix, err)
	}
	if _, err := store.QueueKnowledgeRevision(ctx, revision.RevisionID); err != nil {
		t.Fatalf("queue revision %s: %v", suffix, err)
	}
	ready, err := store.MarkKnowledgeRevisionReady(ctx, revision.RevisionID, "sha256:k5-server-chunks-"+suffix, "bge-m3")
	if err != nil {
		t.Fatalf("mark ready %s: %v", suffix, err)
	}
	return ready
}

func k5ServerRuntimeResponse(request domain.ChatRuntimeRequest, violateScope, violateDocument bool) domain.ChatRuntimeResponse {
	scope := request.KnowledgeScope
	revisionID := scope.RevisionIDs[len(scope.RevisionIDs)-1]
	documentID := "doc_" + strings.TrimPrefix(revisionID, "rev_")
	if violateScope {
		revisionID = "rev_out_of_scope"
		documentID = "doc_out_of_scope"
	} else if violateDocument {
		documentID = "doc_wrong_for_" + revisionID
	}
	return domain.ChatRuntimeResponse{
		RequestID: request.RequestID, ConversationID: request.ConversationID, MessageID: request.MessageID,
		AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "scoped runtime answer"}, Citations: []domain.RuntimeCitation{{
			ChunkID: "chunk_" + revisionID, FileID: "file_" + revisionID, DocumentID: documentID, RevisionID: revisionID,
			ChunkContentSHA256: "sha256:chunk-" + revisionID, KnowledgeSnapshotID: scope.SnapshotID, Filename: "policy.md",
			Rank: 1, Score: 0.99, CitationLabel: "policy", SourceLocator: domain.SourceLocator{SourceFormat: "md", BlockLabel: "policy"}, Snippet: "scoped evidence",
		}},
		FinishReason: "stop", CompletedAt: time.Now().UTC().Format(time.RFC3339), KnowledgeSnapshotID: scope.SnapshotID,
		KnowledgeSnapshotManifestHash: scope.ManifestHash, FreshnessStatus: "current",
	}
}

func quoteK5ServerIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
