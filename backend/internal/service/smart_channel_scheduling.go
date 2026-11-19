package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type smartChannelForwardScopeKey struct{}
type smartChannelFailure struct {
	scope     groupModelSchedulingScope
	accountID int64
	preserve  bool
}

type smartChannelStrikeCache interface {
	SmartChannelStrike(ctx context.Context, groupID int64, model string, accountID, revision int64, success bool) (first bool, err error)
}

// The first plain 502 can spill just this request. Two independent failures
// before a success (within the strike TTL) restore normal failover immediately.
func recordSmartChannelFailure(ctx context.Context, accountID int64, err error) {
	scope, ok := ctx.Value(smartChannelForwardScopeKey{}).(groupModelSchedulingScope)
	var failure *UpstreamFailoverError
	if !ok || scope.model == "" || !errors.As(err, &failure) || ctx.Err() != nil {
		return
	}
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	if state == nil {
		return
	}
	soft := failure.StatusCode == http.StatusBadGateway && (failure.Stage == "" || failure.Stage == GatewayFailureStageInference) && failure.Scope == "" && failure.Reason == "" && failure.ShouldRetryNextAccount()
	state.mu.Lock()
	defer state.mu.Unlock()
	prior := &state.smartFailure
	if prior.scope == scope && prior.accountID > 0 {
		// A second submission on the same request is not independent evidence.
		// A concrete hard failure on its original account supersedes a 502.
		if prior.accountID == accountID && !soft && failure.Reason != "fastest_failover_retry_limit" {
			prior.preserve = false
		}
		return
	}
	*prior = smartChannelFailure{scope: scope, accountID: accountID}
	snapshot := state.snapshots[scope]
	if strikes, ok := snapshot.cache.(smartChannelStrikeCache); soft && snapshot.valid && snapshot.accountID > 0 && ok {
		first, err := strikes.SmartChannelStrike(ctx, scope.groupID, scope.model, accountID, snapshot.revision, false)
		prior.preserve = err == nil && first
	}
}

func smartChannelTemporaryRetry(ctx context.Context, groupID int64) bool {
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	if state == nil || !smartChannelEnabled(ctx) {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.smartFailure.preserve && state.smartFailure.scope.groupID == groupID
}

func smartChannelEnabled(ctx context.Context) bool {
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	return group != nil && group.AccountSchedulingMode == AccountSchedulingModeSmartChannel
}

// Unknown, stale or differently tested reasoning efforts remain available for
// fault recovery, but must not receive deliberate trials on healthy traffic.
func smartChannelTrialEligible(ctx context.Context, sample GroupModelAccountQuality) bool {
	if sample.Quality.tier(time.Now()) > 1 {
		return false
	}
	effort := sample.Quality.Effort
	if requested := RequestedReasoningEffortFromContext(ctx); requested != nil {
		effort = *requested
	}
	return effort == "" || sample.Quality.TestEffort == effort
}

// Empty mappings and passthrough are transport policies, not declarations that
// a relay can serve every public model. An explicit group rule is checked by
// modelAccountCapability before this fallback is used.
func declaredSmartChannelCapability(account *Account, model string) (bool, string) {
	if account == nil || strings.TrimSpace(model) == "" {
		return false, "unknown"
	}
	if _, matched := account.ResolveMappedModel(model); matched {
		return true, "model_mapping"
	}
	upstream := account.GetMappedModel(model)
	if account.IsOpenAICompatible() {
		upstream = resolveOpenAIAccountUpstreamModelForRequest(account, model, false)
	}
	if _, ok := account.GetUpstreamModelMetadata(upstream); ok {
		return true, "model_catalog"
	}
	return false, "unknown"
}

// Recheck after acquiring a slot: a channel's declarations or runtime state may
// have changed while this request was queued. Legacy modes keep their checks.
func recheckSmartChannelAdmission(ctx context.Context, selected *Account, repo AccountRepository, settings *SettingService) (*Account, bool, string) {
	if !smartChannelEnabled(ctx) {
		return selected, false, ""
	}
	if selected == nil || repo == nil {
		return selected, true, "channel_state_unavailable"
	}
	latest, err := repo.GetByID(ctx, selected.ID)
	if err != nil || latest == nil {
		return selected, true, "channel_state_unavailable"
	}
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	model, _ := ctx.Value(ctxkey.Model).(string)
	if strings.TrimSpace(model) == "" {
		model, _ = RequestedPublicModelFromContext(ctx)
	}
	if !settings.IsModelAccountAllowed(ctx, &group.ID, model, latest.ID, latest) {
		return latest, true, "model_not_declared"
	}
	if !latest.IsSchedulableForModelWithContext(ctx, model) {
		return latest, true, "channel_unavailable"
	}
	return latest, false, ""
}

// Queue briefly on the selected channel, then allow a request-local capacity
// spillover. All candidates share this one wait, including subsequent retries.
// Existing Redis wait counters bound the number of pending requests.
func acquireSmartChannelSlot(ctx context.Context, account *Account, concurrency *ConcurrencyService, maxWaiting int, acquire func(context.Context, int64, int) (*AcquireResult, error)) (*AcquireResult, error) {
	result, err := acquire(ctx, account.ID, account.Concurrency)
	if err != nil || result == nil || result.Acquired || !smartChannelEnabled(ctx) || concurrency == nil {
		return result, err
	}
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	if state == nil {
		return result, nil
	}
	state.mu.Lock()
	waited := state.smartCapacityWaited
	state.smartCapacityWaited = true
	state.mu.Unlock()
	if waited {
		return result, nil
	}
	canWait, err := concurrency.IncrementAccountWaitCount(ctx, account.ID, maxWaiting)
	if err != nil || !canWait {
		return result, err
	}
	defer concurrency.DecrementAccountWaitCount(context.WithoutCancel(ctx), account.ID)
	started := time.Now()
	timer := time.NewTimer(schedulingAgentWait(ctx, account.ID))
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			recordSchedulingQueue(ctx, account.ID, time.Since(started))
			return result, nil
		case <-ticker.C:
			next, acquireErr := acquire(ctx, account.ID, account.Concurrency)
			if acquireErr != nil || next == nil || next.Acquired {
				return next, acquireErr
			}
		}
	}
}

// Capacity is not a fault or promotion. A successful spillover must not commit
// the group's primary, or overwrite the healthy session's sticky binding.
func preserveSmartChannelCapacitySelection(ctx context.Context, selection *AccountSelectionResult) *AccountSelectionResult {
	if !smartChannelEnabled(ctx) || selection == nil || selection.Account == nil {
		return selection
	}
	selection.CapacitySpillover = true
	if state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState); state != nil {
		state.mu.Lock()
		state.preservedQualityAccountID = selection.Account.ID
		state.mu.Unlock()
	}
	return selection
}
