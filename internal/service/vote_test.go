package service_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/usecase"
	"github.com/LimeOnTop/voting-service/internal/service"
)

const knownPollID = "9c8f1e2a-1111-4222-8333-444455556666"

func activeSinglePoll() entity.Poll {
	return entity.Poll{
		ID:         knownPollID,
		Question:   "A or B?",
		Type:       entity.PollTypeSingle,
		MaxChoices: 1,
		Status:     entity.PollStatusActive,
		Options: []entity.Option{
			{ID: "11111111-1111-4111-8111-111111111111", Text: "A"},
			{ID: "22222222-2222-4222-8222-222222222222", Text: "B"},
		},
	}
}

func newVoteService(poll entity.Poll, store *fakeStore) *service.VoteService {
	return service.NewVoteService(fixedLoader{poll: poll}, store, time.Now)
}

func ballot(optionID string) service.VoteCommand {
	return service.VoteCommand{
		PollID:     knownPollID,
		OptionIDs:  []string{optionID},
		VoterToken: "voter-token-1",
		ClientIP:   "203.0.113.10",
	}
}

func TestCastVoteAcceptsFirstBallot(t *testing.T) {
	poll := activeSinglePoll()
	store := newFakeStore()
	votes := newVoteService(poll, store)

	if err := votes.Cast(context.Background(), ballot(poll.Options[0].ID)); err != nil {
		t.Fatalf("expected the ballot to be accepted, got %v", err)
	}
	if store.counters[poll.ID+":"+poll.Options[0].ID] != 1 {
		t.Fatal("the selected option counter was not incremented")
	}
}

func TestCastVoteDeduplicatesTheSameDevice(t *testing.T) {
	poll := activeSinglePoll()
	store := newFakeStore()
	votes := newVoteService(poll, store)
	ctx := context.Background()

	if err := votes.Cast(ctx, ballot(poll.Options[0].ID)); err != nil {
		t.Fatalf("first ballot: %v", err)
	}
	err := votes.Cast(ctx, ballot(poll.Options[1].ID))
	if got := entity.CodeOf(err); got != entity.CodeAlreadyVoted {
		t.Fatalf("expected already_voted, got %q", got)
	}
	if store.counters[poll.ID+":"+poll.Options[1].ID] != 0 {
		t.Fatal("a rejected duplicate must not change any counter")
	}
}

// Voter identity ignores network and device attributes.
func TestVoterIdentityIgnoresNetworkAndDevice(t *testing.T) {
	poll := activeSinglePoll()
	store := newFakeStore()
	votes := newVoteService(poll, store)
	ctx := context.Background()

	first := ballot(poll.Options[0].ID)
	if err := votes.Cast(ctx, first); err != nil {
		t.Fatalf("first ballot: %v", err)
	}

	roaming := first
	roaming.ClientIP = "198.51.100.77"
	if got := entity.CodeOf(votes.Cast(ctx, roaming)); got != entity.CodeAlreadyVoted {
		t.Fatalf("changing IP must not grant another vote, got %q", got)
	}
}

func TestCastVoteRejectsClosedPolls(t *testing.T) {
	tests := map[string]entity.PollStatus{
		"draft":    entity.PollStatusDraft,
		"finished": entity.PollStatusFinished,
	}
	for name, status := range tests {
		t.Run(name, func(t *testing.T) {
			poll := activeSinglePoll()
			poll.Status = status
			votes := newVoteService(poll, newFakeStore())

			err := votes.Cast(context.Background(), ballot(poll.Options[0].ID))
			if got := entity.CodeOf(err); got != entity.CodeConflict {
				t.Fatalf("expected conflict, got %q", got)
			}
		})
	}
}

func TestCastVoteHonoursTheVotingWindow(t *testing.T) {
	poll := activeSinglePoll()
	opensAt := time.Now().Add(time.Hour)
	poll.StartsAt = &opensAt

	votes := newVoteService(poll, newFakeStore())
	err := votes.Cast(context.Background(), ballot(poll.Options[0].ID))
	if got := entity.CodeOf(err); got != entity.CodeConflict {
		t.Fatalf("expected conflict before the window opens, got %q", got)
	}
}

func TestCastVoteRejectsBadInput(t *testing.T) {
	poll := activeSinglePoll()

	tests := map[string]struct {
		mutate   func(*service.VoteCommand)
		wantCode entity.ErrorCode
	}{
		"malformed poll id": {func(c *service.VoteCommand) { c.PollID = "nope" }, entity.CodeNotFound},
		"missing token":     {func(c *service.VoteCommand) { c.VoterToken = "" }, entity.CodeInvalidRequest},
		"no options":        {func(c *service.VoteCommand) { c.OptionIDs = nil }, entity.CodeInvalidRequest},
		"foreign option":    {func(c *service.VoteCommand) { c.OptionIDs = []string{"other"} }, entity.CodeInvalidRequest},
		"too many options": {
			func(c *service.VoteCommand) { c.OptionIDs = []string{poll.Options[0].ID, poll.Options[1].ID} },
			entity.CodeInvalidRequest,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			votes := newVoteService(poll, newFakeStore())
			cmd := ballot(poll.Options[0].ID)
			test.mutate(&cmd)

			if got := entity.CodeOf(votes.Cast(context.Background(), cmd)); got != test.wantCode {
				t.Fatalf("expected %q, got %q", test.wantCode, got)
			}
		})
	}
}

func TestCastVoteSurfacesGuardVerdicts(t *testing.T) {
	tests := map[string]struct {
		verdict  usecase.Verdict
		wantCode entity.ErrorCode
	}{
		"rate limited":   {usecase.VerdictRateLimited, entity.CodeRateLimited},
		"quota exceeded": {usecase.VerdictQuotaExceeded, entity.CodeQuotaExceeded},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			poll := activeSinglePoll()
			store := newFakeStore()
			store.verdict = test.verdict
			votes := newVoteService(poll, store)

			err := votes.Cast(context.Background(), ballot(poll.Options[0].ID))
			if got := entity.CodeOf(err); got != test.wantCode {
				t.Fatalf("expected %q, got %q", test.wantCode, got)
			}
		})
	}
}

// Voting fails closed when the store is unavailable.
func TestCastVoteFailsClosedWhenTheStoreIsDown(t *testing.T) {
	tests := map[string]func(*fakeStore){
		"guard unavailable":  func(s *fakeStore) { s.failGuard = true },
		"commit unavailable": func(s *fakeStore) { s.failCastBallot = true },
	}
	for name, breakStore := range tests {
		t.Run(name, func(t *testing.T) {
			poll := activeSinglePoll()
			store := newFakeStore()
			breakStore(store)
			votes := newVoteService(poll, store)

			err := votes.Cast(context.Background(), ballot(poll.Options[0].ID))
			if got := entity.CodeOf(err); got != entity.CodeUnavailable {
				t.Fatalf("expected unavailable, got %q", got)
			}
		})
	}
}

func TestCastVoteAcceptsMultipleChoiceBallot(t *testing.T) {
	poll := activeSinglePoll()
	poll.Type = entity.PollTypeMultiple
	poll.MaxChoices = 2

	store := newFakeStore()
	votes := newVoteService(poll, store)

	cmd := ballot(poll.Options[0].ID)
	cmd.OptionIDs = []string{poll.Options[0].ID, poll.Options[1].ID}
	if err := votes.Cast(context.Background(), cmd); err != nil {
		t.Fatalf("expected the ballot to be accepted, got %v", err)
	}
	for _, option := range poll.Options {
		if store.counters[poll.ID+":"+option.ID] != 1 {
			t.Fatalf("option %s was not counted", option.Text)
		}
	}
}

// Concurrent submissions from one device count once.
func TestConcurrentBallotsFromOneDeviceCountOnce(t *testing.T) {
	const attempts = 64

	poll := activeSinglePoll()
	store := newFakeStore()
	votes := newVoteService(poll, store)

	var accepted atomic.Int64
	var group sync.WaitGroup
	for range attempts {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := votes.Cast(context.Background(), ballot(poll.Options[0].ID)); err == nil {
				accepted.Add(1)
			}
		}()
	}
	group.Wait()

	if accepted.Load() != 1 {
		t.Fatalf("expected exactly one accepted ballot, got %d", accepted.Load())
	}
	if store.counters[poll.ID+":"+poll.Options[0].ID] != 1 {
		t.Fatalf("expected one counted vote, got %d", store.counters[poll.ID+":"+poll.Options[0].ID])
	}
}

func TestVoterHashIsStableAndPollScoped(t *testing.T) {
	first := service.VoterHash("poll-a", "token")
	again := service.VoterHash("poll-a", "token")
	otherPoll := service.VoterHash("poll-b", "token")
	otherVoter := service.VoterHash("poll-a", "different")

	if first != again {
		t.Fatal("the same voter must hash to the same value")
	}
	if first == otherPoll {
		t.Fatal("a voter must be able to vote in a different poll")
	}
	if first == otherVoter {
		t.Fatal("different voters must not collide")
	}
	if len(first) != 32 {
		t.Fatalf("expected a 128-bit hex digest, got %d characters", len(first))
	}
}
