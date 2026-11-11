package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"see-agent/services/backend-go/internal/domain"
)

func TestRunChatPreservesRuntimeModelFailureCategory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"model_generation_timeout"}}`))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, time.Second).RunChat(context.Background(), domain.ChatRuntimeRequest{})
	if err == nil {
		t.Fatal("expected runtime model failure")
	}
	var classified interface{ RuntimeFailure() (string, string) }
	if !errors.As(err, &classified) {
		t.Fatalf("error does not expose RuntimeFailure: %T %v", err, err)
	}
	code, stage := classified.RuntimeFailure()
	if code != "model_generation_timeout" || stage != "model_provider" {
		t.Fatalf("failure = (%q, %q)", code, stage)
	}
}

func TestRunChatClassifiesBackendDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := NewClient(server.URL, time.Second).RunChat(ctx, domain.ChatRuntimeRequest{})
	if err == nil {
		t.Fatal("expected deadline failure")
	}
	var classified interface{ RuntimeFailure() (string, string) }
	if !errors.As(err, &classified) {
		t.Fatalf("error does not expose RuntimeFailure: %T %v", err, err)
	}
	code, stage := classified.RuntimeFailure()
	if code != "backend_deadline_exceeded" || stage != "backend_runtime_deadline" {
		t.Fatalf("failure = (%q, %q)", code, stage)
	}
}

func TestChatRequestsPreserveKnowledgeSnapshotUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"knowledge_snapshot_unavailable"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	for _, testCase := range []struct {
		name string
		call func() error
	}{
		{
			name: "non stream",
			call: func() error {
				_, err := client.RunChat(context.Background(), domain.ChatRuntimeRequest{})
				return err
			},
		},
		{
			name: "stream",
			call: func() error {
				return client.StreamChat(context.Background(), domain.ChatRuntimeRequest{}, func(domain.ChatRuntimeStreamEvent) error {
					return nil
				})
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.call()
			if err == nil {
				t.Fatal("expected runtime failure")
			}
			var classified interface{ RuntimeFailure() (string, string) }
			if !errors.As(err, &classified) {
				t.Fatalf("error does not expose RuntimeFailure: %T %v", err, err)
			}
			code, stage := classified.RuntimeFailure()
			if code != "knowledge_snapshot_unavailable" || stage != "runtime_http" {
				t.Fatalf("failure = (%q, %q)", code, stage)
			}
		})
	}
}
