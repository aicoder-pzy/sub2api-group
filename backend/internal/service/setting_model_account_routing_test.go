package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestModelAccountRoutingPersistenceAndClosedFailures(t *testing.T) {
	ctx := context.Background()
	repo := &panelRateLimitSettingRepo{}
	svc := &SettingService{settingRepo: repo}
	require.True(t, svc.IsModelAccountAllowed(ctx, "gpt-6-astra", 1))
	settings := ModelAccountRoutingSettings{Rules: []ModelAccountRoutingRule{
		{Model: "gpt-6-astra", AccountIDs: []int64{2, 3}},
		{Model: "blocked", AccountIDs: []int64{}},
	}}
	require.NoError(t, svc.SetModelAccountRouting(ctx, settings))
	loaded, err := (&SettingService{settingRepo: repo}).GetModelAccountRouting(ctx)
	require.NoError(t, err)
	require.Equal(t, settings, loaded)
	require.False(t, svc.IsModelAccountAllowed(ctx, "gpt-6-astra", 1))
	require.True(t, svc.IsModelAccountAllowed(ctx, "gpt-6-astra", 2))
	require.True(t, svc.IsModelAccountAllowed(ctx, "gpt-6-astra", 3))
	require.True(t, svc.IsModelAccountAllowed(ctx, "unconfigured", 1))
	require.False(t, svc.IsModelAccountAllowed(ctx, "blocked", 2))
	publicCtx := context.WithValue(ctx, ctxkey.Model, "gpt-6-astra")
	require.False(t, svc.IsModelAccountAllowed(publicCtx, "channel-mapped-model", 1))
	require.True(t, svc.IsModelAccountAllowed(publicCtx, "channel-mapped-model", 2))
	compositeCtx := context.WithValue(ctx, ctxkey.RequestedPublicModel, "gpt-6-astra")
	require.False(t, svc.IsModelAccountAllowed(compositeCtx, "channel-mapped-model", 1))
	require.True(t, svc.IsModelAccountAllowed(compositeCtx, "channel-mapped-model", 2))
	for _, invalid := range []string{
		`{}`, `{"rules":null}`, `{"rules":[{"model":"*","account_ids":[2]}]}`,
		`{"rules":[{"model":"a b","account_ids":[2]}]}`,
		`{"rules":[{"model":"a","account_ids":[0]}]}`,
		`{"rules":[{"model":"a","account_ids":[2,2]}]}`,
		`{"rules":[{"model":"a","account_ids":null}]}`,
		`{"rules":[{"model":"a","account_ids":[]},{"model":"a","account_ids":[2]}]}`,
	} {
		var invalidSettings ModelAccountRoutingSettings
		require.NoError(t, json.Unmarshal([]byte(invalid), &invalidSettings))
		require.Error(t, svc.SetModelAccountRouting(ctx, invalidSettings), invalid)
	}
	require.False(t, svc.IsModelAccountAllowed(ctx, "gpt-6-astra", 1))
	repo.getValueErr = errors.New("offline")
	svc.modelAccountRoutingCache.expires = time.Time{}
	require.False(t, svc.IsModelAccountAllowed(ctx, "gpt-6-astra", 2))
	require.False(t, svc.IsModelAccountAllowed(ctx, "unconfigured", 1))
	repo.getValueErr = nil
	for _, malformed := range []string{`null`, `{}`, `{"rules":null}`, `{"rules":"invalid"}`, `not-json`} {
		require.NoError(t, repo.Set(ctx, SettingKeyModelAccountRouting, malformed))
		svc.modelAccountRoutingCache.expires = time.Time{}
		require.False(t, svc.IsModelAccountAllowed(ctx, "gpt-6-astra", 1), malformed)
		require.False(t, svc.IsModelAccountAllowed(ctx, "unconfigured", 1), malformed)
	}
	require.NoError(t, svc.SetModelAccountRouting(ctx, ModelAccountRoutingSettings{Rules: []ModelAccountRoutingRule{}}))
	require.True(t, svc.IsModelAccountAllowed(ctx, "gpt-6-astra", 1))
	settings.Rules[0].AccountIDs[0] = 99
	require.NoError(t, svc.SetModelAccountRouting(ctx, settings))
	settings.Rules[0].AccountIDs[0] = 1
	require.False(t, svc.IsModelAccountAllowed(ctx, "gpt-6-astra", 1), "saved cache must not alias the caller's slices")
}

func TestModelAccountRoutingSchedulerNeverEscapesWhitelist(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy", "legacy_batch", "fastest_failover"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			groupID := int64(71)
			if mode == "fastest_failover" {
				ctx = context.WithValue(ctx, ctxkey.Group, &Group{ID: groupID, AccountSchedulingMode: AccountSchedulingModeFastestFailover})
			}
			accounts := []Account{
				{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0},
				{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 10},
			}
			settings := &SettingService{settingRepo: &panelRateLimitSettingRepo{}}
			require.NoError(t, settings.SetModelAccountRouting(ctx, ModelAccountRoutingSettings{Rules: []ModelAccountRoutingRule{{Model: "gpt-6-astra", AccountIDs: []int64{2}}}}))
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = mode == "legacy_batch"
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"session": 1}}
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: cache,
				cfg: cfg, settingService: settings, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}
			selectAccount := func(excluded map[int64]struct{}) (*AccountSelectionResult, error) {
				if mode == "advanced" {
					scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
					result, _, err := scheduler.Select(ctx, OpenAIAccountScheduleRequest{GroupID: &groupID, Platform: PlatformOpenAI, SessionHash: "session", RequestedModel: "gpt-6-astra", ExcludedIDs: excluded})
					return result, err
				}
				return svc.SelectAccountWithLoadAwareness(ctx, &groupID, "session", "gpt-6-astra", excluded)
			}
			selection, err := selectAccount(nil)
			require.NoError(t, err)
			require.Equal(t, int64(2), selection.Account.ID, "priority and existing sticky account cannot override the whitelist")
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			selection, err = selectAccount(map[int64]struct{}{2: {}})
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Nil(t, selection, "failover must not use the unlisted account")
			accounts[1].Schedulable = false
			selection, err = selectAccount(nil)
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Nil(t, selection)
		})
	}
}

func TestModelAccountRoutingGatewayGateAndPreviousResponse(t *testing.T) {
	ctx := context.Background()
	account := &Account{ID: 1, Status: StatusActive, Schedulable: true}
	require.True(t, (&OpenAIGatewayService{}).IsModelAccountAllowed(ctx, "gpt-6-astra", account.ID))
	require.True(t, (&GatewayService{}).isAccountSchedulableForModelSelection(ctx, account, "gpt-6-astra"))
	settings := &SettingService{settingRepo: &panelRateLimitSettingRepo{}}
	require.NoError(t, settings.SetModelAccountRouting(ctx, ModelAccountRoutingSettings{Rules: []ModelAccountRoutingRule{{Model: "gpt-6-astra", AccountIDs: []int64{2}}}}))
	gateway := &GatewayService{settingService: settings}
	require.False(t, gateway.isAccountSchedulableForModelSelection(ctx, account, "gpt-6-astra"))
	require.True(t, gateway.isAccountSchedulableForModelSelection(ctx, account, "unconfigured"))
	account.ID = 2
	require.True(t, gateway.isAccountSchedulableForModelSelection(ctx, account, "gpt-6-astra"))
	svc := &OpenAIGatewayService{settingService: settings, cfg: newSchedulerTestOpenAIWSV2Config()}
	store := svc.getOpenAIWSStateStore()
	require.NoError(t, store.BindResponseAccount(ctx, 71, "resp_unlisted", 1, time.Hour))
	groupID := int64(71)
	selection, err := svc.selectAccountByPreviousResponseIDForCapability(ctx, &groupID, "resp_unlisted", "gpt-6-astra", nil, "", false)
	require.NoError(t, err)
	require.Nil(t, selection)
}
