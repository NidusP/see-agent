package taskqueue

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRedisRevisionStreamDeliveryLifecycle(t *testing.T) {
	redisURL := strings.TrimSpace(os.Getenv("K3_TEST_REDIS_URL"))
	if redisURL == "" {
		t.Skip("set K3_TEST_REDIS_URL to run the Redis Streams K3 smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	streamName := "see_agent:k3:test:" + strings.ReplaceAll(t.Name(), "/", ":") + ":" + time.Now().UTC().Format("20060102150405.000000000")
	producerConsumer, err := NewRedisRevisionStream(ctx, redisURL, streamName, "k3-test-group", "k3-test-consumer-a")
	if err != nil {
		t.Fatalf("new redis revision stream: %v", err)
	}
	if err := producerConsumer.EnsureGroup(ctx); err != nil {
		t.Fatalf("ensure group: %v", err)
	}

	event := RevisionStreamEvent{
		OutboxID:       "delivery_001",
		DeliveryID:     "delivery_001",
		DocumentID:     "doc_001",
		RevisionID:     "rev_001",
		ChecksumSHA256: "sha256:body",
		Attempt:        0,
	}
	firstID, err := producerConsumer.Add(ctx, event)
	if err != nil {
		t.Fatalf("add first event: %v", err)
	}
	secondID, err := producerConsumer.Add(ctx, event)
	if err != nil {
		t.Fatalf("add duplicate event: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("idempotent add ids = %q and %q", firstID, secondID)
	}

	messages, err := producerConsumer.Read(ctx, 1, time.Second)
	if err != nil {
		t.Fatalf("read group: %v", err)
	}
	if len(messages) != 1 || messages[0].StreamMessageID != firstID || messages[0].RevisionID != event.RevisionID {
		t.Fatalf("read messages = %#v", messages)
	}

	// Simulate a worker interruption: consumer A leaves a PEL entry, closes,
	// then a separate consumer B reclaims and completes the delivery.
	if err := producerConsumer.Close(); err != nil {
		t.Fatalf("close interrupted consumer: %v", err)
	}
	recoveryConsumer, err := NewRedisRevisionStream(ctx, redisURL, streamName, "k3-test-group", "k3-test-consumer-b")
	if err != nil {
		t.Fatalf("new recovery consumer: %v", err)
	}
	defer recoveryConsumer.Close()
	if err := recoveryConsumer.EnsureGroup(ctx); err != nil {
		t.Fatalf("ensure recovery group: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	reclaimed, err := recoveryConsumer.Reclaim(ctx, time.Millisecond, 1)
	if err != nil {
		t.Fatalf("reclaim pending: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].StreamMessageID != firstID {
		t.Fatalf("reclaimed messages = %#v", reclaimed)
	}

	dlqID, err := recoveryConsumer.DeadLetter(ctx, event, firstID, "runtime identity mismatch")
	if err != nil {
		t.Fatalf("dead-letter event: %v", err)
	}
	duplicateDLQID, err := recoveryConsumer.DeadLetter(ctx, event, firstID, "runtime identity mismatch")
	if err != nil {
		t.Fatalf("dead-letter duplicate: %v", err)
	}
	if duplicateDLQID != dlqID {
		t.Fatalf("idempotent DLQ ids = %q and %q", dlqID, duplicateDLQID)
	}
	if err := recoveryConsumer.Ack(ctx, firstID); err != nil {
		t.Fatalf("ack: %v", err)
	}
}
