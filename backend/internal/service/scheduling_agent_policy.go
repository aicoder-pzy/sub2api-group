package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func schedulingPolicyKey(id int64) string   { return fmt.Sprintf("scheduling_agent_group_%d", id) }
func schedulingAgentRunKey(id int64) string { return fmt.Sprintf("scheduling_agent_run_%d", id) }

type SchedulingPolicy struct {
	Accounts   []int64            `json:"accounts"`
	Models     map[string][]int64 `json:"models"`
	AgentMode  string             `json:"agent_mode"`
	DailyCalls int                `json:"daily_calls"`
	MinWaitMS  int                `json:"min_wait_ms"`
	Version    int64              `json:"version"`
}

func (p SchedulingPolicy) order(model string) []int64 {
	if ids, ok := p.Models[model]; ok {
		return ids
	}
	return p.Accounts
}

func (p SchedulingPolicy) validate(members []int64) error {
	if len(p.Accounts) > 32 || len(p.Models) > 64 || p.DailyCalls < 0 || p.DailyCalls > 144 || p.MinWaitMS < 0 || p.MinWaitMS > 2000 {
		return errors.New("invalid scheduling policy limits")
	}
	switch p.AgentMode {
	case "", "off":
	case "observe", "auto":
		if p.AgentMode == "auto" && (p.MinWaitMS < 100 || p.MinWaitMS >= 2000) {
			return errors.New("automatic mode requires a minimum wait between 100 and 1999 ms")
		}
		if len(p.Models) == 0 || p.DailyCalls == 0 {
			return errors.New("select at least one exact model and a daily call budget")
		}
	default:
		return errors.New("invalid agent mode")
	}
	check := func(ids []int64) bool {
		if len(ids) > 32 {
			return false
		}
		for i, id := range ids {
			if id <= 0 || !slices.Contains(members, id) || slices.Contains(ids[:i], id) {
				return false
			}
		}
		return true
	}
	if !check(p.Accounts) {
		return errors.New("default order contains duplicate or out-of-group accounts")
	}
	for model, ids := range p.Models {
		if len(model) > 200 || model == "" || strings.ContainsAny(model, "*? \t\r\n") || len(ids) == 0 || !check(ids) {
			return errors.New("each exact model must have eligible group accounts in an explicit order")
		}
	}
	return nil
}

type schedulingAgentPolicyEntry struct {
	policy  SchedulingPolicy
	expires time.Time
}
type schedulingAgentIndex struct {
	mu       sync.Mutex
	policies map[int64]schedulingAgentPolicyEntry
	runs     map[int64]schedulingAgentRunEntry
}

func readSchedulingJSON[T any](ctx context.Context, repo SettingRepository, key string) (T, error) {
	var result T
	raw, err := repo.GetValue(ctx, key)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		return result, nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &result)
	}
	return result, err
}

func writeSchedulingJSON(ctx context.Context, repo SettingRepository, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return repo.Set(ctx, key, string(raw))
}

func (s *SettingService) GetSchedulingPolicy(ctx context.Context, id int64) (SchedulingPolicy, error) {
	return readSchedulingJSON[SchedulingPolicy](ctx, s.settingRepo, schedulingPolicyKey(id))
}

func (s *SettingService) SetSchedulingPolicy(ctx context.Context, id int64, policy SchedulingPolicy) error {
	runtime := s.schedulingAgent
	if runtime == nil {
		return errors.New("scheduling agent service unavailable")
	}
	group, err := runtime.groups.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if group.AccountSchedulingMode != AccountSchedulingModeSmartChannel {
		return infraerrors.BadRequest("SCHEDULING_POLICY", "Enable Smart Channel scheduling for this group first")
	}
	members, err := runtime.groups.GetAccountIDsByGroupIDs(ctx, []int64{id})
	if err != nil {
		return err
	}
	if err := policy.validate(members); err != nil {
		return infraerrors.BadRequest("SCHEDULING_POLICY", err.Error())
	}
	policy.Version = time.Now().UnixNano()
	if err := writeSchedulingJSON(ctx, s.settingRepo, schedulingPolicyKey(id), policy); err != nil {
		return err
	}
	s.schedulingIndex.mu.Lock()
	delete(s.schedulingIndex.policies, id)
	delete(s.schedulingIndex.runs, id)
	s.schedulingIndex.mu.Unlock()
	return nil
}

func (s *SettingService) schedulingPolicy(ctx context.Context, id int64) SchedulingPolicy {
	if s == nil || s.schedulingAgent == nil {
		return SchedulingPolicy{}
	}
	index := &s.schedulingIndex
	index.mu.Lock()
	defer index.mu.Unlock()
	now := time.Now()
	if entry, ok := index.policies[id]; ok && now.Before(entry.expires) {
		return entry.policy
	}
	readCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	policy, err := s.GetSchedulingPolicy(readCtx, id)
	if err != nil {
		return index.policies[id].policy
	}
	if index.policies == nil {
		index.policies = make(map[int64]schedulingAgentPolicyEntry)
	}
	index.policies[id] = schedulingAgentPolicyEntry{policy, now.Add(5 * time.Second)}
	return policy
}

func schedulingPublicModel(ctx context.Context, model string) string {
	if name, ok := ctx.Value(ctxkey.Model).(string); ok && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	if name, ok := RequestedPublicModelFromContext(ctx); ok {
		return strings.TrimSpace(name)
	}
	return strings.TrimSpace(model)
}

type fixedSchedulingKey struct{}
type fixedSchedulingScope struct {
	settings *SettingService
	groupID  int64
	model    string
	policy   SchedulingPolicy
	order    []int64
}

func (s *SettingService) withFixedScheduling(ctx context.Context, groupID *int64, model string) context.Context {
	if !smartChannelEnabled(ctx) || groupID == nil || qualityProbe(ctx) {
		return ctx
	}
	policy := s.schedulingPolicy(ctx, *groupID)
	model = schedulingPublicModel(ctx, model)
	ids := policy.order(model)
	if len(ids) == 0 {
		return ctx
	}
	return context.WithValue(ctx, fixedSchedulingKey{}, fixedSchedulingScope{s, *groupID, model, policy, ids})
}

func (s fixedSchedulingScope) allows(id int64) bool {
	return len(s.order) == 0 || slices.Contains(s.order, id)
}

func fixedScheduling(ctx context.Context) fixedSchedulingScope {
	scope, _ := ctx.Value(fixedSchedulingKey{}).(fixedSchedulingScope)
	return scope
}

func orderFixedScheduling(ctx context.Context, scope fixedSchedulingScope, cache GatewayCache, candidates []*Account, samples map[int64]GroupModelAccountQuality) []*Account {
	ordered := make([]*Account, 0, len(candidates))
	for _, id := range scope.order {
		for _, candidate := range candidates {
			if id == candidate.ID && !samples[id].qualityDegraded() {
				ordered = append(ordered, candidate)
				break
			}
		}
	}
	if len(ordered) == 0 {
		return nil
	}

	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	if state == nil {
		return ordered
	}
	var snapshot fixedSessionSnapshot
	session, _ := ctx.Value(modelAutoSessionKey{}).(string)
	if session != "" && cache != nil {
		key := fmt.Sprintf("fixed-model:%x", sha256.Sum256([]byte(scope.model+"\x00"+session)))
		bound, err := cache.GetSessionAccountID(ctx, scope.groupID, key)
		if err == nil || errors.Is(err, ErrStickySessionNotFound) {
			snapshot = fixedSessionSnapshot{cache: cache, group: scope.groupID, key: key, account: bound}
			ordered = moveModelAutoFirst(ordered, bound)
		}
	}
	state.mu.Lock()
	state.fixed = true
	state.fixedSession = snapshot
	state.fixedBackup = 0
	if len(ordered) > 1 && ordered[0].ID == scope.order[0] {
		backup := ordered[1]
		sample := samples[backup.ID]
		if sample.Successes >= 5 && sample.RecentFailures == 0 && sample.Failures <= sample.Successes/10 {
			state.fixedBackup = backup.ID
		}
	}
	state.mu.Unlock()
	return ordered
}

type fixedSessionSnapshot struct {
	cache   GatewayCache
	group   int64
	key     string
	account int64
}

type fixedSessionCAS interface {
	CompareAndSwapFixedSession(ctx context.Context, group int64, key string, previous, account int64, ttl time.Duration) error
}

func (s fixedSessionSnapshot) confirm(ctx context.Context, group, account int64, capacity bool) {
	cache, ok := s.cache.(fixedSessionCAS)
	if !ok || s.group != group || (capacity && s.account > 0 && s.account != account) {
		return
	}
	update, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	_ = cache.CompareAndSwapFixedSession(update, group, s.key, s.account, account, openaiStickySessionTTL)
}
