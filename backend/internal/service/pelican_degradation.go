package service

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const pelicanDegradationCooldown = 5 * time.Minute

type PelicanDegradationState struct {
	Status    string     `json:"status"`
	SampledAt time.Time  `json:"sampled_at"`
	DueAt     *time.Time `json:"due_at,omitempty"`
}

type PelicanDegradationWork struct {
	PlanID, GroupID, AccountID, SampleID int64
	Model, Action                        string
	SampledAt                            time.Time
	PlanUpdatedAt                        time.Time
}

// These methods share the existing plan lease and persist both delayed work and
// the rolling-hour budget, so a restart or another replica cannot duplicate it.
type pelicanDegradationRepository interface {
	ListDegradationDue(context.Context, time.Time) ([]PelicanDegradationWork, error)
	ClaimDegradation(context.Context, PelicanDegradationWork, time.Time, time.Time, int) (bool, error)
	FinishDegradation(context.Context, PelicanDegradationWork, time.Time, time.Time, bool) error
}

type pelicanDegradationEvidenceRepository interface {
	GetPelicanDegradationStates(context.Context, int64, string) (map[int64]*PelicanDegradationState, error)
}

type pelicanDegradationRunKey struct{}
type pelicanDegradationTargetKey struct{}

func (sample GroupModelAccountQuality) intelligenceDegraded() bool {
	if state := sample.Degradation; state != nil && time.Since(state.SampledAt) < 24*time.Hour {
		return state.Status == "confirmed"
	}
	return sample.IntelligenceVerdict == "degraded"
}

func (sample GroupModelAccountQuality) qualityDegraded() bool {
	if state := sample.Degradation; state != nil && time.Since(state.SampledAt) < 24*time.Hour {
		return state.Status == "confirmed"
	}
	return sample.Quality.tier(time.Now()) == 3
}

func withPelicanDegradationEvidence(ctx context.Context, repo UsageLogRepository, groupID int64, model string, samples map[int64]GroupModelAccountQuality) map[int64]GroupModelAccountQuality {
	reader, ok := repo.(pelicanDegradationEvidenceRepository)
	if !ok {
		return samples
	}
	states, err := reader.GetPelicanDegradationStates(ctx, groupID, model)
	if err != nil {
		// An unavailable confirmation store must not turn a first bad drawing
		// into a confirmed reason to evict a working primary.
		states = make(map[int64]*PelicanDegradationState, len(samples))
		for id := range samples {
			states[id] = &PelicanDegradationState{Status: "pending", SampledAt: time.Now()}
		}
	}
	result := make(map[int64]GroupModelAccountQuality, len(samples)+len(states))
	for id, sample := range samples {
		result[id] = sample
		if states[id] == nil && (sample.IntelligenceVerdict == "degraded" || (sample.Quality != nil && sample.Quality.LatestVerdict == "degraded")) {
			states[id] = &PelicanDegradationState{Status: "pending", SampledAt: time.Now()}
		}
	}
	for id, state := range states {
		sample := result[id]
		sample.Degradation = state
		result[id] = sample
	}
	return result
}

func (s *PelicanGroupTestService) runDegradationDue(ctx context.Context, now time.Time, planID int64) {
	repo, ok := s.repo.(pelicanDegradationRepository)
	if !ok {
		return
	}
	// Each completion may enqueue the replacement's immediate test. The next
	// iteration picks it up without waiting for the hourly Cron. A new degraded
	// account always goes back through the five-minute confirmation delay.
	for {
		work, err := repo.ListDegradationDue(ctx, now)
		if err != nil {
			logger.LegacyPrintf("service.pelican_degradation", "list due failed: %v", err)
			return
		}
		progress := false
		for _, item := range work {
			if planID > 0 && item.PlanID != planID {
				continue
			}
			plan, err := s.repo.GetPlan(ctx, item.PlanID)
			if err != nil || plan == nil || !plan.Enabled || strings.TrimSpace(plan.ModelID) == "" || !pelicanWindowAllows(plan.PelicanConfig, now) {
				continue
			}
			if !plan.UpdatedAt.Equal(item.PlanUpdatedAt) {
				continue
			}
			until := now.Add(pelicanGroupTestLease).Truncate(time.Microsecond)
			limit := s.settings.FastestFailoverSettings(ctx).DegradationTestsPerHour
			claimed, err := repo.ClaimDegradation(ctx, item, now, until, limit)
			if err != nil {
				logger.LegacyPrintf("service.pelican_degradation", "plan=%d claim failed: %v", item.PlanID, err)
			}
			if err != nil || !claimed {
				continue
			}
			s.executeDegradation(plan, item, until, repo)
			progress = true
		}
		if !progress || ctx.Err() != nil {
			return
		}
		now = s.now()
	}
}

func (s *PelicanGroupTestService) executeDegradation(plan *PelicanGroupTestPlan, work PelicanDegradationWork, until time.Time, repo pelicanDegradationRepository) {
	ctx, cancel := context.WithTimeout(context.Background(), pelicanGroupTestRunTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, pelicanDegradationRunKey{}, work)
	if work.Action == "retry" {
		ctx = context.WithValue(ctx, pelicanDegradationTargetKey{}, work.AccountID)
		// A confirmation sample cannot change the active account.
		ctx = context.WithValue(ctx, qualitySchedulingProbeKey{}, true)
	}
	group, err := s.groups.GetByID(ctx, plan.GroupID)
	var result *PelicanGroupTestResult
	if err == nil && group != nil && group.Status == StatusActive && usesFailoverScheduling(group.AccountSchedulingMode) {
		result = s.runSample(ctx, plan, group)
	} else {
		result = s.newResult(plan, s.now(), s.now(), "group_unavailable")
	}
	saveCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	completed := result == nil
	if result != nil {
		if saved, err := s.repo.CreateResult(saveCtx, result); err == nil {
			s.showcase.PublishGroupResult(saveCtx, plan.GroupID, saved)
			completed = saved.AccountID > 0
		} else {
			logger.LegacyPrintf("service.pelican_degradation", "plan=%d save failed: %v", plan.ID, err)
		}
	}
	if err := repo.FinishDegradation(saveCtx, work, until, s.now(), completed); err != nil {
		logger.LegacyPrintf("service.pelican_degradation", "plan=%d finish failed: %v", plan.ID, err)
	}
	_ = s.repo.Finish(saveCtx, plan.ID, until, s.now())
	_ = s.repo.PruneResults(saveCtx, plan.ID, pelicanGroupTestKeepResults)
	var accountID int64
	verdict := "skipped"
	if result != nil {
		accountID, verdict = result.AccountID, result.QualityVerdict
	}
	logger.LegacyPrintf("service.pelican_degradation", "plan=%d group=%d model=%s original=%d action=%s account=%d verdict=%s completed=%t", plan.ID, plan.GroupID, work.Model, work.AccountID, work.Action, accountID, verdict, completed)
}
