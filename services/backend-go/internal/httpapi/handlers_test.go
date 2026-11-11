package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/httpapi"
	"see-agent/services/backend-go/internal/repository"
	"see-agent/services/backend-go/internal/server"
	"see-agent/services/backend-go/internal/service"
)

type failingRuntimeClient struct{}

func (failingRuntimeClient) RunIngestion(context.Context, domain.KnowledgeFile, domain.IngestionTaskSummary) (domain.IngestionTaskResult, error) {
	return domain.IngestionTaskResult{}, nil
}

func (failingRuntimeClient) RunChat(context.Context, domain.ChatRuntimeRequest) (domain.ChatRuntimeResponse, error) {
	return domain.ChatRuntimeResponse{}, errors.New("runtime unavailable")
}

func (failingRuntimeClient) StreamChat(context.Context, domain.ChatRuntimeRequest, func(domain.ChatRuntimeStreamEvent) error) error {
	return errors.New("runtime unavailable")
}

type streamingRuntimeClient struct{}

type snapshotUnavailableStreamRuntimeClient struct{}

type snapshotUnavailableRuntimeFailure struct{}

func (snapshotUnavailableRuntimeFailure) Error() string { return "knowledge snapshot unavailable" }
func (snapshotUnavailableRuntimeFailure) RuntimeFailure() (string, string) {
	return "knowledge_snapshot_unavailable", "runtime_http"
}

type snapshotUnavailableRuntimeClient struct{}

type executionAttemptRecordingStore struct {
	*repository.MemoryStore
	attemptID string
}

func (s *executionAttemptRecordingStore) CreateChatExecutionAttempt(ctx context.Context, attempt domain.ChatExecutionAttempt) (domain.ChatExecutionAttempt, error) {
	created, err := s.MemoryStore.CreateChatExecutionAttempt(ctx, attempt)
	if err == nil {
		s.attemptID = created.AttemptID
	}
	return created, err
}

type missingConfigStore struct {
	repository.Store
}

func (s missingConfigStore) CreateOwnedConversation(ownerID, title string) domain.Conversation {
	store, ok := s.Store.(repository.ConversationOwnershipStore)
	if !ok {
		return domain.Conversation{}
	}
	return store.CreateOwnedConversation(ownerID, title)
}

func (s missingConfigStore) ListOwnedConversations(ownerID string) []domain.Conversation {
	store, ok := s.Store.(repository.ConversationOwnershipStore)
	if !ok {
		return []domain.Conversation{}
	}
	return store.ListOwnedConversations(ownerID)
}

func (s missingConfigStore) GetOwnedConversation(ownerID, conversationID string) (domain.Conversation, []domain.Message, bool) {
	store, ok := s.Store.(repository.ConversationOwnershipStore)
	if !ok {
		return domain.Conversation{}, nil, false
	}
	return store.GetOwnedConversation(ownerID, conversationID)
}

func (s missingConfigStore) RenameOwnedConversation(ownerID, conversationID, title string) (domain.Conversation, bool) {
	store, ok := s.Store.(repository.ConversationOwnershipStore)
	if !ok {
		return domain.Conversation{}, false
	}
	return store.RenameOwnedConversation(ownerID, conversationID, title)
}

func (missingConfigStore) GetConfig(string) (domain.AgentConfig, bool) {
	return domain.AgentConfig{}, false
}

func (streamingRuntimeClient) RunIngestion(context.Context, domain.KnowledgeFile, domain.IngestionTaskSummary) (domain.IngestionTaskResult, error) {
	return domain.IngestionTaskResult{}, nil
}

func (streamingRuntimeClient) RunChat(context.Context, domain.ChatRuntimeRequest) (domain.ChatRuntimeResponse, error) {
	return domain.ChatRuntimeResponse{}, nil
}

func (streamingRuntimeClient) StreamChat(_ context.Context, request domain.ChatRuntimeRequest, emit func(domain.ChatRuntimeStreamEvent) error) error {
	for _, delta := range []string{"AA", "BB"} {
		if err := emit(domain.ChatRuntimeStreamEvent{
			Event:     "message.delta",
			RequestID: request.RequestID,
			MessageID: request.MessageID,
			Delta:     delta,
		}); err != nil {
			return err
		}
	}
	return emit(domain.ChatRuntimeStreamEvent{
		Event:     "message.completed",
		RequestID: request.RequestID,
		MessageID: request.MessageID,
		Response: &domain.ChatRuntimeResponse{
			RequestID:      request.RequestID,
			ConversationID: request.ConversationID,
			MessageID:      request.MessageID,
			AssistantMessage: domain.RuntimeAssistantMessage{
				Role:    "assistant",
				Content: "AABB",
			},
			Citations:           []domain.RuntimeCitation{},
			FinishReason:        "stop",
			CompletedAt:         "2026-07-03T00:00:00Z",
			Intent:              "knowledge_query",
			RetrievalPerformed:  true,
			GenerationPerformed: true,
			KnowledgeUsed:       false,
			ContextResolution: &domain.RuntimeContextResolution{
				Intent:            "knowledge_query",
				Route:             "rag",
				Kind:              "standalone",
				ResolverProvider:  "test_router",
				ResolverModelID:   "test-router-v1",
				ResolverPerformed: true,
				Confidence:        0.95,
			},
		},
	})
}

func (snapshotUnavailableStreamRuntimeClient) RunIngestion(context.Context, domain.KnowledgeFile, domain.IngestionTaskSummary) (domain.IngestionTaskResult, error) {
	return domain.IngestionTaskResult{}, nil
}

func (snapshotUnavailableStreamRuntimeClient) RunChat(context.Context, domain.ChatRuntimeRequest) (domain.ChatRuntimeResponse, error) {
	return domain.ChatRuntimeResponse{}, errors.New("unexpected non-stream call")
}

func (snapshotUnavailableStreamRuntimeClient) StreamChat(_ context.Context, request domain.ChatRuntimeRequest, emit func(domain.ChatRuntimeStreamEvent) error) error {
	return emit(domain.ChatRuntimeStreamEvent{
		Event:     "message.error",
		RequestID: request.RequestID,
		MessageID: request.MessageID,
		Error: &domain.ChatRuntimeStreamError{
			Code:      "knowledge_snapshot_unavailable",
			Message:   "a published knowledge snapshot is not available yet",
			Retryable: true,
		},
	})
}

func (snapshotUnavailableRuntimeClient) RunIngestion(context.Context, domain.KnowledgeFile, domain.IngestionTaskSummary) (domain.IngestionTaskResult, error) {
	return domain.IngestionTaskResult{}, nil
}

func (snapshotUnavailableRuntimeClient) RunChat(context.Context, domain.ChatRuntimeRequest) (domain.ChatRuntimeResponse, error) {
	return domain.ChatRuntimeResponse{}, snapshotUnavailableRuntimeFailure{}
}

func (snapshotUnavailableRuntimeClient) StreamChat(context.Context, domain.ChatRuntimeRequest, func(domain.ChatRuntimeStreamEvent) error) error {
	return errors.New("unexpected stream call")
}

func setAuthTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AUTH_SESSION_STORE", "")
	t.Setenv("REDIS_URL", "")
	t.Setenv("SEE_AGENT_ADMIN_USERNAME", "admin")
	t.Setenv("SEE_AGENT_ADMIN_PASSWORD", "admin123")
	t.Setenv("SEE_AGENT_USER_USERNAME", "user")
	t.Setenv("SEE_AGENT_USER_PASSWORD", "user123")
}

func loginTestClient(t *testing.T, baseURL, username, password string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}
	resp, err := client.Post(
		baseURL+"/api/auth/login",
		"application/json",
		bytes.NewBufferString(`{"username":"`+username+`","password":"`+password+`"}`),
	)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected login 200, got %d: %s", resp.StatusCode, string(body))
	}
	return client
}

func TestAgentConfigRejectsUnregisteredPromptVersion(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()
	client := loginTestClient(t, ts.URL, "admin", "admin123")

	request, err := http.NewRequest(
		http.MethodPut,
		ts.URL+"/api/agent/config",
		bytes.NewBufferString(`{"model_id":"qwen-local-dev","system_prompt_version":"prompt_unknown","temperature":0.2,"max_output_tokens":512}`),
	)
	if err != nil {
		t.Fatalf("create config request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("update agent config: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("expected 422, got %d: %s", response.StatusCode, string(body))
	}
}

func TestAgentConfigEnforcesRegisteredPromptAndAnswerPolicyPairs(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()
	client := loginTestClient(t, ts.URL, "admin", "admin123")

	for _, testCase := range []struct {
		name       string
		body       string
		statusCode int
		wantPolicy string
	}{
		{
			name:       "legacy prompt with omitted policy",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v7","response_style":"concise","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusOK,
			wantPolicy: domain.LegacyAnswerPolicyVersion,
		},
		{
			name:       "v1 policy pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v8","response_style":"concise","answer_policy_version":"answer_policy_v1","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusOK,
			wantPolicy: domain.AnswerPolicyV1,
		},
		{
			name:       "v2 policy pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v9","response_style":"concise","answer_policy_version":"answer_policy_v2","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusOK,
			wantPolicy: domain.AnswerPolicyV2,
		},
		{
			name:       "v3 policy pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v10","response_style":"concise","answer_policy_version":"answer_policy_v3","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusOK,
			wantPolicy: domain.AnswerPolicyV3,
		},
		{
			name:       "v4 policy pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v11","response_style":"concise","answer_policy_version":"answer_policy_v4","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusOK,
			wantPolicy: domain.AnswerPolicyV4,
		},
		{
			name:       "v5 policy pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v12","response_style":"concise","answer_policy_version":"answer_policy_v5","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusOK,
			wantPolicy: domain.AnswerPolicyV5,
		},
		{
			name:       "v8 requires v1 policy",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v8","response_style":"concise","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "v9 requires v2 policy",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v9","response_style":"concise","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "v10 requires v3 policy",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v10","response_style":"concise","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "v11 requires v4 policy",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v11","response_style":"concise","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "v12 requires v5 policy",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v12","response_style":"concise","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "v1 cross pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v8","response_style":"concise","answer_policy_version":"answer_policy_v2","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "v2 cross pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v9","response_style":"concise","answer_policy_version":"answer_policy_v1","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "v1 and v3 cross pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v8","response_style":"concise","answer_policy_version":"answer_policy_v3","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "v2 and v3 cross pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v9","response_style":"concise","answer_policy_version":"answer_policy_v3","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "v3 and v1 cross pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v10","response_style":"concise","answer_policy_version":"answer_policy_v1","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "v3 and v2 cross pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v10","response_style":"concise","answer_policy_version":"answer_policy_v2","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "v4 and v3 cross pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v11","response_style":"concise","answer_policy_version":"answer_policy_v3","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "v5 and v4 cross pair",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v12","response_style":"concise","answer_policy_version":"answer_policy_v4","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "legacy prompt rejects policy",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v7","response_style":"concise","answer_policy_version":"answer_policy_v1","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
		{
			name:       "unknown policy",
			body:       `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v10","response_style":"concise","answer_policy_version":"answer_policy_v5","temperature":0.2,"top_p":1,"max_output_tokens":1024}`,
			statusCode: http.StatusUnprocessableEntity,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPut, ts.URL+"/api/agent/config", bytes.NewBufferString(testCase.body))
			if err != nil {
				t.Fatalf("create config request: %v", err)
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("update agent config: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != testCase.statusCode {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want %d: %s", response.StatusCode, testCase.statusCode, string(body))
			}
			if testCase.statusCode != http.StatusOK {
				return
			}
			var payload struct {
				Config domain.AgentConfig `json:"config"`
			}
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatalf("decode config response: %v", err)
			}
			if payload.Config.AnswerPolicyVersion != testCase.wantPolicy {
				t.Fatalf("answer_policy_version = %q, want %q", payload.Config.AnswerPolicyVersion, testCase.wantPolicy)
			}
		})
	}
}

func TestRetrievalConfigOnlyAcceptsRegisteredStrategies(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()
	client := loginTestClient(t, ts.URL, "admin", "admin123")

	for _, body := range []string{
		`{"retrieval_strategy":"untrusted_python_plugin","embedding_model":"all-minilm","top_k":6,"rerank_enabled":true,"evidence_min_score":0.15}`,
		`{"embedding_model":"all-minilm","top_k":6,"rerank_enabled":true,"evidence_min_score":0.15}`,
	} {
		request, err := http.NewRequest(http.MethodPut, ts.URL+"/api/agent/retrieval-config", bytes.NewBufferString(body))
		if err != nil {
			t.Fatalf("create invalid retrieval config request: %v", err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("update invalid retrieval config: %v", err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("invalid retrieval config status = %d, want 422", response.StatusCode)
		}
	}

	request, err := http.NewRequest(
		http.MethodPut,
		ts.URL+"/api/agent/retrieval-config",
		bytes.NewBufferString(`{"retrieval_strategy":"native_dense_v1","embedding_model":"all-minilm","top_k":6,"rerank_enabled":true,"evidence_min_score":0.15}`),
	)
	if err != nil {
		t.Fatalf("create valid retrieval config request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("update valid retrieval config: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("valid retrieval config status = %d: %s", response.StatusCode, string(body))
	}
	var payload struct {
		Config domain.RetrievalConfig `json:"config"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode retrieval config response: %v", err)
	}
	if payload.Config.RetrievalStrategy != domain.RetrievalStrategyNativeDenseV1 {
		t.Fatalf("retrieval_strategy = %q, want %q", payload.Config.RetrievalStrategy, domain.RetrievalStrategyNativeDenseV1)
	}
}

func TestConfigVersionsListsAgentAndRetrievalHistory(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()
	client := loginTestClient(t, ts.URL, "admin", "admin123")

	for _, requestSpec := range []struct {
		path string
		body string
	}{
		{"/api/agent/config", `{"model_id":"llama3.2:latest","system_prompt_version":"prompt_v2","response_style":"detailed","temperature":0.3,"top_p":0.9,"max_output_tokens":1200}`},
		{"/api/agent/retrieval-config", `{"retrieval_strategy":"native_dense_v1","embedding_model":"all-minilm","top_k":4,"rerank_enabled":true,"evidence_min_score":0.2}`},
	} {
		request, err := http.NewRequest(http.MethodPut, ts.URL+requestSpec.path, bytes.NewBufferString(requestSpec.body))
		if err != nil {
			t.Fatalf("create update request: %v", err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("update config: %v", err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("update status = %d, want 200", response.StatusCode)
		}
	}

	response, err := client.Get(ts.URL + "/api/agent/config/versions")
	if err != nil {
		t.Fatalf("list config versions: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("list status = %d: %s", response.StatusCode, string(body))
	}
	var payload struct {
		Items          []domain.AgentConfig     `json:"items"`
		RetrievalItems []domain.RetrievalConfig `json:"retrieval_items"`
		Total          int                      `json:"total"`
		RetrievalTotal int                      `json:"retrieval_total"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode version history: %v", err)
	}
	if payload.Total != len(payload.Items) || payload.RetrievalTotal != len(payload.RetrievalItems) {
		t.Fatalf("history totals do not match items: %+v", payload)
	}
	if len(payload.Items) < 2 || payload.Items[len(payload.Items)-1].ResponseStyle != "detailed" {
		t.Fatalf("agent history did not retain new config: %+v", payload.Items)
	}
	if len(payload.RetrievalItems) < 2 || payload.RetrievalItems[len(payload.RetrievalItems)-1].TopK != 4 {
		t.Fatalf("retrieval history did not retain new config: %+v", payload.RetrievalItems)
	}
}

func TestConversationLifecycle(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()
	client := loginTestClient(t, ts.URL, "admin", "admin123")

	resp, err := client.Post(ts.URL+"/api/chat/conversations", "application/json", bytes.NewBufferString(`{"title":"Ops Q&A"}`))
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var created struct {
		Conversation struct {
			ConversationID string `json:"conversation_id"`
			Title          string `json:"title"`
		} `json:"conversation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Conversation.ConversationID == "" {
		t.Fatal("expected conversation_id")
	}
	if created.Conversation.Title != "Ops Q&A" {
		t.Fatalf("explicit create title = %q, want Ops Q&A", created.Conversation.Title)
	}

	detailResp, err := client.Get(ts.URL + "/api/chat/conversations/" + created.Conversation.ConversationID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	defer detailResp.Body.Close()
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", detailResp.StatusCode)
	}
	var detail struct {
		Messages []domain.Message `json:"messages"`
	}
	if err := json.NewDecoder(detailResp.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detail.Messages == nil {
		t.Fatal("expected empty messages array, got null")
	}
	if len(detail.Messages) != 0 {
		t.Fatalf("expected no messages, got %d", len(detail.Messages))
	}
}

func TestConversationOwnershipIsolation(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()
	adminClient := loginTestClient(t, ts.URL, "admin", "admin123")
	userClient := loginTestClient(t, ts.URL, "user", "user123")

	adminConversation := createConversationForTest(t, adminClient, ts.URL, "Admin private")
	userConversation := createConversationForTest(t, userClient, ts.URL, "User private")
	if adminConversation.OwnerID != "user_admin" {
		t.Fatalf("admin owner_id = %q, want user_admin", adminConversation.OwnerID)
	}
	if userConversation.OwnerID != "user_member" {
		t.Fatalf("user owner_id = %q, want user_member", userConversation.OwnerID)
	}

	assertConversationListForTest(t, adminClient, ts.URL, adminConversation.ConversationID, userConversation.ConversationID)
	assertConversationListForTest(t, userClient, ts.URL, userConversation.ConversationID, adminConversation.ConversationID)

	assertStatusForTest(t, userClient, http.MethodGet, ts.URL+"/api/chat/conversations/"+adminConversation.ConversationID, "", http.StatusNotFound)
	assertStatusForTest(t, userClient, http.MethodPost, ts.URL+"/api/chat/conversations/"+adminConversation.ConversationID+"/messages", `{"content":"cross-owner write","stream":false}`, http.StatusNotFound)
	assertStatusForTest(t, userClient, http.MethodGet, ts.URL+"/api/chat/conversations/"+adminConversation.ConversationID+"/sources/msg_unknown", "", http.StatusNotFound)
	assertStatusForTest(t, userClient, http.MethodPost, ts.URL+"/api/chat/conversations/"+adminConversation.ConversationID+"/messages/msg_unknown/revalidate", `{"reason":"cross-owner"}`, http.StatusNotFound)
	assertStatusForTest(t, userClient, http.MethodGet, ts.URL+"/api/chat/conversations/"+adminConversation.ConversationID+"/revalidations/reval_unknown", "", http.StatusNotFound)
	assertStatusForTest(t, adminClient, http.MethodGet, ts.URL+"/api/chat/conversations/"+userConversation.ConversationID, "", http.StatusNotFound)

	detailResponse, err := adminClient.Get(ts.URL + "/api/chat/conversations/" + adminConversation.ConversationID)
	if err != nil {
		t.Fatalf("get owner conversation after denied write: %v", err)
	}
	defer detailResponse.Body.Close()
	var detail struct {
		Messages []domain.Message `json:"messages"`
	}
	if err := json.NewDecoder(detailResponse.Body).Decode(&detail); err != nil {
		t.Fatalf("decode owner conversation after denied write: %v", err)
	}
	if len(detail.Messages) != 0 {
		t.Fatalf("cross-owner write changed conversation: got %d messages, want 0", len(detail.Messages))
	}
}

func createConversationForTest(t *testing.T, client *http.Client, baseURL, title string) domain.Conversation {
	t.Helper()
	response, err := client.Post(baseURL+"/api/chat/conversations", "application/json", bytes.NewBufferString(`{"title":"`+title+`"}`))
	if err != nil {
		t.Fatalf("create conversation %q: %v", title, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("create conversation %q: got %d: %s", title, response.StatusCode, string(body))
	}
	var payload struct {
		Conversation domain.Conversation `json:"conversation"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode conversation %q: %v", title, err)
	}
	return payload.Conversation
}

func createMessageForTest(t *testing.T, client *http.Client, baseURL, conversationID string) string {
	t.Helper()
	response, err := client.Post(baseURL+"/api/chat/conversations/"+conversationID+"/messages", "application/json", bytes.NewBufferString(`{"content":"owner message","stream":false}`))
	if err != nil {
		t.Fatalf("create owner message: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("create owner message: got %d: %s", response.StatusCode, string(body))
	}
	var payload struct {
		AssistantMessage domain.Message `json:"assistant_message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode owner message: %v", err)
	}
	return payload.AssistantMessage.MessageID
}

func assertConversationListForTest(t *testing.T, client *http.Client, baseURL, expectedID, forbiddenID string) {
	t.Helper()
	response, err := client.Get(baseURL + "/api/chat/conversations")
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	defer response.Body.Close()
	var payload struct {
		Items []domain.Conversation `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode conversation list: %v", err)
	}
	foundExpected := false
	for _, conversation := range payload.Items {
		if conversation.ConversationID == forbiddenID {
			t.Fatalf("conversation list leaked %s", forbiddenID)
		}
		foundExpected = foundExpected || conversation.ConversationID == expectedID
	}
	if !foundExpected {
		t.Fatalf("conversation list did not contain owned conversation %s", expectedID)
	}
}

func assertStatusForTest(t *testing.T, client *http.Client, method, url, body string, expectedStatus int) {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, url, err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost {
		request.Header.Set("Idempotency-Key", "ownership-test-"+strings.ReplaceAll(url, "/", "-"))
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s: got %d, want %d: %s", method, url, response.StatusCode, expectedStatus, string(payload))
	}
}

func TestMessageAndSourcesFlow(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()
	client := loginTestClient(t, ts.URL, "admin", "admin123")

	createResp, err := client.Post(ts.URL+"/api/chat/conversations", "application/json", bytes.NewBufferString(`{"title":"Claims"}`))
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	defer createResp.Body.Close()

	var created struct {
		Conversation struct {
			ConversationID string `json:"conversation_id"`
		} `json:"conversation"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}

	messageResp, err := client.Post(ts.URL+"/api/chat/conversations/"+created.Conversation.ConversationID+"/messages", "application/json", bytes.NewBufferString(`{"content":"What is the reimbursement deadline?","stream":false}`))
	if err != nil {
		t.Fatalf("post message: %v", err)
	}
	defer messageResp.Body.Close()
	if messageResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", messageResp.StatusCode)
	}
	body, err := io.ReadAll(messageResp.Body)
	if err != nil {
		t.Fatalf("read unavailable response: %v", err)
	}
	if !strings.Contains(string(body), `"code":"runtime_unavailable"`) {
		t.Fatalf("expected runtime unavailable error, got %s", body)
	}
}

func TestStreamingMessageFlow(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()
	client := loginTestClient(t, ts.URL, "admin", "admin123")

	createResp, err := client.Post(ts.URL+"/api/chat/conversations", "application/json", bytes.NewBufferString(`{"title":"Streaming"}`))
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	defer createResp.Body.Close()

	var created struct {
		Conversation struct {
			ConversationID string `json:"conversation_id"`
		} `json:"conversation"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}

	req, err := http.NewRequest(
		http.MethodPost,
		ts.URL+"/api/chat/conversations/"+created.Conversation.ConversationID+"/messages",
		bytes.NewBufferString(`{"content":"Stream this answer.","stream":true}`),
	)
	if err != nil {
		t.Fatalf("create stream request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")

	streamResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post streaming message: %v", err)
	}
	defer streamResp.Body.Close()
	if streamResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", streamResp.StatusCode)
	}
	body, err := io.ReadAll(streamResp.Body)
	if err != nil {
		t.Fatalf("read stream response: %v", err)
	}
	if !strings.Contains(string(body), `"code":"runtime_unavailable"`) {
		t.Fatalf("expected runtime unavailable error, got %s", body)
	}
}

func TestStreamingMessageFlowForwardsRuntimeDeltas(t *testing.T) {
	store := repository.NewMemoryStore()
	svc := service.New(store, streamingRuntimeClient{}, "", true)
	conversation := svc.CreateConversationAs("admin", "Runtime stream")
	handlers := httpapi.New(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/chat/conversations/{conversation_id}/messages", handlers.ConversationMessages)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, err := http.NewRequest(
		http.MethodPost,
		ts.URL+"/api/chat/conversations/"+conversation.ConversationID+"/messages",
		bytes.NewBufferString(`{"content":"Stream from runtime.","stream":true}`),
	)
	if err != nil {
		t.Fatalf("create stream request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")

	streamResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post streaming message: %v", err)
	}
	defer streamResp.Body.Close()
	if streamResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", streamResp.StatusCode)
	}
	body, err := io.ReadAll(streamResp.Body)
	if err != nil {
		t.Fatalf("read stream response: %v", err)
	}
	streamText := string(body)
	if !strings.Contains(streamText, `"delta":"AA"`) || !strings.Contains(streamText, `"delta":"BB"`) {
		t.Fatalf("expected runtime deltas to be forwarded, got %s", streamText)
	}
	if !strings.Contains(streamText, `"content":"AABB"`) {
		t.Fatalf("expected completed runtime content, got %s", streamText)
	}

	_, messages, ok := svc.GetConversation(conversation.ConversationID)
	if !ok {
		t.Fatal("expected conversation to remain readable")
	}
	if len(messages) != 2 || messages[0].MessageID == "" || messages[1].Content != "AABB" {
		t.Fatalf("expected streamed exchange to be persisted once, got %#v", messages)
	}
}

func TestStreamingMessagePreservesSnapshotUnavailableErrorAndAttemptEvidence(t *testing.T) {
	store := &executionAttemptRecordingStore{MemoryStore: repository.NewMemoryStore()}
	svc := service.New(store, snapshotUnavailableStreamRuntimeClient{}, "", true)
	conversation := svc.CreateConversationAs("admin", "Missing runtime snapshot")
	handlers := httpapi.New(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/chat/conversations/{conversation_id}/messages", handlers.ConversationMessages)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, err := http.NewRequest(
		http.MethodPost,
		ts.URL+"/api/chat/conversations/"+conversation.ConversationID+"/messages",
		bytes.NewBufferString(`{"content":"What does the policy say?","stream":true}`),
	)
	if err != nil {
		t.Fatalf("create stream request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post streaming message: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("stream status = %d: %s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream response: %v", err)
	}
	streamText := string(body)
	if !strings.Contains(streamText, "event: message.error") {
		t.Fatalf("stream must preserve the established message.error event, got %s", streamText)
	}
	if !strings.Contains(streamText, `"code":"knowledge_snapshot_unavailable"`) {
		t.Fatalf("stream must preserve snapshot error code, got %s", streamText)
	}
	if store.attemptID == "" {
		t.Fatal("expected stream execution attempt")
	}
	attempt, found, err := store.GetChatExecutionAttempt(context.Background(), store.attemptID)
	if err != nil || !found {
		t.Fatalf("get execution attempt found=%v err=%v", found, err)
	}
	if attempt.State != domain.ChatExecutionAttemptFailed || attempt.ErrorCode != "knowledge_snapshot_unavailable" || attempt.ErrorStage != "runtime_stream" || !attempt.Retryable {
		t.Fatalf("stream attempt = %#v", attempt)
	}
	_, messages, ok := store.GetConversation(conversation.ConversationID)
	if !ok {
		t.Fatal("expected conversation detail")
	}
	if len(messages) != 0 {
		t.Fatalf("stream error must not append an exchange, got %#v", messages)
	}
}

func TestStreamingMessageMissingConfigReturnsServiceUnavailableWithoutExchange(t *testing.T) {
	store := repository.NewMemoryStore()
	conversation := store.CreateOwnedConversation("admin", "Missing stream config")
	svc := service.New(missingConfigStore{Store: store}, streamingRuntimeClient{}, "", true)
	handlers := httpapi.New(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/chat/conversations/{conversation_id}/messages", handlers.ConversationMessages)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, err := http.NewRequest(
		http.MethodPost,
		ts.URL+"/api/chat/conversations/"+conversation.ConversationID+"/messages",
		bytes.NewBufferString(`{"content":"Do not stream this.","stream":true}`),
	)
	if err != nil {
		t.Fatalf("create stream request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post streaming message: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode runtime unavailable response: %v", err)
	}
	if payload.Error.Code != "runtime_unavailable" {
		t.Fatalf("expected runtime_unavailable, got %q", payload.Error.Code)
	}

	_, messages, ok := store.GetConversation(conversation.ConversationID)
	if !ok {
		t.Fatal("expected conversation detail")
	}
	if len(messages) != 0 {
		t.Fatalf("missing config must not append an exchange, got %#v", messages)
	}
}

func TestRenameConversationValidatesAndKeepsOwnerBoundary(t *testing.T) {
	store := repository.NewMemoryStore()
	conversation := store.CreateOwnedConversation("admin", "")
	svc := service.New(store, nil, "", false)
	handlers := httpapi.New(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/chat/conversations/{conversation_id}", handlers.RenameConversation)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	request, err := http.NewRequest(http.MethodPatch, ts.URL+"/api/chat/conversations/"+conversation.ConversationID, strings.NewReader(`{"title":"  我的\n会话  "}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("rename status = %d", response.StatusCode)
	}
	var renamed struct {
		Conversation domain.Conversation `json:"conversation"`
	}
	if err := json.NewDecoder(response.Body).Decode(&renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Conversation.Title != "我的 会话" || renamed.Conversation.TitleSource != "" {
		t.Fatalf("rename response = %#v", renamed.Conversation)
	}

	invalid, err := http.NewRequest(http.MethodPatch, ts.URL+"/api/chat/conversations/"+conversation.ConversationID, strings.NewReader(`{"title":"   "}`))
	if err != nil {
		t.Fatal(err)
	}
	invalid.Header.Set("Content-Type", "application/json")
	invalidResponse, err := http.DefaultClient.Do(invalid)
	if err != nil {
		t.Fatal(err)
	}
	defer invalidResponse.Body.Close()
	if invalidResponse.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid rename status = %d", invalidResponse.StatusCode)
	}
}

func TestRuntimeChatFailureReturnsServiceUnavailable(t *testing.T) {
	store := repository.NewMemoryStore()
	svc := service.New(store, failingRuntimeClient{}, "", true)
	conversation := svc.CreateConversationAs("admin", "Runtime outage")
	handlers := httpapi.New(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/chat/conversations/{conversation_id}/messages", handlers.ConversationMessages)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	messageResp, err := http.Post(ts.URL+"/api/chat/conversations/"+conversation.ConversationID+"/messages", "application/json", bytes.NewBufferString(`{"content":"Will this fail?","stream":false}`))
	if err != nil {
		t.Fatalf("post message: %v", err)
	}
	defer messageResp.Body.Close()
	if messageResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", messageResp.StatusCode)
	}

	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(messageResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode runtime failure response: %v", err)
	}
	if payload.Error.Code != "runtime_unavailable" {
		t.Fatalf("expected runtime_unavailable, got %s", payload.Error.Code)
	}
}

func TestRuntimeSnapshotUnavailableReturnsConflictWithoutExchange(t *testing.T) {
	store := repository.NewMemoryStore()
	svc := service.New(store, snapshotUnavailableRuntimeClient{}, "", true)
	conversation := svc.CreateConversationAs("admin", "Runtime snapshot unavailable")
	handlers := httpapi.New(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/chat/conversations/{conversation_id}/messages", handlers.ConversationMessages)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	messageResp, err := http.Post(ts.URL+"/api/chat/conversations/"+conversation.ConversationID+"/messages", "application/json", bytes.NewBufferString(`{"content":"What does the policy say?","stream":false}`))
	if err != nil {
		t.Fatalf("post message: %v", err)
	}
	defer messageResp.Body.Close()
	if messageResp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(messageResp.Body)
		t.Fatalf("expected 409, got %d: %s", messageResp.StatusCode, body)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(messageResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode snapshot unavailable response: %v", err)
	}
	if payload.Error.Code != "knowledge_snapshot_unavailable" {
		t.Fatalf("error code = %q", payload.Error.Code)
	}
	_, messages, ok := store.GetConversation(conversation.ConversationID)
	if !ok {
		t.Fatal("expected conversation detail")
	}
	if len(messages) != 0 {
		t.Fatalf("snapshot failure must not append an exchange, got %#v", messages)
	}
}

func TestKnowledgeUploadAndDetail(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()
	client := loginTestClient(t, ts.URL, "admin", "admin123")

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "policy-catalog.json")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(`{"policy":"travel"}`)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/kb/files", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload file: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	var upload struct {
		File struct {
			FileID       string `json:"file_id"`
			SourceFormat string `json:"source_format"`
		} `json:"file"`
		IngestionTask struct {
			Status string `json:"status"`
		} `json:"ingestion_task"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&upload); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if upload.File.SourceFormat != "json" {
		t.Fatalf("expected json source format, got %s", upload.File.SourceFormat)
	}
	if upload.IngestionTask.Status != "queued" {
		t.Fatalf("expected queued task, got %s", upload.IngestionTask.Status)
	}

	detailResp, err := client.Get(ts.URL + "/api/kb/files/" + upload.File.FileID)
	if err != nil {
		t.Fatalf("get file detail: %v", err)
	}
	defer detailResp.Body.Close()
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", detailResp.StatusCode)
	}
}

func TestProtectedRoutesRequireLogin(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/chat/conversations")
	if err != nil {
		t.Fatalf("get conversations: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestUserCannotAccessAdminKnowledgeRoutes(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()
	client := loginTestClient(t, ts.URL, "user", "user123")

	resp, err := client.Get(ts.URL + "/api/kb/files")
	if err != nil {
		t.Fatalf("get knowledge files: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestAuthMeReturnsCurrentUser(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()
	client := loginTestClient(t, ts.URL, "admin", "admin123")

	resp, err := client.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatalf("get current user: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload struct {
		User struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode current user: %v", err)
	}
	if payload.User.Username != "admin" || payload.User.Role != "system_admin" {
		t.Fatalf("expected system-admin current user, got %#v", payload.User)
	}
}

func TestK7AdminEvidenceRoutesEnforceRoleAndValidateRegistration(t *testing.T) {
	t.Setenv("AI_RUNTIME_BASE_URL", "")
	setAuthTestEnv(t)

	ts := httptest.NewServer(server.New(":0").Handler())
	defer ts.Close()
	unauthenticated, err := http.Get(ts.URL + "/api/admin/evidence/executions")
	if err != nil {
		t.Fatalf("get unauthenticated evidence: %v", err)
	}
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated evidence status = %d, want 401", unauthenticated.StatusCode)
	}

	userClient := loginTestClient(t, ts.URL, "user", "user123")
	for _, path := range []string{"/api/admin/evidence/executions", "/api/admin/evidence/deliveries", "/api/eval/reports"} {
		response, err := userClient.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("user GET %s: %v", path, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("user GET %s status = %d, want 403", path, response.StatusCode)
		}
	}

	adminClient := loginTestClient(t, ts.URL, "admin", "admin123")
	invalidDelivery, err := adminClient.Get(ts.URL + "/api/admin/evidence/deliveries?status=unknown")
	if err != nil {
		t.Fatalf("get invalid delivery filter: %v", err)
	}
	invalidDelivery.Body.Close()
	if invalidDelivery.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid delivery filter status = %d, want 400", invalidDelivery.StatusCode)
	}

	missingKey, err := adminClient.Post(ts.URL+"/api/eval/reports", "application/json", bytes.NewBufferString(`{"report_id":"eval_missing_key"}`))
	if err != nil {
		t.Fatalf("register eval report without key: %v", err)
	}
	missingKey.Body.Close()
	if missingKey.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing eval idempotency key status = %d, want 400", missingKey.StatusCode)
	}

	invalidRequest, err := http.NewRequest(http.MethodPost, ts.URL+"/api/eval/reports", bytes.NewBufferString(`{"report_id":"eval_invalid"}`))
	if err != nil {
		t.Fatalf("create invalid eval registration request: %v", err)
	}
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidRequest.Header.Set("Idempotency-Key", "eval-invalid")
	invalidResponse, err := adminClient.Do(invalidRequest)
	if err != nil {
		t.Fatalf("register invalid eval report: %v", err)
	}
	invalidResponse.Body.Close()
	if invalidResponse.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid eval report status = %d, want 422", invalidResponse.StatusCode)
	}
}
