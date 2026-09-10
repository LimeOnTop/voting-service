package pollcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const keyPrefix = "pollcache:v1:"

// RedisStore is the Redis-backed shared poll cache.
type RedisStore struct {
	client redis.UniversalClient
}

var _ SharedStore = (*RedisStore)(nil)

// NewRedisStore adapts a Redis client to SharedStore.
func NewRedisStore(client redis.UniversalClient) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) Get(ctx context.Context, pollID string) (*Entry, error) {
	raw, err := s.client.Get(ctx, keyPrefix+pollID).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("poll cache get: %w", err)
	}
	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, nil
	}
	return &entry, nil
}

func (s *RedisStore) Set(ctx context.Context, pollID string, entry Entry, ttl time.Duration) error {
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("poll cache marshal: %w", err)
	}
	if err := s.client.Set(ctx, keyPrefix+pollID, raw, ttl).Err(); err != nil {
		return fmt.Errorf("poll cache set: %w", err)
	}
	return nil
}

func (s *RedisStore) Delete(ctx context.Context, pollID string) error {
	if err := s.client.Del(ctx, keyPrefix+pollID).Err(); err != nil {
		return fmt.Errorf("poll cache delete: %w", err)
	}
	return nil
}
