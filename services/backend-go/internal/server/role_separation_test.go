package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"see-agent/services/backend-go/internal/auth"
)

func TestRoleSeparationGuardsKnowledgeAndSystemRoutes(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_URL", "")
	t.Setenv("K3_DELIVERY_ENABLED", "false")
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	t.Setenv("AUTH_SESSION_STORE", "memory")
	t.Setenv("BACKEND_UPLOAD_DIR", t.TempDir())
	t.Setenv("SEE_AGENT_ADMIN_USERNAME", "system")
	t.Setenv("SEE_AGENT_ADMIN_PASSWORD", "system-pass")
	t.Setenv("SEE_AGENT_KNOWLEDGE_ADMIN_USERNAME", "knowledge")
	t.Setenv("SEE_AGENT_KNOWLEDGE_ADMIN_PASSWORD", "knowledge-pass")
	t.Setenv("SEE_AGENT_USER_USERNAME", "user")
	t.Setenv("SEE_AGENT_USER_PASSWORD", "user-pass")

	backend := New("127.0.0.1:0")
	defer backend.Shutdown(t.Context())
	api := httptest.NewServer(backend.Handler())
	defer api.Close()

	user := roleLoginClient(t, api.URL, "user", "user-pass")
	knowledge := roleLoginClient(t, api.URL, "knowledge", "knowledge-pass")
	system := roleLoginClient(t, api.URL, "system", "system-pass")

	assertRoleStatus(t, user, api.URL+"/api/kb/documents", http.StatusForbidden)
	assertRoleStatus(t, knowledge, api.URL+"/api/kb/documents", http.StatusServiceUnavailable)
	assertRoleStatus(t, system, api.URL+"/api/kb/documents", http.StatusServiceUnavailable)
	assertRoleStatus(t, user, api.URL+"/api/agent/config", http.StatusForbidden)
	assertRoleStatus(t, knowledge, api.URL+"/api/agent/config", http.StatusForbidden)
	assertRoleStatus(t, system, api.URL+"/api/agent/config", http.StatusOK)
	assertRoleMethodStatus(t, user, http.MethodPost, api.URL+"/api/kb/revisions/rev_missing/publish", http.StatusForbidden)
	assertRoleMethodStatus(t, knowledge, http.MethodPost, api.URL+"/api/kb/revisions/rev_missing/publish", http.StatusForbidden)
	assertRoleMethodStatus(t, system, http.MethodPost, api.URL+"/api/kb/revisions/rev_missing/publish", http.StatusServiceUnavailable)
	assertRoleStatus(t, knowledge, api.URL+"/api/kb/documents/doc_missing/retirement-readiness", http.StatusForbidden)
	assertRoleStatus(t, system, api.URL+"/api/kb/documents/doc_missing/retirement-readiness", http.StatusServiceUnavailable)
	assertRoleMethodStatus(t, knowledge, http.MethodPost, api.URL+"/api/kb/documents/doc_missing/disable", http.StatusForbidden)
	assertRoleMethodStatus(t, system, http.MethodPost, api.URL+"/api/kb/documents/doc_missing/disable", http.StatusServiceUnavailable)
	assertRoleMethodStatus(t, knowledge, http.MethodDelete, api.URL+"/api/kb/documents/doc_missing", http.StatusForbidden)
	assertRoleMethodStatus(t, system, http.MethodDelete, api.URL+"/api/kb/documents/doc_missing", http.StatusServiceUnavailable)
}

func TestLegacyAdminSessionRetainsSystemPrivilege(t *testing.T) {
	manager := auth.NewManager([]auth.Credential{{User: auth.User{UserID: "legacy", Username: "legacy", Role: auth.RoleLegacyAdmin}, Password: "secret"}}, time.Hour)
	user, ok := manager.Authenticate("legacy", "secret")
	if !ok {
		t.Fatal("legacy user did not authenticate")
	}
	token, _, err := manager.CreateSession(user)
	if err != nil {
		t.Fatal(err)
	}
	handler := requireSystemAdmin(manager, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/system", nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	response := httptest.NewRecorder()
	handler(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("legacy system role status=%d", response.Code)
	}
}

func roleLoginClient(t *testing.T, baseURL, username, password string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	response, err := client.Post(baseURL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login %s status=%d", username, response.StatusCode)
	}
	return client
}

func assertRoleStatus(t *testing.T, client *http.Client, url string, expected int) {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expected {
		t.Fatalf("GET %s status=%d want=%d", url, response.StatusCode, expected)
	}
}

func assertRoleMethodStatus(t *testing.T, client *http.Client, method, url string, expected int) {
	t.Helper()
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Idempotency-Key", "role-separation-publish")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expected {
		t.Fatalf("%s %s status=%d want=%d", method, url, response.StatusCode, expected)
	}
}
