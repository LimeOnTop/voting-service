// Package pollcache serves poll configuration from local and Redis caches.
package pollcache

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/LimeOnTop/voting-service/internal/ttlcache"
	"github.com/LimeOnTop/voting-service/internal/entity"
	"golang.org/x/sync/singleflight"
)

// Entry is a cached poll lookup, including negative (missing) results.
type Entry struct {
	Poll    entity.Poll `json:"poll"`
	Missing bool        `json:"missing"`
}

// SharedStore is the cross-replica cache tier.
type SharedStore interface {
	Get(ctx context.Context, pollID string) (*Entry, error)
	Set(ctx context.Context, pollID string, entry Entry, ttl time.Duration) error
	Delete(ctx context.Context, pollID string) error
}

// Origin is the durable source of poll configuration.
type Origin interface {
	GetPoll(ctx context.Context, pollID string) (entity.Poll, error)
}

// Config tunes local and shared cache TTLs and capacity.
type Config struct {
	LocalTTL      time.Duration
	SharedTTL     time.Duration
	NegativeTTL   time.Duration
	LocalCapacity int
}

// Cache implements usecase.PollLoader.
type Cache struct {
	local  *ttlcache.TTL[Entry]
	shared SharedStore
	origin Origin
	cfg    Config
	group  singleflight.Group
	log    *slog.Logger
}

// New builds a two-tier poll cache.
func New(shared SharedStore, origin Origin, cfg Config, log *slog.Logger) *Cache {
	if log == nil {
		log = slog.Default()
	}
	return &Cache{
		local:  ttlcache.NewTTL[Entry](cfg.LocalCapacity),
		shared: shared,
		origin: origin,
		cfg:    cfg,
		log:    log,
	}
}

// Poll resolves poll configuration from local cache, Redis, then origin.
func (c *Cache) Poll(ctx context.Context, pollID string) (entity.Poll, error) {
	if entry, ok := c.local.Get(pollID); ok {
		return unwrap(entry)
	}

	resolved, err, _ := c.group.Do(pollID, func() (any, error) {
		return c.resolve(ctx, pollID)
	})
	if err != nil {
		return entity.Poll{}, err
	}
	return unwrap(resolved.(Entry))
}

func (c *Cache) resolve(ctx context.Context, pollID string) (Entry, error) {
	if entry, ok := c.local.Get(pollID); ok {
		return entry, nil
	}

	if entry, err := c.shared.Get(ctx, pollID); err != nil {
		c.log.WarnContext(ctx, "poll cache read failed, falling back to origin",
			"poll_id", pollID, "error", err.Error())
	} else if entry != nil {
		c.local.Set(pollID, *entry, c.localTTLFor(*entry))
		return *entry, nil
	}

	poll, err := c.origin.GetPoll(ctx, pollID)
	switch {
	case err == nil:
		return c.store(ctx, pollID, Entry{Poll: poll}, c.cfg.SharedTTL), nil
	case entity.CodeOf(err) == entity.CodeNotFound:
		return c.store(ctx, pollID, Entry{Missing: true}, c.cfg.NegativeTTL), nil
	default:
		return Entry{}, err
	}
}

func (c *Cache) store(ctx context.Context, pollID string, entry Entry, sharedTTL time.Duration) Entry {
	if err := c.shared.Set(ctx, pollID, entry, sharedTTL); err != nil {
		c.log.WarnContext(ctx, "poll cache write failed",
			"poll_id", pollID, "error", err.Error())
	}
	c.local.Set(pollID, entry, c.localTTLFor(entry))
	return entry
}

// Invalidate drops a poll from both cache tiers.
func (c *Cache) Invalidate(ctx context.Context, pollID string) error {
	c.local.Delete(pollID)
	if err := c.shared.Delete(ctx, pollID); err != nil {
		return fmt.Errorf("invalidate poll cache: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to invalidate poll cache"))
	}
	return nil
}

func (c *Cache) localTTLFor(entry Entry) time.Duration {
	if entry.Missing && c.cfg.NegativeTTL < c.cfg.LocalTTL {
		return c.cfg.NegativeTTL
	}
	return c.cfg.LocalTTL
}

func unwrap(entry Entry) (entity.Poll, error) {
	if entry.Missing {
		return entity.Poll{}, entity.ErrPollNotFound()
	}
	return entry.Poll, nil
}
