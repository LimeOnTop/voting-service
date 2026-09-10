// Package controller implements the HTTP handlers for the voting service.
package controller

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/httpjson"
	"github.com/LimeOnTop/voting-service/internal/service"
	"github.com/go-chi/chi/v5"
)

// Polls is the poll use case used by handlers.
type Polls interface {
	Create(ctx context.Context, cmd service.CreatePollCommand) (entity.Poll, error)
	Get(ctx context.Context, pollID string) (entity.Poll, error)
	List(ctx context.Context, limit, offset int) (service.PollPage, error)
	Activate(ctx context.Context, pollID string) (entity.Poll, error)
	Finish(ctx context.Context, pollID string) (entity.Poll, error)
	Results(ctx context.Context, pollID string) (service.Results, error)
}

// Votes is the vote use case used by handlers.
type Votes interface {
	Cast(ctx context.Context, cmd service.VoteCommand) error
}

// Handlers serves public and admin HTTP endpoints.
type Handlers struct {
	polls        Polls
	votes        Votes
	pollCacheTTL time.Duration
	now          func() time.Time
}

// NewHandlers constructs the HTTP handlers.
func NewHandlers(polls Polls, votes Votes, pollCacheTTL time.Duration, now func() time.Time) *Handlers {
	if now == nil {
		now = time.Now
	}
	return &Handlers{polls: polls, votes: votes, pollCacheTTL: pollCacheTTL, now: now}
}

// GetPoll returns poll configuration without vote counts.
func (h *Handlers) GetPoll(w http.ResponseWriter, r *http.Request) {
	poll, err := h.polls.Get(r.Context(), chi.URLParam(r, "poll_id"))
	if err != nil {
		httpjson.WriteError(w, r, err)
		return
	}

	maxAge := int(h.pollCacheTTL.Seconds())
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(maxAge)+", stale-while-revalidate="+strconv.Itoa(maxAge*2))
	httpjson.WriteJSON(w, r, http.StatusOK, newPollResponse(poll))
}

// CastVote records a ballot.
func (h *Handlers) CastVote(w http.ResponseWriter, r *http.Request) {
	var request voteRequest
	if err := httpjson.DecodeJSON(r, &request); err != nil {
		httpjson.WriteError(w, r, err)
		return
	}

	err := h.votes.Cast(r.Context(), service.VoteCommand{
		PollID:     chi.URLParam(r, "poll_id"),
		OptionIDs:  request.OptionIDs,
		VoterToken: httpjson.VoterToken(r.Context()),
		ClientIP:   httpjson.ClientIP(r.Context()),
	})
	if err != nil {
		httpjson.WriteError(w, r, err)
		return
	}

	httpjson.WriteJSON(w, r, http.StatusCreated, voteResponse{Status: "accepted"})
}

// CreatePoll creates a draft poll.
func (h *Handlers) CreatePoll(w http.ResponseWriter, r *http.Request) {
	var request createPollRequest
	if err := httpjson.DecodeJSON(r, &request); err != nil {
		httpjson.WriteError(w, r, err)
		return
	}
	poll, err := h.polls.Create(r.Context(), request.toCommand())
	if err != nil {
		httpjson.WriteError(w, r, err)
		return
	}
	httpjson.WriteJSON(w, r, http.StatusCreated, newPollResponse(poll))
}

// ListPolls returns a page of polls.
func (h *Handlers) ListPolls(w http.ResponseWriter, r *http.Request) {
	limit, err := intQuery(r, "limit", 0)
	if err != nil {
		httpjson.WriteError(w, r, err)
		return
	}
	offset, err := intQuery(r, "offset", 0)
	if err != nil {
		httpjson.WriteError(w, r, err)
		return
	}

	page, err := h.polls.List(r.Context(), limit, offset)
	if err != nil {
		httpjson.WriteError(w, r, err)
		return
	}

	items := make([]pollResponse, 0, len(page.Items))
	for _, poll := range page.Items {
		items = append(items, newPollResponse(poll))
	}
	httpjson.WriteJSON(w, r, http.StatusOK, pollListResponse{
		Items: items, Total: page.Total, Limit: page.Limit, Offset: page.Offset,
	})
}

// ActivatePoll opens a poll for voting.
func (h *Handlers) ActivatePoll(w http.ResponseWriter, r *http.Request) {
	poll, err := h.polls.Activate(r.Context(), chi.URLParam(r, "poll_id"))
	if err != nil {
		httpjson.WriteError(w, r, err)
		return
	}
	httpjson.WriteJSON(w, r, http.StatusOK, newPollResponse(poll))
}

// FinishPoll closes a poll.
func (h *Handlers) FinishPoll(w http.ResponseWriter, r *http.Request) {
	poll, err := h.polls.Finish(r.Context(), chi.URLParam(r, "poll_id"))
	if err != nil {
		httpjson.WriteError(w, r, err)
		return
	}
	httpjson.WriteJSON(w, r, http.StatusOK, newPollResponse(poll))
}

// GetResults returns aggregated poll results.
func (h *Handlers) GetResults(w http.ResponseWriter, r *http.Request) {
	results, err := h.polls.Results(r.Context(), chi.URLParam(r, "poll_id"))
	if err != nil {
		httpjson.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpjson.WriteJSON(w, r, http.StatusOK, newResultsResponse(results, h.now().UTC()))
}

func intQuery(r *http.Request, name string, fallback int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, entity.WrapError(entity.CodeInvalidRequest, err, name+" must be an integer")
	}
	if value < 0 {
		return 0, entity.NewError(entity.CodeInvalidRequest, name+" must not be negative")
	}
	return value, nil
}
