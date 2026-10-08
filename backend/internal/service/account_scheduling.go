package service

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
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

type GroupModelSchedulingAtomicCache interface {
	GetSessionAccountState(context.Context, int64, string) (int64, int64, error)
	CompareAndSwapSessionAccountID(context.Context, int64, string, int64, int64) (bool, error)
}

type groupModelSchedulingRequestKey struct{}

type groupModelSchedulingScope struct {
	groupID int64
	model   string
}

type groupModelSchedulingSnapshot struct {
	cache     GroupModelSchedulingAtomicCache
	accountID int64
	revision  int64
	valid     bool
}

type groupModelSchedulingRequestState struct {
	mu        sync.Mutex
	snapshots map[groupModelSchedulingScope]groupModelSchedulingSnapshot
}

// Share the original binding revision across account retries and detached streams.
func WithFastestFailoverRequestState(ctx context.Context) context.Context {
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if group == nil || group.AccountSchedulingMode != AccountSchedulingModeFastestFailover ||
		ctx.Value(groupModelSchedulingRequestKey{}) != nil || ctx.Value(schedulingEvaluationKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, groupModelSchedulingRequestKey{}, &groupModelSchedulingRequestState{
		snapshots: make(map[groupModelSchedulingScope]groupModelSchedulingSnapshot),
	})
}

func prepareFastestFailoverBinding(ctx context.Context, cache GatewayCache, model string) context.Context {
	ctx = WithFastestFailoverRequestState(ctx)
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if group != nil && fastestFailoverEnabled(ctx, &group.ID) {
		groupModelSchedulingActiveAccount(ctx, cache, &group.ID, model)
	}
	return ctx
}

func limitFastestFailoverRetry(ctx context.Context, err error, attempts int) error {
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	var failoverErr *UpstreamFailoverError
	if group != nil && fastestFailoverEnabled(ctx, &group.ID) && errors.As(err, &failoverErr) &&
		failoverErr.RetryableOnSameAccount {
		limited := *failoverErr
		if attempts > 1 {
			limited.RetryableOnSameAccount = false
		} else if limited.SameAccountRetryMax == 0 || limited.SameAccountRetryMax > 1 {
			limited.SameAccountRetryMax = 1
		}
		return &limited
	}
	return err
}

func confirmGroupModelSchedulingAccount(ctx context.Context, model string, accountID int64) {
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if state == nil || group == nil || accountID <= 0 || ctx.Err() != nil || ctx.Value(schedulingEvaluationKey{}) != nil {
		return
	}
	scope := groupModelSchedulingScope{group.ID, model}
	state.mu.Lock()
	snapshot, ok := state.snapshots[scope]
	state.mu.Unlock()
	if !ok || !snapshot.valid || snapshot.accountID == accountID {
		return
	}
	updateCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	changed, err := snapshot.cache.CompareAndSwapSessionAccountID(updateCtx, group.ID,
		groupModelSchedulingStickyKey(model), snapshot.revision, accountID)
	if err != nil {
		logger.LegacyPrintf("service.account_scheduling", "fastest failover binding confirmation failed: group=%d model=%s account=%d error=%v", group.ID, model, accountID, err)
	} else if changed {
		logger.LegacyPrintf("service.account_scheduling", "fastest failover binding confirmed: group=%d model=%s previous=%d account=%d", group.ID, model, snapshot.accountID, accountID)
	}
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
	if atomicCache, ok := cache.(GroupModelSchedulingAtomicCache); ok {
		accountID, revision, err := atomicCache.GetSessionAccountState(ctx, *groupID, groupModelSchedulingStickyKey(model))
		if state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState); state != nil {
			scope := groupModelSchedulingScope{*groupID, model}
			state.mu.Lock()
			if _, captured := state.snapshots[scope]; !captured {
				state.snapshots[scope] = groupModelSchedulingSnapshot{atomicCache, accountID, revision, err == nil}
			}
			state.mu.Unlock()
		}
		if err != nil {
			return 0
		}
		return accountID
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
	if activeIndex >= 0 {
		for index, account := range ordered {
			if account.ID == activeID {
				ordered[0], ordered[index] = ordered[index], ordered[0]
				return ordered
			}
		}
	}
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
