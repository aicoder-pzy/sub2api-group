package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func testSchedulingPoolPolicy() SchedulingPolicy {
	return SchedulingPolicy{Version: 11, AgentMode: "auto", DailyCalls: 24, MinWaitMS: 500, ObserveModels: []string{"astra", "sol"}, Pools: []SchedulingPool{
		{ID: "primary", Name: "主力", Accounts: []int64{1, 2}, Models: map[string][]int64{"astra": {2, 1}, "sol": {1}, "disabled": {}}},
		{ID: "secondary", Name: "二用", Accounts: []int64{3}, Models: map[string][]int64{"disabled": {}}},
		{ID: "fallback", Name: "兜底", Accounts: []int64{4}, Models: map[string][]int64{"disabled": {}}},
	}}
}

func TestSchedulingPoolsStayWithinUserOrderAndCapabilities(t *testing.T) {
	p := testSchedulingPoolPolicy()
	require.NoError(t, p.validate([]int64{1, 2, 3, 4}))
	require.Equal(t, []int64{2, 1, 3, 4}, p.order("astra"))
	require.Equal(t, []int64{1, 3, 4}, p.order("sol"))
	require.Empty(t, p.order("disabled"))
	ctx := WithFastestFailoverRequestState(context.Background())
	accounts := []*Account{{ID: 4}, {ID: 3}, {ID: 1}, {ID: 2}, {ID: 99}}
	scope := fixedSchedulingScope{model: "astra", policy: p, order: p.order("astra")}
	ordered := orderFixedScheduling(ctx, scope, nil, accounts, nil)
	require.Equal(t, []int64{2, 1, 3, 4}, accountIDs(ordered))
	// Primary members missing the model are absent from the eligible candidates.
	ordered = orderFixedScheduling(ctx, scope, nil, accounts[:2], nil)
	require.Equal(t, []int64{3, 4}, accountIDs(ordered))
	scope.model, scope.order = "disabled", p.order("disabled")
	require.True(t, scope.enabled())
	require.False(t, scope.allows(1))
	require.Empty(t, orderFixedScheduling(ctx, scope, nil, accounts, nil))
	// No evidence or observation model is needed for deterministic pool routing.
	scope.model, scope.order = "other", p.order("other")
	require.Equal(t, []int64{1, 2, 3, 4}, accountIDs(orderFixedScheduling(ctx, scope, nil, accounts, nil)))
}

func accountIDs(accounts []*Account) []int64 {
	var ids []int64
	for _, a := range accounts {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestSchedulingPoolsRejectAmbiguousMembership(t *testing.T) {
	for _, mutate := range []func(*SchedulingPolicy){
		func(p *SchedulingPolicy) { p.Pools[1].Accounts = []int64{1} },
		func(p *SchedulingPolicy) { p.Pools[1].ID = p.Pools[0].ID },
		func(p *SchedulingPolicy) { p.Pools[0].Models["astra"] = []int64{3} },
		func(p *SchedulingPolicy) { p.Pools[0].Accounts = []int64{99} },
		func(p *SchedulingPolicy) { p.Accounts = []int64{1} },
		func(p *SchedulingPolicy) { p.ObserveModels = []string{"astra*"} },
	} {
		p := testSchedulingPoolPolicy()
		mutate(&p)
		require.Error(t, p.validate([]int64{1, 2, 3, 4}))
	}
}

type poolTestCache struct {
	agentTestSticky
	allocation int64
	claims     int
	outcomes   []string
}

func (c *poolTestCache) SchedulingPoolRecovery(_ context.Context, _, mode, _ string) (int64, error) {
	if mode == "claim" {
		c.claims++
		return c.allocation, nil
	}
	c.outcomes = append(c.outcomes, mode)
	return 0, nil
}

func TestSchedulingPoolAgentIsolationAndRecoveryPreserveSessions(t *testing.T) {
	p := testSchedulingPoolPolicy()
	repo := &agentTestSettings{values: map[string]string{}}
	s := &SettingService{settingRepo: repo}
	r := &schedulingAgentRuntime{settings: s, actions: make(map[int64]schedulingActionCount)}
	s.schedulingAgent = r
	group := &Group{ID: 24, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
	run := SchedulingAgentRun{At: time.Now(), Version: p.Version, ConnectionVersion: 7, State: "applied", Expires: time.Now().Add(5 * time.Minute), Decision: schedulingAgentDecision{Action: "defer_member", Model: "astra", AccountID: 2}}
	ctx := context.Background()
	require.NoError(t, writeSchedulingJSON(ctx, repo, schedulingPolicyKey(group.ID), p))
	require.NoError(t, writeSchedulingJSON(ctx, repo, schedulingAgentConnectionKey, SchedulingAgentConnection{Version: 7, APIKey: "configured"}))
	require.NoError(t, r.storeRun(ctx, group.ID, run))
	samples := map[int64]GroupModelAccountQuality{1: {Successes: 20}, 2: {Successes: 20, Failures: 2, RecentFailures: 2}, 3: {Successes: 20}, 4: {Successes: 20}}
	request := func(model string, cache *poolTestCache, candidates ...*Account) (context.Context, []*Account) {
		c := WithFastestFailoverRequestState(context.WithValue(ctx, ctxkey.Group, group))
		c = withModelAutoSession(s.withFixedScheduling(c, &group.ID, model), "stable-session")
		return c, fastestFailoverCandidateOrder(c, accountSchedulingQualityStub{quality: samples}, cache, &group.ID, model, candidates)
	}
	accounts := []*Account{{ID: 4}, {ID: 3}, {ID: 2}, {ID: 1}}
	cache := &poolTestCache{}
	c, ordered := request("astra", cache, accounts...)
	require.Equal(t, []int64{1, 3, 4}, accountIDs(ordered), "other primary member precedes the secondary pool")
	confirmGroupModelSchedulingAccount(c, "astra", 1)
	require.Equal(t, int64(1), cache.id)
	cache.id = 2
	c, ordered = request("astra", cache, accounts...)
	require.Equal(t, []int64{2, 1, 3, 4}, accountIDs(ordered), "healthy existing sessions are never moved by an agent action")
	state := c.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	state.smartFailure = smartChannelFailure{scope: groupModelSchedulingScope{group.ID, "astra"}, preserve: true}
	confirmGroupModelSchedulingAccount(c, "astra", 1)
	require.EqualValues(t, 2, cache.id, "one isolated transient failure cannot migrate a warm session")
	cache.id = 0
	_, ordered = request("sol", cache, accounts...)
	require.Equal(t, []int64{1, 3, 4}, accountIDs(ordered), "isolation is exact-model scoped")
	_, ordered = request("astra", cache, &Account{ID: 2})
	require.Equal(t, []int64{2}, accountIDs(ordered), "agent cannot remove the last candidate")
	run.Expires = time.Now().Add(-time.Minute)
	failedAt := time.Now().Add(-time.Minute)
	samples[2] = GroupModelAccountQuality{Successes: 20, Failures: 2, RecentFailures: 2, LastFailureAt: &failedAt}
	require.NoError(t, r.storeRun(ctx, group.ID, run))
	_, ordered = request("astra", cache, accounts...)
	require.Equal(t, []int64{1, 3, 4}, accountIDs(ordered), "recovery waits for a shared trial allocation")
	cache.allocation = 2
	c, ordered = request("astra", cache, accounts...)
	require.Equal(t, []int64{2, 1, 3, 4}, accountIDs(ordered))
	confirmGroupModelSchedulingAccount(c, "astra", 2)
	require.Equal(t, []string{"success"}, cache.outcomes)
	cache.id, cache.allocation = 0, 1
	_, ordered = request("astra", cache, accounts...)
	require.Equal(t, []int64{2, 1, 3, 4}, accountIDs(ordered), "successful recovery restores the configured order")
	p.Version++
	require.NoError(t, writeSchedulingJSON(ctx, repo, schedulingPolicyKey(group.ID), p))
	s.schedulingIndex.policies = nil
	_, ordered = request("astra", cache, accounts...)
	require.Equal(t, []int64{1, 3, 4}, accountIDs(ordered), "revocation also cancels recovery's exception to historical reliability filtering")
}

func TestSchedulingPoolAgentCannotInventEvidence(t *testing.T) {
	p := testSchedulingPoolPolicy()
	d := schedulingAgentDecision{Action: "defer_member", Model: "astra", AccountID: 2}
	evidence := []schedulingAgentEvidence{{Model: "astra", AccountID: 2, Eligible: true, RecentFailures: 2}, {Model: "astra", AccountID: 1, Eligible: true, Successes: 10}}
	require.True(t, allowedSchedulingDecision(p, d, evidence))
	evidence[0].RecentFailures = 1
	require.False(t, allowedSchedulingDecision(p, d, evidence), "one failure is not sufficient")
	evidence[0].RecentFailures = 2
	evidence[1].Eligible = false
	require.False(t, allowedSchedulingDecision(p, d, evidence), "an unsupported backup is not evidence")
	evidence[1].Eligible = true
	d.Model = "sol"
	require.False(t, allowedSchedulingDecision(p, d, evidence), "evidence cannot cross model boundaries")
}

type poolSessionCache struct{ *accountSchedulingCacheStub }

func (c *poolSessionCache) CompareAndSwapFixedSession(_ context.Context, _ int64, key string, previous, id int64, _ time.Duration) error {
	if c.bindings[key] == previous {
		c.bindings[key] = id
	}
	return nil
}

func TestSchedulingPoolActualSelectorsPreserveLegacySessions(t *testing.T) {
	for _, protocol := range []string{"openai", "generic"} {
		t.Run(protocol, func(t *testing.T) {
			group := &Group{ID: 71, Hydrated: true, Platform: PlatformOpenAI, Status: StatusActive, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
			p := testSchedulingPoolPolicy()
			p.AgentMode = "off"
			repo := &agentTestSettings{values: map[string]string{}}
			settings := &SettingService{settingRepo: repo, schedulingAgent: &schedulingAgentRuntime{}}
			require.NoError(t, writeSchedulingJSON(context.Background(), repo, schedulingPolicyKey(group.ID), p))
			cache := &poolSessionCache{&accountSchedulingCacheStub{bindings: map[string]int64{}}}
			accounts := []Account{}
			for _, id := range []int64{1, 2, 3, 4} {
				accounts = append(accounts, Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{group.ID}, Credentials: map[string]any{"model_mapping": map[string]any{"astra": "astra", "sol": "sol"}}})
			}
			accountRepo := schedulerTestOpenAIAccountRepo{accounts: accounts}
			accounts[2].Credentials["model_mapping"].(map[string]any)["secondary-only"] = "secondary-only"
			openai := &OpenAIGatewayService{accountRepo: accountRepo, cache: cache, cfg: &config.Config{}, settingService: settings}
			generic := &GatewayService{accountRepo: accountRepo, groupRepo: schedulingRefreshGroups{group: group}, cache: cache, cfg: &config.Config{}, settingService: settings}
			pick := func(session, model string, excluded map[int64]struct{}) (*Account, context.Context, error) {
				ctx := WithFastestFailoverRequestState(context.WithValue(context.Background(), ctxkey.Group, group))
				if protocol == "openai" {
					selection, err := openai.SelectAccountWithLoadAwareness(ctx, &group.ID, session, model, excluded)
					if err != nil {
						return nil, ctx, err
					}
					if selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
					return selection.Account, ctx, nil
				}
				a, err := generic.SelectAccountForModelWithExclusions(ctx, &group.ID, session, model, excluded)
				return a, ctx, err
			}
			legacyKey := "warm-session"
			if protocol == "openai" {
				legacyKey = "openai:" + legacyKey
			}
			cache.bindings[legacyKey] = 3
			a, ctx, err := pick("warm-session", "astra", nil)
			require.NoError(t, err)
			require.EqualValues(t, 3, a.ID, "policy activation preserves a healthy old secondary session")
			confirmGroupModelSchedulingAccount(ctx, "astra", a.ID)
			snapshot := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState).fixedSession
			require.EqualValues(t, 3, cache.bindings[snapshot.key], "legacy session is committed into the model key only after success")
			a, _, err = pick("fresh-session", "astra", nil)
			require.NoError(t, err)
			require.EqualValues(t, 2, a.ID, "fresh sessions enter the primary pool's exact-model order")
			a, _, err = pick("another-session", "astra", map[int64]struct{}{2: {}})
			require.NoError(t, err)
			require.EqualValues(t, 1, a.ID, "exhaust the primary pool before a secondary")
			a, _, err = pick("missing-model-member", "sol", nil)
			require.NoError(t, err)
			require.EqualValues(t, 1, a.ID)
			a, _, err = pick("secondary-model", "secondary-only", nil)
			require.NoError(t, err)
			require.EqualValues(t, 3, a.ID, "skip a primary pool with no supporting member")
			_, _, err = pick("denied-model", "disabled", nil)
			require.Error(t, err, "empty model orders must never escape to unrestricted routing")
		})
	}
}
