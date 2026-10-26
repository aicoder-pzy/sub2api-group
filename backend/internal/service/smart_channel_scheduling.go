package service

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

func smartChannelEnabled(ctx context.Context) bool {
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	return group != nil && group.AccountSchedulingMode == AccountSchedulingModeSmartChannel
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
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
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
