// Package repository implements PostgreSQL storage for polls and results.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/usecase"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pollColumns = `p.id, p.question, p.type, p.max_choices, p.status,
	p.created_at, p.starts_at, p.ends_at, p.finished_at`

// PollRepository stores polls, options, and aggregated results.
type PollRepository struct {
	db *pgxpool.Pool
}

var _ usecase.PollRepository = (*PollRepository)(nil)

// NewPollRepository constructs a poll repository.
func NewPollRepository(db *pgxpool.Pool) *PollRepository {
	return &PollRepository{db: db}
}

func (r *PollRepository) Ping(ctx context.Context) error {
	if err := r.db.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	return nil
}

// BeginTx starts a write transaction.
func (r *PollRepository) BeginTx(ctx context.Context) (usecase.PollTx, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to create poll"))
	}
	return &pollTx{tx: tx}, nil
}

type pollTx struct {
	tx pgx.Tx
}

func (t *pollTx) Commit(ctx context.Context) error {
	if err := t.tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to create poll"))
	}
	return nil
}

func (t *pollTx) Rollback(ctx context.Context) error {
	if err := t.tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return fmt.Errorf("rollback tx: %w", err)
	}
	return nil
}

func (t *pollTx) InsertPoll(ctx context.Context, poll entity.Poll) error {
	_, err := t.tx.Exec(ctx, `
		INSERT INTO polls (id, question, type, max_choices, status, created_at, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		poll.ID, poll.Question, string(poll.Type), poll.MaxChoices, string(poll.Status),
		poll.CreatedAt, poll.StartsAt, poll.EndsAt)
	if err != nil {
		return fmt.Errorf("insert poll: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to create poll"))
	}
	return nil
}

func (t *pollTx) InsertPollOptions(ctx context.Context, poll entity.Poll) error {
	_, err := t.tx.CopyFrom(ctx,
		pgx.Identifier{"poll_options"},
		[]string{"id", "poll_id", "text", "position"},
		pgx.CopyFromSlice(len(poll.Options), func(i int) ([]any, error) {
			option := poll.Options[i]
			return []any{option.ID, poll.ID, option.Text, option.Position}, nil
		}))
	if err != nil {
		return fmt.Errorf("insert poll options: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to create poll options"))
	}
	return nil
}

func (t *pollTx) InsertZeroResults(ctx context.Context, poll entity.Poll) error {
	_, err := t.tx.CopyFrom(ctx,
		pgx.Identifier{"poll_results"},
		[]string{"poll_id", "option_id", "votes"},
		pgx.CopyFromSlice(len(poll.Options), func(i int) ([]any, error) {
			return []any{poll.ID, poll.Options[i].ID, int64(0)}, nil
		}))
	if err != nil {
		return fmt.Errorf("insert poll results: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to initialise poll results"))
	}
	return nil
}

// GetPoll loads a poll with its options in one JOIN query.
func (r *PollRepository) GetPoll(ctx context.Context, pollID string) (entity.Poll, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+pollColumns+`, o.id, o.text, o.position
		FROM polls p
		LEFT JOIN poll_options o ON o.poll_id = p.id
		WHERE p.id = $1
		ORDER BY o.position, o.id`, pollID)
	if err != nil {
		return entity.Poll{}, fmt.Errorf("get poll: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to load poll"))
	}
	defer rows.Close()

	var (
		poll  entity.Poll
		found bool
	)
	for rows.Next() {
		var (
			row        entity.Poll
			pollType   string
			status     string
			optionID   *string
			optionText *string
			position   *int
		)
		if err := rows.Scan(&row.ID, &row.Question, &pollType, &row.MaxChoices, &status,
			&row.CreatedAt, &row.StartsAt, &row.EndsAt, &row.FinishedAt,
			&optionID, &optionText, &position); err != nil {
			return entity.Poll{}, fmt.Errorf("scan poll: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to load poll"))
		}
		if !found {
			row.Type = entity.PollType(pollType)
			row.Status = entity.PollStatus(status)
			poll = row
			found = true
		}
		if optionID != nil {
			poll.Options = append(poll.Options, entity.Option{ID: *optionID, Text: *optionText, Position: *position})
		}
	}
	if err := rows.Err(); err != nil {
		return entity.Poll{}, fmt.Errorf("iterate poll: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to load poll"))
	}
	if !found {
		return entity.Poll{}, entity.ErrPollNotFound()
	}
	return poll, nil
}

// CountPolls returns the total number of polls.
func (r *PollRepository) CountPolls(ctx context.Context) (int, error) {
	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM polls`).Scan(&total); err != nil {
		return 0, fmt.Errorf("count polls: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to count polls"))
	}
	return total, nil
}

// ListPollPage returns poll headers without options.
func (r *PollRepository) ListPollPage(ctx context.Context, limit, offset int) ([]entity.Poll, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+pollColumns+`
		FROM polls p
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list polls: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to list polls"))
	}
	defer rows.Close()

	polls := make([]entity.Poll, 0, limit)
	for rows.Next() {
		var (
			poll     entity.Poll
			pollType string
			status   string
		)
		if err := rows.Scan(&poll.ID, &poll.Question, &pollType, &poll.MaxChoices, &status,
			&poll.CreatedAt, &poll.StartsAt, &poll.EndsAt, &poll.FinishedAt); err != nil {
			return nil, fmt.Errorf("scan poll page: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to list polls"))
		}
		poll.Type = entity.PollType(pollType)
		poll.Status = entity.PollStatus(status)
		polls = append(polls, poll)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate poll page: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to list polls"))
	}
	return polls, nil
}

// ListOptionsByPollIDs loads options for the given polls.
func (r *PollRepository) ListOptionsByPollIDs(ctx context.Context, pollIDs []string) (map[string][]entity.Option, error) {
	out := make(map[string][]entity.Option, len(pollIDs))
	if len(pollIDs) == 0 {
		return out, nil
	}

	rows, err := r.db.Query(ctx, `
		SELECT poll_id, id, text, position
		FROM poll_options
		WHERE poll_id = ANY($1::uuid[])
		ORDER BY position, id`, pollIDs)
	if err != nil {
		return nil, fmt.Errorf("list options: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to load poll options"))
	}
	defer rows.Close()

	for rows.Next() {
		var (
			pollID string
			option entity.Option
		)
		if err := rows.Scan(&pollID, &option.ID, &option.Text, &option.Position); err != nil {
			return nil, fmt.Errorf("scan options: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to load poll options"))
		}
		out[pollID] = append(out[pollID], option)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate options: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to load poll options"))
	}
	return out, nil
}

// UpdatePollStatus updates status when the current value is in from.
func (r *PollRepository) UpdatePollStatus(ctx context.Context, pollID string, from []entity.PollStatus, to entity.PollStatus, at time.Time) error {
	allowed := make([]string, len(from))
	for i, status := range from {
		allowed[i] = string(status)
	}

	var finishedAt *time.Time
	if to == entity.PollStatusFinished {
		finishedAt = &at
	}

	var updated string
	err := r.db.QueryRow(ctx, `
		UPDATE polls
		SET status = $2, finished_at = $3
		WHERE id = $1 AND status = ANY($4::text[])
		RETURNING id`, pollID, string(to), finishedAt, allowed).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return usecase.ErrNotUpdated
	}
	if err != nil {
		return fmt.Errorf("update poll status: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to update poll status"))
	}
	return nil
}

// GetPollStatus returns the current lifecycle status of a poll.
func (r *PollRepository) GetPollStatus(ctx context.Context, pollID string) (entity.PollStatus, error) {
	var current string
	err := r.db.QueryRow(ctx, `SELECT status FROM polls WHERE id = $1`, pollID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", entity.ErrPollNotFound()
	}
	if err != nil {
		return "", fmt.Errorf("get poll status: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to load poll status"))
	}
	return entity.PollStatus(current), nil
}

// GetResults returns durable vote counts for a poll.
func (r *PollRepository) GetResults(ctx context.Context, pollID string) (map[string]int64, error) {
	rows, err := r.db.Query(ctx, `
		SELECT option_id, votes FROM poll_results WHERE poll_id = $1`, pollID)
	if err != nil {
		return nil, fmt.Errorf("get results: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to load results"))
	}
	defer rows.Close()

	counts := make(map[string]int64)
	for rows.Next() {
		var (
			optionID string
			votes    int64
		)
		if err := rows.Scan(&optionID, &votes); err != nil {
			return nil, fmt.Errorf("scan results: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to load results"))
		}
		counts[optionID] = votes
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate results: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to load results"))
	}
	return counts, nil
}

// SaveResults upserts absolute vote counts without lowering existing values.
func (r *PollRepository) SaveResults(ctx context.Context, pollID string, counts map[string]int64) error {
	if len(counts) == 0 {
		return nil
	}
	optionIDs := make([]string, 0, len(counts))
	votes := make([]int64, 0, len(counts))
	for optionID, count := range counts {
		optionIDs = append(optionIDs, optionID)
		votes = append(votes, count)
	}

	_, err := r.db.Exec(ctx, `
		INSERT INTO poll_results (poll_id, option_id, votes, updated_at)
		SELECT $1, incoming.option_id, incoming.votes, now()
		FROM unnest($2::uuid[], $3::bigint[]) AS incoming(option_id, votes)
		ON CONFLICT (poll_id, option_id) DO UPDATE
		SET votes = GREATEST(poll_results.votes, EXCLUDED.votes),
		    updated_at = now()`, pollID, optionIDs, votes)
	if err != nil {
		return fmt.Errorf("save results: %w", entity.WrapError(entity.CodeUnavailable, err, "failed to persist results"))
	}
	return nil
}
