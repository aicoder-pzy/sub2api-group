//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Groups outside the OpenAI-compatible family go through the /v1/messages scheduler.
func TestPelicanGroupRouterRoutesThroughTheGatewayScheduler(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10)
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{
			{ID: 1, Name: "claude-a", Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true, Concurrency: 5, AccountGroups: []AccountGroup{{GroupID: groupID}}},
			{ID: 2, Name: "claude-b", Platform: PlatformAnthropic, Priority: 2, Status: StatusActive, Schedulable: true, Concurrency: 5, AccountGroups: []AccountGroup{{GroupID: groupID}}},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}
	groupRepo := &mockGroupRepoForGateway{groups: map[int64]*Group{
		groupID: {ID: groupID, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true},
	}}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	concurrencyCache := &mockConcurrencyCache{}
	gateway := &GatewayService{
		accountRepo:        repo,
		groupRepo:          groupRepo,
		cache:              &mockGatewayCacheForPlatform{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(concurrencyCache),
	}
	router := &gatewayPelicanGroupRouter{gateway: gateway, concurrency: gateway.concurrencyService, slotWait: 50 * time.Millisecond}
	group := groupRepo.groups[groupID]
	model := "claude-3-5-sonnet-20241022"

	first, err := router.route(ctx, group, model, map[int64]struct{}{})
	require.NoError(t, err)
	require.Equal(t, int64(1), first.account.ID, "the scheduler's priority decides, not the test")
	require.Positive(t, concurrencyCache.acquireAccountCalls, "the sample holds a concurrency slot like a user request")
	first.release()

	second, err := router.route(ctx, group, model, map[int64]struct{}{1: {}})
	require.NoError(t, err)
	require.Equal(t, int64(2), second.account.ID, "a failed account is excluded like in failover")
	second.release()

	_, err = router.route(ctx, group, model, map[int64]struct{}{1: {}, 2: {}})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
}

func TestSmartChannelPelicanRouterPreservesPublicModelThroughFinalAdmission(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
		for _, composite := range []bool{false, true} {
			name := platform + "/native"
			if composite {
				name = platform + "/composite"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background() // Scheduled tests have no HTTP model context.
				group := &Group{ID: 71, Platform: platform, Status: StatusActive, Hydrated: true, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
				public, upstream := "claude-sonnet-4-5", "claude-sonnet-4-5"
				if composite {
					group.Platform, public = PlatformComposite, "public-alias"
				}
				account := Account{ID: 1, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
					GroupIDs: []int64{group.ID}, AccountGroups: []AccountGroup{{GroupID: group.ID}},
					Credentials: map[string]any{"model_mapping": map[string]any{upstream: upstream}}}
				settings := &SettingService{settingRepo: &panelRateLimitSettingRepo{}}
				rules := ModelAccountRoutingSettings{Rules: []ModelAccountRoutingRule{{Model: public, AccountIDs: []int64{1}}, {Model: "denied", AccountIDs: []int64{}}}}
				require.NoError(t, settings.SetModelAccountRouting(ctx, group.ID, rules))
				if composite {
					rules.Rules = append(rules.Rules, ModelAccountRoutingRule{Model: upstream, AccountIDs: []int64{}})
					require.NoError(t, settings.SetModelAccountRouting(ctx, group.ID, rules))
				}
				cfg := testConfig()
				concurrency := NewConcurrencyService(schedulerTestConcurrencyCache{})
				cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
				gateway := &GatewayService{
					accountRepo: &mockAccountRepoForPlatform{accounts: []Account{account}, accountsByID: map[int64]*Account{1: &account}},
					groupRepo:   &mockGroupRepoForGateway{groups: map[int64]*Group{group.ID: group}},
					cfg:         cfg, cache: cache, concurrencyService: concurrency, settingService: settings,
					compositeResolver: NewCompositeRouteResolver(compositeRouteRepoStub{routes: []CompositeModelRoute{{ID: 1, GroupID: group.ID, PublicModel: public,
						MatchType: CompositeRouteMatchExact, Endpoint: CompositeRouteEndpointAny, TargetPlatform: platform, UpstreamModel: upstream, Enabled: true}}}),
				}
				openai := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}, cfg: cfg, cache: cache, concurrencyService: concurrency, settingService: settings}
				router := &gatewayPelicanGroupRouter{gateway: gateway, openai: openai, concurrency: concurrency}
				route, err := router.route(ctx, group, public, nil)
				require.NoError(t, err, "the real post-slot admission must see the public model")
				require.EqualValues(t, 1, route.account.ID)
				require.Equal(t, upstream, route.model)
				route.release()
				// A switch supplement targets the committed primary and must not
				// test a stale channel after waiting for a concurrency slot.
				rememberGroupModelSchedulingAccount(ctx, cache, &group.ID, upstream, 1)
				probe := context.WithValue(ctx, qualitySchedulingProbeKey{}, true)
				probe = context.WithValue(probe, pelicanDegradationTargetKey{}, int64(1))
				probe = context.WithValue(probe, pelicanSwitchTestKey{}, int64(1))
				route, err = router.route(probe, group, public, nil)
				require.NoError(t, err)
				require.EqualValues(t, 1, route.account.ID)
				route.release()
				rememberGroupModelSchedulingAccount(ctx, cache, &group.ID, upstream, 2)
				_, err = router.route(probe, group, public, nil)
				require.Error(t, err, "a stale switch must not spend tokens on the old primary")
				rules.Rules[0].AccountIDs = []int64{}
				require.NoError(t, settings.SetModelAccountRouting(ctx, group.ID, rules))
				_, err = router.route(ctx, group, public, nil)
				require.Error(t, err, "fixing test context must not bypass an explicit denial")
			})
		}
	}
}
