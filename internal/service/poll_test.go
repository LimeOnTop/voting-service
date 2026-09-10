package service_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/service"
)

func testLimits() service.PollLimits {
	return service.PollLimits{
		MaxQuestionLength: 500,
		MaxOptionLength:   200,
		MaxOptions:        20,
		DefaultPageSize:   20,
		MaxPageSize:       100,
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newPollService(repo *fakeRepo, store *fakeStore) *service.PollService {
	return service.NewPollService(repo, store, repoLoader{repo}, testLimits(), time.Now, discardLogger())
}

func createActivePoll(t *testing.T, polls *service.PollService, cmd service.CreatePollCommand) entity.Poll {
	t.Helper()
	poll, err := polls.Create(context.Background(), cmd)
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}
	activated, err := polls.Activate(context.Background(), poll.ID)
	if err != nil {
		t.Fatalf("activate poll: %v", err)
	}
	return activated
}

func TestCreatePollValidation(t *testing.T) {
	tests := map[string]service.CreatePollCommand{
		"blank question": {Question: "   ", Options: []string{"A", "B"}},
		"long question":  {Question: strings.Repeat("q", 501), Options: []string{"A", "B"}},
		"one option":     {Question: "Q?", Options: []string{"A"}},
		"blank options":  {Question: "Q?", Options: []string{" ", ""}},
		"long option":    {Question: "Q?", Options: []string{"A", strings.Repeat("o", 201)}},
		"duplicates":     {Question: "Q?", Options: []string{"A", "A"}},
		"too many":       {Question: "Q?", Options: make([]string, 0)},
		"unknown type":   {Question: "Q?", Type: "ranked", Options: []string{"A", "B"}},
		"max choices too high": {
			Question: "Q?", Type: entity.PollTypeMultiple, MaxChoices: 5, Options: []string{"A", "B"},
		},
	}
	// Build the "too many" case from the configured limit rather than a literal.
	tooMany := make([]string, testLimits().MaxOptions+1)
	for i := range tooMany {
		tooMany[i] = string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	tests["too many"] = service.CreatePollCommand{Question: "Q?", Options: tooMany}

	polls := newPollService(newFakeRepo(), newFakeStore())
	for name, cmd := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := polls.Create(context.Background(), cmd)
			if got := entity.CodeOf(err); got != entity.CodeInvalidRequest {
				t.Fatalf("expected invalid_request, got %q (err=%v)", got, err)
			}
		})
	}
}

func TestCreatePollRejectsInvertedWindow(t *testing.T) {
	start := time.Now().Add(time.Hour)
	end := start.Add(-time.Minute)

	polls := newPollService(newFakeRepo(), newFakeStore())
	_, err := polls.Create(context.Background(), service.CreatePollCommand{
		Question: "Q?", Options: []string{"A", "B"}, StartsAt: &start, EndsAt: &end,
	})
	if got := entity.CodeOf(err); got != entity.CodeInvalidRequest {
		t.Fatalf("expected invalid_request, got %q", got)
	}
}

func TestCreatePollNormalisesSingleChoice(t *testing.T) {
	polls := newPollService(newFakeRepo(), newFakeStore())
	poll, err := polls.Create(context.Background(), service.CreatePollCommand{
		Question: " Best language? ",
		Options:  []string{" Go ", "Rust"},
		// A single-choice poll must ignore an inflated max_choices rather than
		// silently accepting multi-select ballots later.
		MaxChoices: 7,
	})
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}
	if poll.Question != "Best language?" {
		t.Fatalf("question was not trimmed: %q", poll.Question)
	}
	if poll.Options[0].Text != "Go" {
		t.Fatalf("option was not trimmed: %q", poll.Options[0].Text)
	}
	if poll.Type != entity.PollTypeSingle || poll.MaxChoices != 1 {
		t.Fatalf("expected single choice with one pick, got %s/%d", poll.Type, poll.MaxChoices)
	}
	if poll.Status != entity.PollStatusDraft {
		t.Fatalf("new polls must start as draft, got %s", poll.Status)
	}
}

func TestLifecycleTransitions(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	polls := newPollService(repo, store)
	ctx := context.Background()

	poll, err := polls.Create(ctx, service.CreatePollCommand{Question: "Q?", Options: []string{"A", "B"}})
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}

	if _, err := polls.Activate(ctx, poll.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !store.isTracked(poll.ID) {
		t.Fatal("an activated poll must be registered for durable synchronisation")
	}
	if _, err := polls.Activate(ctx, poll.ID); entity.CodeOf(err) != entity.CodeConflict {
		t.Fatalf("re-activating must conflict, got %v", err)
	}

	finished, err := polls.Finish(ctx, poll.ID)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if finished.FinishedAt == nil {
		t.Fatal("finishing a poll must record finished_at")
	}
	if !store.isTracked(poll.ID) {
		t.Fatal("a finished poll must stay tracked until the synchroniser settles it")
	}
	if _, err := polls.Finish(ctx, poll.ID); entity.CodeOf(err) != entity.CodeConflict {
		t.Fatalf("re-finishing must conflict, got %v", err)
	}
}

func TestUnknownPollIsNotFound(t *testing.T) {
	polls := newPollService(newFakeRepo(), newFakeStore())
	ctx := context.Background()

	// A syntactically invalid id must be rejected without reaching storage.
	if _, err := polls.Get(ctx, "not-a-uuid"); entity.CodeOf(err) != entity.CodeNotFound {
		t.Fatalf("expected not_found for a malformed id, got %v", err)
	}
	if _, err := polls.Get(ctx, "8f1c3b6e-0000-4000-8000-000000000000"); entity.CodeOf(err) != entity.CodeNotFound {
		t.Fatalf("expected not_found for an unknown id, got %v", err)
	}
}

func TestListPollsClampsPagination(t *testing.T) {
	polls := newPollService(newFakeRepo(), newFakeStore())

	page, err := polls.List(context.Background(), 10_000, -5)
	if err != nil {
		t.Fatalf("list polls: %v", err)
	}
	if page.Limit != testLimits().MaxPageSize {
		t.Fatalf("expected the limit to be clamped to %d, got %d", testLimits().MaxPageSize, page.Limit)
	}
	if page.Offset != 0 {
		t.Fatalf("expected a negative offset to be clamped to 0, got %d", page.Offset)
	}
}

// Results take the higher of live and durable counts.
func TestResultsMergeTakesTheHigherCount(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	polls := newPollService(repo, store)
	ctx := context.Background()

	poll := createActivePoll(t, polls, service.CreatePollCommand{Question: "Q?", Options: []string{"A", "B"}})
	optionA, optionB := poll.Options[0].ID, poll.Options[1].ID

	// Durable ahead on A; Redis ahead on B.
	if err := repo.SaveResults(ctx, poll.ID, map[string]int64{optionA: 900, optionB: 10}); err != nil {
		t.Fatalf("save results: %v", err)
	}
	store.counters[poll.ID+":"+optionA] = 5
	store.counters[poll.ID+":"+optionB] = 100

	results, err := polls.Results(ctx, poll.ID)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if results.Counts[optionA] != 900 {
		t.Fatalf("expected the durable count to win for A, got %d", results.Counts[optionA])
	}
	if results.Counts[optionB] != 100 {
		t.Fatalf("expected the live count to win for B, got %d", results.Counts[optionB])
	}
	if results.Total != 1000 {
		t.Fatalf("expected total 1000, got %d", results.Total)
	}
}

// Empty Redis still reports durable results.
func TestResultsSurviveEmptyRedis(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	polls := newPollService(repo, store)
	ctx := context.Background()

	poll := createActivePoll(t, polls, service.CreatePollCommand{Question: "Q?", Options: []string{"A", "B"}})
	if err := repo.SaveResults(ctx, poll.ID, map[string]int64{poll.Options[0].ID: 42}); err != nil {
		t.Fatalf("save results: %v", err)
	}

	results, err := polls.Results(ctx, poll.ID)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if results.Total != 42 {
		t.Fatalf("expected durable results to be served, got %d", results.Total)
	}
}

func TestResultsToleratesOneFailedSource(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	polls := newPollService(repo, store)
	ctx := context.Background()

	poll := createActivePoll(t, polls, service.CreatePollCommand{Question: "Q?", Options: []string{"A", "B"}})
	store.counters[poll.ID+":"+poll.Options[0].ID] = 7

	repo.failGetResults = true
	results, err := polls.Results(ctx, poll.ID)
	if err != nil {
		t.Fatalf("results must survive a durable-store failure: %v", err)
	}
	if results.Total != 7 {
		t.Fatalf("expected live counters to be served, got %d", results.Total)
	}

	store.failCounters = true
	if _, err := polls.Results(ctx, poll.ID); entity.CodeOf(err) != entity.CodeUnavailable {
		t.Fatalf("expected unavailable when both sources fail, got %v", err)
	}
}
