//go:build unit

package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestModelAutoLiveSelectorsKeepTrialsOutOfPrimaryBinding(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
		for _, loadBatch := range []bool{false, true} {
			t.Run(platform+"/"+strconv.FormatBool(loadBatch), func(t *testing.T) {
				groupID := int64(109)
				group := &Group{ID: groupID, Platform: platform, Status: StatusActive, Hydrated: true, AccountSchedulingMode: AccountSchedulingModeModelAuto}
				ctx := context.WithValue(context.Background(), ctxkey.Group, group)
				accounts := []Account{{ID: 1}, {ID: 2}, {ID: 3}}
				byID := make(map[int64]*Account)
				for i := range accounts {
					accounts[i].Platform, accounts[i].Type, accounts[i].Status = platform, AccountTypeAPIKey, StatusActive
					accounts[i].Schedulable, accounts[i].Concurrency = true, 1
					accounts[i].GroupIDs = []int64{groupID}
					accounts[i].AccountGroups = []AccountGroup{{GroupID: groupID}}
					accounts[i].Credentials = map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"}}
					byID[accounts[i].ID] = &accounts[i]
				}
				cache := &modelAutoCacheFake{accountSchedulingCacheStub: &accountSchedulingCacheStub{bindings: map[string]int64{groupModelSchedulingStickyKey("claude-sonnet-4-5"): 1}}, sessions: make(map[string]int64), counts: make(map[string]int)}
				repo := accountSchedulingQualityStub{quality: map[int64]GroupModelAccountQuality{1: modelAutoSample(10000), 2: modelAutoSample(7000), 3: modelAutoSample(12000)}}
				cfg := testConfig()
				cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatch
				concurrency := NewConcurrencyService(schedulerTestConcurrencyCache{})
				openai := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: cache, cfg: cfg, concurrencyService: concurrency, usageLogRepo: repo}
				gateway := &GatewayService{accountRepo: &mockAccountRepoForPlatform{accounts: accounts, accountsByID: byID}, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: group}}, cache: cache, cfg: cfg, concurrencyService: concurrency, usageLogRepo: repo}
				for i := 1; i <= 20; i++ {
					request := WithFastestFailoverRequestState(ctx)
					var selection *AccountSelectionResult
					var err error
					if platform == PlatformOpenAI {
						selection, _, err = openai.SelectAccountWithScheduler(request, &groupID, "", strconv.Itoa(i), "claude-sonnet-4-5", nil, OpenAIUpstreamTransportAny, false)
					} else {
						selection, err = gateway.SelectAccountWithLoadAwareness(request, &groupID, strconv.Itoa(i), "claude-sonnet-4-5", nil, "", 0)
					}
					require.NoError(t, err)
					require.NotNil(t, selection)
					want := int64(1)
					if i == 10 {
						want = 2
					}
					if i == 20 {
						want = 3
					}
					require.Equal(t, want, selection.Account.ID)
					if selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
					confirmGroupModelSchedulingAccount(request, "claude-sonnet-4-5", selection.Account.ID)
					require.EqualValues(t, 1, cache.bindings[groupModelSchedulingStickyKey("claude-sonnet-4-5")])
				}
			})
		}
	}
}

func TestFastestFailoverPelicanUsesLiveSchedulerPaths(t *testing.T) {
	for _, mode := range []string{AccountSchedulingModeFastestFailover, AccountSchedulingModeQualityPreferred, AccountSchedulingModeModelAuto} {
		for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
			for _, loadBatch := range []bool{false, true} {
				t.Run(mode+"/"+platform+"/"+map[bool]string{false: "legacy", true: "load_aware"}[loadBatch], func(t *testing.T) {
					groupID := int64(105)
					group := &Group{ID: groupID, Platform: platform, Status: StatusActive, Hydrated: true, AccountSchedulingMode: mode}
					ctx := context.WithValue(context.Background(), ctxkey.Group, group)
					accounts := []Account{{ID: 1}, {ID: 2}, {ID: 3}}
					accountsByID := make(map[int64]*Account)
					for i := range accounts {
						accounts[i].Platform, accounts[i].Type, accounts[i].Status = platform, AccountTypeAPIKey, StatusActive
						accounts[i].Schedulable, accounts[i].Concurrency = true, 1
						accounts[i].GroupIDs = []int64{groupID}
						accounts[i].AccountGroups = []AccountGroup{{GroupID: groupID}}
						accountsByID[accounts[i].ID] = &accounts[i]
						accounts[i].Credentials = map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"}}
					}
					cache := &accountSchedulingCacheStub{bindings: map[string]int64{"old-session": 1, groupModelSchedulingStickyKey("claude-sonnet-4-5"): 1}}
					quality := map[int64]GroupModelAccountQuality{
						1: {Successes: 100, LatencyMS: 10, IntelligenceVerdict: "degraded"},
						2: {Successes: 100, LatencyMS: 20, IntelligenceVerdict: "normal"},
						3: {Successes: 100, LatencyMS: 30, IntelligenceVerdict: "normal"},
					}
					now := time.Now()
					for id, sample := range quality {
						sample.Quality = &AccountQualityEvidence{LatestVerdict: sample.IntelligenceVerdict, LatestAt: &now, NormalStreak: 2, LatencyP95MS: sample.LatencyMS}
						quality[id] = sample
					}
					cfg := testConfig()
					cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatch
					concurrency := NewConcurrencyService(schedulerTestConcurrencyCache{})
					groups := &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: group}}
					openai := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: cache, cfg: cfg, concurrencyService: concurrency, usageLogRepo: accountSchedulingQualityStub{quality: quality}}
					require.NoError(t, openai.setStickySessionAccountID(ctx, &groupID, "old-session", 1, time.Minute))
					gateway := &GatewayService{accountRepo: &mockAccountRepoForPlatform{accounts: accounts, accountsByID: accountsByID}, groupRepo: groups, cache: cache, cfg: cfg, concurrencyService: concurrency, usageLogRepo: accountSchedulingQualityStub{quality: quality}}
					selectID := func(want int64, session string) {
						requestCtx := WithFastestFailoverRequestState(ctx)
						var selected *AccountSelectionResult
						var err error
						if platform == PlatformOpenAI {
							selected, _, err = openai.SelectAccountWithScheduler(requestCtx, &groupID, "", session, "claude-sonnet-4-5", nil, OpenAIUpstreamTransportAny, false)
						} else {
							selected, err = gateway.SelectAccountWithLoadAwareness(requestCtx, &groupID, session, "claude-sonnet-4-5", nil, "", 0)
						}
						if err != nil || selected == nil || selected.Account.ID != want {
							t.Fatalf("session %s: want account %d, selection=%+v error=%v", session, want, selected, err)
						}
						if selected.ReleaseFunc != nil {
							selected.ReleaseFunc()
						}
						confirmGroupModelSchedulingAccount(requestCtx, "claude-sonnet-4-5", selected.Account.ID)
					}
					pending := quality[1]
					pending.Degradation = &PelicanDegradationState{Status: "pending", SampledAt: now}
					quality[1] = pending
					selectID(1, "old-session")
					selectID(1, "grace-new-session")
					require.Equal(t, int64(1), groupModelSchedulingActiveAccount(ctx, cache, &groupID, "claude-sonnet-4-5"), "first degradation keeps serving existing and new requests")
					pending.Degradation.Status = "confirmed"
					selectID(2, "confirmation-new-session")
					require.Equal(t, int64(2), groupModelSchedulingActiveAccount(ctx, cache, &groupID, "claude-sonnet-4-5"), "confirmation replaces the primary")
					pending.Degradation.Status = "normal"
					selectID(2, "recovery-new-session")
					plan := groupTestPlan(3)
					plan.GroupID, plan.ModelID = groupID, "claude-sonnet-4-5"
					plan.PelicanConfig.ReasoningEffort = "medium"
					tests, _ := newGroupTestService(newGroupTestRepoFake(plan),
						&gatewayPelicanGroupRouter{gateway: gateway, openai: openai, concurrency: concurrency},
						answers(map[int64]ScheduledTestResult{2: {Status: "success", ResponseText: "<html>drawing</html>"}}))
					tests.groups = groupTestGroupsFake{groupID: group}
					for _, result := range tests.runSamples(context.Background(), plan) {
						require.EqualValues(t, 2, result.AccountID, "every routine sample tests the healthy primary, including parallel samples")
						require.Equal(t, "success", result.Status)
					}
					result := tests.runSample(context.Background(), plan, group)
					require.EqualValues(t, 2, result.AccountID, "a single routine sample also follows the primary rather than rotating to an untested account")
					require.Equal(t, "success", result.Status)
					require.EqualValues(t, 2, groupModelSchedulingActiveAccount(ctx, cache, &groupID, plan.ModelID))
					pending.Degradation = nil
					quality[1] = pending
					require.NoError(t, cache.SetSessionAccountID(ctx, groupID, groupModelSchedulingStickyKey("claude-sonnet-4-5"), 1, 0))
					pending.Degradation = &PelicanDegradationState{Status: "confirmed", SampledAt: now}
					quality[1] = pending
					router := &gatewayPelicanGroupRouter{gateway: gateway, openai: openai, concurrency: concurrency}
					replacementCtx := context.WithValue(ctx, pelicanDegradationRunKey{}, PelicanDegradationWork{AccountID: 1, Action: "replacement"})
					route, err := router.route(replacementCtx, group, "claude-sonnet-4-5", nil)
					require.NoError(t, err)
					require.EqualValues(t, 2, route.account.ID)
					require.True(t, route.confirm())
					route.release()
					require.Equal(t, int64(2), groupModelSchedulingActiveAccount(ctx, cache, &groupID, "claude-sonnet-4-5"), "replacement confirmation commits the binding before its immediate test")
					pending.Degradation = nil
					quality[1] = pending
					require.NoError(t, cache.SetSessionAccountID(ctx, groupID, groupModelSchedulingStickyKey("claude-sonnet-4-5"), 1, 0))
					if mode == AccountSchedulingModeQualityPreferred {
						accounts[1].Extra = map[string]any{"scheduling_preferred": true}
						quality[1].Quality.LatestVerdict = "normal"
						quality[2].Quality.LatestVerdict = "unknown"
						selectID(1, "new-session")
						require.Equal(t, int64(1), groupModelSchedulingActiveAccount(ctx, cache, &groupID, "claude-sonnet-4-5"), "manual preference cannot preempt a healthy primary for new sessions")
						quality[1].Quality.LatestVerdict = "degraded"
						quality[2].Quality.LatestVerdict = "normal"
						selectID(2, "new-session")
						require.Equal(t, int64(2), groupModelSchedulingActiveAccount(ctx, cache, &groupID, "claude-sonnet-4-5"), "degradation still replaces the primary immediately")
						quality[1].Quality.LatestVerdict = "normal"
						accounts[0].Extra = map[string]any{"scheduling_preferred": true}
						accounts[0].Priority, accounts[1].Priority = 1, 2
						selectID(2, "another-new-session")
						require.Equal(t, int64(2), groupModelSchedulingActiveAccount(ctx, cache, &groupID, "claude-sonnet-4-5"), "recovered preferred and faster A cannot reclaim healthy B")
						selectID(1, "old-session")
						require.Equal(t, int64(2), groupModelSchedulingActiveAccount(ctx, cache, &groupID, "claude-sonnet-4-5"), "normal existing session cannot overwrite the current primary")
						quality[1].Quality.LatestVerdict = "degraded"
						quality[2].Quality.LatestVerdict = "normal"
					}
					for _, want := range []int64{2, 3, 1} {
						selectID(want, "old-session")
						sample := quality[want]
						sample.IntelligenceVerdict = "degraded"
						sample.Quality.LatestVerdict = "degraded"
						quality[want] = sample
					}
				})
			}
		}
	}
}
