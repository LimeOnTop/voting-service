// Command api serves the voting HTTP API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LimeOnTop/voting-service/internal/config"
	"github.com/LimeOnTop/voting-service/internal/controller"
	"github.com/LimeOnTop/voting-service/internal/health"
	"github.com/LimeOnTop/voting-service/internal/clients"
	"github.com/LimeOnTop/voting-service/internal/middleware"
	"github.com/LimeOnTop/voting-service/internal/pollcache"
	"github.com/LimeOnTop/voting-service/internal/repository"
	"github.com/LimeOnTop/voting-service/internal/service"
	"github.com/LimeOnTop/voting-service/internal/votestore"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api startup failed", "error", err.Error())
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
	polls := pollcache.New(pollcache.NewRedisStore(redisClient), repo, pollcache.Config{
		LocalTTL:      cfg.Cache.LocalTTL,
		SharedTTL:     cfg.Cache.SharedTTL,
		NegativeTTL:   cfg.Cache.NegativeTTL,
		LocalCapacity: cfg.Cache.Capacity,
	}, log)

	pollService := service.NewPollService(repo, store, polls, service.PollLimits{
		MaxQuestionLength: cfg.Polls.MaxQuestionLength,
		MaxOptionLength:   cfg.Polls.MaxOptionLength,
		MaxOptions:        cfg.Polls.MaxOptions,
		DefaultPageSize:   cfg.Polls.DefaultPageSize,
		MaxPageSize:       cfg.Polls.MaxPageSize,
	}, time.Now, log)
	voteService := service.NewVoteService(polls, store, time.Now)

	checker := health.New(cfg.ProbeTimeout, cfg.ProbeCacheFor,
		health.Dependency{Name: "redis", Critical: true, Check: store.Ping},
		health.Dependency{Name: "postgres", Critical: false, Check: repo.Ping},
	)

	handlers := controller.NewHandlers(pollService, voteService, cfg.Cache.SharedTTL, time.Now)
	router := controller.NewRouter(handlers, controller.RouterConfig{
		AdminToken:      cfg.Admin.Token,
		MaxRequestBytes: cfg.HTTP.MaxRequestBytes,
		RequestTimeout:  cfg.HTTP.RequestTimeout,
		TrustedProxies:  cfg.TrustedProxies,
		VoterCookie: middleware.VoterCookieConfig{
			Name:     cfg.Cookie.Name,
			TTL:      cfg.Cookie.TTL,
			Secure:   cfg.Cookie.Secure,
			SameSite: cfg.Cookie.SameSite,
			Domain:   cfg.Cookie.Domain,
		},
		Logger: log,
		Health: checker,
	})

	server := &http.Server{
		Addr:              cfg.HTTP.Address,
		Handler:           router,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		MaxHeaderBytes:    cfg.HTTP.MaxHeaderBytes,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "address", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("server shutdown incomplete", "error", err.Error())
	}

	log.Info("shutdown complete")
	return nil
}
