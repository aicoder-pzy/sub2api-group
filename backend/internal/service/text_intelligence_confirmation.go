package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

type TextIntelligenceRetry struct {
	PlanID, AccountID, ResultID int64
	PlanUpdatedAt               time.Time
}

type textIntelligenceRetryRepository interface {
	ListTextIntelligenceDue(context.Context, time.Time) ([]TextIntelligenceRetry, error)
	ClaimTextIntelligenceRetry(context.Context, TextIntelligenceRetry, time.Time, time.Time) (bool, error)
	FinishTextIntelligenceRetry(context.Context, TextIntelligenceRetry, time.Time, time.Time) error
}

func (s *PelicanGroupTestService) runTextIntelligenceDue(ctx context.Context, now time.Time) {
	repo, ok := s.repo.(textIntelligenceRetryRepository)
	if !ok {
		return
	}
	work, err := repo.ListTextIntelligenceDue(ctx, now)
	if err != nil {
		logger.LegacyPrintf("service.text_intelligence", "list retries failed: %v", err)
		return
	}
	// One sample at a time, using the existing scheduler slots and plan leases.
	for _, item := range work {
		if ctx.Err() != nil {
			return
		}
		plan, err := s.repo.GetPlan(ctx, item.PlanID)
		if err != nil || plan == nil || !plan.Enabled || !isTextIntelligenceTest(plan.PelicanConfig) ||
			!plan.UpdatedAt.Equal(item.PlanUpdatedAt) || !pelicanWindowAllows(plan.PelicanConfig, now) {
			continue
		}
		until := now.Add(pelicanGroupTestLease).Truncate(time.Microsecond)
		claimed, err := repo.ClaimTextIntelligenceRetry(ctx, item, now, until)
		if err != nil || !claimed {
			continue
		}
		s.executeTextIntelligenceRetry(plan, item, until, repo)
		return
	}
}

func (s *PelicanGroupTestService) executeTextIntelligenceRetry(plan *PelicanGroupTestPlan, work TextIntelligenceRetry, until time.Time, repo textIntelligenceRetryRepository) {
	ctx, cancel := context.WithTimeout(context.Background(), pelicanGroupTestRunTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, pelicanDegradationTargetKey{}, work.AccountID)
	ctx = context.WithValue(ctx, qualitySchedulingProbeKey{}, true)
	// The existing handling flag disables failover after a failed API call.
	ctx = context.WithValue(ctx, pelicanDegradationRunKey{}, PelicanDegradationWork{Action: "retry"})
	group, err := s.groups.GetByID(ctx, plan.GroupID)
	var result *PelicanGroupTestResult
	if err == nil && group != nil && group.Status == StatusActive {
		result = s.runSample(ctx, plan, group)
	} else {
		result = s.newResult(plan, s.now(), s.now(), "group_unavailable")
	}
	saveCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	if result != nil {
		if saved, err := s.repo.CreateResult(saveCtx, result); err == nil {
			s.showcase.PublishGroupResult(saveCtx, plan.GroupID, saved)
		} else {
			logger.LegacyPrintf("service.text_intelligence", "plan=%d save retry failed: %v", plan.ID, err)
		}
	}
	if err := repo.FinishTextIntelligenceRetry(saveCtx, work, until, s.now()); err != nil {
		logger.LegacyPrintf("service.text_intelligence", "plan=%d finish retry failed: %v", plan.ID, err)
	}
	_ = s.repo.Finish(saveCtx, plan.ID, until, s.now())
	_ = s.repo.PruneResults(saveCtx, plan.ID, pelicanGroupTestKeepResults)
}
