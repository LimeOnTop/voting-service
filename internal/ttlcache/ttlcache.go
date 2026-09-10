// Package ttlcache provides a bounded, sharded, TTL-based in-process cache.
package ttlcache

import (
	"container/list"
	"hash/maphash"
	"sync"
	"time"
)

const shardCount = 16

// TTL is a fixed-capacity LRU cache with per-entry expiry.
type TTL[V any] struct {
	shards [shardCount]*shard[V]
	seed   maphash.Seed
}

type shard[V any] struct {
	mu       sync.Mutex
	capacity int
	entries  map[string]*list.Element
	order    *list.List // front = most recently used
}

type entry[V any] struct {
	key       string
	value     V
	expiresAt time.Time
}

// NewTTL builds a cache holding at most capacity entries in total.
func NewTTL[V any](capacity int) *TTL[V] {
	perShard := capacity / shardCount
	if perShard < 1 {
		perShard = 1
	}
	c := &TTL[V]{seed: maphash.MakeSeed()}
	for i := range c.shards {
		c.shards[i] = &shard[V]{
			capacity: perShard,
			entries:  make(map[string]*list.Element, perShard),
			order:    list.New(),
		}
	}
	return c
}

// Get returns the live value for key, treating expired entries as misses.
func (c *TTL[V]) Get(key string) (V, bool) {
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	element, ok := s.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	item := element.Value.(*entry[V])
	if time.Now().After(item.expiresAt) {
		s.removeLocked(element)
		var zero V
		return zero, false
	}
	s.order.MoveToFront(element)
	return item.value, true
}

// Set stores value under key for ttl, evicting LRU entries when full.
func (c *TTL[V]) Set(key string, value V, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	expiresAt := time.Now().Add(ttl)
	if element, ok := s.entries[key]; ok {
		item := element.Value.(*entry[V])
		item.value = value
		item.expiresAt = expiresAt
		s.order.MoveToFront(element)
		return
	}

	if s.order.Len() >= s.capacity {
		if oldest := s.order.Back(); oldest != nil {
			s.removeLocked(oldest)
		}
	}
	s.entries[key] = s.order.PushFront(&entry[V]{key: key, value: value, expiresAt: expiresAt})
}

// Delete drops key if present.
func (c *TTL[V]) Delete(key string) {
	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if element, ok := s.entries[key]; ok {
		s.removeLocked(element)
	}
}

func (s *shard[V]) removeLocked(element *list.Element) {
	s.order.Remove(element)
	delete(s.entries, element.Value.(*entry[V]).key)
}

func (c *TTL[V]) shardFor(key string) *shard[V] {
	return c.shards[maphash.String(c.seed, key)%shardCount]
}
