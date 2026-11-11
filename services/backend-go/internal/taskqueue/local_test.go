package taskqueue

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLocalQueueEnqueueDequeue(t *testing.T) {
	queue := NewLocalQueue(1)
	job := IngestionJob{FileID: "file_123", TaskID: "task_456"}

	if err := queue.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	got, err := queue.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if got != job {
		t.Fatalf("unexpected job: %#v", got)
	}
}

func TestLocalQueueCloseUnblocksDequeue(t *testing.T) {
	queue := NewLocalQueue(1)
	done := make(chan error, 1)
	go func() {
		_, err := queue.Dequeue(context.Background())
		done <- err
	}()

	if err := queue.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("expected ErrClosed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dequeue did not unblock after close")
	}
}
