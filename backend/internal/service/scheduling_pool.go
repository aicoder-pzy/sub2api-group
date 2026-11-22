package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"time"
)

// Recovery allocations are shared by replicas. Three completed natural trials,
// at most one new session per minute, reopen a deferred member for new traffic.
type SchedulingPoolRecoveryCache interface {
	SchedulingPoolRecovery(context.Context, string, string, string) (int64, error)
}

func currentSchedulingAgentRun(ctx context.Context, scope fixedSchedulingScope) (SchedulingAgentRun, bool) {
	if scope.settings == nil || scope.policy.AgentMode != "auto" || ctx.Value(schedulingAgentStreamRequestKey{}) == nil || qualityProbe(ctx) {
		return SchedulingAgentRun{}, false
	}
	index := &scope.settings.schedulingIndex
	index.mu.Lock()
	defer index.mu.Unlock()
	now := time.Now()
	entry := index.runs[scope.groupID]
	if now.After(entry.until) {
		read, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		run, err := scope.settings.GetSchedulingAgentRun(read, scope.groupID)
		connection, configErr := readSchedulingJSON[SchedulingAgentConnection](read, scope.settings.settingRepo, schedulingAgentConnectionKey)
		if err != nil || configErr != nil || connection.APIKey == "" || connection.Version != run.ConnectionVersion {
			return SchedulingAgentRun{}, false
		}
		entry = schedulingAgentRunEntry{run, now.Add(3 * time.Second)}
		if index.runs == nil {
			index.runs = make(map[int64]schedulingAgentRunEntry)
		}
		index.runs[scope.groupID] = entry
	}
	return entry.run, entry.run.Version == scope.policy.Version
}

func applySchedulingPoolAction(ctx context.Context, scope fixedSchedulingScope, snapshot fixedSessionSnapshot, candidates []*Account, samples map[int64]GroupModelAccountQuality) ([]*Account, int64) {
	// No reliable session identity means no model-directed traffic movement.
	if len(scope.policy.Pools) == 0 || snapshot.cache == nil || snapshot.account > 0 || len(candidates) < 2 {
		return candidates, 0
	}
	run, ok := currentSchedulingAgentRun(ctx, scope)
	if !ok {
		return candidates, 0
	}
	entries := append([]SchedulingAgentRun{run}, run.History...)
	for _, action := range entries {
		if action.Version != scope.policy.Version || action.ConnectionVersion != run.ConnectionVersion || action.Decision.Action != "defer_member" || action.Decision.Model != scope.model {
			continue
		}
		if action.State != "applied" && action.State != "expired" && action.State != "reassessed" {
			continue
		}
		if time.Since(action.Expires) > 30*time.Minute {
			continue
		}
		id := action.Decision.AccountID
		index := slices.IndexFunc(candidates, func(a *Account) bool { return a.ID == id })
		if index < 0 {
			continue
		}
		// A fresh healthy alternative is required again at request time. The model
		// cannot turn stale evidence into an outage or exclude the last candidate.
		backup := int64(0)
		for _, a := range candidates {
			s := samples[a.ID]
			if a.ID != id && s.Successes >= 5 && s.RecentFailures == 0 && s.Failures <= s.Successes/10 && !s.qualityDegraded() {
				backup = a.ID
				break
			}
		}
		if backup == 0 {
			continue
		}
		state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
		if state == nil {
			return candidates, 0
		}
		if time.Now().Before(action.Expires) {
			r := scope.settings.schedulingAgent
			r.mu.Lock()
			count := r.actions[scope.groupID]
			if !count.at.Equal(action.At) {
				count = schedulingActionCount{at: action.At}
			}
			if count.failed >= 2 && count.failed*10 > count.completed+count.failed {
				r.mu.Unlock()
				return candidates, 0
			}
			count.count++
			r.actions[scope.groupID] = count
			r.mu.Unlock()
			state.mu.Lock()
			state.agentEffect = schedulingActionEffect{runtime: r, group: scope.groupID, at: action.At, backup: backup}
			state.mu.Unlock()
		} else if !slices.ContainsFunc(candidates[:index], func(a *Account) bool { return samples[a.ID].reliabilityTier(10) != 3 }) {
			cache, ok := snapshot.cache.(SchedulingPoolRecoveryCache)
			if !ok {
				return candidates, 0
			}
			key := fmt.Sprintf("scheduler:pool-recovery:%d:%d:%d:%x", scope.groupID, action.At.UnixNano(), id, sha256.Sum256([]byte(scope.model)))
			owner := fmt.Sprintf("%s:%d", snapshot.key, time.Now().UnixNano())
			read, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
			allocation, err := cache.SchedulingPoolRecovery(read, key, "claim", owner)
			cancel()
			if err != nil {
				return candidates, 0
			}
			if allocation == 1 {
				return candidates, id
			}
			if allocation == 2 {
				state.mu.Lock()
				state.agentEffect = schedulingActionEffect{backup: id, recovery: cache, recoveryKey: key, recoveryOwner: owner}
				state.mu.Unlock()
				return candidates, id
			}
		}
		result := append([]*Account(nil), candidates[:index]...)
		return append(result, candidates[index+1:]...), 0
	}
	return candidates, 0
}
