package service

import (
	"context"
	"encoding/base64"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

const groupModelSchedulingStickyTTL = 0
const GroupModelSchedulingKeyPrefix = "__group_model_scheduler__:"

type GroupModelSchedulingBinding struct {
	Model     string
	AccountID int64
}

type GroupModelSchedulingBindingsReader interface {
	ListGroupModelSchedulingBindings(context.Context, int64) ([]GroupModelSchedulingBinding, error)
}

func groupModelSchedulingStickyKey(model string) string {
	return GroupModelSchedulingKeyPrefix + base64.RawURLEncoding.EncodeToString([]byte(model))
}

func GroupModelSchedulingModelFromKey(key string) (string, bool) {
	encoded := strings.TrimPrefix(key, GroupModelSchedulingKeyPrefix)
	if encoded == key || encoded == "" {
		return "", false
	}
	model, err := base64.RawURLEncoding.DecodeString(encoded)
	return string(model), err == nil && string(model) != ""
}

func groupModelSchedulingActiveAccount(ctx context.Context, cache GatewayCache, groupID *int64, model string) int64 {
	if ctx.Value(schedulingEvaluationKey{}) != nil {
		return 0
	}
	if cache == nil || groupID == nil || model == "" {
		return 0
	}
	accountID, err := cache.GetSessionAccountID(ctx, *groupID, groupModelSchedulingStickyKey(model))
	if err != nil {
		return 0
	}
	return accountID
}

func rememberGroupModelSchedulingAccount(ctx context.Context, cache GatewayCache, groupID *int64, model string, accountID int64) {
	if ctx.Value(schedulingEvaluationKey{}) != nil {
		return
	}
	if cache == nil || groupID == nil || model == "" || accountID <= 0 {
		return
	}
	_ = cache.SetSessionAccountID(ctx, *groupID, groupModelSchedulingStickyKey(model), accountID, groupModelSchedulingStickyTTL)
}

func fastestFailoverCandidateOrder(ctx context.Context, repo UsageLogRepository, cache GatewayCache, groupID *int64, model string, candidates []*Account) []*Account {
	ordered := append([]*Account(nil), candidates...)
	if len(ordered) == 0 {
		return ordered
	}
	activeID := groupModelSchedulingActiveAccount(ctx, cache, groupID, model)
	activeIndex := -1
	hasPreferred := false
	for index, account := range ordered {
		if account.ID == activeID {
			activeIndex = index
		}
		hasPreferred = hasPreferred || accountSchedulingPreferred(account)
	}
	sortAccountsByPriorityAndLastUsed(ordered, false)
	if hasPreferred {
		sort.SliceStable(ordered, func(first, second int) bool {
			candidate, current := ordered[first], ordered[second]
			if accountSchedulingPreferred(candidate) != accountSchedulingPreferred(current) {
				return accountSchedulingPreferred(candidate)
			}
			if candidate.Priority != current.Priority {
				return candidate.Priority < current.Priority
			}
			if evaluation, ok := ctx.Value(schedulingEvaluationKey{}).(map[int64]GroupModelAccountQuality); ok {
				return evaluation[candidate.ID].LatencyMS < evaluation[current.ID].LatencyMS
			}
			return candidate.ID == activeID && current.ID != activeID
		})
		return ordered
	}
	if activeIndex >= 0 {
		for index, account := range ordered {
			if account.ID == activeID {
				ordered[0], ordered[index] = ordered[index], ordered[0]
				return ordered
			}
		}
	}
	quality := groupModelAccountQuality(ctx, repo, groupID, model)
	bestFailureRate := math.Inf(1)
	for _, account := range ordered {
		if sample, ok := quality[account.ID]; ok && sample.LatencyMS > 0 {
			bestFailureRate = math.Min(bestFailureRate, sample.failureRate())
		}
	}
	fastest := math.Inf(1)
	for _, account := range ordered {
		if sample, ok := quality[account.ID]; ok && sample.LatencyMS > 0 && sample.failureRate() <= bestFailureRate+0.02 {
			fastest = math.Min(fastest, sample.LatencyMS)
		}
	}
	qualified := make(map[int64]bool, len(ordered))
	if math.IsInf(bestFailureRate, 1) {
		for _, account := range ordered {
			bestFailureRate = math.Min(bestFailureRate, quality[account.ID].failureRate())
		}
	}
	for _, account := range ordered {
		sample := quality[account.ID]
		qualified[account.ID] = sample.failureRate() <= bestFailureRate+0.02 && (math.IsInf(fastest, 1) || (sample.LatencyMS > 0 && sample.LatencyMS <= fastest*1.2))
	}
	qualityTier := func(account *Account) int {
		if qualified[account.ID] {
			return 0
		}
		return 1
	}
	now := time.Now()
	sort.SliceStable(ordered, func(first, second int) bool {
		candidate, current := ordered[first], ordered[second]
		candidateTier, currentTier := qualityTier(candidate), qualityTier(current)
		if candidateTier != currentTier {
			return candidateTier < currentTier
		}
		if candidateTier == 1 {
			candidateSample, currentSample := quality[candidate.ID], quality[current.ID]
			if candidateSample.failureRate() != currentSample.failureRate() {
				return candidateSample.failureRate() < currentSample.failureRate()
			}
			if candidateSample.LatencyMS != currentSample.LatencyMS {
				return candidateSample.LatencyMS > 0 && (currentSample.LatencyMS <= 0 || candidateSample.LatencyMS < currentSample.LatencyMS)
			}
		}
		candidateRate, currentRate := groupModelSchedulingRate(candidate, now), groupModelSchedulingRate(current, now)
		if candidateRate != currentRate {
			return candidateRate < currentRate
		}
		return false
	})
	return ordered
}

func accountSchedulingPreferred(account *Account) bool {
	preferred, _ := account.Extra["scheduling_preferred"].(bool)
	return preferred
}

func groupModelSchedulingRate(account *Account, now time.Time) float64 {
	if rate, ok := openAIFreshUpstreamBillingRate(account, now); ok && rate >= 0 && !math.IsNaN(rate) && !math.IsInf(rate, 0) {
		return rate
	}
	rate := account.BillingRateMultiplier()
	if math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 1
	}
	return rate
}

type GroupModelAccountQuality struct {
	Successes int64
	Failures  int64
	LatencyMS float64
}

func (sample GroupModelAccountQuality) failureRate() float64 {
	attempts := sample.Successes + sample.Failures
	if attempts < 5 {
		attempts = 5
	}
	return float64(sample.Failures) / float64(attempts)
}

type groupModelAccountQualityRepository interface {
	GetGroupModelAccountQuality(context.Context, int64, string, time.Time) (map[int64]GroupModelAccountQuality, error)
}

func fastestFailoverEnabled(ctx context.Context, groupID *int64) bool {
	if groupID == nil {
		return false
	}
	group, ok := ctx.Value(ctxkey.Group).(*Group)
	return ok && group != nil && group.ID == *groupID && group.AccountSchedulingMode == AccountSchedulingModeFastestFailover
}

func groupModelAccountQuality(ctx context.Context, repo UsageLogRepository, groupID *int64, model string) map[int64]GroupModelAccountQuality {
	if evaluation, ok := ctx.Value(schedulingEvaluationKey{}).(map[int64]GroupModelAccountQuality); ok {
		return evaluation
	}
	if groupID == nil || model == "" {
		return nil
	}
	qualityRepo, ok := repo.(groupModelAccountQualityRepository)
	if !ok {
		return nil
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	quality, err := qualityRepo.GetGroupModelAccountQuality(queryCtx, *groupID, model, time.Now().Add(-24*time.Hour))
	if err != nil {
		return nil
	}
	return quality
}
