package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/usecase"
	"github.com/google/uuid"
)

// PollLimits bounds admin-supplied poll content and list page sizes.
type PollLimits struct {
	MaxQuestionLength int
	MaxOptionLength   int
	MaxOptions        int
	MaxPageSize       int
	DefaultPageSize   int
}

// CreatePollCommand is the input for creating a poll.
type CreatePollCommand struct {
	Question   string
	Type       entity.PollType
	MaxChoices int
	Options    []string
	StartsAt   *time.Time
	EndsAt     *time.Time
}

// Results is a poll with its current vote counts.
type Results struct {
	Poll   entity.Poll
	Counts map[string]int64
	Total  int64
}

// PollService manages poll lifecycle and result reads.
type PollService struct {
	repo   usecase.PollRepository
	store  usecase.VoteStore
	cache  usecase.PollLoader
	limits PollLimits
	now    func() time.Time
	log    *slog.Logger
}

// NewPollService constructs a poll service.
func NewPollService(repo usecase.PollRepository, store usecase.VoteStore, cache usecase.PollLoader, limits PollLimits, now func() time.Time, log *slog.Logger) *PollService {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &PollService{repo: repo, store: store, cache: cache, limits: limits, now: now, log: log}
}

// Create stores a new poll in draft state.
func (s *PollService) Create(ctx context.Context, cmd CreatePollCommand) (entity.Poll, error) {
	poll, err := s.buildPoll(cmd)
	if err != nil {
		return entity.Poll{}, err
	}
	if err := s.repo.CreatePoll(ctx, poll); err != nil {
		return entity.Poll{}, err
	}
	return poll, nil
}

func (s *PollService) buildPoll(cmd CreatePollCommand) (entity.Poll, error) {
	question := strings.TrimSpace(cmd.Question)
	if question == "" {
		return entity.Poll{}, entity.NewError(entity.CodeInvalidRequest, "question is required")
	}
	if len([]rune(question)) > s.limits.MaxQuestionLength {
		return entity.Poll{}, entity.NewError(entity.CodeInvalidRequest, "question is too long")
	}

	pollType := cmd.Type
	if pollType == "" {
		pollType = entity.PollTypeSingle
	}
	if !pollType.Valid() {
		return entity.Poll{}, entity.NewError(entity.CodeInvalidRequest, "unsupported poll type")
	}

	texts := make([]string, 0, len(cmd.Options))
	seen := make(map[string]struct{}, len(cmd.Options))
	for _, raw := range cmd.Options {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		if len([]rune(text)) > s.limits.MaxOptionLength {
			return entity.Poll{}, entity.NewError(entity.CodeInvalidRequest, "option text is too long")
		}
		if _, duplicate := seen[text]; duplicate {
			return entity.Poll{}, entity.NewError(entity.CodeInvalidRequest, "options must be distinct")
		}
		seen[text] = struct{}{}
		texts = append(texts, text)
	}
	if len(texts) < 2 {
		return entity.Poll{}, entity.NewError(entity.CodeInvalidRequest, "at least two options are required")
	}
	if len(texts) > s.limits.MaxOptions {
		return entity.Poll{}, entity.NewError(entity.CodeInvalidRequest, "too many options")
	}

	maxChoices := cmd.MaxChoices
	switch pollType {
	case entity.PollTypeSingle:
		maxChoices = 1
	case entity.PollTypeMultiple:
		if maxChoices <= 0 {
			maxChoices = len(texts)
		}
		if maxChoices > len(texts) {
			return entity.Poll{}, entity.NewError(entity.CodeInvalidRequest, "max_choices exceeds the number of options")
		}
	}

	if cmd.StartsAt != nil && cmd.EndsAt != nil && !cmd.EndsAt.After(*cmd.StartsAt) {
		return entity.Poll{}, entity.NewError(entity.CodeInvalidRequest, "ends_at must be after starts_at")
	}

	poll := entity.Poll{
		ID:         uuid.NewString(),
		Question:   question,
		Type:       pollType,
		MaxChoices: maxChoices,
		Status:     entity.PollStatusDraft,
		CreatedAt:  s.now().UTC(),
		StartsAt:   cmd.StartsAt,
		EndsAt:     cmd.EndsAt,
		Options:    make([]entity.Option, 0, len(texts)),
	}
	for i, text := range texts {
		poll.Options = append(poll.Options, entity.Option{
			ID:       uuid.NewString(),
			Text:     text,
			Position: i,
		})
	}
	return poll, nil
}

// Get returns poll configuration via the cache.
func (s *PollService) Get(ctx context.Context, pollID string) (entity.Poll, error) {
	if uuid.Validate(pollID) != nil {
		return entity.Poll{}, entity.ErrPollNotFound()
	}
	return s.cache.Poll(ctx, pollID)
}

// PollPage is one page of the admin poll list.
type PollPage struct {
	Items  []entity.Poll
	Total  int
	Limit  int
	Offset int
}

// List returns a page of polls for the admin console.
func (s *PollService) List(ctx context.Context, limit, offset int) (PollPage, error) {
	if limit <= 0 {
		limit = s.limits.DefaultPageSize
	}
	if limit > s.limits.MaxPageSize {
		limit = s.limits.MaxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	polls, total, err := s.repo.ListPolls(ctx, limit, offset)
	if err != nil {
		return PollPage{}, err
	}
	return PollPage{Items: polls, Total: total, Limit: limit, Offset: offset}, nil
}

// Activate opens a draft poll for voting.
func (s *PollService) Activate(ctx context.Context, pollID string) (entity.Poll, error) {
	poll, err := s.transition(ctx, pollID, []entity.PollStatus{entity.PollStatusDraft}, entity.PollStatusActive)
	if err != nil {
		return entity.Poll{}, err
	}
	if err := s.store.TrackPoll(ctx, poll.ID); err != nil {
		return entity.Poll{}, err
	}
	return poll, s.invalidate(ctx, poll.ID)
}

// Finish closes a poll.
func (s *PollService) Finish(ctx context.Context, pollID string) (entity.Poll, error) {
	poll, err := s.transition(ctx, pollID,
		[]entity.PollStatus{entity.PollStatusDraft, entity.PollStatusActive}, entity.PollStatusFinished)
	if err != nil {
		return entity.Poll{}, err
	}
	return poll, s.invalidate(ctx, poll.ID)
}

func (s *PollService) transition(ctx context.Context, pollID string, from []entity.PollStatus, to entity.PollStatus) (entity.Poll, error) {
	if uuid.Validate(pollID) != nil {
		return entity.Poll{}, entity.ErrPollNotFound()
	}
	return s.repo.TransitionStatus(ctx, pollID, from, to, s.now().UTC())
}

func (s *PollService) invalidate(ctx context.Context, pollID string) error {
	if err := s.cache.Invalidate(ctx, pollID); err != nil {
		s.log.WarnContext(ctx, "poll cache invalidation failed",
			"poll_id", pollID, "error", err.Error())
	}
	return nil
}

// Results merges live Redis counters with the durable PostgreSQL snapshot.
func (s *PollService) Results(ctx context.Context, pollID string) (Results, error) {
	poll, err := s.Get(ctx, pollID)
	if err != nil {
		return Results{}, err
	}
	optionIDs := poll.OptionIDs()

	durable, durableErr := s.repo.GetResults(ctx, pollID)
	if durableErr != nil {
		s.log.ErrorContext(ctx, "durable results read failed",
			"poll_id", pollID, "error", durableErr.Error())
	}
	live, liveErr := s.store.Counters(ctx, pollID, optionIDs)
	if liveErr != nil {
		s.log.ErrorContext(ctx, "live results read failed",
			"poll_id", pollID, "error", liveErr.Error())
	}
	if durableErr != nil && liveErr != nil {
		return Results{}, entity.WrapError(entity.CodeUnavailable, liveErr, "results are temporarily unavailable")
	}

	results := Results{Poll: poll, Counts: make(map[string]int64, len(optionIDs))}
	for _, id := range optionIDs {
		count := durable[id]
		if live[id] > count {
			count = live[id]
		}
		results.Counts[id] = count
		results.Total += count
	}
	return results, nil
}
