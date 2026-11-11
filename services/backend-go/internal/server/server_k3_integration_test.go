package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/repository"
)

func TestServerK3DeliveryWiring(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("K3_TEST_DATABASE_URL"))
	redisURL := strings.TrimSpace(os.Getenv("K3_TEST_REDIS_URL"))
	if baseURL == "" || redisURL == "" {
		t.Skip("set K3_TEST_DATABASE_URL and K3_TEST_REDIS_URL to run the Server K3 smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("k3_server_%d", time.Now().UTC().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteServerK3Identifier(schema)); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	defer func() {
		if _, err := adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quoteServerK3Identifier(schema)+` CASCADE`); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	}()
	databaseURL := serverK3DatabaseURLWithSearchPath(t, baseURL, schema)
	schemaDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open isolated database: %v", err)
	}
	defer schemaDB.Close()

	requests := make(chan map[string]any, 2)
	var requestMu sync.Mutex
	requestNumber := 0
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/ingestion/run" {
			http.NotFound(w, r)
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- request
		requestMu.Lock()
		requestNumber++
		currentRequest := requestNumber
		requestMu.Unlock()
		response := map[string]any{
			"request_id":  request["request_id"],
			"task_id":     request["task_id"],
			"file_id":     request["file_id"],
			"document_id": request["document_id"],
			"revision_id": request["revision_id"],
			"delivery_id": request["delivery_id"],
			"updated_at":  time.Now().UTC().Format(time.RFC3339),
		}
		if currentRequest == 1 {
			response["status"] = "failed"
			response["error"] = map[string]any{"code": "runtime_timeout", "message": "retry me", "retryable": true}
		} else {
			response["status"] = "succeeded"
			response["chunk_manifest_sha256"] = "sha256:server-k3-chunks"
			response["embedding_model"] = "bge-m3"
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer runtimeServer.Close()

	marker := strings.ReplaceAll(schema, "_", "-")
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("REDIS_URL", redisURL)
	t.Setenv("AI_RUNTIME_BASE_URL", runtimeServer.URL)
	t.Setenv("K3_DELIVERY_ENABLED", "true")
	t.Setenv("K3_DELIVERY_RELAY_INTERVAL_MS", "50")
	t.Setenv("REDIS_K3_REVISION_STREAM", "see_agent:k3:server:"+marker)
	t.Setenv("REDIS_K3_REVISION_GROUP", "server-k3-group-"+marker)
	t.Setenv("REDIS_K3_REVISION_CONSUMER", "server-k3-consumer-"+marker)
	t.Setenv("AUTH_SESSION_STORE", "memory")

	backend := New("127.0.0.1:0")
	defer func() {
		if err := backend.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown K3 backend: %v", err)
		}
	}()

	store, err := repository.NewPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open K3 test store: %v", err)
	}
	defer store.Close()
	document, err := store.CreateKnowledgeDocument(ctx, domain.KnowledgeDocument{DocumentID: "doc_server_k3", DisplayName: "Server K3", OwnerID: "admin"})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	revision, err := store.CreateKnowledgeRevision(ctx, domain.KnowledgeRevision{
		RevisionID:     "rev_server_k3",
		DocumentID:     document.DocumentID,
		Filename:       "server-k3.md",
		MIMEType:       "text/markdown",
		SourceFormat:   "md",
		ObjectURI:      "s3://knowledge-docs/server-k3.md",
		ChecksumSHA256: "sha256:server-k3",
		ParseVersion:   "parse_v1",
		IndexVersion:   "index_v1",
	})
	if err != nil {
		t.Fatalf("create revision: %v", err)
	}
	if _, err := store.QueueKnowledgeRevision(ctx, revision.RevisionID); err != nil {
		t.Fatalf("queue revision: %v", err)
	}

	for expectedAttempt := 1; expectedAttempt <= 2; expectedAttempt++ {
		select {
		case request := <-requests:
			attempt, ok := request["attempt"].(float64)
			if !ok || request["document_id"] != revision.DocumentID || request["revision_id"] != revision.RevisionID || request["checksum_sha256"] != revision.ChecksumSHA256 || request["delivery_id"] == "" || int(attempt) != expectedAttempt {
				t.Fatalf("runtime request did not preserve K3 identity/attempt: %#v", request)
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for server K3 runtime attempt %d", expectedAttempt)
		}
	}

	for {
		var revisionStatus, deliveryStatus, manifest string
		err := schemaDB.QueryRowContext(ctx, `
			SELECT r.status, o.status, r.chunk_manifest_sha256
			FROM knowledge_revisions r
			JOIN knowledge_outbox o ON o.revision_id = r.revision_id
			WHERE r.revision_id = $1 AND o.topic = 'knowledge.revision.index_requested'`, revision.RevisionID).Scan(&revisionStatus, &deliveryStatus, &manifest)
		if err == nil && revisionStatus == "ready" && deliveryStatus == "delivered" && manifest == "sha256:server-k3-chunks" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for ready/delivered: status=%s delivery=%s manifest=%s err=%v", revisionStatus, deliveryStatus, manifest, err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	var activeSnapshot sql.NullString
	if err := schemaDB.QueryRowContext(ctx, `SELECT snapshot_id FROM knowledge_active_snapshot WHERE singleton = TRUE`).Scan(&activeSnapshot); err != nil {
		t.Fatalf("read active snapshot: %v", err)
	}
	if activeSnapshot.Valid {
		t.Fatalf("K3 indexing unexpectedly changed active snapshot to %q", activeSnapshot.String)
	}
}

func serverK3DatabaseURLWithSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse K3 database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func quoteServerK3Identifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
