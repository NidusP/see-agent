package taskqueue

import (
	"context"
	"sync"
)

const defaultLocalQueueSize = 100

type LocalQueue struct {
	mu     sync.RWMutex
	jobs   chan IngestionJob
	closed bool
}

func NewLocalQueue(size int) *LocalQueue {
	if size < 1 {
		size = defaultLocalQueueSize
	}
	return &LocalQueue{jobs: make(chan IngestionJob, size)}
}

func (q *LocalQueue) Enqueue(ctx context.Context, job IngestionJob) error {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		return ErrClosed
	}
	select {
	case q.jobs <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *LocalQueue) Dequeue(ctx context.Context) (IngestionJob, error) {
	select {
	case job, ok := <-q.jobs:
		if !ok {
			return IngestionJob{}, ErrClosed
		}
		return job, nil
	case <-ctx.Done():
		return IngestionJob{}, ctx.Err()
	}
}

func (q *LocalQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	close(q.jobs)
	return nil
}

var _ Queue = (*LocalQueue)(nil)
