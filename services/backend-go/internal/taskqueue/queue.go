package taskqueue

import (
	"context"
	"errors"
)

const DefaultRedisQueueName = "see_agent:ingestion"

var ErrClosed = errors.New("task queue is closed")

type IngestionJob struct {
	FileID string `json:"file_id"`
	TaskID string `json:"task_id"`
}

type Queue interface {
	Enqueue(ctx context.Context, job IngestionJob) error
	Dequeue(ctx context.Context) (IngestionJob, error)
	Close() error
}

// HealthQueue is intentionally separate from Queue so local smoke queues do
// not masquerade as a Redis-backed full-stack dependency.
type HealthQueue interface {
	Ping(context.Context) error
}
