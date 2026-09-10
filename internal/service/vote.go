package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/usecase"
	"github.com/google/uuid"
)

// VoteCommand is one submitted ballot.
type VoteCommand struct {
	PollID     string
	OptionIDs  []string
	VoterToken string
	ClientIP   string
}

// VoteService accepts ballots on the Redis hot path.
type VoteService struct {
	polls usecase.PollLoader
	store usecase.VoteStore
	now   func() time.Time
}

// NewVoteService constructs a vote service.
func NewVoteService(polls usecase.PollLoader, store usecase.VoteStore, now func() time.Time) *VoteService {
	if now == nil {
		now = time.Now
	}
	return &VoteService{polls: polls, store: store, now: now}
}

// Cast validates and records a ballot.
func (s *VoteService) Cast(ctx context.Context, cmd VoteCommand) error {
	if uuid.Validate(cmd.PollID) != nil {
		return entity.ErrPollNotFound()
	}
	if strings.TrimSpace(cmd.VoterToken) == "" {
		return entity.NewError(entity.CodeInvalidRequest, "voter token is required")
	}

	poll, err := s.polls.Poll(ctx, cmd.PollID)
	if err != nil {
		return fmt.Errorf("cast vote: %w", err)
	}
	if err := poll.CheckOpenAt(s.now().UTC()); err != nil {
		return fmt.Errorf("cast vote: %w", err)
	}
	if err := poll.ValidateBallot(cmd.OptionIDs); err != nil {
		return fmt.Errorf("cast vote: %w", err)
	}

	verdict, err := s.store.Guard(ctx, usecase.GuardRequest{ClientIP: cmd.ClientIP, PollID: cmd.PollID})
	if err != nil {
		return fmt.Errorf("cast vote: %w", err)
	}
	switch verdict {
	case usecase.VerdictRateLimited:
		return entity.NewError(entity.CodeRateLimited, "too many requests, please retry shortly")
	case usecase.VerdictQuotaExceeded:
		return entity.NewError(entity.CodeQuotaExceeded, "vote limit for this network has been reached")
	case usecase.VerdictAllow:
	}

	if err := s.store.CastBallot(ctx, usecase.Ballot{
		PollID:    cmd.PollID,
		VoterHash: VoterHash(cmd.PollID, cmd.VoterToken),
		OptionIDs: cmd.OptionIDs,
	}); err != nil {
		return fmt.Errorf("cast vote: %w", err)
	}
	return nil
}

// VoterHash derives the per-poll deduplication identity for a voter token.
func VoterHash(pollID, voterToken string) string {
	digest := sha256.New()
	digest.Write([]byte(pollID))
	digest.Write([]byte{0})
	digest.Write([]byte(voterToken))
	return hex.EncodeToString(digest.Sum(nil)[:16])
}
