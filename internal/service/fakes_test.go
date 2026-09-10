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

type fakeTx struct {
	repo *fakeRepo
}

func (r *fakeRepo) BeginTx(context.Context) (usecase.PollTx, error) {
	return &fakeTx{repo: r}, nil
}

func (t *fakeTx) InsertPoll(_ context.Context, poll entity.Poll) error {
	t.repo.mu.Lock()
	defer t.repo.mu.Unlock()
	t.repo.polls[poll.ID] = poll
	t.repo.results[poll.ID] = map[string]int64{}
	return nil
}

func (t *fakeTx) InsertPollOptions(_ context.Context, poll entity.Poll) error {
	t.repo.mu.Lock()
	defer t.repo.mu.Unlock()
	t.repo.polls[poll.ID] = poll
	return nil
}

func (t *fakeTx) InsertZeroResults(_ context.Context, poll entity.Poll) error {
	t.repo.mu.Lock()
	defer t.repo.mu.Unlock()
	if t.repo.results[poll.ID] == nil {
		t.repo.results[poll.ID] = map[string]int64{}
	}
	for _, option := range poll.Options {
		t.repo.results[poll.ID][option.ID] = 0
	}
	return nil
}

func (t *fakeTx) Commit(context.Context) error   { return nil }
func (t *fakeTx) Rollback(context.Context) error { return nil }

func (r *fakeRepo) GetPoll(_ context.Context, pollID string) (entity.Poll, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	poll, ok := r.polls[pollID]
	if !ok {
		return entity.Poll{}, entity.ErrPollNotFound()
	}
	return poll, nil
}

func (r *fakeRepo) CountPolls(context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.polls), nil
}

func (r *fakeRepo) ListPollPage(_ context.Context, limit, offset int) ([]entity.Poll, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := make([]entity.Poll, 0, len(r.polls))
	for _, poll := range r.polls {
		all = append(all, poll)
	}
	if offset >= len(all) {
		return nil, nil
	}
	end := min(offset+limit, len(all))
	return all[offset:end], nil
}

func (r *fakeRepo) ListOptionsByPollIDs(_ context.Context, pollIDs []string) (map[string][]entity.Option, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string][]entity.Option, len(pollIDs))
	for _, id := range pollIDs {
		if poll, ok := r.polls[id]; ok {
			out[id] = poll.Options
		}
	}
	return out, nil
}

func (r *fakeRepo) UpdatePollStatus(_ context.Context, pollID string, from []entity.PollStatus, to entity.PollStatus, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	poll, ok := r.polls[pollID]
	if !ok {
		return usecase.ErrNotUpdated
	}
	allowed := false
	for _, status := range from {
		if poll.Status == status {
			allowed = true
			break
		}
	}
	if !allowed {
		return usecase.ErrNotUpdated
	}
	poll.Status = to
	if to == entity.PollStatusFinished {
		finishedAt := at
		poll.FinishedAt = &finishedAt
	}
	r.polls[pollID] = poll
	return nil
}

func (r *fakeRepo) GetPollStatus(_ context.Context, pollID string) (entity.PollStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	poll, ok := r.polls[pollID]
	if !ok {
		return "", entity.ErrPollNotFound()
	}
	return poll.Status, nil
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

func (s *fakeStore) CastBallot(_ context.Context, ballot usecase.Ballot) error {
	if s.failCastBallot {
		return entity.WrapError(entity.CodeUnavailable, errUnreachable, "voting is temporarily unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := ballot.PollID + ":" + ballot.VoterHash
	if _, seen := s.voted[key]; seen {
		return entity.NewError(entity.CodeAlreadyVoted, "this device has already voted in this poll")
	}
	s.voted[key] = struct{}{}
	for _, optionID := range ballot.OptionIDs {
		s.counters[ballot.PollID+":"+optionID]++
	}
	return nil
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

type repoLoader struct{ repo *fakeRepo }

func (l repoLoader) Poll(ctx context.Context, pollID string) (entity.Poll, error) {
	return l.repo.GetPoll(ctx, pollID)
}

func (l repoLoader) Invalidate(context.Context, string) error { return nil }
