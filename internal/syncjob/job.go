// Package syncjob runs a single synchronisation pass.
package syncjob

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/LimeOnTop/voting-service/internal/service"
)

// Synchroniser persists live counters to durable storage.
type Synchroniser interface {
	Sync(ctx context.Context) (service.Report, error)
}

// Job runs one sync cycle and exits.
type Job struct {
	synchroniser Synchroniser
	timeout      time.Duration
	log          *slog.Logger
}

// NewJob constructs a one-shot synchronisation job.
func NewJob(synchroniser Synchroniser, timeout time.Duration, log *slog.Logger) *Job {
	if log == nil {
		log = slog.Default()
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Job{synchroniser: synchroniser, timeout: timeout, log: log}
}

// RunOnce flushes live Redis counters into PostgreSQL.
func (j *Job) RunOnce(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, j.timeout)
	defer cancel()

	report, err := j.synchroniser.Sync(ctx)
	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	j.log.InfoContext(ctx, "synchronisation completed",
		"tracked_polls", report.Tracked,
		"failed_polls", report.Failed,
	)
	return nil
}
