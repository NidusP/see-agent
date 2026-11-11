package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"see-agent/services/backend-go/internal/repository"
	runtimeclient "see-agent/services/backend-go/internal/runtime"
)

// Opt-in fullstack gate. It deliberately uses the same Service path as HTTP
// handlers so an adapter/configuration regression cannot hide behind a direct
// Runtime client smoke test.
func TestLiveFullstackControlledChat(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("LIVE_FULLSTACK_DATABASE_URL"))
	runtimeURL := strings.TrimSpace(os.Getenv("LIVE_RUNTIME_TEST_URL"))
	if databaseURL == "" || runtimeURL == "" {
		t.Skip("set LIVE_FULLSTACK_DATABASE_URL and LIVE_RUNTIME_TEST_URL for the fullstack service gate")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := repository.NewPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open fullstack Postgres store: %v", err)
	}
	defer store.Close()
	client := runtimeclient.NewClient(runtimeURL, 5*time.Second)
	svc := New(store, client, "", true)
	defer svc.Close()
	conversation := svc.CreateConversation("fullstack controlled chat")
	_, assistant, ok, err := svc.CreateMessage(conversation.ConversationID, "你好", false)
	if err != nil || !ok {
		t.Fatalf("create controlled fullstack message ok=%v err=%v", ok, err)
	}
	if assistant.Content == "" || assistant.CitationsCount != 0 || assistant.KnowledgeUsed {
		t.Fatalf("controlled fullstack assistant is invalid: %#v", assistant)
	}
}
