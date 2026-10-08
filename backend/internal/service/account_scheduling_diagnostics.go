package service

import (
	"context"
	"errors"
	"math"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type SchedulingCandidateStatus struct {
	AccountID       int64      `json:"account_id"`
	Name            string     `json:"name"`
	Current         bool       `json:"current"`
	Eligible        bool       `json:"eligible"`
	Rank            int        `json:"rank"`
	Confidence      string     `json:"confidence"`
	Samples         int64      `json:"samples"`
	SuccessRate     *float64   `json:"success_rate"`
	RecentFailures  int64      `json:"recent_failures"`
	LastSuccessAt   *time.Time `json:"last_success_at"`
	LastFailureAt   *time.Time `json:"last_failure_at"`
	CooldownSeconds int64      `json:"cooldown_seconds"`
	Reason          string     `json:"reason,omitempty"`
}

type GroupSchedulingStatus struct {
	Model            string                          `json:"model"`
	CurrentAccountID int64                           `json:"current_account_id"`
	MinimumSamples   int                             `json:"minimum_samples"`
	Transition       *GroupModelSchedulingTransition `json:"transition"`
	Candidates       []SchedulingCandidateStatus     `json:"candidates"`
}

func (s *GatewayService) GroupSchedulingStatus(ctx context.Context, tests *AccountTestService, groupID int64, model string) (*GroupSchedulingStatus, error) {
	if err := ValidateSchedulingProbeModel(model); err != nil {
		return nil, err
	}
	group, err := s.groupRepo.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group == nil || group.Status != StatusActive || group.AccountSchedulingMode != AccountSchedulingModeFastestFailover {
		return nil, infraerrors.BadRequest("INVALID_SCHEDULING_MODE", "Choose an active fastest failover group")
	}
	if !group.ModelAllowlist.Allows(model) {
		return nil, infraerrors.BadRequest("MODEL_NOT_ALLOWED", "Model is not allowed by this group")
	}
	if s.cache == nil {
		return nil, errors.New("scheduling cache unavailable")
	}
	ctx, upstreamModel, selectAccount, err := s.groupSchedulingSelector(ctx, tests, group, model)
	if err != nil {
		return nil, err
	}
	accounts, err := s.accountRepo.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	settings := s.settingService.FastestFailoverSettings(ctx)
	quality := groupModelAccountQuality(ctx, s.usageLogRepo, &groupID, upstreamModel)
	activeID := groupModelSchedulingActiveAccount(ctx, s.cache, &groupID, upstreamModel)
	result := &GroupSchedulingStatus{Model: model, CurrentAccountID: activeID, MinimumSamples: settings.MinimumSamples, Candidates: make([]SchedulingCandidateStatus, 0, len(accounts))}
	if reader, ok := s.cache.(GroupModelSchedulingHistoryReader); ok {
		result.Transition, err = reader.GetGroupModelSchedulingTransition(ctx, groupID, upstreamModel)
		if err != nil {
			return nil, err
		}
	}
	eligible := make([]*Account, 0, len(accounts))
	statuses := make(map[int64]SchedulingCandidateStatus, len(accounts))
	for i := range accounts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		account := &accounts[i]
		sample := quality[account.ID]
		status := SchedulingCandidateStatus{AccountID: account.ID, Name: account.Name, Current: account.ID == activeID,
			Confidence: sample.confidence(int64(settings.MinimumSamples)), Samples: sample.attempts(), RecentFailures: sample.RecentFailures,
			LastSuccessAt: sample.LastSuccessAt, LastFailureAt: sample.LastFailureAt}
		if sample.attempts() >= int64(settings.MinimumSamples) {
			rate := 1 - sample.failureRate()
			status.SuccessRate = &rate
		}
		remaining := account.GetModelRateLimitRemainingTimeWithContext(ctx, upstreamModel)
		for _, until := range []*time.Time{account.RateLimitResetAt, account.OverloadUntil, account.TempUnschedulableUntil} {
			if until != nil && time.Until(*until) > remaining {
				remaining = time.Until(*until)
			}
		}
		status.CooldownSeconds = int64(math.Max(0, math.Ceil(remaining.Seconds())))
		excluded := make(map[int64]struct{}, len(accounts))
		for _, other := range accounts {
			if other.ID != account.ID {
				excluded[other.ID] = struct{}{}
			}
		}
		selected, selectionErr := selectAccount(excluded)
		status.Eligible = !account.IsSyntheticUITest() && selectionErr == nil && selected != nil && selected.ID == account.ID
		if status.Eligible {
			eligible = append(eligible, account)
		} else {
			status.Reason = "unavailable"
			if status.CooldownSeconds > 0 {
				status.Reason = "cooldown"
			}
			if account.Status != StatusActive || !account.Schedulable {
				status.Reason = "paused"
			}
		}
		statuses[account.ID] = status
	}
	for index, account := range orderFastestFailoverCandidates(eligible, quality, activeID, settings) {
		status := statuses[account.ID]
		status.Rank = index + 1
		result.Candidates = append(result.Candidates, status)
	}
	for _, account := range accounts {
		if !statuses[account.ID].Eligible {
			result.Candidates = append(result.Candidates, statuses[account.ID])
		}
	}
	return result, nil
}
