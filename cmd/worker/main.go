// Command worker runs one Redis → PostgreSQL synchronisation cycle and exits.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LimeOnTop/voting-service/internal/config"
	"github.com/LimeOnTop/voting-service/internal/clients"
	"github.com/LimeOnTop/voting-service/internal/repository"
	"github.com/LimeOnTop/voting-service/internal/service"
	"github.com/LimeOnTop/voting-service/internal/syncjob"
	"github.com/LimeOnTop/voting-service/internal/votestore"
)

func main() {
	if err := run(); err != nil {
		slog.Error("sync job failed", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := clients.NewPostgresPool(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer pool.Close()

	redisClient, err := clients.NewRedisClient(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer func() { _ = redisClient.Close() }()

	repo := repository.NewPollRepository(pool)
	store := votestore.New(redisClient, votestore.Config{
		Shards:           cfg.Vote.Shards,
		DedupTTL:         cfg.Vote.DedupTTL,
		CounterRetention: cfg.Vote.CounterRetention,
		RateLimit:        cfg.Vote.RateLimit,
		RateWindow:       cfg.Vote.RateWindow,
		PollQuota:        cfg.Vote.PollIPQuota,
		PollQuotaWindow:  cfg.Vote.PollIPQuotaWindow,
	})

	synchroniser := service.NewSyncService(repo, store, cfg.Sync.SettleAfter, time.Now, log)
	job := syncjob.NewJob(synchroniser, cfg.Sync.Timeout, log)

	log.Info("starting synchronisation job")
	return job.RunOnce(ctx)
}
