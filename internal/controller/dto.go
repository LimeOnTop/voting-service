package controller

import (
	"math"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/service"
)

type optionResponse struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Position int    `json:"position"`
}

type pollResponse struct {
	ID         string           `json:"id"`
	Question   string           `json:"question"`
	Type       string           `json:"type"`
	MaxChoices int              `json:"max_choices"`
	Status     string           `json:"status"`
	Options    []optionResponse `json:"options"`
	CreatedAt  time.Time        `json:"created_at"`
	StartsAt   *time.Time       `json:"starts_at,omitempty"`
	EndsAt     *time.Time       `json:"ends_at,omitempty"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
}

func newPollResponse(poll entity.Poll) pollResponse {
	options := make([]optionResponse, 0, len(poll.Options))
	for _, option := range poll.Options {
		options = append(options, optionResponse{ID: option.ID, Text: option.Text, Position: option.Position})
	}
	return pollResponse{
		ID:         poll.ID,
		Question:   poll.Question,
		Type:       string(poll.Type),
		MaxChoices: poll.MaxChoices,
		Status:     string(poll.Status),
		Options:    options,
		CreatedAt:  poll.CreatedAt,
		StartsAt:   poll.StartsAt,
		EndsAt:     poll.EndsAt,
		FinishedAt: poll.FinishedAt,
	}
}

type pollListResponse struct {
	Items  []pollResponse `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

type createPollRequest struct {
	Question   string     `json:"question"`
	Type       string     `json:"type,omitempty"`
	MaxChoices int        `json:"max_choices,omitempty"`
	Options    []string   `json:"options"`
	StartsAt   *time.Time `json:"starts_at,omitempty"`
	EndsAt     *time.Time `json:"ends_at,omitempty"`
}

func (r createPollRequest) toCommand() service.CreatePollCommand {
	return service.CreatePollCommand{
		Question:   r.Question,
		Type:       entity.PollType(r.Type),
		MaxChoices: r.MaxChoices,
		Options:    r.Options,
		StartsAt:   r.StartsAt,
		EndsAt:     r.EndsAt,
	}
}

type voteRequest struct {
	OptionIDs []string `json:"option_ids"`
}

type voteResponse struct {
	Status string `json:"status"`
}

type optionResultResponse struct {
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	Votes      int64   `json:"votes"`
	Percentage float64 `json:"percentage"`
}

type resultsResponse struct {
	PollID      string                 `json:"poll_id"`
	Question    string                 `json:"question"`
	Status      string                 `json:"status"`
	TotalVotes  int64                  `json:"total_votes"`
	Options     []optionResultResponse `json:"options"`
	GeneratedAt time.Time              `json:"generated_at"`
}

func newResultsResponse(results service.Results, generatedAt time.Time) resultsResponse {
	options := make([]optionResultResponse, 0, len(results.Poll.Options))
	for _, option := range results.Poll.Options {
		votes := results.Counts[option.ID]
		options = append(options, optionResultResponse{
			ID:         option.ID,
			Text:       option.Text,
			Votes:      votes,
			Percentage: percentage(votes, results.Total),
		})
	}
	return resultsResponse{
		PollID:      results.Poll.ID,
		Question:    results.Poll.Question,
		Status:      string(results.Poll.Status),
		TotalVotes:  results.Total,
		Options:     options,
		GeneratedAt: generatedAt,
	}
}

func percentage(votes, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return math.Round(float64(votes)*10000/float64(total)) / 100
}
