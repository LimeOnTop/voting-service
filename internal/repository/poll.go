// Package repository implements PostgreSQL storage for polls and results.
package repository

import (
	"context"
	"errors"
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
	return r.db.Ping(ctx)
}

// CreatePoll inserts a poll, its options, and zeroed result rows.
func (r *PollRepository) CreatePoll(ctx context.Context, poll entity.Poll) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return entity.WrapError(entity.CodeUnavailable, err, "failed to create poll")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO polls (id, question, type, max_choices, status, created_at, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		poll.ID, poll.Question, string(poll.Type), poll.MaxChoices, string(poll.Status),
		poll.CreatedAt, poll.StartsAt, poll.EndsAt)
	if err != nil {
		return entity.WrapError(entity.CodeUnavailable, err, "failed to create poll")
	}

	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"poll_options"},
		[]string{"id", "poll_id", "text", "position"},
		pgx.CopyFromSlice(len(poll.Options), func(i int) ([]any, error) {
			option := poll.Options[i]
			return []any{option.ID, poll.ID, option.Text, option.Position}, nil
		}))
	if err != nil {
		return entity.WrapError(entity.CodeUnavailable, err, "failed to create poll options")
	}

	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"poll_results"},
		[]string{"poll_id", "option_id", "votes"},
		pgx.CopyFromSlice(len(poll.Options), func(i int) ([]any, error) {
			return []any{poll.ID, poll.Options[i].ID, int64(0)}, nil
		}))
	if err != nil {
		return entity.WrapError(entity.CodeUnavailable, err, "failed to initialise poll results")
	}

	if err := tx.Commit(ctx); err != nil {
		return entity.WrapError(entity.CodeUnavailable, err, "failed to create poll")
	}
	return nil
}

// GetPoll loads a poll with its options.
func (r *PollRepository) GetPoll(ctx context.Context, pollID string) (entity.Poll, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+pollColumns+`, o.id, o.text, o.position
		FROM polls p
		LEFT JOIN poll_options o ON o.poll_id = p.id
		WHERE p.id = $1
		ORDER BY o.position, o.id`, pollID)
	if err != nil {
		return entity.Poll{}, entity.WrapError(entity.CodeUnavailable, err, "failed to load poll")
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
		err := rows.Scan(&row.ID, &row.Question, &pollType, &row.MaxChoices, &status,
			&row.CreatedAt, &row.StartsAt, &row.EndsAt, &row.FinishedAt,
			&optionID, &optionText, &position)
		if err != nil {
			return entity.Poll{}, entity.WrapError(entity.CodeUnavailable, err, "failed to load poll")
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
		return entity.Poll{}, entity.WrapError(entity.CodeUnavailable, err, "failed to load poll")
	}
	if !found {
		return entity.Poll{}, entity.ErrPollNotFound()
	}
	return poll, nil
}

// ListPolls returns a page of polls with options, newest first.
func (r *PollRepository) ListPolls(ctx context.Context, limit, offset int) ([]entity.Poll, int, error) {
	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM polls`).Scan(&total); err != nil {
		return nil, 0, entity.WrapError(entity.CodeUnavailable, err, "failed to count polls")
	}

	rows, err := r.db.Query(ctx, `
		SELECT `+pollColumns+`
		FROM polls p
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, entity.WrapError(entity.CodeUnavailable, err, "failed to list polls")
	}
	defer rows.Close()

	polls := make([]entity.Poll, 0, limit)
	index := make(map[string]int, limit)
	ids := make([]string, 0, limit)
	for rows.Next() {
		var (
			poll     entity.Poll
			pollType string
			status   string
		)
		if err := rows.Scan(&poll.ID, &poll.Question, &pollType, &poll.MaxChoices, &status,
			&poll.CreatedAt, &poll.StartsAt, &poll.EndsAt, &poll.FinishedAt); err != nil {
			return nil, 0, entity.WrapError(entity.CodeUnavailable, err, "failed to list polls")
		}
		poll.Type = entity.PollType(pollType)
		poll.Status = entity.PollStatus(status)
		index[poll.ID] = len(polls)
		ids = append(ids, poll.ID)
		polls = append(polls, poll)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, entity.WrapError(entity.CodeUnavailable, err, "failed to list polls")
	}
	if len(ids) == 0 {
		return polls, total, nil
	}

	optionRows, err := r.db.Query(ctx, `
		SELECT poll_id, id, text, position
		FROM poll_options
		WHERE poll_id = ANY($1::uuid[])
		ORDER BY position, id`, ids)
	if err != nil {
		return nil, 0, entity.WrapError(entity.CodeUnavailable, err, "failed to load poll options")
	}
	defer optionRows.Close()

	for optionRows.Next() {
		var (
			pollID string
			option entity.Option
		)
		if err := optionRows.Scan(&pollID, &option.ID, &option.Text, &option.Position); err != nil {
			return nil, 0, entity.WrapError(entity.CodeUnavailable, err, "failed to load poll options")
		}
		if position, ok := index[pollID]; ok {
			polls[position].Options = append(polls[position].Options, option)
		}
	}
	if err := optionRows.Err(); err != nil {
		return nil, 0, entity.WrapError(entity.CodeUnavailable, err, "failed to load poll options")
	}
	return polls, total, nil
}

// TransitionStatus updates poll status when the current status is allowed.
func (r *PollRepository) TransitionStatus(ctx context.Context, pollID string, from []entity.PollStatus, to entity.PollStatus, at time.Time) (entity.Poll, error) {
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
		return entity.Poll{}, r.explainFailedTransition(ctx, pollID, to)
	}
	if err != nil {
		return entity.Poll{}, entity.WrapError(entity.CodeUnavailable, err, "failed to update poll status")
	}
	return r.GetPoll(ctx, pollID)
}

func (r *PollRepository) explainFailedTransition(ctx context.Context, pollID string, to entity.PollStatus) error {
	var current string
	err := r.db.QueryRow(ctx, `SELECT status FROM polls WHERE id = $1`, pollID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.ErrPollNotFound()
	}
	if err != nil {
		return entity.WrapError(entity.CodeUnavailable, err, "failed to update poll status")
	}
	return entity.NewError(entity.CodeConflict,
		"cannot change poll status from "+current+" to "+string(to))
}

// GetResults returns durable vote counts for a poll.
func (r *PollRepository) GetResults(ctx context.Context, pollID string) (map[string]int64, error) {
	rows, err := r.db.Query(ctx, `
		SELECT option_id, votes FROM poll_results WHERE poll_id = $1`, pollID)
	if err != nil {
		return nil, entity.WrapError(entity.CodeUnavailable, err, "failed to load results")
	}
	defer rows.Close()

	counts := make(map[string]int64)
	for rows.Next() {
		var (
			optionID string
			votes    int64
		)
		if err := rows.Scan(&optionID, &votes); err != nil {
			return nil, entity.WrapError(entity.CodeUnavailable, err, "failed to load results")
		}
		counts[optionID] = votes
	}
	if err := rows.Err(); err != nil {
		return nil, entity.WrapError(entity.CodeUnavailable, err, "failed to load results")
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
		return entity.WrapError(entity.CodeUnavailable, err, "failed to persist results")
	}
	return nil
}
