package taskqueue

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	DefaultRedisRevisionStreamName = "see_agent:knowledge:revision"
	DefaultRedisRevisionDLQName    = "see_agent:knowledge:revision:dlq"
	DefaultRedisRevisionGroupName  = "see_agent:revision-indexer"
)

// RevisionStreamEvent is a K3-only event. It deliberately has no overlap with
// the legacy List ingestion job, whose payload only contains file/task ids.
type RevisionStreamEvent struct {
	OutboxID       string
	DeliveryID     string
	DocumentID     string
	RevisionID     string
	ChecksumSHA256 string
	Attempt        int
}

type RevisionStreamMessage struct {
	StreamMessageID string
	RevisionStreamEvent
}

// RevisionStream is deliberately small so the delivery state machine can be
// tested without Redis and the Redis implementation can be smoke-tested alone.
type RevisionStream interface {
	EnsureGroup(ctx context.Context) error
	Add(ctx context.Context, event RevisionStreamEvent) (string, error)
	Read(ctx context.Context, count int64, block time.Duration) ([]RevisionStreamMessage, error)
	Reclaim(ctx context.Context, minIdle time.Duration, count int64) ([]RevisionStreamMessage, error)
	Ack(ctx context.Context, streamMessageIDs ...string) error
	DeadLetter(ctx context.Context, event RevisionStreamEvent, sourceMessageID, reason string) (string, error)
	Close() error
}

type RedisRevisionStream struct {
	client       *redis.Client
	streamName   string
	dlqName      string
	groupName    string
	consumerName string
}

func NewRedisRevisionStream(ctx context.Context, redisURL, streamName, groupName, consumerName string) (*RedisRevisionStream, error) {
	options, err := redis.ParseURL(strings.TrimSpace(redisURL))
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	if streamName = strings.TrimSpace(streamName); streamName == "" {
		streamName = DefaultRedisRevisionStreamName
	}
	if groupName = strings.TrimSpace(groupName); groupName == "" {
		groupName = DefaultRedisRevisionGroupName
	}
	if consumerName = strings.TrimSpace(consumerName); consumerName == "" {
		return nil, errors.New("redis revision stream consumer name is required")
	}

	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &RedisRevisionStream{
		client:       client,
		streamName:   streamName,
		dlqName:      streamName + ":dlq",
		groupName:    groupName,
		consumerName: consumerName,
	}, nil
}

func (s *RedisRevisionStream) EnsureGroup(ctx context.Context) error {
	err := s.client.XGroupCreateMkStream(ctx, s.streamName, s.groupName, "0").Err()
	if err == nil || strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return fmt.Errorf("create revision stream group: %w", err)
}

func (s *RedisRevisionStream) Add(ctx context.Context, event RevisionStreamEvent) (string, error) {
	if err := validateRevisionStreamEvent(event); err != nil {
		return "", err
	}
	messageID, err := s.idempotentXAdd(ctx, s.streamName, s.idempotencyKey(event), revisionStreamValues(event))
	if err != nil {
		return "", fmt.Errorf("add revision stream event: %w", err)
	}
	return messageID, nil
}

func (s *RedisRevisionStream) Read(ctx context.Context, count int64, block time.Duration) ([]RevisionStreamMessage, error) {
	if count < 1 {
		count = 1
	}
	streams, err := s.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    s.groupName,
		Consumer: s.consumerName,
		Streams:  []string{s.streamName, ">"},
		Count:    count,
		Block:    block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read revision stream group: %w", err)
	}
	return decodeStreamMessages(streams)
}

func (s *RedisRevisionStream) Reclaim(ctx context.Context, minIdle time.Duration, count int64) ([]RevisionStreamMessage, error) {
	if minIdle <= 0 {
		return nil, errors.New("revision stream reclaim idle duration must be positive")
	}
	if count < 1 {
		count = 1
	}
	messages, _, err := s.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   s.streamName,
		Group:    s.groupName,
		Consumer: s.consumerName,
		MinIdle:  minIdle,
		Start:    "0-0",
		Count:    count,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reclaim revision stream pending messages: %w", err)
	}
	return decodeRedisMessages(messages)
}

func (s *RedisRevisionStream) Ack(ctx context.Context, streamMessageIDs ...string) error {
	if len(streamMessageIDs) == 0 {
		return nil
	}
	if err := s.client.XAck(ctx, s.streamName, s.groupName, streamMessageIDs...).Err(); err != nil {
		return fmt.Errorf("ack revision stream messages: %w", err)
	}
	return nil
}

func (s *RedisRevisionStream) DeadLetter(ctx context.Context, event RevisionStreamEvent, sourceMessageID, reason string) (string, error) {
	if err := validateRevisionStreamEvent(event); err != nil {
		return "", err
	}
	values := revisionStreamValues(event)
	values["source_stream_message_id"] = strings.TrimSpace(sourceMessageID)
	values["error"] = strings.TrimSpace(reason)
	messageID, err := s.idempotentXAdd(ctx, s.dlqName, s.idempotencyKey(event)+":dlq", values)
	if err != nil {
		return "", fmt.Errorf("add revision stream dead letter: %w", err)
	}
	return messageID, nil
}

func (s *RedisRevisionStream) Close() error {
	return s.client.Close()
}

func (s *RedisRevisionStream) idempotentXAdd(ctx context.Context, stream, key string, values map[string]any) (string, error) {
	args := make([]any, 0, len(values)*2)
	for name, value := range values {
		args = append(args, name, value)
	}
	result, err := s.client.Eval(ctx, `
local existing = redis.call('GET', KEYS[1])
if existing then
  return existing
end
local message_id = redis.call('XADD', KEYS[2], '*', unpack(ARGV))
redis.call('SET', KEYS[1], message_id)
return message_id
`, []string{s.dedupeKey(key), stream}, args...).Result()
	if err != nil {
		return "", err
	}
	messageID, ok := result.(string)
	if !ok || strings.TrimSpace(messageID) == "" {
		return "", fmt.Errorf("unexpected idempotent XADD result %T", result)
	}
	return messageID, nil
}

func (s *RedisRevisionStream) dedupeKey(key string) string {
	return s.streamName + ":idempotency:" + key
}

func (s *RedisRevisionStream) idempotencyKey(event RevisionStreamEvent) string {
	return event.OutboxID + ":attempt:" + strconv.Itoa(event.Attempt)
}

func revisionStreamValues(event RevisionStreamEvent) map[string]any {
	return map[string]any{
		"outbox_id":       event.OutboxID,
		"delivery_id":     event.DeliveryID,
		"document_id":     event.DocumentID,
		"revision_id":     event.RevisionID,
		"checksum_sha256": event.ChecksumSHA256,
		"attempt":         event.Attempt,
	}
}

func validateRevisionStreamEvent(event RevisionStreamEvent) error {
	if strings.TrimSpace(event.OutboxID) == "" || strings.TrimSpace(event.DeliveryID) == "" || strings.TrimSpace(event.DocumentID) == "" || strings.TrimSpace(event.RevisionID) == "" || strings.TrimSpace(event.ChecksumSHA256) == "" {
		return errors.New("revision stream event requires outbox_id, delivery_id, document_id, revision_id and checksum_sha256")
	}
	if event.OutboxID != event.DeliveryID {
		return errors.New("revision stream outbox_id must equal delivery_id")
	}
	if event.Attempt < 0 {
		return errors.New("revision stream event attempt cannot be negative")
	}
	return nil
}

func decodeStreamMessages(streams []redis.XStream) ([]RevisionStreamMessage, error) {
	result := []RevisionStreamMessage{}
	for _, stream := range streams {
		messages, err := decodeRedisMessages(stream.Messages)
		if err != nil {
			return nil, err
		}
		result = append(result, messages...)
	}
	return result, nil
}

func decodeRedisMessages(messages []redis.XMessage) ([]RevisionStreamMessage, error) {
	result := make([]RevisionStreamMessage, 0, len(messages))
	for _, message := range messages {
		event, err := decodeRevisionStreamEvent(message.Values)
		if err != nil {
			return nil, fmt.Errorf("decode revision stream message %s: %w", message.ID, err)
		}
		result = append(result, RevisionStreamMessage{StreamMessageID: message.ID, RevisionStreamEvent: event})
	}
	return result, nil
}

func decodeRevisionStreamEvent(values map[string]any) (RevisionStreamEvent, error) {
	attempt, err := strconv.Atoi(valueString(values["attempt"]))
	if err != nil {
		return RevisionStreamEvent{}, fmt.Errorf("invalid attempt: %w", err)
	}
	event := RevisionStreamEvent{
		OutboxID:       valueString(values["outbox_id"]),
		DeliveryID:     valueString(values["delivery_id"]),
		DocumentID:     valueString(values["document_id"]),
		RevisionID:     valueString(values["revision_id"]),
		ChecksumSHA256: valueString(values["checksum_sha256"]),
		Attempt:        attempt,
	}
	if err := validateRevisionStreamEvent(event); err != nil {
		return RevisionStreamEvent{}, err
	}
	return event, nil
}

func valueString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

var _ RevisionStream = (*RedisRevisionStream)(nil)
