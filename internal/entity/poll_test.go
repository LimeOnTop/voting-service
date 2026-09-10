package entity_test

import (
	"testing"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
)

func singlePoll() entity.Poll {
	return entity.Poll{
		ID:         "poll",
		Type:       entity.PollTypeSingle,
		MaxChoices: 1,
		Status:     entity.PollStatusActive,
		Options: []entity.Option{
			{ID: "a", Text: "A", Position: 0},
			{ID: "b", Text: "B", Position: 1},
		},
	}
}

func TestValidateBallot(t *testing.T) {
	multiple := singlePoll()
	multiple.Type = entity.PollTypeMultiple
	multiple.MaxChoices = 2

	tests := map[string]struct {
		poll      entity.Poll
		optionIDs []string
		wantCode  entity.ErrorCode
	}{
		"single choice accepted":       {singlePoll(), []string{"a"}, ""},
		"empty ballot rejected":        {singlePoll(), nil, entity.CodeInvalidRequest},
		"unknown option rejected":      {singlePoll(), []string{"zz"}, entity.CodeInvalidRequest},
		"second choice rejected":       {singlePoll(), []string{"a", "b"}, entity.CodeInvalidRequest},
		"multiple choice accepted":     {multiple, []string{"a", "b"}, ""},
		"duplicate selection rejected": {multiple, []string{"a", "a"}, entity.CodeInvalidRequest},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := test.poll.ValidateBallot(test.optionIDs)
			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("expected ballot to be accepted, got %v", err)
				}
				return
			}
			if got := entity.CodeOf(err); got != test.wantCode {
				t.Fatalf("expected code %q, got %q (err=%v)", test.wantCode, got, err)
			}
		})
	}
}

// A multiple-choice poll with no explicit limit must accept every option.
func TestValidateBallotUnboundedMultipleChoice(t *testing.T) {
	poll := singlePoll()
	poll.Type = entity.PollTypeMultiple
	poll.MaxChoices = 0

	if err := poll.ValidateBallot([]string{"a", "b"}); err != nil {
		t.Fatalf("expected ballot to be accepted, got %v", err)
	}
}

func TestCheckOpenAt(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	before := now.Add(-time.Hour)
	after := now.Add(time.Hour)

	tests := map[string]struct {
		mutate   func(*entity.Poll)
		wantCode entity.ErrorCode
	}{
		"active poll is open":        {func(*entity.Poll) {}, ""},
		"draft poll is closed":       {func(p *entity.Poll) { p.Status = entity.PollStatusDraft }, entity.CodeConflict},
		"finished poll is closed":    {func(p *entity.Poll) { p.Status = entity.PollStatusFinished }, entity.CodeConflict},
		"window not started":         {func(p *entity.Poll) { p.StartsAt = &after }, entity.CodeConflict},
		"window already ended":       {func(p *entity.Poll) { p.EndsAt = &before }, entity.CodeConflict},
		"inside the declared window": {func(p *entity.Poll) { p.StartsAt, p.EndsAt = &before, &after }, ""},
		"window closes exactly now":  {func(p *entity.Poll) { p.EndsAt = &now }, entity.CodeConflict},
		"window opens exactly now":   {func(p *entity.Poll) { p.StartsAt = &now }, ""},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			poll := singlePoll()
			test.mutate(&poll)

			err := poll.CheckOpenAt(now)
			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("expected poll to be open, got %v", err)
				}
				return
			}
			if got := entity.CodeOf(err); got != test.wantCode {
				t.Fatalf("expected code %q, got %q", test.wantCode, got)
			}
		})
	}
}

// Errors that did not originate in the domain must never leak their text to
// clients.
func TestCodeAndMessageOfForeignError(t *testing.T) {
	foreign := errForeign{}
	if got := entity.CodeOf(foreign); got != entity.CodeInternal {
		t.Fatalf("expected internal code, got %q", got)
	}
	if got := entity.MessageOf(foreign); got == foreign.Error() {
		t.Fatalf("internal error text leaked to the client message: %q", got)
	}
}

type errForeign struct{}

func (errForeign) Error() string { return "postgres: password authentication failed for user admin" }
