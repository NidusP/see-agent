package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"see-agent/services/backend-go/internal/repository"
)

func TestServerK4LifecycleHTTPContract(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("K4_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("set K4_TEST_DATABASE_URL to run the Server K4 lifecycle smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("k4_server_%d", time.Now().UTC().UnixNano())
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quoteServerK3Identifier(schema)); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	defer func() {
		if _, err := adminDB.ExecContext(context.Background(), `DROP SCHEMA `+quoteServerK3Identifier(schema)+` CASCADE`); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	}()
	databaseURL := serverK4DatabaseURLWithSearchPath(t, baseURL, schema)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("REDIS_URL", "")
	t.Setenv("K3_DELIVERY_ENABLED", "false")
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	t.Setenv("AUTH_SESSION_STORE", "memory")
	t.Setenv("BACKEND_UPLOAD_DIR", t.TempDir())
	t.Setenv("SEE_AGENT_ADMIN_USERNAME", "admin")
	t.Setenv("SEE_AGENT_ADMIN_PASSWORD", "admin123")
	t.Setenv("SEE_AGENT_USER_USERNAME", "user")
	t.Setenv("SEE_AGENT_USER_PASSWORD", "user123")

	backend := New("127.0.0.1:0")
	defer backend.Shutdown(context.Background())
	ts := httptest.NewServer(backend.Handler())
	defer ts.Close()

	assertStatus(t, http.MethodGet, ts.URL+"/api/kb/documents", nil, nil, http.StatusUnauthorized)
	user := loginK4Client(t, ts.URL, "user", "user123")
	request, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/kb/documents", nil)
	response, err := user.Do(request)
	if err != nil {
		t.Fatalf("user list request: %v", err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("user list status = %d, want 403", response.StatusCode)
	}
	response.Body.Close()
	admin := loginK4Client(t, ts.URL, "admin", "admin123")

	missingKeyBody, missingKeyType := k4MultipartBody(t, "policy.md", []byte("version one"), "Policy", "")
	request, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/kb/documents", missingKeyBody)
	request.Header.Set("Content-Type", missingKeyType)
	response, err = admin.Do(request)
	if err != nil {
		t.Fatalf("missing key upload: %v", err)
	}
	if response.StatusCode != http.StatusBadRequest || k4ErrorCode(t, response) != "missing_idempotency_key" {
		t.Fatalf("missing key response = %d", response.StatusCode)
	}

	create := func(content []byte, key string) (*http.Response, map[string]any) {
		body, contentType := k4MultipartBody(t, "policy.md", content, "Policy", "")
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/kb/documents", body)
		if err != nil {
			t.Fatalf("new document request: %v", err)
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Idempotency-Key", key)
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatalf("document upload: %v", err)
		}
		payload := k4Payload(t, resp)
		return resp, payload
	}
	createResponse, createPayload := create([]byte("version one"), "create-policy")
	if createResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d, payload=%#v", createResponse.StatusCode, createPayload)
	}
	documentID := k4NestedString(t, createPayload, "document", "document_id")
	revisionID := k4NestedString(t, createPayload, "revision", "revision_id")
	if got := k4NestedString(t, createPayload, "revision", "status"); got != "queued" {
		t.Fatalf("upload revision status = %q, want queued", got)
	}
	if got := k4NestedString(t, createPayload, "delivery", "status"); got != "pending" {
		t.Fatalf("upload delivery status = %q, want pending", got)
	}
	repeatResponse, repeatPayload := create([]byte("version one"), "create-policy")
	if repeatResponse.StatusCode != http.StatusAccepted || k4NestedString(t, repeatPayload, "revision", "revision_id") != revisionID {
		t.Fatalf("idempotent repeat = %d %#v", repeatResponse.StatusCode, repeatPayload)
	}
	mismatchResponse, mismatchPayload := create([]byte("changed content"), "create-policy")
	if mismatchResponse.StatusCode != http.StatusConflict || k4PayloadErrorCode(mismatchPayload) != "idempotency_key_reused" {
		t.Fatalf("idempotency mismatch = %d %#v", mismatchResponse.StatusCode, mismatchPayload)
	}

	publish := func(id, key string) (*http.Response, map[string]any) {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/kb/revisions/"+id+"/publish", nil)
		if err != nil {
			t.Fatalf("new publish request: %v", err)
		}
		req.Header.Set("Idempotency-Key", key)
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
		return resp, k4Payload(t, resp)
	}
	notReady, notReadyPayload := publish(revisionID, "publish-not-ready")
	if notReady.StatusCode != http.StatusConflict || k4PayloadErrorCode(notReadyPayload) != "revision_not_ready" {
		t.Fatalf("not-ready publish = %d %#v", notReady.StatusCode, notReadyPayload)
	}

	store, err := repository.NewPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open lifecycle verification store: %v", err)
	}
	defer store.Close()
	if _, err := store.MarkKnowledgeRevisionReady(ctx, revisionID, "sha256:policy-v1", "bge-m3"); err != nil {
		t.Fatalf("mark initial ready: %v", err)
	}
	publishedResponse, publishedPayload := publish(revisionID, "publish-ready")
	if publishedResponse.StatusCode != http.StatusOK || k4NestedString(t, publishedPayload, "snapshot", "snapshot_id") == "" {
		t.Fatalf("ready publish = %d %#v", publishedResponse.StatusCode, publishedPayload)
	}

	replacementBody, replacementType := k4MultipartBody(t, "policy.md", []byte("version two"), "", "")
	replacementRequest, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/kb/documents/"+documentID+"/revisions", replacementBody)
	replacementRequest.Header.Set("Content-Type", replacementType)
	replacementRequest.Header.Set("Idempotency-Key", "replace-policy")
	replacementResponse, err := admin.Do(replacementRequest)
	if err != nil {
		t.Fatalf("replacement upload: %v", err)
	}
	replacementPayload := k4Payload(t, replacementResponse)
	if replacementResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("replacement = %d %#v", replacementResponse.StatusCode, replacementPayload)
	}
	replacementID := k4NestedString(t, replacementPayload, "revision", "revision_id")

	disable := func(key string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/kb/documents/"+documentID+"/disable", nil)
		req.Header.Set("Idempotency-Key", key)
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatalf("disable request: %v", err)
		}
		return resp, k4Payload(t, resp)
	}
	inflight, inflightPayload := disable("disable-inflight")
	if inflight.StatusCode != http.StatusConflict || k4PayloadErrorCode(inflightPayload) != "retirement_not_ready" {
		t.Fatalf("disable without replacement proof = %d %#v", inflight.StatusCode, inflightPayload)
	}
	if _, err := store.MarkKnowledgeRevisionReady(ctx, replacementID, "sha256:policy-v2", "bge-m3"); err != nil {
		t.Fatalf("mark replacement ready: %v", err)
	}
	if response, payload := publish(replacementID, "publish-replacement"); response.StatusCode != http.StatusOK {
		t.Fatalf("publish replacement = %d %#v", response.StatusCode, payload)
	}
	blockedResponse, blockedPayload := disable("disable-missing-replacement-proof")
	if blockedResponse.StatusCode != http.StatusConflict || k4PayloadErrorCode(blockedPayload) != "retirement_not_ready" {
		t.Fatalf("disable without replacement proof = %d %#v", blockedResponse.StatusCode, blockedPayload)
	}

	legacyBody, legacyType := k4MultipartBody(t, "legacy.md", []byte("legacy compatibility"), "", "")
	legacyRequest, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/kb/files", legacyBody)
	legacyRequest.Header.Set("Content-Type", legacyType)
	legacyResponse, err := admin.Do(legacyRequest)
	if err != nil {
		t.Fatalf("legacy upload: %v", err)
	}
	legacyPayload := k4Payload(t, legacyResponse)
	if legacyResponse.StatusCode != http.StatusAccepted || k4NestedString(t, legacyPayload, "file", "file_id") == "" {
		t.Fatalf("legacy compatibility upload = %d %#v", legacyResponse.StatusCode, legacyPayload)
	}
}

func serverK4DatabaseURLWithSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func loginK4Client(t *testing.T, baseURL, username, password string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}
	response, err := client.Post(baseURL+"/api/auth/login", "application/json", strings.NewReader(fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)))
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("login %s = %d: %s", username, response.StatusCode, body)
	}
	return client
}

func k4MultipartBody(t *testing.T, filename string, content []byte, displayName, effectiveAt string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if displayName != "" {
		_ = writer.WriteField("display_name", displayName)
	}
	if effectiveAt != "" {
		_ = writer.WriteField("effective_at", effectiveAt)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}
	return body, writer.FormDataContentType()
}

func k4Payload(t *testing.T, response *http.Response) map[string]any {
	t.Helper()
	defer response.Body.Close()
	payload := map[string]any{}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response status %d: %v", response.StatusCode, err)
	}
	return payload
}

func k4ErrorCode(t *testing.T, response *http.Response) string {
	t.Helper()
	return k4PayloadErrorCode(k4Payload(t, response))
}

func k4PayloadErrorCode(payload map[string]any) string {
	errorPayload, _ := payload["error"].(map[string]any)
	code, _ := errorPayload["code"].(string)
	return code
}

func k4NestedString(t *testing.T, payload map[string]any, section, key string) string {
	t.Helper()
	object, ok := payload[section].(map[string]any)
	if !ok {
		return ""
	}
	value, _ := object[key].(string)
	return value
}

func assertStatus(t *testing.T, method, target string, body io.Reader, headers http.Header, want int) {
	t.Helper()
	request, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request %s: %v", target, err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status %s %s = %d, want %d: %s", method, target, response.StatusCode, want, body)
	}
}
