package repository

import (
	"strings"
	"testing"

	"see-agent/services/backend-go/internal/domain"
)

func appendTitleRuntimeExchange(t *testing.T, store *MemoryStore, conversationID, content, messageID string) {
	t.Helper()
	_, _, _, ok := store.AppendRuntimeExchange(conversationID, content, domain.ChatRuntimeResponse{
		MessageID:        messageID,
		AssistantMessage: domain.RuntimeAssistantMessage{Role: "assistant", Content: "answer"},
		FinishReason:     "stop",
		Intent:           "social_greeting",
	})
	if !ok {
		t.Fatal("append runtime exchange")
	}
}

func TestMemoryConversationTitleLifecycle(t *testing.T) {
	store := NewMemoryStore()
	conversation := store.CreateOwnedConversation("user", "")
	if conversation.Title != DefaultConversationTitle || conversation.TitleSource != ConversationTitleSystemDefault {
		t.Fatalf("default conversation = %#v", conversation)
	}

	firstQuestion := "  请说明   差旅报销   的提交期限  "
	appendTitleRuntimeExchange(t, store, conversation.ConversationID, firstQuestion, "msg_title_001")
	updated, _, found := store.GetOwnedConversation("user", conversation.ConversationID)
	if !found || updated.Title != "请说明 差旅报销 的提交期限" || updated.TitleSource != ConversationTitleFirstUserMessage {
		t.Fatalf("first title = %#v found=%v", updated, found)
	}
	appendTitleRuntimeExchange(t, store, conversation.ConversationID, "第二条问题不能覆盖标题", "msg_title_002")
	updated, _, found = store.GetOwnedConversation("user", conversation.ConversationID)
	if !found || updated.Title != "请说明 差旅报销 的提交期限" {
		t.Fatalf("second exchange overwrote title = %#v", updated)
	}

	renamed, found := store.RenameOwnedConversation("user", conversation.ConversationID, "手动标题")
	if !found || renamed.Title != "手动标题" || renamed.TitleSource != ConversationTitleUserEdited {
		t.Fatalf("rename = %#v found=%v", renamed, found)
	}
	appendTitleRuntimeExchange(t, store, conversation.ConversationID, "重试也不能覆盖手动标题", "msg_title_003")
	updated, _, found = store.GetOwnedConversation("user", conversation.ConversationID)
	if !found || updated.Title != "手动标题" || updated.TitleSource != ConversationTitleUserEdited {
		t.Fatalf("manual title was overwritten = %#v", updated)
	}
}

func TestNormalizeConversationTitle(t *testing.T) {
	if got, err := NormalizeConversationTitle("  团队\n  周会  "); err != nil || got != "团队 周会" {
		t.Fatalf("normalized title = %q err=%v", got, err)
	}
	if _, err := NormalizeConversationTitle(" \t\n "); err != ErrInvalidConversationTitle {
		t.Fatalf("blank title err = %v", err)
	}
	if _, err := NormalizeConversationTitle(strings.Repeat("题", 49)); err != ErrInvalidConversationTitle {
		t.Fatalf("long title err = %v", err)
	}
	if _, err := NormalizeConversationTitle(string([]byte{0xff})); err != ErrInvalidConversationTitle {
		t.Fatalf("invalid utf8 title err = %v", err)
	}
	if got := firstUserMessageTitle(strings.Repeat("问", 29)); got != strings.Repeat("问", 27)+"…" {
		t.Fatalf("derived title = %q", got)
	}
}
