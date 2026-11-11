package repository

import (
	"errors"
	"strings"
	"unicode/utf8"

	"see-agent/services/backend-go/internal/domain"
)

const (
	ConversationTitleSystemDefault    = "system_default"
	ConversationTitleFirstUserMessage = "first_user_message"
	ConversationTitleUserEdited       = "user_edited"
	ConversationTitleLegacy           = "legacy"
	DefaultConversationTitle          = "新对话"
)

var ErrInvalidConversationTitle = errors.New("invalid conversation title")

func NormalizeConversationTitle(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", ErrInvalidConversationTitle
	}
	normalized := strings.Join(strings.Fields(value), " ")
	runeCount := utf8.RuneCountInString(normalized)
	if runeCount < 1 || runeCount > 48 {
		return "", ErrInvalidConversationTitle
	}
	return normalized, nil
}

func firstUserMessageTitle(content string) string {
	normalized := strings.Join(strings.Fields(content), " ")
	if normalized == "" {
		return ""
	}
	runes := []rune(normalized)
	if len(runes) <= 28 {
		return normalized
	}
	return string(runes[:27]) + "…"
}

func applyAutomaticConversationTitle(conversation *domain.Conversation, content string) {
	if conversation.TitleSource != ConversationTitleSystemDefault {
		return
	}
	if title := firstUserMessageTitle(content); title != "" {
		conversation.Title = title
		conversation.TitleSource = ConversationTitleFirstUserMessage
	}
}
