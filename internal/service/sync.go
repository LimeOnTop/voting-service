package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/usecase"
)

// SyncService copies live Redis counters into PostgreSQL.
type SyncService struct {
	repo        usecase.PollRepository
	store       usecase.VoteStore
	settleAfter time.Duration
	now         func() time.Time
	log         *slog.Logger
}

// NewSyncService constructs a synchroniser.
func NewSyncService(repo usecase.PollRepository, store usecase.VoteStore, settleAfter time.Duration, now func() time.Time, log *slog.Logger) *SyncService {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &SyncService{repo: repo, store: store, settleAfter: settleAfter, now: now, log: log}
}

// Report summarises one synchronisation cycle.
type Report struct {
	Tracked int
	Failed  int
}

// Sync persists counters for every tracked poll.
func (s *SyncService) Sync(ctx context.Context) (Report, error) {
	pollIDs, err := s.store.TrackedPolls(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("sync: %w", err)
	}

	report := Report{Tracked: len(pollIDs)}
	for _, pollID := range pollIDs {
		if err := s.syncPoll(ctx, pollID); err != nil {
			report.Failed++
			s.log.ErrorContext(ctx, "poll synchronisation failed",
				"poll_id", pollID, "error", err.Error())
		}
	}
	if report.Failed > 0 {
		return report, fmt.Errorf("failed to synchronise %d of %d polls", report.Failed, report.Tracked)
	}
	return report, nil
}

func (s *SyncService) syncPoll(ctx context.Context, pollID string) error {
	poll, err := s.repo.GetPoll(ctx, pollID)
	if err != nil {
		if entity.CodeOf(err) == entity.CodeNotFound {
			if untrackErr := s.store.UntrackPoll(ctx, pollID); untrackErr != nil {
				return fmt.Errorf("untrack missing poll: %w", untrackErr)
			}
			return nil
		}
		return fmt.Errorf("sync poll %s: %w", pollID, err)
	}

	counts, err := s.store.Counters(ctx, pollID, poll.OptionIDs())
	if err != nil {
		return fmt.Errorf("sync poll %s: %w", pollID, err)
	}
	if err := s.repo.SaveResults(ctx, pollID, counts); err != nil {
		return fmt.Errorf("sync poll %s: %w", pollID, err)
	}

	if s.settled(poll) {
		if err := s.store.UntrackPoll(ctx, pollID); err != nil {
			return fmt.Errorf("untrack settled poll: %w", err)
		}
	}
	return nil
}

func (s *SyncService) settled(poll entity.Poll) bool {
	if poll.Status != entity.PollStatusFinished || poll.FinishedAt == nil {
		return false
	}
	return s.now().UTC().Sub(*poll.FinishedAt) >= s.settleAfter
}
