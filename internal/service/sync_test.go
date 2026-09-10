package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/service"
)

func TestSyncPersistsLiveCounters(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	polls := newPollService(repo, store)
	ctx := context.Background()

	poll := createActivePoll(t, polls, service.CreatePollCommand{Question: "Q?", Options: []string{"A", "B"}})
	store.counters[poll.ID+":"+poll.Options[0].ID] = 12
	store.counters[poll.ID+":"+poll.Options[1].ID] = 3

	synchroniser := service.NewSyncService(repo, store, time.Minute, time.Now, discardLogger())
	report, err := synchroniser.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if report.Tracked != 1 || report.Failed != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}

	durable, err := repo.GetResults(ctx, poll.ID)
	if err != nil {
		t.Fatalf("get results: %v", err)
	}
	if durable[poll.Options[0].ID] != 12 || durable[poll.Options[1].ID] != 3 {
		t.Fatalf("counters were not persisted: %+v", durable)
	}
}

// Sync is idempotent when replayed.
func TestSyncIsIdempotent(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	polls := newPollService(repo, store)
	ctx := context.Background()

	poll := createActivePoll(t, polls, service.CreatePollCommand{Question: "Q?", Options: []string{"A", "B"}})
	store.counters[poll.ID+":"+poll.Options[0].ID] = 9

	synchroniser := service.NewSyncService(repo, store, time.Minute, time.Now, discardLogger())
	for range 5 {
		if _, err := synchroniser.Sync(ctx); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}

	durable, err := repo.GetResults(ctx, poll.ID)
	if err != nil {
		t.Fatalf("get results: %v", err)
	}
	if durable[poll.Options[0].ID] != 9 {
		t.Fatalf("repeated cycles must not accumulate, got %d", durable[poll.Options[0].ID])
	}
}

// Durable counts never regress when Redis is empty.
func TestSyncNeverLowersDurableCounts(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	polls := newPollService(repo, store)
	ctx := context.Background()

	poll := createActivePoll(t, polls, service.CreatePollCommand{Question: "Q?", Options: []string{"A", "B"}})
	store.counters[poll.ID+":"+poll.Options[0].ID] = 500

	synchroniser := service.NewSyncService(repo, store, time.Minute, time.Now, discardLogger())
	if _, err := synchroniser.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Simulate Redis losing its data set.
	store.counters = map[string]int64{}
	if _, err := synchroniser.Sync(ctx); err != nil {
		t.Fatalf("sync after data loss: %v", err)
	}

	durable, err := repo.GetResults(ctx, poll.ID)
	if err != nil {
		t.Fatalf("get results: %v", err)
	}
	if durable[poll.Options[0].ID] != 500 {
		t.Fatalf("durable count regressed to %d", durable[poll.Options[0].ID])
	}
}

func TestSyncUntracksSettledPolls(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	polls := newPollService(repo, store)
	ctx := context.Background()

	poll := createActivePoll(t, polls, service.CreatePollCommand{Question: "Q?", Options: []string{"A", "B"}})
	if _, err := polls.Finish(ctx, poll.ID); err != nil {
		t.Fatalf("finish: %v", err)
	}

	settleAfter := 10 * time.Minute
	stillSettling := service.NewSyncService(repo, store, settleAfter, time.Now, discardLogger())
	if _, err := stillSettling.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !store.isTracked(poll.ID) {
		t.Fatal("a freshly finished poll must remain tracked so late ballots are still persisted")
	}

	// Advance the clock past the settle window.
	later := func() time.Time { return time.Now().Add(settleAfter + time.Minute) }
	settled := service.NewSyncService(repo, store, settleAfter, later, discardLogger())
	if _, err := settled.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if store.isTracked(poll.ID) {
		t.Fatal("a settled poll must stop being tracked")
	}
}

// One failed poll does not block syncing others.
func TestSyncIsolatesFailures(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	ctx := context.Background()

	// Tracked poll absent from Postgres should be untracked.
	if err := store.TrackPoll(ctx, "deleted-poll"); err != nil {
		t.Fatalf("track poll: %v", err)
	}

	polls := newPollService(repo, store)
	poll := createActivePoll(t, polls, service.CreatePollCommand{Question: "Q?", Options: []string{"A", "B"}})
	store.counters[poll.ID+":"+poll.Options[0].ID] = 4

	synchroniser := service.NewSyncService(repo, store, time.Minute, time.Now, discardLogger())
	if _, err := synchroniser.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	durable, err := repo.GetResults(ctx, poll.ID)
	if err != nil {
		t.Fatalf("get results: %v", err)
	}
	if durable[poll.Options[0].ID] != 4 {
		t.Fatal("a healthy poll must still be synchronised alongside a broken one")
	}
	if store.isTracked("deleted-poll") {
		t.Fatal("a poll missing from durable storage must stop being tracked")
	}
}

func TestSyncReportsPersistenceFailures(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	polls := newPollService(repo, store)
	ctx := context.Background()

	poll := createActivePoll(t, polls, service.CreatePollCommand{Question: "Q?", Options: []string{"A", "B"}})
	repo.failSaveResults = true

	synchroniser := service.NewSyncService(repo, store, time.Minute, time.Now, discardLogger())
	report, err := synchroniser.Sync(ctx)
	if err == nil {
		t.Fatal("expected the cycle to report an error")
	}
	if report.Failed != 1 {
		t.Fatalf("expected one failure, got %+v", report)
	}
	if !store.isTracked(poll.ID) {
		t.Fatal("a poll that failed to persist must stay tracked for the next cycle")
	}
	if entity.CodeOf(err) != entity.CodeInternal {
		t.Fatalf("unexpected classification: %v", err)
	}
}
