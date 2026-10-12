//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestFastestFailoverPelicanUsesLiveSchedulerPaths(t *testing.T) {
	for _, mode := range []string{AccountSchedulingModeFastestFailover, AccountSchedulingModeQualityPreferred} {
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
