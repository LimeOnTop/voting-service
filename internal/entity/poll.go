// Package entity holds the core voting model.
package entity

import (
	"fmt"
	"time"
)

// PollStatus is the lifecycle state of a poll.
type PollStatus string

const (
	PollStatusDraft    PollStatus = "draft"
	PollStatusActive   PollStatus = "active"
	PollStatusFinished PollStatus = "finished"
)

// Valid reports whether the status is recognised.
func (s PollStatus) Valid() bool {
	switch s {
	case PollStatusDraft, PollStatusActive, PollStatusFinished:
		return true
	default:
		return false
	}
}

// PollType is how many options a ballot may select.
type PollType string

const (
	PollTypeSingle   PollType = "single"
	PollTypeMultiple PollType = "multiple"
)

// Valid reports whether the type is recognised.
func (t PollType) Valid() bool {
	switch t {
	case PollTypeSingle, PollTypeMultiple:
		return true
	default:
		return false
	}
}

// Option is one answer choice on a poll.
type Option struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Position int    `json:"position"`
}

// Poll is a question with its options and lifecycle fields.
type Poll struct {
	ID         string     `json:"id"`
	Question   string     `json:"question"`
	Type       PollType   `json:"type"`
	MaxChoices int        `json:"max_choices"`
	Status     PollStatus `json:"status"`
	Options    []Option   `json:"options"`
	CreatedAt  time.Time  `json:"created_at"`
	StartsAt   *time.Time `json:"starts_at,omitempty"`
	EndsAt     *time.Time `json:"ends_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// OptionIDs returns option identifiers in presentation order.
func (p Poll) OptionIDs() []string {
	ids := make([]string, len(p.Options))
	for i, o := range p.Options {
		ids[i] = o.ID
	}
	return ids
}

// ValidateBallot checks that optionIDs form a valid ballot for this poll.
func (p Poll) ValidateBallot(optionIDs []string) error {
	if len(optionIDs) == 0 {
		return NewError(CodeInvalidRequest, "at least one option must be selected")
	}

	maxChoices := p.effectiveMaxChoices()
	if len(optionIDs) > maxChoices {
		return NewError(CodeInvalidRequest,
			fmt.Sprintf("this poll accepts at most %d option(s) per ballot", maxChoices))
	}

	known := make(map[string]struct{}, len(p.Options))
	for _, o := range p.Options {
		known[o.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(optionIDs))
	for _, id := range optionIDs {
		if _, ok := known[id]; !ok {
			return NewError(CodeInvalidRequest, "option does not belong to this poll")
		}
		if _, dup := seen[id]; dup {
			return NewError(CodeInvalidRequest, "duplicate option in ballot")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (p Poll) effectiveMaxChoices() int {
	if p.Type == PollTypeSingle {
		return 1
	}
	if p.MaxChoices <= 0 || p.MaxChoices > len(p.Options) {
		return len(p.Options)
	}
	return p.MaxChoices
}

// CheckOpenAt reports whether the poll accepts ballots at now.
func (p Poll) CheckOpenAt(now time.Time) error {
	if p.Status != PollStatusActive {
		return NewError(CodeConflict, "poll is not accepting votes")
	}
	if p.StartsAt != nil && now.Before(*p.StartsAt) {
		return NewError(CodeConflict, "voting has not started yet")
	}
	if p.EndsAt != nil && !now.Before(*p.EndsAt) {
		return NewError(CodeConflict, "voting has ended")
	}
	return nil
}

// Result is the durable vote count for one option.
type Result struct {
	PollID   string
	OptionID string
	Votes    int64
}
