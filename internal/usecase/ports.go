// Package usecase defines ports between services and infrastructure.
package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
)

// ErrNotUpdated means a conditional write matched no rows.
var ErrNotUpdated = errors.New("not updated")

// PollRepository is the durable store for polls and aggregated results.
type PollRepository interface {
	BeginTx(ctx context.Context) (PollTx, error)
	GetPoll(ctx context.Context, pollID string) (entity.Poll, error)
	CountPolls(ctx context.Context) (int, error)
	ListPollPage(ctx context.Context, limit, offset int) ([]entity.Poll, error)
	ListOptionsByPollIDs(ctx context.Context, pollIDs []string) (map[string][]entity.Option, error)
	UpdatePollStatus(ctx context.Context, pollID string, from []entity.PollStatus, to entity.PollStatus, at time.Time) error
	GetPollStatus(ctx context.Context, pollID string) (entity.PollStatus, error)
	GetResults(ctx context.Context, pollID string) (map[string]int64, error)
	SaveResults(ctx context.Context, pollID string, counts map[string]int64) error
	Ping(ctx context.Context) error
}

// PollTx groups single-statement writes in one database transaction.
type PollTx interface {
	InsertPoll(ctx context.Context, poll entity.Poll) error
	InsertPollOptions(ctx context.Context, poll entity.Poll) error
	InsertZeroResults(ctx context.Context, poll entity.Poll) error
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// GuardRequest describes abuse checks applied before accepting a ballot.
type GuardRequest struct {
	ClientIP string
	PollID   string
}

// Verdict is the outcome of the abuse guard.
type Verdict int

const (
	VerdictAllow Verdict = iota
	VerdictRateLimited
	VerdictQuotaExceeded
)

// Ballot is a deduplicated set of option increments for one voter.
type Ballot struct {
	PollID    string
	VoterHash string
	OptionIDs []string
}

// VoteStore is the Redis hot path for guards, ballots, and counters.
type VoteStore interface {
	Guard(ctx context.Context, req GuardRequest) (Verdict, error)
	CastBallot(ctx context.Context, ballot Ballot) error
	Counters(ctx context.Context, pollID string, optionIDs []string) (map[string]int64, error)
	TrackPoll(ctx context.Context, pollID string) error
	TrackedPolls(ctx context.Context) ([]string, error)
	UntrackPoll(ctx context.Context, pollID string) error
	Ping(ctx context.Context) error
}

// PollLoader resolves poll configuration for the vote hot path.
type PollLoader interface {
	Poll(ctx context.Context, pollID string) (entity.Poll, error)
	Invalidate(ctx context.Context, pollID string) error
}
