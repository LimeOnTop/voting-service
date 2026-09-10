package service_test

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/usecase"
)

var errUnreachable = errors.New("connection refused")

// fakeRepo is an in-memory PollRepository for tests.
type fakeRepo struct {
	mu      sync.Mutex
	polls   map[string]entity.Poll
	results map[string]map[string]int64

	failGetResults  bool
	failSaveResults bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		polls:   map[string]entity.Poll{},
		results: map[string]map[string]int64{},
	}
}

func (r *fakeRepo) CreatePoll(_ context.Context, poll entity.Poll) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.polls[poll.ID] = poll
	r.results[poll.ID] = map[string]int64{}
	for _, option := range poll.Options {
		r.results[poll.ID][option.ID] = 0
	}
	return nil
}

func (r *fakeRepo) GetPoll(_ context.Context, pollID string) (entity.Poll, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	poll, ok := r.polls[pollID]
	if !ok {
		return entity.Poll{}, entity.ErrPollNotFound()
	}
	return poll, nil
}

func (r *fakeRepo) ListPolls(_ context.Context, limit, offset int) ([]entity.Poll, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := make([]entity.Poll, 0, len(r.polls))
	for _, poll := range r.polls {
		all = append(all, poll)
	}
	if offset >= len(all) {
		return nil, len(all), nil
	}
	end := min(offset+limit, len(all))
	return all[offset:end], len(all), nil
}

func (r *fakeRepo) TransitionStatus(_ context.Context, pollID string, from []entity.PollStatus, to entity.PollStatus, at time.Time) (entity.Poll, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	poll, ok := r.polls[pollID]
	if !ok {
		return entity.Poll{}, entity.ErrPollNotFound()
	}
	allowed := false
	for _, status := range from {
		if poll.Status == status {
			allowed = true
			break
		}
	}
	if !allowed {
		return entity.Poll{}, entity.NewError(entity.CodeConflict,
			"cannot change poll status from "+string(poll.Status)+" to "+string(to))
	}
	poll.Status = to
	if to == entity.PollStatusFinished {
		finishedAt := at
		poll.FinishedAt = &finishedAt
	}
	r.polls[pollID] = poll
	return poll, nil
}

func (r *fakeRepo) GetResults(_ context.Context, pollID string) (map[string]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failGetResults {
		return nil, errUnreachable
	}
	counts := make(map[string]int64, len(r.results[pollID]))
	for optionID, votes := range r.results[pollID] {
		counts[optionID] = votes
	}
	return counts, nil
}

func (r *fakeRepo) SaveResults(_ context.Context, pollID string, counts map[string]int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failSaveResults {
		return errUnreachable
	}
	if r.results[pollID] == nil {
		r.results[pollID] = map[string]int64{}
	}
	for optionID, votes := range counts {
		if votes > r.results[pollID][optionID] {
			r.results[pollID][optionID] = votes
		}
	}
	return nil
}

func (r *fakeRepo) Ping(context.Context) error { return nil }

// fakeStore is an in-memory VoteStore for tests.
type fakeStore struct {
	mu       sync.Mutex
	voted    map[string]struct{}
	counters map[string]int64
	tracked  map[string]struct{}

	verdict        usecase.Verdict
	failGuard      bool
	failCastBallot bool
	failCounters   bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		voted:    map[string]struct{}{},
		counters: map[string]int64{},
		tracked:  map[string]struct{}{},
		verdict:  usecase.VerdictAllow,
	}
}

func (s *fakeStore) Guard(context.Context, usecase.GuardRequest) (usecase.Verdict, error) {
	if s.failGuard {
		return usecase.VerdictAllow, entity.WrapError(entity.CodeUnavailable, errUnreachable, "voting is temporarily unavailable")
	}
	return s.verdict, nil
}

func (s *fakeStore) CastBallot(_ context.Context, ballot usecase.Ballot) (bool, error) {
	if s.failCastBallot {
		return false, entity.WrapError(entity.CodeUnavailable, errUnreachable, "voting is temporarily unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := ballot.PollID + ":" + ballot.VoterHash
	if _, seen := s.voted[key]; seen {
		return false, nil
	}
	s.voted[key] = struct{}{}
	for _, optionID := range ballot.OptionIDs {
		s.counters[ballot.PollID+":"+optionID]++
	}
	return true, nil
}

func (s *fakeStore) Counters(_ context.Context, pollID string, optionIDs []string) (map[string]int64, error) {
	if s.failCounters {
		return nil, entity.WrapError(entity.CodeUnavailable, errUnreachable, "counters unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	counts := make(map[string]int64, len(optionIDs))
	for _, optionID := range optionIDs {
		counts[optionID] = s.counters[pollID+":"+optionID]
	}
	return counts, nil
}

func (s *fakeStore) TrackPoll(_ context.Context, pollID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tracked[pollID] = struct{}{}
	return nil
}

func (s *fakeStore) TrackedPolls(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.tracked))
	for id := range s.tracked {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *fakeStore) UntrackPoll(_ context.Context, pollID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tracked, pollID)
	return nil
}

func (s *fakeStore) Ping(context.Context) error { return nil }

func (s *fakeStore) isTracked(pollID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.tracked[pollID]
	return ok
}

// fixedLoader serves a fixed poll for vote tests.
type fixedLoader struct {
	poll entity.Poll
	err  error
}

func (l fixedLoader) Poll(context.Context, string) (entity.Poll, error) {
	if l.err != nil {
		return entity.Poll{}, l.err
	}
	return l.poll, nil
}

func (l fixedLoader) Invalidate(context.Context, string) error { return nil }

// repoLoader reads polls through the fake repository.
type repoLoader struct{ repo *fakeRepo }

func (l repoLoader) Poll(ctx context.Context, pollID string) (entity.Poll, error) {
	return l.repo.GetPoll(ctx, pollID)
}

func (l repoLoader) Invalidate(context.Context, string) error { return nil }
