package service

import (
	"context"
	"errors"
	"testing"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/repository"
)

type chatRuntimeStub struct {
	err                      error
	requests                 []domain.ChatRuntimeRequest
	result                   *domain.ChatRuntimeResponse
	preserveResponseIdentity bool
}

type classifiedRuntimeFailure struct{}

func (classifiedRuntimeFailure) Error() string { return "classified runtime failure" }
func (classifiedRuntimeFailure) RuntimeFailure() (string, string) {
	return "model_generation_timeout", "model_provider"
}

type knowledgeSnapshotRuntimeFailure struct{}

func (knowledgeSnapshotRuntimeFailure) Error() string { return "knowledge snapshot unavailable" }
func (knowledgeSnapshotRuntimeFailure) RuntimeFailure() (string, string) {
	return "knowledge_snapshot_unavailable", "runtime_http"
}

func TestRuntimeFailureFactsPreservesTypedRuntimeFailure(t *testing.T) {
	code, stage := runtimeFailureFacts(classifiedRuntimeFailure{})
	if code != "model_generation_timeout" || stage != "model_provider" {
		t.Fatalf("facts = (%q, %q)", code, stage)
	}
}

type missingConfigStore struct {
	repository.Store
}

type noSnapshotControlledStore struct {
	repository.Store
	resolveCalls int
}

func (s *noSnapshotControlledStore) ResolveActiveKnowledgeScope(context.Context) (domain.RuntimeKnowledgeScope, error) {
	s.resolveCalls++
	return domain.RuntimeKnowledgeScope{}, repository.ErrActiveSnapshotUnavailable
}

func (s *noSnapshotControlledStore) AppendScopedRuntimeExchange(context.Context, string, string, string, domain.RuntimeKnowledgeScope, domain.ChatRuntimeResponse) (domain.Message, domain.Message, []domain.Citation, error) {
	return domain.Message{}, domain.Message{}, nil, errors.New("unexpected scoped append")
}

func (s *noSnapshotControlledStore) GetMessageSources(messageID string) (domain.MessageSources, bool) {
	return s.Store.GetMessageSources(messageID)
}

func (s *noSnapshotControlledStore) AppendControlledRuntimeExchange(_ context.Context, conversationID, userMessageID, content string, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, error) {
	user, assistant, _, ok := s.Store.AppendRuntimeExchangeWithIDs(conversationID, userMessageID, content, result)
	if !ok {
		return domain.Message{}, domain.Message{}, errors.New("controlled append failed")
	}
	user.Intent, user.KnowledgeUsed = result.Intent, false
	assistant.Intent, assistant.KnowledgeUsed = result.Intent, result.KnowledgeUsed
	assistant.KnowledgeSnapshotID, assistant.KnowledgeSnapshotManifestHash, assistant.FreshnessStatus = "", "", ""
	return user, assistant, nil
}

func (missingConfigStore) GetConfig(string) (domain.AgentConfig, bool) {
	return domain.AgentConfig{}, false
}

func (s *chatRuntimeStub) RunIngestion(context.Context, domain.KnowledgeFile, domain.IngestionTaskSummary) (domain.IngestionTaskResult, error) {
	return domain.IngestionTaskResult{}, nil
}

func (s *chatRuntimeStub) RunChat(_ context.Context, request domain.ChatRuntimeRequest) (domain.ChatRuntimeResponse, error) {
	s.requests = append(s.requests, request)
	if s.err != nil {
		return domain.ChatRuntimeResponse{}, s.err
	}
	if s.result != nil {
		result := *s.result
		if !s.preserveResponseIdentity {
			result.RequestID = request.RequestID
			result.ConversationID = request.ConversationID
			result.MessageID = request.MessageID
		}
		return withStubRoutingEvidence(request, result), nil
	}
	return withStubRoutingEvidence(request, domain.ChatRuntimeResponse{
		RequestID:      request.RequestID,
		ConversationID: request.ConversationID,
		MessageID:      request.MessageID,
		AssistantMessage: domain.RuntimeAssistantMessage{
			Role:    "assistant",
			Content: "runtime answer",
		},
		Citations:           []domain.RuntimeCitation{},
		FinishReason:        "stop",
		CompletedAt:         "2026-07-02T00:00:00Z",
		Intent:              "knowledge_query",
		RetrievalPerformed:  true,
		GenerationPerformed: true,
		KnowledgeUsed:       false,
	}), nil
}

func withStubRoutingEvidence(request domain.ChatRuntimeRequest, result domain.ChatRuntimeResponse) domain.ChatRuntimeResponse {
	if request.Intent != "context_routing_required" || result.ContextResolution != nil {
		return result
	}
	route, kind := "controlled_reply", "standalone"
	if result.Intent == "knowledge_query" {
		route = "rag"
	}
	result.ContextResolution = &domain.RuntimeContextResolution{
		Intent:            result.Intent,
		Route:             route,
		Kind:              kind,
		ResolverProvider:  "test_router",
		ResolverModelID:   "test-router-v1",
		ResolverPerformed: true,
		Confidence:        0.95,
	}
	return result
}

func TestCreateMessageRejectsMissingRuntimeIdentityWithoutWritingExchange(t *testing.T) {
	store := repository.NewMemoryStore()
	runtime := &chatRuntimeStub{
		preserveResponseIdentity: true,
		result: &domain.ChatRuntimeResponse{
			AssistantMessage:    domain.RuntimeAssistantMessage{Role: "assistant", Content: "untrusted answer"},
			FinishReason:        "stop",
			Intent:              "knowledge_query",
			RetrievalPerformed:  true,
			GenerationPerformed: true,
			KnowledgeUsed:       false,
		},
	}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("Missing runtime identity")

	_, _, ok, err := svc.CreateMessage(conversation.ConversationID, "需要什么流程？", false)
	if !ok || !errors.Is(err, ErrRuntimeExecutionFactViolation) {
		t.Fatalf("create message ok=%v err=%v", ok, err)
	}
	_, messages, found := svc.GetConversation(conversation.ConversationID)
	if !found || len(messages) != 0 {
		t.Fatalf("invalid Runtime response must not persist messages: %#v", messages)
	}
	attempt, found, attemptErr := store.GetChatExecutionAttempt(context.Background(), "attempt_"+runtime.requests[0].RequestID)
	if attemptErr != nil || !found || attempt.State != domain.ChatExecutionAttemptFailed || attempt.ErrorCode != "runtime_contract_violation" || attempt.ErrorStage != "runtime_response_validation" {
		t.Fatalf("invalid Runtime attempt = %#v found=%v err=%v", attempt, found, attemptErr)
	}
}

func TestFullstackProfileRejectsLegacyRuntimeResponseWithoutObservation(t *testing.T) {
	t.Setenv("SEE_AGENT_PROFILE", "fullstack_dev")
	request := domain.ChatRuntimeRequest{
		RequestID: "req_strict", ConversationID: "conv_strict", MessageID: "msg_strict", ModelID: "model_strict",
		Intent: "knowledge_query", ResponseOverrides: &domain.RuntimeResponseOverrides{RetrievalStrategy: "native_dense_v1", EmbeddingModel: "bge-m3"},
		KnowledgeSnapshotID: "snap_strict", KnowledgeScope: &domain.RuntimeKnowledgeScope{SnapshotID: "snap_strict", ManifestHash: "sha256:strict", RevisionIDs: []string{"rev_strict"}, ResolvedAt: "2026-07-28T00:00:00Z"},
	}
	legacy := domain.ChatRuntimeResponse{
		RequestID: request.RequestID, ConversationID: request.ConversationID, MessageID: request.MessageID,
		AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "unverified"}, Intent: request.Intent,
		RetrievalPerformed: true,
	}
	if err := validateRuntimeExecutionFacts(request, legacy); !errors.Is(err, ErrRuntimeExecutionFactViolation) {
		t.Fatalf("legacy runtime response error = %v, want strict observation violation", err)
	}

	candidateCount := 1
	strict := legacy
	strict.Citations = []domain.RuntimeCitation{{Rank: 1, CitationLabel: "policy"}}
	strict.CitationAnchors = []domain.CitationAnchor{{StartRune: 0, EndRune: 10, CitationRanks: []int{1}}}
	strict.AnswerDisposition = "grounded_answer"
	strict.AnswerPolicyVersion = "answer_policy_v1"
	strict.UsedCitationRanks = []int{1}
	strict.Execution = &domain.RuntimeExecutionObservation{
		Policy:     "performed",
		Retrieval:  domain.RuntimeRetrievalObservation{Status: "performed", Strategy: "native_dense_v1", EmbeddingModel: "bge-m3", Collection: "see_agent_chunks", CandidateCount: &candidateCount},
		Generation: domain.RuntimeGenerationObservation{Status: "performed", Provider: "openai_compatible", ModelID: request.ModelID},
	}
	strict.ContextResolution = &domain.RuntimeContextResolution{
		Intent:            "knowledge_query",
		Route:             "rag",
		Kind:              "standalone",
		BasisMessageIDs:   []string{},
		ResolverProvider:  "test_router",
		ResolverModelID:   "test-router-v1",
		ResolverPerformed: true,
		Confidence:        0.95,
	}
	if err := validateRuntimeExecutionFacts(request, strict); err != nil {
		t.Fatalf("strict runtime response error = %v", err)
	}
}

func TestCreateMessageRejectsEmptyRuntimeAnswerWithoutWritingExchange(t *testing.T) {
	store := repository.NewMemoryStore()
	runtime := &chatRuntimeStub{result: &domain.ChatRuntimeResponse{
		AssistantMessage:    domain.RuntimeAssistantMessage{Role: "assistant", Content: "  "},
		FinishReason:        "stop",
		Intent:              "knowledge_query",
		RetrievalPerformed:  true,
		GenerationPerformed: true,
		KnowledgeUsed:       false,
	}}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("Empty runtime answer")

	_, _, ok, err := svc.CreateMessage(conversation.ConversationID, "需要什么流程？", false)
	if !ok || !errors.Is(err, ErrRuntimeExecutionFactViolation) {
		t.Fatalf("create message ok=%v err=%v", ok, err)
	}
	_, messages, found := svc.GetConversation(conversation.ConversationID)
	if !found || len(messages) != 0 {
		t.Fatalf("empty Runtime response must not persist messages: %#v", messages)
	}
}

func TestCreateMessagePersistsValidCitationAnchorsInMemory(t *testing.T) {
	store := repository.NewMemoryStore()
	runtime := &chatRuntimeStub{result: &domain.ChatRuntimeResponse{
		AssistantMessage:    domain.RuntimeAssistantMessage{Role: "assistant", Content: "政策要求十日内提交。"},
		Citations:           []domain.RuntimeCitation{{Rank: 1, CitationLabel: "员工手册"}},
		CitationAnchors:     []domain.CitationAnchor{{StartRune: 0, EndRune: 10, CitationRanks: []int{1}}},
		FinishReason:        "stop",
		CompletedAt:         "2026-07-15T00:00:00Z",
		Intent:              "knowledge_query",
		RetrievalPerformed:  true,
		GenerationPerformed: true,
		KnowledgeUsed:       true,
	}}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("Anchor persistence")

	_, assistant, ok, err := svc.CreateMessage(conversation.ConversationID, "提交期限？", false)
	if !ok || err != nil {
		t.Fatalf("create message ok=%v err=%v", ok, err)
	}
	if len(assistant.CitationAnchors) != 1 || assistant.CitationAnchors[0].CitationRanks[0] != 1 {
		t.Fatalf("assistant anchors = %#v", assistant.CitationAnchors)
	}
	_, messages, ok := svc.GetConversation(conversation.ConversationID)
	if !ok || len(messages) != 2 || len(messages[1].CitationAnchors) != 1 {
		t.Fatalf("persisted messages = %#v", messages)
	}
	attempt, found, attemptErr := store.GetChatExecutionAttempt(context.Background(), "attempt_"+runtime.requests[0].RequestID)
	if attemptErr != nil || !found || attempt.State != domain.ChatExecutionAttemptSucceeded || attempt.TerminalAt == "" {
		t.Fatalf("successful attempt = %#v found=%v err=%v", attempt, found, attemptErr)
	}
}

func TestControlledGreetingDoesNotRequireActiveSnapshot(t *testing.T) {
	store := &noSnapshotControlledStore{Store: repository.NewMemoryStore()}
	runtime := &chatRuntimeStub{result: &domain.ChatRuntimeResponse{
		AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "你好！我主要根据已发布的内部知识回答问题。"},
		Citations:        []domain.RuntimeCitation{}, FinishReason: "stop", CompletedAt: "2026-07-16T00:00:00Z",
		Intent: "social_greeting", RetrievalPerformed: false, GenerationPerformed: false, KnowledgeUsed: false,
	}}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("No snapshot greeting")

	_, assistant, ok, err := svc.CreateMessage(conversation.ConversationID, "你好", false)
	if !ok || err != nil {
		t.Fatalf("controlled greeting ok=%v err=%v", ok, err)
	}
	if store.resolveCalls != 1 || assistant.Intent != "social_greeting" || assistant.KnowledgeSnapshotID != "" {
		t.Fatalf("controlled greeting resolved snapshot or invented provenance: calls=%d assistant=%#v", store.resolveCalls, assistant)
	}

}

func TestOutOfScopeDoesNotRequireActiveSnapshot(t *testing.T) {
	store := &noSnapshotControlledStore{Store: repository.NewMemoryStore()}
	runtime := &chatRuntimeStub{result: &domain.ChatRuntimeResponse{
		AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "我不能回答天气等公共开放世界问题。"},
		Citations:        []domain.RuntimeCitation{}, FinishReason: "refusal", CompletedAt: "2026-07-22T00:00:00Z",
		Intent: "out_of_scope", Refusal: true, RetrievalPerformed: false, GenerationPerformed: false, KnowledgeUsed: false,
	}}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("No snapshot public-world refusal")

	_, assistant, ok, err := svc.CreateMessage(conversation.ConversationID, "今天天气如何", false)
	if !ok || err != nil {
		t.Fatalf("out-of-scope request ok=%v err=%v", ok, err)
	}
	if store.resolveCalls != 1 || len(runtime.requests) != 1 {
		t.Fatalf("out-of-scope request must permit no snapshot: resolve=%d runtime=%d", store.resolveCalls, len(runtime.requests))
	}
	if runtime.requests[0].Intent != "context_routing_required" || runtime.requests[0].KnowledgeSnapshotID != "" {
		t.Fatalf("out-of-scope request = %#v", runtime.requests[0])
	}
	if assistant.Intent != "out_of_scope" || assistant.KnowledgeUsed || assistant.KnowledgeSnapshotID != "" {
		t.Fatalf("out-of-scope response invented knowledge provenance: %#v", assistant)
	}
}

func TestControlledGreetingKeepsLegacyMemoryDevelopmentPathUsable(t *testing.T) {
	store := repository.NewMemoryStore()
	runtime := &chatRuntimeStub{result: &domain.ChatRuntimeResponse{
		AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "你好！请问想查询什么？"},
		Citations:        []domain.RuntimeCitation{}, FinishReason: "stop", CompletedAt: "2026-07-16T00:00:00Z",
		Intent: "social_greeting", RetrievalPerformed: false, GenerationPerformed: false, KnowledgeUsed: false,
	}}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("Memory greeting")

	_, assistant, ok, err := svc.CreateMessage(conversation.ConversationID, "你好", false)
	if !ok || err != nil || assistant.Intent != "social_greeting" || assistant.KnowledgeUsed {
		t.Fatalf("memory controlled greeting assistant=%#v ok=%v err=%v", assistant, ok, err)
	}
}

func TestCreateMessageRejectsInvalidCitationAnchorsWithoutWritingExchange(t *testing.T) {
	store := repository.NewMemoryStore()
	runtime := &chatRuntimeStub{result: &domain.ChatRuntimeResponse{
		AssistantMessage:    domain.RuntimeAssistantMessage{Role: "assistant", Content: "短回答。"},
		Citations:           []domain.RuntimeCitation{{Rank: 1, CitationLabel: "员工手册"}},
		CitationAnchors:     []domain.CitationAnchor{{StartRune: 0, EndRune: 99, CitationRanks: []int{1}}},
		FinishReason:        "stop",
		CompletedAt:         "2026-07-15T00:00:00Z",
		Intent:              "knowledge_query",
		RetrievalPerformed:  true,
		GenerationPerformed: true,
		KnowledgeUsed:       true,
	}}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("Invalid anchor")

	_, _, ok, err := svc.CreateMessage(conversation.ConversationID, "提交期限？", false)
	if !ok || !errors.Is(err, ErrRuntimeCitationAnchorViolation) {
		t.Fatalf("create message ok=%v err=%v", ok, err)
	}
	_, messages, ok := svc.GetConversation(conversation.ConversationID)
	if !ok || len(messages) != 0 {
		t.Fatalf("invalid anchor must not write messages: %#v", messages)
	}
}

func (s *chatRuntimeStub) StreamChat(_ context.Context, request domain.ChatRuntimeRequest, emit func(domain.ChatRuntimeStreamEvent) error) error {
	s.requests = append(s.requests, request)
	if s.err != nil {
		return s.err
	}
	if err := emit(domain.ChatRuntimeStreamEvent{
		Event:     "message.delta",
		RequestID: request.RequestID,
		MessageID: request.MessageID,
		Delta:     "runtime ",
	}); err != nil {
		return err
	}
	response := withStubRoutingEvidence(request, domain.ChatRuntimeResponse{
		RequestID:      request.RequestID,
		ConversationID: request.ConversationID,
		MessageID:      request.MessageID,
		AssistantMessage: domain.RuntimeAssistantMessage{
			Role:    "assistant",
			Content: "runtime stream answer",
		},
		Citations:           []domain.RuntimeCitation{},
		FinishReason:        "stop",
		CompletedAt:         "2026-07-02T00:00:00Z",
		Intent:              "knowledge_query",
		RetrievalPerformed:  true,
		GenerationPerformed: true,
	})
	return emit(domain.ChatRuntimeStreamEvent{
		Event:     "message.completed",
		RequestID: request.RequestID,
		MessageID: request.MessageID,
		Response:  &response,
	})
}

func TestCreateMessageRuntimeRequestUsesMessagesAsHistoryOnly(t *testing.T) {
	store := repository.NewMemoryStore()
	runtime := &chatRuntimeStub{}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("History mapping")

	if _, _, ok, err := svc.CreateMessage(conversation.ConversationID, "first question", false); !ok || err != nil {
		t.Fatalf("create first message ok=%v err=%v", ok, err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("expected 1 runtime request, got %d", len(runtime.requests))
	}
	if len(runtime.requests[0].Messages) != 0 {
		t.Fatalf("expected first request history to be empty, got %#v", runtime.requests[0].Messages)
	}
	if runtime.requests[0].UserQuery != "first question" {
		t.Fatalf("expected user_query to carry first question, got %q", runtime.requests[0].UserQuery)
	}

	if _, _, ok, err := svc.CreateMessage(conversation.ConversationID, "second question", false); !ok || err != nil {
		t.Fatalf("create second message ok=%v err=%v", ok, err)
	}
	if len(runtime.requests) != 2 {
		t.Fatalf("expected 2 runtime requests, got %d", len(runtime.requests))
	}
	secondRequest := runtime.requests[1]
	if secondRequest.UserQuery != "second question" {
		t.Fatalf("expected user_query to carry second question, got %q", secondRequest.UserQuery)
	}
	if len(secondRequest.Messages) != 2 {
		t.Fatalf("expected second request to include prior user/assistant history, got %#v", secondRequest.Messages)
	}
	for _, message := range secondRequest.Messages {
		if message.MessageID == "" {
			t.Fatal("bounded history message must retain immutable message_id")
		}
		if message.Content == "second question" {
			t.Fatal("current user query must not be duplicated into runtime history messages")
		}
	}
}

func TestContextRoutingLeavesConfirmationInterpretationToRuntime(t *testing.T) {
	store := repository.NewMemoryStore()
	runtime := &chatRuntimeStub{result: &domain.ChatRuntimeResponse{
		AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "确认"},
		Citations:        []domain.RuntimeCitation{}, FinishReason: "stop", CompletedAt: "2026-07-02T00:00:00Z",
		Intent: "social_greeting", RetrievalPerformed: false, GenerationPerformed: false, KnowledgeUsed: false,
	}}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("Confirmation")
	if _, _, ok, err := svc.CreateMessage(conversation.ConversationID, "你好", false); !ok || err != nil {
		t.Fatalf("create greeting ok=%v err=%v", ok, err)
	}
	runtime.result.Intent = "capability_help"
	runtime.result.ContextResolution = &domain.RuntimeContextResolution{
		Intent:            "capability_help",
		Route:             "controlled_reply",
		Kind:              "standalone",
		ResolverProvider:  "test_router",
		ResolverModelID:   "test-router-v1",
		BasisMessageIDs:   []string{},
		ResolverPerformed: true,
		Confidence:        0.95,
	}
	if _, _, ok, err := svc.CreateMessage(conversation.ConversationID, "真的吗", false); !ok || err != nil {
		t.Fatalf("create confirmation ok=%v err=%v", ok, err)
	}
	if got := runtime.requests[len(runtime.requests)-1].Intent; got != "context_routing_required" {
		t.Fatalf("router request intent = %q", got)
	}
}

func TestValidateRuntimeContextResolutionRejectsUntrustedFollowUpEvidence(t *testing.T) {
	request := domain.ChatRuntimeRequest{
		Intent:   "context_routing_required",
		Messages: []domain.RuntimeConversationMessage{{MessageID: "msg_user_1", Role: "user", Content: "差旅报销提交期限是什么？"}},
	}
	result := domain.ChatRuntimeResponse{Intent: "knowledge_query", ContextResolution: &domain.RuntimeContextResolution{
		Intent:            "knowledge_query",
		Route:             "rag",
		Kind:              "follow_up",
		ResolvedQuery:     "差旅报销超过期限怎么办？",
		BasisMessageIDs:   []string{"msg_not_in_history"},
		ResolverProvider:  "ollama_native",
		ResolverModelID:   "qwen3:14b",
		ResolverPerformed: true,
		Confidence:        0.95,
	}}
	if err := validateRuntimeContextResolution(request, result); !errors.Is(err, ErrRuntimeExecutionFactViolation) {
		t.Fatalf("out-of-history basis error = %v", err)
	}

	result.ContextResolution.BasisMessageIDs = []string{"msg_user_1"}
	if err := validateRuntimeContextResolution(request, result); err != nil {
		t.Fatalf("valid follow-up context resolution error = %v", err)
	}

	result.ContextResolution.ResolverProvider = ""
	if err := validateRuntimeContextResolution(request, result); !errors.Is(err, ErrRuntimeExecutionFactViolation) {
		t.Fatalf("missing resolver provider error = %v", err)
	}
}

func TestConversationRouterPolicyEvidenceIsCodeOwned(t *testing.T) {
	result := domain.ChatRuntimeResponse{ContextResolution: &domain.RuntimeContextResolution{
		Intent:              "capability_help",
		Route:               "controlled_reply",
		Kind:                "standalone",
		RouterPolicyVersion: "runtime-supplied-policy",
		ResolverProvider:    "ollama_native",
		ResolverModelID:     "qwen3:14b",
		ResolverPerformed:   true,
		Confidence:          0.95,
	}}

	attachConversationRouterPolicyEvidence(&result)

	if got := result.ContextResolution.RouterPolicyVersion; got != conversationRouterPolicyVersion {
		t.Fatalf("router policy version = %q, want %q", got, conversationRouterPolicyVersion)
	}

	legacy := domain.ChatRuntimeResponse{ContextResolution: &domain.RuntimeContextResolution{Kind: "standalone"}}
	attachConversationRouterPolicyEvidence(&legacy)
	if got := legacy.ContextResolution.RouterPolicyVersion; got != "" {
		t.Fatalf("legacy unperformed resolution must not be stamped, got %q", got)
	}
}

func TestCompleteRuntimeStreamMessageUsesResolvedIntentForControlledRouteWithAttachedScope(t *testing.T) {
	store := repository.NewMemoryStore()
	svc := New(store, &chatRuntimeStub{}, "", true)
	conversation := svc.CreateConversation("Stream controlled with scope")
	request := domain.ChatRuntimeRequest{
		RequestID:           "req_stream_controlled",
		ConversationID:      conversation.ConversationID,
		MessageID:           "msg_stream_controlled",
		Intent:              "context_routing_required",
		KnowledgeSnapshotID: "snap_active",
		KnowledgeScope:      &domain.RuntimeKnowledgeScope{SnapshotID: "snap_active", ManifestHash: "sha256:active", RevisionIDs: []string{"rev_active"}, ResolvedAt: "2026-07-29T00:00:00Z"},
	}
	result := domain.ChatRuntimeResponse{
		RequestID:      request.RequestID,
		ConversationID: request.ConversationID,
		MessageID:      request.MessageID,
		AssistantMessage: domain.RuntimeAssistantMessage{
			Role:    "assistant",
			Content: "我只依据已发布知识库回答制度、流程和业务问题。",
		},
		Citations:           []domain.RuntimeCitation{},
		FinishReason:        "stop",
		CompletedAt:         "2026-07-29T00:00:00Z",
		Intent:              "capability_help",
		AnswerDisposition:   "controlled_response",
		RetrievalPerformed:  false,
		GenerationPerformed: false,
		KnowledgeUsed:       false,
		Execution: &domain.RuntimeExecutionObservation{
			Policy:       "performed",
			Retrieval:    domain.RuntimeRetrievalObservation{Status: "not_run"},
			Generation:   domain.RuntimeGenerationObservation{Status: "not_run"},
			FallbackUsed: false,
		},
		ContextResolution: &domain.RuntimeContextResolution{
			Intent:            "capability_help",
			Route:             "controlled_reply",
			Kind:              "standalone",
			ResolverProvider:  "test_router",
			ResolverModelID:   "test-router-v1",
			ResolverPerformed: true,
			Confidence:        0.95,
		},
	}

	userMessage, assistantMessage, ok, err := svc.CompleteRuntimeStreamMessage(request, "msg_user_stream_controlled", "真的吗？", result)
	if !ok || err != nil {
		t.Fatalf("complete controlled stream ok=%v err=%v", ok, err)
	}
	if userMessage.Intent != "capability_help" || assistantMessage.Intent != "capability_help" || assistantMessage.KnowledgeUsed || assistantMessage.KnowledgeSnapshotID != "" {
		t.Fatalf("controlled stream persisted with knowledge facts: user=%#v assistant=%#v", userMessage, assistantMessage)
	}
}

func TestCreateMessageRuntimeFailureDoesNotAppendFallbackExchange(t *testing.T) {
	store := repository.NewMemoryStore()
	runtime := &chatRuntimeStub{err: errors.New("dial failed")}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("Runtime failure")

	_, _, ok, err := svc.CreateMessage(conversation.ConversationID, "will runtime fail?", false)
	if !ok {
		t.Fatal("expected conversation to exist")
	}
	if !errors.Is(err, ErrRuntimeChatUnavailable) {
		t.Fatalf("expected ErrRuntimeChatUnavailable, got %v", err)
	}

	_, messages, ok := svc.GetConversation(conversation.ConversationID)
	if !ok {
		t.Fatal("expected conversation detail after runtime failure")
	}
	if len(messages) != 0 {
		t.Fatalf("runtime failure should not append fallback messages, got %#v", messages)
	}
}

func TestCreateMessageMapsRuntimeKnowledgeSnapshotUnavailableWithoutFallback(t *testing.T) {
	store := repository.NewMemoryStore()
	runtime := &chatRuntimeStub{err: knowledgeSnapshotRuntimeFailure{}}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("Missing snapshot")

	_, _, ok, err := svc.CreateMessage(conversation.ConversationID, "What does the policy say?", false)
	if !ok {
		t.Fatal("expected conversation to exist")
	}
	if !errors.Is(err, ErrKnowledgeSnapshotUnavailable) {
		t.Fatalf("expected ErrKnowledgeSnapshotUnavailable, got %v", err)
	}
	if errors.Is(err, ErrRuntimeChatUnavailable) {
		t.Fatalf("snapshot failure must remain distinct from ErrRuntimeChatUnavailable: %v", err)
	}
	_, messages, ok := svc.GetConversation(conversation.ConversationID)
	if !ok {
		t.Fatal("expected conversation detail after runtime failure")
	}
	if len(messages) != 0 {
		t.Fatalf("snapshot failure must not append a fallback exchange, got %#v", messages)
	}
}

func TestCreateMessageUsesConversationConfigOverridesAfterActiveConfigChanges(t *testing.T) {
	store := repository.NewMemoryStore()
	snapshot := store.CreateConfig("snapshot-model:latest", "prompt_v2", "concise", "", 0.35, 1, 768)
	runtime := &chatRuntimeStub{}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("Snapshot overrides")
	active, err := svc.UpdateConfig("new-active-model:latest", "prompt_v3", "concise", "", 0.8, 1, 2048)
	if err != nil {
		t.Fatalf("update active config: %v", err)
	}

	if conversation.ConfigVersion != snapshot.ConfigVersion {
		t.Fatalf("conversation config_version = %q, want %q", conversation.ConfigVersion, snapshot.ConfigVersion)
	}
	if active.ConfigVersion == conversation.ConfigVersion {
		t.Fatal("active config must change after conversation creation")
	}
	if _, _, ok, err := svc.CreateMessage(conversation.ConversationID, "Use my snapshot.", false); !ok || err != nil {
		t.Fatalf("create message ok=%v err=%v", ok, err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("expected 1 runtime request, got %d", len(runtime.requests))
	}
	assertRuntimeRequestUsesConfig(t, runtime.requests[0], conversation, snapshot, store.ActiveRetrievalConfig())
}

func TestStreamChatUsesConversationConfigOverridesAfterActiveConfigChanges(t *testing.T) {
	store := repository.NewMemoryStore()
	snapshot := store.CreateConfig("stream-snapshot-model:latest", "prompt_v2", "concise", "", 0.55, 1, 896)
	runtime := &chatRuntimeStub{}
	svc := New(store, runtime, "", true)
	conversation := svc.CreateConversation("Stream snapshot overrides")
	active, err := svc.UpdateConfig("new-stream-active-model:latest", "prompt_v3", "concise", "", 0.9, 1, 4096)
	if err != nil {
		t.Fatalf("update active config: %v", err)
	}

	if active.ConfigVersion == conversation.ConfigVersion {
		t.Fatal("active config must change after conversation creation")
	}
	request, _, _, ok, err := svc.PrepareRuntimeStreamMessage(conversation.ConversationID, "Stream with my snapshot.")
	if !ok || err != nil {
		t.Fatalf("prepare runtime stream ok=%v err=%v", ok, err)
	}
	if err := svc.StreamRuntimeChat(context.Background(), request, func(domain.ChatRuntimeStreamEvent) error { return nil }); err != nil {
		t.Fatalf("stream runtime chat: %v", err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("expected 1 runtime stream request, got %d", len(runtime.requests))
	}
	if !runtime.requests[0].Stream {
		t.Fatal("expected runtime request to enable streaming")
	}
	assertRuntimeRequestUsesConfig(t, runtime.requests[0], conversation, snapshot, store.ActiveRetrievalConfig())
}

func TestCreateMessageMissingConversationConfigDoesNotCallRuntimeOrAppendExchange(t *testing.T) {
	store := repository.NewMemoryStore()
	conversation := store.CreateConversation("Missing config")
	runtime := &chatRuntimeStub{}
	svc := New(missingConfigStore{Store: store}, runtime, "", true)

	_, _, ok, err := svc.CreateMessage(conversation.ConversationID, "Do not dispatch this.", false)
	if !ok {
		t.Fatal("expected conversation to exist")
	}
	if !errors.Is(err, ErrRuntimeChatUnavailable) {
		t.Fatalf("expected ErrRuntimeChatUnavailable, got %v", err)
	}
	if len(runtime.requests) != 0 {
		t.Fatalf("missing config must not call runtime, got %#v", runtime.requests)
	}
	_, messages, ok := store.GetConversation(conversation.ConversationID)
	if !ok {
		t.Fatal("expected conversation detail")
	}
	if len(messages) != 0 {
		t.Fatalf("missing config must not append an exchange, got %#v", messages)
	}
}

func TestPrepareRuntimeStreamMessageMissingConversationConfigDoesNotDispatch(t *testing.T) {
	store := repository.NewMemoryStore()
	conversation := store.CreateConversation("Missing stream config")
	runtime := &chatRuntimeStub{}
	svc := New(missingConfigStore{Store: store}, runtime, "", true)

	request, userMessage, assistantMessage, ok, err := svc.PrepareRuntimeStreamMessage(conversation.ConversationID, "Do not stream this.")
	if !ok {
		t.Fatal("expected conversation to exist")
	}
	if !errors.Is(err, ErrRuntimeChatUnavailable) {
		t.Fatalf("expected ErrRuntimeChatUnavailable, got %v", err)
	}
	if request.RequestID != "" || request.ConversationID != "" || request.MessageID != "" || userMessage.MessageID != "" || assistantMessage.MessageID != "" {
		t.Fatalf("missing config must not prepare a stream exchange: request=%#v user=%#v assistant=%#v", request, userMessage, assistantMessage)
	}
	if len(runtime.requests) != 0 {
		t.Fatalf("missing config must not call runtime, got %#v", runtime.requests)
	}
	_, messages, ok := store.GetConversation(conversation.ConversationID)
	if !ok {
		t.Fatal("expected conversation detail")
	}
	if len(messages) != 0 {
		t.Fatalf("missing config must not append an exchange, got %#v", messages)
	}
}

func assertRuntimeRequestUsesConfig(t *testing.T, request domain.ChatRuntimeRequest, conversation domain.Conversation, config domain.AgentConfig, retrieval domain.RetrievalConfig) {
	t.Helper()
	if request.ConfigVersion != conversation.ConfigVersion {
		t.Fatalf("request config_version = %q, want conversation config_version %q", request.ConfigVersion, conversation.ConfigVersion)
	}
	if request.ModelID != conversation.ModelID {
		t.Fatalf("request model_id = %q, want conversation model_id %q", request.ModelID, conversation.ModelID)
	}
	if request.ResponseOverrides == nil {
		t.Fatal("expected response_overrides")
	}
	if request.ResponseOverrides.Temperature == nil || *request.ResponseOverrides.Temperature != config.Temperature {
		t.Fatalf("request temperature = %#v, want %v", request.ResponseOverrides.Temperature, config.Temperature)
	}
	if request.ResponseOverrides.MaxOutputTokens == nil || *request.ResponseOverrides.MaxOutputTokens != config.MaxOutputTokens {
		t.Fatalf("request max_output_tokens = %#v, want %d", request.ResponseOverrides.MaxOutputTokens, config.MaxOutputTokens)
	}
	if request.ResponseOverrides.EmbeddingModel != retrieval.EmbeddingModel {
		t.Fatalf("request embedding_model = %q, want %q", request.ResponseOverrides.EmbeddingModel, retrieval.EmbeddingModel)
	}
	if request.ResponseOverrides.RetrievalStrategy != retrieval.RetrievalStrategy {
		t.Fatalf("request retrieval_strategy = %q, want %q", request.ResponseOverrides.RetrievalStrategy, retrieval.RetrievalStrategy)
	}
	if request.ResponseOverrides.EvidenceMinScore == nil || *request.ResponseOverrides.EvidenceMinScore != retrieval.EvidenceMinScore {
		t.Fatalf("request evidence_min_score = %#v, want %v", request.ResponseOverrides.EvidenceMinScore, retrieval.EvidenceMinScore)
	}
}
