package service

import (
	"context"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

const modelAutoMinimumSamples = 20

// Each bucket contains complete real requests at one reasoning effort and a
// comparable context size. Test generation times are never used for promotion.
type ModelAutoSpeedBucket struct {
	ContextBucket int     `json:"context_bucket"`
	Samples       int64   `json:"samples"`
	P50MS         float64 `json:"p50_ms"`
	P95MS         float64 `json:"p95_ms"`
	OutputTPS     float64 `json:"output_tps"`
	CostRatio     float64 `json:"cost_ratio"`
	OutputSamples int64   `json:"output_samples"`
}

type ModelAutoAccountEvidence struct {
	Effort  string                 `json:"effort"`
	Buckets []ModelAutoSpeedBucket `json:"buckets"`
}

type ModelAutoReview struct {
	PrimaryID      int64  `json:"primary_id"`
	Revision       int64  `json:"revision"`
	CandidateID    int64  `json:"candidate_id"`
	Evaluations    int    `json:"evaluations"`
	LastEvaluated  int64  `json:"last_evaluated"`
	NextEvaluation int64  `json:"next_evaluation"`
	HoldUntil      int64  `json:"hold_until"`
	Ready          bool   `json:"ready"`
	Reason         string `json:"reason"`
}

type ModelAutoReviewInput struct {
	PrimaryID, Revision, CandidateID int64
	ChangedAt, Now                   time.Time
	Reason                           string
}

// Redis owns both the evaluation cadence and the allocation counter. This
// keeps replicas, retries and simultaneous requests from multiplying trials.
type ModelAutoSchedulingCache interface {
	ReviewModelAutoCandidate(context.Context, int64, string, string, ModelAutoReviewInput) (*ModelAutoReview, error)
	GetModelAutoReview(context.Context, int64, string, string) (*ModelAutoReview, error)
	AssignModelAutoSession(context.Context, int64, string, string, int64, []int64, []int64, time.Duration) (int64, bool, error)
}

type modelAutoQualityRepository interface {
	GetModelAutoAccountQuality(context.Context, int64, string, string) (map[int64]GroupModelAccountQuality, error)
}

type modelAutoSessionKey struct{}
type modelAutoPrimaryOnlyKey struct{}

func modelAutoEnabled(ctx context.Context) bool {
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	return group != nil && (group.AccountSchedulingMode == AccountSchedulingModeModelAuto || group.AccountSchedulingMode == AccountSchedulingModeSmartChannel)
}

func withModelAutoSession(ctx context.Context, session string) context.Context {
	if modelAutoEnabled(ctx) {
		return context.WithValue(ctx, modelAutoSessionKey{}, session)
	}
	return ctx
}

func modelAutoPending(sample GroupModelAccountQuality) bool {
	return sample.Degradation != nil && sample.Degradation.Status == "pending" && time.Since(sample.Degradation.SampledAt) < 24*time.Hour
}

func modelAutoHealthy(sample GroupModelAccountQuality, minimum int64) bool {
	return !sample.qualityDegraded() && sample.reliabilityTier(minimum) != 3
}

// A fixed set of shared buckets gives every candidate the same weights. Do
// not sort using pairwise bucket intersections: that is not transitive.
func modelAutoComparable(candidates []*Account, samples map[int64]GroupModelAccountQuality) map[int64]ModelAutoSpeedBucket {
	measured := make([]int64, 0, len(candidates))
	for _, account := range candidates {
		if evidence := samples[account.ID].Auto; evidence != nil {
			var count int64
			for _, bucket := range evidence.Buckets {
				count += bucket.Samples
			}
			if count >= modelAutoMinimumSamples {
				measured = append(measured, account.ID)
			}
		}
	}
	result := make(map[int64]ModelAutoSpeedBucket)
	if len(measured) < 2 {
		return result
	}
	shared := make(map[int]bool)
	for _, bucket := range samples[measured[0]].Auto.Buckets {
		shared[bucket.ContextBucket] = bucket.Samples >= 5
	}
	for _, id := range measured[1:] {
		present := make(map[int]bool)
		for _, bucket := range samples[id].Auto.Buckets {
			present[bucket.ContextBucket] = bucket.Samples >= 5
		}
		for key := range shared {
			shared[key] = shared[key] && present[key]
		}
	}
	for _, id := range measured {
		var average ModelAutoSpeedBucket
		var count float64
		for _, bucket := range samples[id].Auto.Buckets {
			if !shared[bucket.ContextBucket] {
				continue
			}
			average.Samples += bucket.Samples
			average.P50MS += bucket.P50MS
			average.P95MS += bucket.P95MS
			average.OutputTPS += bucket.OutputTPS
			average.CostRatio += bucket.CostRatio
			average.OutputSamples += bucket.OutputSamples
			count++
		}
		if average.Samples < modelAutoMinimumSamples {
			return map[int64]ModelAutoSpeedBucket{}
		}
		if count > 0 {
			average.P50MS /= count
			average.P95MS /= count
			average.OutputTPS /= count
			average.CostRatio /= count
			result[id] = average
		}
	}
	return result
}

func orderModelAutoCandidates(candidates []*Account, samples map[int64]GroupModelAccountQuality, minimum int64) []*Account {
	ordered := append([]*Account(nil), candidates...)
	now := time.Now()
	comparable := modelAutoComparable(candidates, samples)
	allDegraded := len(candidates) > 0
	for _, account := range candidates {
		allDegraded = allDegraded && samples[account.ID].qualityDegraded()
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		x, y := samples[a.ID], samples[b.ID]
		if allDegraded {
			return qualityLatencyLess(x, y, a.ID, b.ID)
		}
		if x.qualityDegraded() != y.qualityDegraded() {
			return !x.qualityDegraded()
		}
		if (x.reliabilityTier(minimum) == 3) != (y.reliabilityTier(minimum) == 3) {
			return x.reliabilityTier(minimum) != 3
		}
		if x.Quality.tier(now) != y.Quality.tier(now) {
			return x.Quality.tier(now) < y.Quality.tier(now)
		}
		qualityA, qualityB := 0.0, 0.0
		if x.Quality != nil {
			qualityA = x.Quality.QualityLowerBound
		}
		if y.Quality != nil {
			qualityB = y.Quality.QualityLowerBound
		}
		if qualityBucket(qualityA) != qualityBucket(qualityB) {
			return qualityA > qualityB
		}
		if x.reliabilityTier(minimum) != y.reliabilityTier(minimum) {
			return x.reliabilityTier(minimum) < y.reliabilityTier(minimum)
		}
		if x.RecentFailures != y.RecentFailures {
			return x.RecentFailures < y.RecentFailures
		}
		if qualityBucket(QualitySuccessLowerBound(x.Successes, x.Failures)) != qualityBucket(QualitySuccessLowerBound(y.Successes, y.Failures)) {
			return QualitySuccessLowerBound(x.Successes, x.Failures) > QualitySuccessLowerBound(y.Successes, y.Failures)
		}
		speedA, speedB := comparable[a.ID], comparable[b.ID]
		if (speedA.Samples > 0) != (speedB.Samples > 0) {
			return speedA.Samples > 0
		}
		if speedA.Samples > 0 && speedB.Samples > 0 {
			if speedA.P95MS != speedB.P95MS {
				return speedA.P95MS < speedB.P95MS
			}
			if speedA.P50MS != speedB.P50MS {
				return speedA.P50MS < speedB.P50MS
			}
			if speedA.OutputTPS != speedB.OutputTPS {
				return speedA.OutputTPS > speedB.OutputTPS
			}
			if speedA.CostRatio != speedB.CostRatio {
				return speedA.CostRatio < speedB.CostRatio
			}
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ID < b.ID
	})
	return ordered
}

// Promotion is deliberately stricter than first selection or fault recovery.
// A small 100% sample may be tried, but cannot beat a proven primary on rate alone.
func modelAutoPromotionReason(current, candidate GroupModelAccountQuality) string {
	if modelAutoPending(current) || modelAutoPending(candidate) || !modelAutoHealthy(candidate, modelAutoMinimumSamples) || candidate.Quality == nil || candidate.Quality.tier(time.Now()) > 1 {
		return "quality_unverified"
	}
	if current.Auto == nil || candidate.Auto == nil || current.Auto.Effort != candidate.Auto.Effort {
		return "insufficient_samples"
	}
	matched := modelAutoComparable([]*Account{{ID: 1}, {ID: 2}}, map[int64]GroupModelAccountQuality{1: current, 2: candidate})
	x, y := matched[1], matched[2]
	if x.Samples < modelAutoMinimumSamples || y.Samples < modelAutoMinimumSamples {
		return "insufficient_samples"
	}
	if candidate.failureRate() > current.failureRate()+0.01 || candidate.RecentFailures > current.RecentFailures {
		return "stability_not_better"
	}
	if qualityRecentFailureTier(candidate.Quality, 10) > qualityRecentFailureTier(current.Quality, 10) {
		return "stability_not_better"
	}
	if candidate.Quality.tier(time.Now()) > current.Quality.tier(time.Now()) {
		return "quality_not_better"
	}
	if current.Quality != nil && current.Quality.Normal+current.Quality.Degraded >= 10 && candidate.Quality.Normal+candidate.Quality.Degraded >= 10 && candidate.Quality.QualityLowerBound+0.02 < current.Quality.QualityLowerBound {
		return "quality_not_better"
	}
	if current.Quality.tier(time.Now()) >= 2 && candidate.Quality.NormalStreak >= 2 {
		return "quality"
	}
	if candidate.attempts() >= 20 && current.attempts() >= 20 &&
		QualitySuccessLowerBound(candidate.Successes, candidate.Failures) > 1-QualitySuccessLowerBound(current.Failures, current.Successes)+0.02 {
		return "reliability"
	}
	if x.P95MS > 0 && y.P95MS <= x.P95MS*0.8 && x.P95MS-y.P95MS >= 1000 && y.P50MS <= x.P50MS*1.1 {
		return "latency"
	}
	if x.P95MS <= 0 || y.P95MS > x.P95MS*1.05 || y.P50MS > x.P50MS*1.05 {
		return "no_clear_advantage"
	}
	if x.OutputSamples >= 20 && y.OutputSamples >= 20 && x.OutputTPS > 0 && y.OutputTPS >= x.OutputTPS*1.2 {
		return "output_speed"
	}
	if x.CostRatio > 0 && y.CostRatio > 0 && y.CostRatio <= x.CostRatio*0.85 && y.OutputTPS >= x.OutputTPS*0.95 {
		return "cost"
	}
	return "no_clear_advantage"
}

func modelAutoAdvantage(reason string) bool {
	return reason == "quality" || reason == "reliability" || reason == "latency" || reason == "output_speed" || reason == "cost"
}

func moveModelAutoFirst(accounts []*Account, id int64) []*Account {
	for index, account := range accounts {
		if account.ID == id {
			copy(accounts[1:index+1], accounts[:index])
			accounts[0] = account
			break
		}
	}
	return accounts
}

func orderModelAutoScheduling(ctx context.Context, cache GatewayCache, groupID int64, model string, candidates []*Account, samples map[int64]GroupModelAccountQuality, activeID int64, settings FastestFailoverSettings) []*Account {
	ordered := orderModelAutoCandidates(candidates, samples, int64(settings.MinimumSamples))
	if len(ordered) == 0 {
		return ordered
	}
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	if state == nil {
		return orderSchedulingCandidates(ctx, candidates, samples, activeID, settings, true)
	}
	scope := groupModelSchedulingScope{groupID, model}
	state.mu.Lock()
	state.preservedQualityAccountID = 0
	delete(state.modelAutoUpgrades, scope)
	snapshot := state.snapshots[scope]
	state.mu.Unlock()
	activeAvailable := false
	for _, account := range candidates {
		activeAvailable = activeAvailable || account.ID == activeID
	}
	activeHealthy := activeAvailable && modelAutoHealthy(samples[activeID], int64(settings.MinimumSamples))
	if activeID > 0 && !activeHealthy {
		reason := "原账号不在本次可选范围"
		if activeAvailable {
			reason = "原账号近期请求不稳定"
			if samples[activeID].qualityDegraded() {
				reason = "原账号冷却 5 分钟后复测仍降智"
			}
		}
		recordSchedulingSelectionReason(ctx, activeID, reason)
	}
	primary := ordered[0].ID
	if activeHealthy {
		primary = activeID
	}
	if ctx.Value(modelAutoPrimaryOnlyKey{}) != nil {
		return moveModelAutoFirst(ordered, primary)
	}
	autoCache, ok := cache.(ModelAutoSchedulingCache)
	if !ok {
		return moveModelAutoFirst(ordered, primary)
	}
	var review *ModelAutoReview
	if activeHealthy && snapshot.valid {
		var challenger int64
		reason := ""
		for _, account := range ordered {
			if account.ID != activeID {
				if why := modelAutoPromotionReason(samples[activeID], samples[account.ID]); modelAutoAdvantage(why) {
					challenger, reason = account.ID, why
					break
				}
			}
		}
		var changedAt time.Time
		if reader, ok := cache.(GroupModelSchedulingHistoryReader); ok {
			if transition, err := reader.GetGroupModelSchedulingTransition(ctx, groupID, model); err == nil && transition != nil {
				changedAt = transition.ChangedAt
			}
		}
		effort := ""
		if samples[activeID].Auto != nil {
			effort = samples[activeID].Auto.Effort
		}
		var err error
		review, err = autoCache.ReviewModelAutoCandidate(ctx, groupID, model, effort, ModelAutoReviewInput{PrimaryID: activeID, Revision: snapshot.revision, CandidateID: challenger, ChangedAt: changedAt, Now: time.Now(), Reason: reason})
		if err == nil && review != nil && review.Ready && review.CandidateID == challenger && challenger > 0 {
			primary = challenger
		}
	}
	session, _ := ctx.Value(modelAutoSessionKey{}).(string)
	if session == "" {
		return moveModelAutoFirst(ordered, activeOrReplacement(activeHealthy, activeID, ordered[0].ID))
	}
	valid := make([]int64, 0, len(candidates))
	backups := make([]int64, 0, len(candidates))
	var trialCooldown time.Duration
	if smartChannelEnabled(ctx) {
		trialCooldown = 30 * time.Minute
	}
	for _, account := range candidates {
		if modelAutoHealthy(samples[account.ID], int64(settings.MinimumSamples)) {
			valid = append(valid, account.ID)
			if activeHealthy && primary == activeID && account.ID != activeID && !modelAutoPending(samples[activeID]) && !modelAutoPending(samples[account.ID]) {
				if !smartChannelEnabled(ctx) || smartChannelTrialEligible(ctx, samples[account.ID]) {
					backups = append(backups, account.ID)
				}
			}
		}
	}
	if len(valid) == 0 {
		// All confirmed degraded: keep only the fastest fallback eligible for
		// session retention, so old degraded sessions cannot keep rotating.
		valid = append(valid, primary)
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i] < backups[j] })
	selected, fresh, err := autoCache.AssignModelAutoSession(ctx, groupID, model, session, primary, valid, backups, trialCooldown)
	if err != nil || selected <= 0 {
		return moveModelAutoFirst(ordered, activeOrReplacement(activeHealthy, activeID, ordered[0].ID))
	}
	state.mu.Lock()
	if fresh && review != nil && review.Ready && selected == review.CandidateID && primary != activeID {
		if state.modelAutoUpgrades == nil {
			state.modelAutoUpgrades = make(map[groupModelSchedulingScope]int64)
		}
		state.modelAutoUpgrades[scope] = selected
	} else if activeHealthy || selected != primary {
		state.preservedQualityAccountID = selected
	}
	state.mu.Unlock()
	return moveModelAutoFirst(ordered, selected)
}

func activeOrReplacement(healthy bool, active, replacement int64) int64 {
	if healthy {
		return active
	}
	return replacement
}

// The existing CAS remains the only place that can commit a new primary.
func GroupModelSchedulingConfirmationReason(ctx context.Context, model string, accountID int64) string {
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if state != nil && group != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.modelAutoUpgrades[groupModelSchedulingScope{group.ID, model}] == accountID {
			return "model_auto_upgrade"
		}
	}
	return "confirmed_failover"
}
