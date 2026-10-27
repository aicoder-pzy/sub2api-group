package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

type pelicanSwitchTestKey struct{}

// The claim is atomic with checking the current binding. A failed generation
// keeps its cooldown; switching away and back cannot repeatedly spend credits.
type PelicanSwitchTestCache interface {
	GroupModelSchedulingHistoryReader
	ClaimPelicanSwitchTest(context.Context, int64, string, string, string, *GroupModelSchedulingTransition, time.Time, int) (bool, error)
}

type pelicanSwitchTestRepository interface {
	ClaimSwitchTest(context.Context, *PelicanGroupTestPlan, int64, string, time.Time, time.Time) (bool, error)
	ReleaseSwitchTest(context.Context, int64, time.Time) error
	HasRecentSwitchTest(context.Context, int64, int64, string, string, time.Time) (bool, error)
}

type pelicanSwitchTestRoute struct {
	cache PelicanSwitchTestCache
	model string
}

type pelicanSwitchTestRouter interface {
	switchTestRoute(context.Context, *Group, string) (*pelicanSwitchTestRoute, error)
}

// The pre-existing degradation replacement path also tests a new primary.
// Use the same evidence/cooldown gate so it cannot duplicate the new worker.
func (s *PelicanGroupTestService) claimReplacementTest(ctx context.Context, plan *PelicanGroupTestPlan, group *Group, accountID int64, model string) (bool, error) {
	repo, ok := s.repo.(pelicanSwitchTestRepository)
	if !ok {
		return false, nil
	}
	effort := ""
	if plan.PelicanConfig != nil {
		effort = plan.PelicanConfig.ReasoningEffort
	}
	now := s.now()
	recent, err := repo.HasRecentSwitchTest(ctx, group.ID, accountID, model, effort, now)
	if err != nil || recent {
		return false, err
	}
	router, ok := s.router.(pelicanSwitchTestRouter)
	if !ok {
		return false, nil
	}
	route, err := router.switchTestRoute(ctx, group, plan.ModelID)
	if err != nil || route == nil {
		return false, err
	}
	change, err := route.cache.GetGroupModelSchedulingTransition(ctx, group.ID, route.model)
	if err != nil || change == nil || change.AccountID != accountID {
		return false, err
	}
	return route.cache.ClaimPelicanSwitchTest(ctx, group.ID, plan.ModelID, route.model, effort, change, now, s.settings.FastestFailoverSettings(ctx).DegradationTestsPerHour)
}

func (r *gatewayPelicanGroupRouter) switchTestRoute(ctx context.Context, group *Group, model string) (*pelicanSwitchTestRoute, error) {
	_, _, upstream, cache, err := r.routingContext(ctx, group, model)
	if err != nil {
		return nil, err
	}
	claims, ok := cache.(PelicanSwitchTestCache)
	if !ok {
		return nil, nil
	}
	return &pelicanSwitchTestRoute{cache: claims, model: upstream}, nil
}

// RunSwitchTests observes committed primary transitions independently of the
// user's cron. It never creates/enables/edits a plan or advances next_run_at.
func (s *PelicanGroupTestService) RunSwitchTests(ctx context.Context, now time.Time) {
	if s == nil || !s.switchMu.TryLock() {
		return
	}
	defer s.switchMu.Unlock()
	repo, ok := s.repo.(pelicanSwitchTestRepository)
	if !ok {
		return
	}
	router, ok := s.router.(pelicanSwitchTestRouter)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	plans, err := s.repo.ListPlans(ctx)
	if err != nil {
		logger.LegacyPrintf("service.pelican_switch_test", "list plans failed: %v", err)
		return
	}
	if s.switchSlots == nil {
		s.switchSlots = make(chan struct{}, pelicanGroupTestMaxRunning)
	}
	for _, plan := range plans {
		if !plan.Enabled || plan.PelicanConfig == nil || plan.ModelID == "" || !pelicanWindowAllows(plan.PelicanConfig, now) || (plan.RunningUntil != nil && !plan.RunningUntil.Before(now)) {
			continue
		}
		group, err := s.groups.GetByID(ctx, plan.GroupID)
		if err != nil || group == nil || group.Status != StatusActive || group.AccountSchedulingMode != AccountSchedulingModeSmartChannel {
			continue
		}
		route, err := router.switchTestRoute(ctx, group, plan.ModelID)
		if err != nil || route == nil {
			continue
		}
		change, err := route.cache.GetGroupModelSchedulingTransition(ctx, group.ID, route.model)
		if err != nil || change == nil || change.PreviousAccountID <= 0 || change.AccountID <= 0 || change.AccountID == change.PreviousAccountID || change.ChangedAt.After(now) || now.Sub(change.ChangedAt) >= time.Hour {
			continue
		}
		select {
		case s.switchSlots <- struct{}{}:
		default:
			return
		}
		until := now.Add(pelicanGroupTestLease).Truncate(time.Microsecond)
		claimed, err := repo.ClaimSwitchTest(ctx, plan, change.AccountID, route.model, now, until)
		if err != nil || !claimed {
			<-s.switchSlots
			continue
		}
		limit := s.settings.FastestFailoverSettings(ctx).DegradationTestsPerHour
		claimed, err = route.cache.ClaimPelicanSwitchTest(ctx, group.ID, plan.ModelID, route.model, plan.PelicanConfig.ReasoningEffort, change, now, limit)
		if err != nil || !claimed {
			_ = repo.ReleaseSwitchTest(ctx, plan.ID, until)
			<-s.switchSlots
			continue
		}
		s.runs.Add(1)
		go func() {
			defer s.runs.Done()
			defer func() { <-s.switchSlots }()
			s.executeSwitchTest(plan, group, change.AccountID, until)
		}()
	}
}

func (s *PelicanGroupTestService) executeSwitchTest(plan *PelicanGroupTestPlan, group *Group, target int64, until time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), pelicanGroupTestRunTimeout)
	defer cancel()
	// An administrator may disable/edit the plan after its lease was claimed.
	current, err := s.repo.GetPlan(ctx, plan.ID)
	if err != nil || current == nil || !current.Enabled || !current.UpdatedAt.Equal(plan.UpdatedAt) || !pelicanWindowAllows(current.PelicanConfig, s.now()) {
		if repo, ok := s.repo.(pelicanSwitchTestRepository); ok {
			_ = repo.ReleaseSwitchTest(ctx, plan.ID, until)
		}
		return
	}
	group, err = s.groups.GetByID(ctx, group.ID)
	if err != nil || group == nil || group.Status != StatusActive || group.AccountSchedulingMode != AccountSchedulingModeSmartChannel {
		if repo, ok := s.repo.(pelicanSwitchTestRepository); ok {
			_ = repo.ReleaseSwitchTest(ctx, plan.ID, until)
		}
		return
	}
	logger.LegacyPrintf("service.pelican_switch_test", "plan=%d group=%d model=%s account=%d started", plan.ID, group.ID, plan.ModelID, target)
	ctx = context.WithValue(ctx, pelicanSwitchTestKey{}, target)
	ctx = context.WithValue(ctx, pelicanDegradationTargetKey{}, target)
	ctx = context.WithValue(ctx, qualitySchedulingProbeKey{}, true)
	// Handling forbids retrying this sample on a different account.
	ctx = context.WithValue(ctx, pelicanDegradationRunKey{}, PelicanDegradationWork{Action: "switch"})
	result := s.runSample(ctx, plan, group)
	saveCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	if result != nil {
		if saved, err := s.repo.CreateResult(saveCtx, result); err == nil {
			s.showcase.PublishGroupResult(saveCtx, group.ID, saved)
		} else {
			logger.LegacyPrintf("service.pelican_switch_test", "plan=%d save failed: %v", plan.ID, err)
		}
	}
	_ = s.repo.Finish(saveCtx, plan.ID, until, s.now())
	_ = s.repo.PruneResults(saveCtx, plan.ID, pelicanGroupTestKeepResults)
	logger.LegacyPrintf("service.pelican_switch_test", "plan=%d group=%d model=%s account=%d finished", plan.ID, group.ID, plan.ModelID, target)
}
