package repository

import (
	"context"
	"testing"

	"see-agent/services/backend-go/internal/domain"
)

func TestMemoryChatExecutionAttemptStateMachineIsForwardOnly(t *testing.T) {
	store := NewMemoryStore()
	conversation := store.CreateConversation("attempt audit")
	attempt, err := store.CreateChatExecutionAttempt(context.Background(), domain.ChatExecutionAttempt{
		AttemptID:              "attempt_001",
		RequestID:              "request_001",
		ConversationID:         conversation.ConversationID,
		UserMessageID:          "msg_001",
		AssistantMessageID:     "msg_002",
		State:                  domain.ChatExecutionAttemptAccepted,
		ConfigVersion:          conversation.ConfigVersion,
		RetrievalConfigVersion: conversation.RetrievalConfigVersion,
	})
	if err != nil || attempt.State != domain.ChatExecutionAttemptAccepted || attempt.AcceptedAt == "" {
		t.Fatalf("create attempt = %#v err=%v", attempt, err)
	}
	if _, err := store.TransitionChatExecutionAttempt(context.Background(), attempt.AttemptID, domain.ChatExecutionAttemptSucceeded, "", "", false); err == nil {
		t.Fatal("accepted attempt must not skip running")
	}
	running, err := store.TransitionChatExecutionAttempt(context.Background(), attempt.AttemptID, domain.ChatExecutionAttemptRunning, "", "", false)
	if err != nil || running.StartedAt == "" {
		t.Fatalf("start attempt = %#v err=%v", running, err)
	}
	failed, err := store.TransitionChatExecutionAttempt(context.Background(), attempt.AttemptID, domain.ChatExecutionAttemptFailed, "dependency_unavailable", "runtime_call", true)
	if err != nil || failed.TerminalAt == "" || failed.ErrorCode != "dependency_unavailable" || !failed.Retryable {
		t.Fatalf("fail attempt = %#v err=%v", failed, err)
	}
	if _, err := store.TransitionChatExecutionAttempt(context.Background(), attempt.AttemptID, domain.ChatExecutionAttemptSucceeded, "", "", false); err == nil {
		t.Fatal("terminal attempt must not transition again")
	}
}
