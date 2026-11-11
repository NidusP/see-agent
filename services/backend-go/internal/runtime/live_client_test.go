package runtime

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"see-agent/services/backend-go/internal/domain"
)

// This is intentionally opt-in: it verifies the real private Runtime client
// contract during the fullstack gate without turning unit tests into network tests.
func TestLiveRuntimeClientControlledResponse(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("LIVE_RUNTIME_TEST_URL"))
	if baseURL == "" {
		t.Skip("set LIVE_RUNTIME_TEST_URL for the fullstack Runtime client gate")
	}
	client := NewClient(baseURL, 5*time.Second)
	response, err := client.RunChat(context.Background(), domain.ChatRuntimeRequest{
		RequestID: "live_runtime_client_controlled",
		ConversationID: "live_runtime_client_controlled",
		MessageID: "live_runtime_client_controlled",
		UserQuery: "你好",
		ModelID: "llama3.2:latest",
		ConfigVersion: "cfg_004",
		RetrievalConfigVersion: "rcfg_001",
		Intent: "social_greeting",
		Messages: []domain.RuntimeConversationMessage{},
	})
	if err != nil {
		t.Fatalf("run Runtime controlled response: %v", err)
	}
	if response.AnswerDisposition != "controlled_response" || response.Execution == nil || response.Execution.Retrieval.Status != "not_run" || response.Execution.Generation.Status != "not_run" {
		t.Fatalf("controlled Runtime response is not strict: %#v", response)
	}
}
