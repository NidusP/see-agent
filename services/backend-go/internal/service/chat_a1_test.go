package service

import (
	"context"
	"errors"
	"testing"

	"see-agent/services/backend-go/internal/domain"
	"see-agent/services/backend-go/internal/repository"
)

func TestValidateA1RuntimeFactsAcceptsGroundedAndPostRetrievalClarification(t *testing.T) {
	request := a1RuntimeRequest()
	grounded := a1GroundedResponse(request)
	if err := validateRuntimeExecutionFacts(request, grounded); err != nil {
		t.Fatalf("grounded A1 response error = %v", err)
	}

	clarification := grounded
	clarification.AssistantMessage.Content = "你要查询哪个差旅制度版本？"
	clarification.Citations = nil
	clarification.CitationAnchors = nil
	clarification.UsedCitationRanks = nil
	clarification.AnswerDisposition = "clarification_required"
	clarification.AnswerGenerationPerformed = boolPointer(false)
	clarification.RetrievalPerformed = true
	clarification.GenerationPerformed = false
	clarification.KnowledgeUsed = false
	clarification.Clarification = &domain.RuntimeClarification{MissingDimensions: []string{"policy_version"}, Question: "你要查询哪个差旅制度版本？"}
	clarification.Coverage = nil
	clarification.Execution.Generation = domain.RuntimeGenerationObservation{Status: "not_run"}
	if err := validateRuntimeExecutionFacts(request, clarification); err != nil {
		t.Fatalf("post-retrieval clarification error = %v", err)
	}

	preRetrieval := clarification
	preRetrieval.RetrievalPerformed = false
	preRetrieval.RetrievalCandidates = nil
	preRetrieval.Execution.Retrieval = domain.RuntimeRetrievalObservation{Status: "not_run"}
	policyCalls := 1
	preRetrieval.Execution.PolicyCallCount = &policyCalls
	if err := validateRuntimeExecutionFacts(request, preRetrieval); err != nil {
		t.Fatalf("pre-retrieval clarification error = %v", err)
	}
}

func TestValidateA1RuntimeFactsRejectsUnselectedCitationAndPendingMismatch(t *testing.T) {
	request := a1RuntimeRequest()
	response := a1GroundedResponse(request)
	response.Citations[0].Rank = 2
	if err := validateRuntimeExecutionFacts(request, response); !errors.Is(err, ErrRuntimeExecutionFactViolation) {
		t.Fatalf("unselected citation error = %v", err)
	}

	pending := domain.RuntimePendingClarification{
		ClarificationID: "clrf_001", StateVersion: 2, OriginalUserMessageID: "msg_prior", OriginalQuery: "总结三条差旅规则",
		MissingDimensions: []string{"policy_version"}, Question: "哪个版本？", TurnCount: 1,
	}
	request.PendingClarification = &pending
	response = a1GroundedResponse(request)
	response.Resolution = &domain.RuntimeClarificationResolution{
		PendingClarificationID: pending.ClarificationID, PendingStateVersion: pending.StateVersion, Action: "resolved", ResolvedQuery: "差旅报销制度中三条规则",
	}
	response.ResolvedQuery = response.Resolution.ResolvedQuery
	if err := validateRuntimeExecutionFacts(request, response); err != nil {
		t.Fatalf("resolved pending response error = %v", err)
	}
	response.Resolution.PendingStateVersion++
	if err := validateRuntimeExecutionFacts(request, response); !errors.Is(err, ErrRuntimeExecutionFactViolation) {
		t.Fatalf("stale pending resolution error = %v", err)
	}

	response = a1GroundedResponse(request)
	response.Resolution = &domain.RuntimeClarificationResolution{
		PendingClarificationID: pending.ClarificationID, PendingStateVersion: pending.StateVersion, Action: "needs_more",
	}
	if err := validateRuntimeExecutionFacts(request, response); !errors.Is(err, ErrRuntimeExecutionFactViolation) {
		t.Fatalf("needs-more final answer error = %v", err)
	}
}

func TestValidateA1RuntimeFactsAllowsControlledReplyWithPendingClarification(t *testing.T) {
	request := a1RuntimeRequest()
	request.Intent = "capability_help"
	pending := domain.RuntimePendingClarification{ClarificationID: "clrf_001", StateVersion: 2}
	request.PendingClarification = &pending
	response := a1ControlledResponse(request)
	if err := validateRuntimeExecutionFacts(request, response); err != nil {
		t.Fatalf("controlled reply with pending clarification error = %v", err)
	}

	response.Resolution = &domain.RuntimeClarificationResolution{PendingClarificationID: pending.ClarificationID, PendingStateVersion: pending.StateVersion, Action: "abandoned"}
	if err := validateRuntimeExecutionFacts(request, response); !errors.Is(err, ErrRuntimeExecutionFactViolation) {
		t.Fatalf("controlled reply must not resolve pending clarification error = %v", err)
	}
}

func TestCompleteRuntimeStreamMessageKeepsPendingClarificationForControlledReply(t *testing.T) {
	store := &a1ControlledStore{Store: repository.NewMemoryStore()}
	svc := New(store, nil, "", true)
	conversation := store.CreateConversation("A1 controlled reply")
	request := a1RuntimeRequest()
	request.ConversationID = conversation.ConversationID
	request.Intent = "capability_help"
	request.PendingClarification = &domain.RuntimePendingClarification{ClarificationID: "clrf_001", StateVersion: 2}
	response := a1ControlledResponse(request)

	_, assistant, ok, err := svc.CompleteRuntimeStreamMessage(request, "msg_user_a1", "你能做什么？", response)
	if err != nil || !ok {
		t.Fatalf("complete controlled stream ok=%v err=%v", ok, err)
	}
	if store.controlledAppends != 1 || store.lastResult == nil || store.lastResult.Resolution != nil || store.lastResult.ResolvedQuery != "" {
		t.Fatalf("controlled persistence must not receive a clarification resolution: appends=%d result=%#v", store.controlledAppends, store.lastResult)
	}
	if assistant.Intent != "capability_help" || assistant.KnowledgeUsed {
		t.Fatalf("controlled assistant projection = %#v", assistant)
	}
}

func TestValidateA1RuntimeFactsRejectsInvalidPolicyEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*domain.ChatRuntimeResponse)
	}{
		{
			name: "missing provider",
			mutate: func(response *domain.ChatRuntimeResponse) {
				response.Execution.PolicyProvider = ""
			},
		},
		{
			name: "missing model id",
			mutate: func(response *domain.ChatRuntimeResponse) {
				response.Execution.PolicyModelID = ""
			},
		},
		{
			name: "missing call count",
			mutate: func(response *domain.ChatRuntimeResponse) {
				response.Execution.PolicyCallCount = nil
			},
		},
		{
			name: "nonpositive call count",
			mutate: func(response *domain.ChatRuntimeResponse) {
				calls := 0
				response.Execution.PolicyCallCount = &calls
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := a1GroundedResponse(a1RuntimeRequest())
			test.mutate(&response)
			if err := validateRuntimeExecutionFacts(a1RuntimeRequest(), response); !errors.Is(err, ErrRuntimeExecutionFactViolation) {
				t.Fatalf("invalid policy evidence error = %v", err)
			}
		})
	}
}

func TestValidateA1RuntimeFactsRejectsProviderEvidenceWhenPolicyIsNotRun(t *testing.T) {
	request := a1RuntimeRequest()
	request.Intent = "capability_help"
	response := a1ControlledResponse(request)
	response.Execution.PolicyProvider = "openai_compatible"
	if err := validateRuntimeExecutionFacts(request, response); !errors.Is(err, ErrRuntimeExecutionFactViolation) {
		t.Fatalf("unperformed policy provider evidence error = %v", err)
	}
}

func a1RuntimeRequest() domain.ChatRuntimeRequest {
	return domain.ChatRuntimeRequest{
		RequestID: "req_a1", ConversationID: "conv_a1", MessageID: "msg_a1", ModelID: "qwen-test",
		Intent: "knowledge_query", AnswerPolicyVersion: domain.AnswerPolicyV1,
		KnowledgeSnapshotID: "snap_a1",
		KnowledgeScope:      &domain.RuntimeKnowledgeScope{SnapshotID: "snap_a1", ManifestHash: "sha256:a1", RevisionIDs: []string{"rev_a1"}, ResolvedAt: "2026-08-02T00:00:00Z"},
	}
}

func a1GroundedResponse(request domain.ChatRuntimeRequest) domain.ChatRuntimeResponse {
	policy := true
	candidateCount := 2
	policyCallCount := 2
	candidate := domain.RuntimeRetrievalCandidate{
		ChunkID: "chunk_a1", FileID: "file_a1", DocumentID: "doc_a1", RevisionID: "rev_a1", ChunkContentSHA256: "sha256:chunk-a1",
		KnowledgeSnapshotID: "snap_a1", Rank: 1, Score: .98, SourceLocator: domain.SourceLocator{SourceFormat: "md", BlockLabel: "rule"},
	}
	return domain.ChatRuntimeResponse{
		RequestID: request.RequestID, ConversationID: request.ConversationID, MessageID: request.MessageID,
		AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "第一条规则。"},
		Citations: []domain.RuntimeCitation{{
			ChunkID: candidate.ChunkID, FileID: candidate.FileID, DocumentID: candidate.DocumentID, RevisionID: candidate.RevisionID,
			ChunkContentSHA256: candidate.ChunkContentSHA256, KnowledgeSnapshotID: candidate.KnowledgeSnapshotID,
			Filename: "travel.md", Rank: candidate.Rank, Score: candidate.Score, CitationLabel: "travel", SourceLocator: candidate.SourceLocator, Snippet: "第一条规则。",
		}},
		CitationAnchors: []domain.CitationAnchor{{StartRune: 0, EndRune: 6, CitationRanks: []int{1}}},
		Refusal:         false, FinishReason: "stop", CompletedAt: "2026-08-02T00:00:00Z", KnowledgeSnapshotID: "snap_a1", KnowledgeSnapshotManifestHash: "sha256:a1", FreshnessStatus: "current",
		Intent: "knowledge_query", AnswerDisposition: "grounded_answer", AnswerPolicyVersion: request.AnswerPolicyVersion,
		PolicyEvaluationPerformed: &policy, AnswerGenerationPerformed: &policy,
		RetrievalCandidates: []domain.RuntimeRetrievalCandidate{candidate, {
			ChunkID: "chunk_a1_extra", FileID: "file_a1", DocumentID: "doc_a1", RevisionID: "rev_a1", ChunkContentSHA256: "sha256:chunk-a1-extra",
			KnowledgeSnapshotID: "snap_a1", Rank: 2, Score: .7, SourceLocator: domain.SourceLocator{SourceFormat: "md", BlockLabel: "other"},
		}},
		Execution: &domain.RuntimeExecutionObservation{
			Policy:          "performed",
			PolicyProvider:  "openai_compatible",
			PolicyModelID:   request.ModelID,
			PolicyCallCount: &policyCallCount,
			Retrieval:       domain.RuntimeRetrievalObservation{Status: "performed", Strategy: "native_dense_v1", EmbeddingModel: "qwen-embed", Collection: "a1", CandidateCount: &candidateCount},
			Generation:      domain.RuntimeGenerationObservation{Status: "performed", Provider: "openai_compatible", ModelID: request.ModelID},
		},
		UsedCitationRanks: []int{1}, RetrievalPerformed: true, GenerationPerformed: true, KnowledgeUsed: true,
	}
}

func a1ControlledResponse(request domain.ChatRuntimeRequest) domain.ChatRuntimeResponse {
	notPerformed := false
	return domain.ChatRuntimeResponse{
		RequestID: request.RequestID, ConversationID: request.ConversationID, MessageID: request.MessageID,
		AssistantMessage:          domain.RuntimeAssistantMessage{Role: "assistant", Content: "我可以帮助查询已接入的知识。"},
		FinishReason:              "stop",
		Intent:                    request.Intent,
		AnswerDisposition:         "controlled_response",
		PolicyEvaluationPerformed: &notPerformed,
		AnswerGenerationPerformed: &notPerformed,
		Execution: &domain.RuntimeExecutionObservation{
			Policy: "not_run", Retrieval: domain.RuntimeRetrievalObservation{Status: "not_run"}, Generation: domain.RuntimeGenerationObservation{Status: "not_run"},
		},
	}
}

type a1ControlledStore struct {
	repository.Store
	controlledAppends int
	lastResult        *domain.ChatRuntimeResponse
}

func (s *a1ControlledStore) AppendControlledRuntimeExchange(_ context.Context, conversationID, userMessageID, content string, result domain.ChatRuntimeResponse) (domain.Message, domain.Message, error) {
	s.controlledAppends++
	copy := result
	s.lastResult = &copy
	user, assistant, _, ok := s.Store.AppendRuntimeExchangeWithIDs(conversationID, userMessageID, content, result)
	if !ok {
		return domain.Message{}, domain.Message{}, errors.New("controlled append failed")
	}
	user.Intent, user.KnowledgeUsed = result.Intent, false
	assistant.Intent, assistant.KnowledgeUsed = result.Intent, false
	assistant.KnowledgeSnapshotID, assistant.KnowledgeSnapshotManifestHash, assistant.FreshnessStatus = "", "", ""
	return user, assistant, nil
}

func boolPointer(value bool) *bool {
	return &value
}
