package ttlcache_test

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/LimeOnTop/voting-service/internal/ttlcache"
)

func TestGetReturnsStoredValue(t *testing.T) {
	c := ttlcache.NewTTL[string](64)
	c.Set("key", "value", time.Minute)

	value, ok := c.Get("key")
	if !ok || value != "value" {
		t.Fatalf("expected the stored value, got %q (found=%t)", value, ok)
	}
}

func TestGetMissesForUnknownKey(t *testing.T) {
	c := ttlcache.NewTTL[string](64)
	if _, ok := c.Get("absent"); ok {
		t.Fatal("expected a miss for an unknown key")
	}
}

func TestEntriesExpire(t *testing.T) {
	c := ttlcache.NewTTL[string](64)
	c.Set("key", "value", 10*time.Millisecond)

	if _, ok := c.Get("key"); !ok {
		t.Fatal("expected a hit before expiry")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := c.Get("key"); ok {
		t.Fatal("expected a miss after expiry")
	}
}

func TestSetIgnoresNonPositiveTTL(t *testing.T) {
	c := ttlcache.NewTTL[string](64)
	c.Set("key", "value", 0)

	if _, ok := c.Get("key"); ok {
		t.Fatal("an entry with no lifetime must not be stored")
	}
}

func TestDeleteRemovesEntry(t *testing.T) {
	c := ttlcache.NewTTL[string](64)
	c.Set("key", "value", time.Minute)
	c.Delete("key")

	if _, ok := c.Get("key"); ok {
		t.Fatal("expected the entry to be gone")
	}
}

func TestSetOverwritesExistingKey(t *testing.T) {
	c := ttlcache.NewTTL[int](64)
	c.Set("key", 1, time.Minute)
	c.Set("key", 2, time.Minute)

	value, _ := c.Get("key")
	if value != 2 {
		t.Fatalf("expected the value to be replaced, got %d", value)
	}
}

// Capacity must bound memory, so a flood of unique keys (for example negative
// lookups driven by an attacker) cannot grow the cache without limit.
func TestCapacityIsEnforced(t *testing.T) {
	const capacity = 256
	c := ttlcache.NewTTL[int](capacity)

	const inserted = capacity * 20
	for i := range inserted {
		c.Set(strconv.Itoa(i), i, time.Minute)
	}

	live := 0
	for i := range inserted {
		if _, ok := c.Get(strconv.Itoa(i)); ok {
			live++
		}
	}
	if live > capacity {
		t.Fatalf("expected at most %d live entries, found %d", capacity, live)
	}
	if live == 0 {
		t.Fatal("expected the cache to retain recent entries")
	}
}

func TestConcurrentAccessIsSafe(t *testing.T) {
	c := ttlcache.NewTTL[int](1024)

	var group sync.WaitGroup
	for worker := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			for i := range 500 {
				key := strconv.Itoa(worker) + ":" + strconv.Itoa(i)
				c.Set(key, i, time.Minute)
				c.Get(key)
				if i%3 == 0 {
					c.Delete(key)
				}
			}
		}()
	}
	group.Wait()
}
