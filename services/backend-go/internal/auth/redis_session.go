package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const DefaultRedisSessionPrefix = "see_agent:session:"

type RedisSessionStore struct {
	client *redis.Client
	prefix string
}

func NewRedisSessionStore(ctx context.Context, redisURL, keyPrefix string) (*RedisSessionStore, error) {
	options, err := redis.ParseURL(strings.TrimSpace(redisURL))
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	keyPrefix = strings.TrimSpace(keyPrefix)
	if keyPrefix == "" {
		keyPrefix = DefaultRedisSessionPrefix
	}

	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &RedisSessionStore{client: client, prefix: keyPrefix}, nil
}

func (s *RedisSessionStore) Save(ctx context.Context, token string, record SessionRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	ttl := time.Until(record.ExpiresAt)
	if ttl <= 0 {
		return fmt.Errorf("session is already expired")
	}
	if err := s.client.Set(ctx, s.key(token), payload, ttl).Err(); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

func (s *RedisSessionStore) Find(ctx context.Context, token string) (SessionRecord, bool, error) {
	payload, err := s.client.Get(ctx, s.key(token)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return SessionRecord{}, false, nil
		}
		return SessionRecord{}, false, fmt.Errorf("find session: %w", err)
	}

	var record SessionRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return SessionRecord{}, false, fmt.Errorf("decode session: %w", err)
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		_ = s.Delete(ctx, token)
		return SessionRecord{}, false, nil
	}
	return record, true, nil
}

func (s *RedisSessionStore) Delete(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	if err := s.client.Del(ctx, s.key(token)).Err(); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *RedisSessionStore) Close() error {
	return s.client.Close()
}

func (s *RedisSessionStore) key(token string) string {
	return s.prefix + strings.TrimSpace(token)
}

var _ SessionStore = (*RedisSessionStore)(nil)
