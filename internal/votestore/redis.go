// Package votestore implements Redis vote guards, deduplication, and counters.
package votestore

import (
	"context"
	"fmt"
	"hash/crc32"
	"strconv"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/usecase"
	"github.com/redis/go-redis/v9"
)

const trackedPollsKey = "votes:polls"

// Config tunes shards, TTLs, and abuse limits.
type Config struct {
	Shards           int
	DedupTTL         time.Duration
	CounterRetention time.Duration
	RateLimit        int
	RateWindow       time.Duration
	PollQuota        int
	PollQuotaWindow  time.Duration
}

// Store implements usecase.VoteStore.
type Store struct {
	client redis.UniversalClient
	cfg    Config
}

var _ usecase.VoteStore = (*Store)(nil)

// New builds a Redis-backed vote store.
func New(client redis.UniversalClient, cfg Config) *Store {
	return &Store{client: client, cfg: cfg}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

// Guard applies rate limiting and per-poll IP quota.
func (s *Store) Guard(ctx context.Context, req usecase.GuardRequest) (usecase.Verdict, error) {
	tag := "{ip:" + req.ClientIP + "}"
	keys := []string{tag + ":rl", tag + ":pq:" + req.PollID}
	args := []any{
		s.cfg.RateLimit,
		int(s.cfg.RateWindow.Seconds()),
		s.cfg.PollQuota,
		int(s.cfg.PollQuotaWindow.Seconds()),
	}

	verdict, err := guardScript.Run(ctx, s.client, keys, args...).Int()
	if err != nil {
		return usecase.VerdictAllow, entity.WrapError(entity.CodeUnavailable, err, "voting is temporarily unavailable")
	}
	return usecase.Verdict(verdict), nil
}

// CastBallot deduplicates the voter and increments selected options.
func (s *Store) CastBallot(ctx context.Context, ballot usecase.Ballot) (bool, error) {
	tag := s.shardTag(ballot.PollID, ballot.VoterHash)
	keys := []string{tag + ":voted:" + ballot.VoterHash, tag + ":counts"}

	args := make([]any, 0, 2+len(ballot.OptionIDs))
	args = append(args, int(s.cfg.DedupTTL.Seconds()), int(s.cfg.CounterRetention.Seconds()))
	for _, id := range ballot.OptionIDs {
		args = append(args, id)
	}

	counted, err := castBallotScript.Run(ctx, s.client, keys, args...).Int()
	if err != nil {
		return false, entity.WrapError(entity.CodeUnavailable, err, "voting is temporarily unavailable")
	}
	return counted == 1, nil
}

// Counters sums each option across all shards of the poll.
func (s *Store) Counters(ctx context.Context, pollID string, optionIDs []string) (map[string]int64, error) {
	counts := make(map[string]int64, len(optionIDs))
	for _, id := range optionIDs {
		counts[id] = 0
	}

	pipe := s.client.Pipeline()
	commands := make([]*redis.MapStringStringCmd, s.cfg.Shards)
	for shard := 0; shard < s.cfg.Shards; shard++ {
		commands[shard] = pipe.HGetAll(ctx, s.tagForShard(pollID, shard)+":counts")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, entity.WrapError(entity.CodeUnavailable, err, "vote counters are temporarily unavailable")
	}

	for _, command := range commands {
		for optionID, raw := range command.Val() {
			if _, tracked := counts[optionID]; !tracked {
				continue
			}
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return nil, entity.WrapError(entity.CodeInternal, err,
					fmt.Sprintf("corrupt counter for option %s", optionID))
			}
			counts[optionID] += value
		}
	}
	return counts, nil
}

// TrackPoll marks a poll as needing durable synchronisation.
func (s *Store) TrackPoll(ctx context.Context, pollID string) error {
	if err := s.client.SAdd(ctx, trackedPollsKey, pollID).Err(); err != nil {
		return entity.WrapError(entity.CodeUnavailable, err, "failed to track poll for synchronisation")
	}
	return nil
}

// TrackedPolls lists polls whose counters still need to be persisted.
func (s *Store) TrackedPolls(ctx context.Context) ([]string, error) {
	ids, err := s.client.SMembers(ctx, trackedPollsKey).Result()
	if err != nil {
		return nil, entity.WrapError(entity.CodeUnavailable, err, "failed to list tracked polls")
	}
	return ids, nil
}

// UntrackPoll stops synchronising a poll.
func (s *Store) UntrackPoll(ctx context.Context, pollID string) error {
	if err := s.client.SRem(ctx, trackedPollsKey, pollID).Err(); err != nil {
		return entity.WrapError(entity.CodeUnavailable, err, "failed to untrack poll")
	}
	return nil
}

func (s *Store) shardTag(pollID, voterHash string) string {
	shard := int(crc32.ChecksumIEEE([]byte(voterHash)) % uint32(s.cfg.Shards))
	return s.tagForShard(pollID, shard)
}

func (s *Store) tagForShard(pollID string, shard int) string {
	return "{v:" + pollID + ":" + strconv.Itoa(shard) + "}"
}
