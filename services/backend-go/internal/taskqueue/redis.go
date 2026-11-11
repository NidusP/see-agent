package taskqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

type RedisQueue struct {
	client   *redis.Client
	queueKey string
}

func NewRedisQueue(ctx context.Context, redisURL, queueName string) (*RedisQueue, error) {
	options, err := redis.ParseURL(strings.TrimSpace(redisURL))
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	queueName = strings.TrimSpace(queueName)
	if queueName == "" {
		queueName = DefaultRedisQueueName
	}

	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &RedisQueue{client: client, queueKey: queueName}, nil
}

func (q *RedisQueue) Enqueue(ctx context.Context, job IngestionJob) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if err := q.client.RPush(ctx, q.queueKey, payload).Err(); err != nil {
		return fmt.Errorf("redis enqueue: %w", err)
	}
	return nil
}

func (q *RedisQueue) Dequeue(ctx context.Context) (IngestionJob, error) {
	values, err := q.client.BLPop(ctx, 0, q.queueKey).Result()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return IngestionJob{}, err
		}
		return IngestionJob{}, fmt.Errorf("redis dequeue: %w", err)
	}
	if len(values) != 2 {
		return IngestionJob{}, fmt.Errorf("redis dequeue returned %d values", len(values))
	}
	var job IngestionJob
	if err := json.Unmarshal([]byte(values[1]), &job); err != nil {
		return IngestionJob{}, fmt.Errorf("decode ingestion job: %w", err)
	}
	return job, nil
}

func (q *RedisQueue) Close() error {
	return q.client.Close()
}

func (q *RedisQueue) Ping(ctx context.Context) error {
	return q.client.Ping(ctx).Err()
}

var _ Queue = (*RedisQueue)(nil)
