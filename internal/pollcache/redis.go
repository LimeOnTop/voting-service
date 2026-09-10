package pollcache

import (
	"context"
	"encoding/json"
	"errors"
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

func (s *RedisStore) Get(ctx context.Context, pollID string) (Entry, bool, error) {
	raw, err := s.client.Get(ctx, keyPrefix+pollID).Bytes()
	if errors.Is(err, redis.Nil) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return Entry{}, false, nil
	}
	return entry, true, nil
}

func (s *RedisStore) Set(ctx context.Context, pollID string, entry Entry, ttl time.Duration) error {
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, keyPrefix+pollID, raw, ttl).Err()
}

func (s *RedisStore) Delete(ctx context.Context, pollID string) error {
	return s.client.Del(ctx, keyPrefix+pollID).Err()
}
