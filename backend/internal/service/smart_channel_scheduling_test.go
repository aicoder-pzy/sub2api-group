package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func smartChannelTestContext() context.Context {
	return WithFastestFailoverRequestState(context.WithValue(context.Background(), ctxkey.Group,
		&Group{ID: 71, Platform: PlatformOpenAI, AccountSchedulingMode: AccountSchedulingModeSmartChannel}))
}

func TestSmartChannelCapabilityDeclarations(t *testing.T) {
	ctx := smartChannelTestContext()
	groupID := int64(71)
	settings := &SettingService{settingRepo: &panelRateLimitSettingRepo{}}
	require.True(t, modelAutoEnabled(ctx))
	require.True(t, qualityPreferredEnabled(ctx))
	require.True(t, fastestFailoverEnabled(ctx, &groupID))
	mode, err := normalizeAccountSchedulingMode("smart_channel")
	require.NoError(t, err)
	require.Equal(t, AccountSchedulingModeSmartChannel, mode)
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	require.False(t, settings.IsModelAccountAllowed(ctx, &groupID, "gpt-6.1-sol", account.ID, account))
	account.Extra = map[string]any{"openai_passthrough": true}
	account.Credentials = map[string]any{}
	require.False(t, settings.IsModelAccountAllowed(ctx, &groupID, "gpt-6.1-sol", account.ID, account), "passthrough cannot claim unknown models")
	account.Credentials["model_mapping"] = map[string]any{"gpt-6.1-sol": "supplier-6.1"}
	allowed, source := settings.modelAccountCapability(ctx, &groupID, "gpt-6.1-sol", account.ID, account)
	require.True(t, allowed)
	require.Equal(t, "model_mapping", source)
	require.False(t, settings.IsModelAccountAllowed(ctx, &groupID, "gpt-6-astra", account.ID, account))
	account.Credentials = nil
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"gpt-6.1-sol": {ID: "gpt-6.1-sol"}}})
	allowed, source = settings.modelAccountCapability(ctx, &groupID, "gpt-6.1-sol", account.ID, account)
	require.True(t, allowed)
	require.Equal(t, "model_catalog", source)
	account.Extra = nil
	require.NoError(t, settings.SetModelAccountRouting(ctx, groupID, ModelAccountRoutingSettings{Rules: []ModelAccountRoutingRule{{Model: "gpt-6.1-sol", AccountIDs: []int64{1}}}}))
	allowed, source = settings.modelAccountCapability(ctx, &groupID, "gpt-6.1-sol", account.ID, account)
	require.True(t, allowed, "a group rule is an explicit administrator declaration")
	require.Equal(t, "group_rule", source)
	require.False(t, settings.IsModelAccountAllowed(ctx, &groupID, "gpt-6.1-sol", 2, &Account{ID: 2}))
	publicCtx := context.WithValue(ctx, ctxkey.Model, "gpt-6.1-sol")
	require.True(t, settings.IsModelAccountAllowed(publicCtx, &groupID, "supplier-6.1", 1, account), "group rules use the public model before channel remapping")
	require.True(t, settings.IsModelAccountAllowed(context.Background(), &groupID, "unconfigured", 1), "legacy groups retain their existing admission policy")
}

func TestSmartChannelCapacitySpilloverPreservesPrimary(t *testing.T) {
	for _, batch := range []bool{false, true} {
		name := "without_batch_load"
		if batch {
			name = "with_batch_load"
		}
		t.Run(name, func(t *testing.T) { testSmartChannelCapacitySpilloverPreservesPrimary(t, batch) })
	}
}

func testSmartChannelCapacitySpilloverPreservesPrimary(t *testing.T, batch bool) {
	ctx := smartChannelTestContext()
	groupID := int64(71)
	state, ok := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	require.True(t, ok)
	state.smartCapacityWaited = true // Exercise the spill after the shared wait budget.
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6.1-sol": "gpt-6.1-sol"}}},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 10, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6.1-sol": "gpt-6.1-sol"}}},
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: -10},
	}
	cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
	rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "gpt-6.1-sol", 1)
	groupModelSchedulingActiveAccount(ctx, cache, &groupID, "gpt-6.1-sol")
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: cache,
		cfg: &config.Config{}, settingService: &SettingService{settingRepo: &panelRateLimitSettingRepo{}},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{1: false, 2: true, 3: true}})}
	svc.cfg.Gateway.Scheduling.LoadBatchEnabled = batch
	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "gpt-6.1-sol", nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), selection.Account.ID, "unknown model support cannot win even with higher priority and free capacity")
	require.True(t, selection.Acquired)
	require.True(t, selection.CapacitySpillover)
	defer selection.ReleaseFunc()
	confirmGroupModelSchedulingAccount(ctx, "gpt-6.1-sol", 2)
	require.Equal(t, int64(1), groupModelSchedulingActiveAccount(ctx, cache, &groupID, "gpt-6.1-sol"), "a successful capacity request must not promote its channel")

	faultCtx := smartChannelTestContext()
	groupModelSchedulingActiveAccount(faultCtx, cache, &groupID, "gpt-6.1-sol")
	selection, err = svc.SelectAccountWithLoadAwareness(faultCtx, &groupID, "", "gpt-6.1-sol", map[int64]struct{}{1: {}})
	require.NoError(t, err)
	require.False(t, selection.CapacitySpillover)
	defer selection.ReleaseFunc()
	confirmGroupModelSchedulingAccount(faultCtx, "gpt-6.1-sol", 2)
	require.Equal(t, int64(2), groupModelSchedulingActiveAccount(faultCtx, cache, &groupID, "gpt-6.1-sol"), "a completed fault takeover may update the primary")
}

func TestSmartChannelAdmissionRechecksDeclarationAfterWait(t *testing.T) {
	ctx := context.WithValue(smartChannelTestContext(), ctxkey.Model, "gpt-6.1-sol")
	selected := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6.1-sol": "gpt-6.1-sol"}}}
	latest := selected
	latest.Credentials = nil
	repo := schedulerTestOpenAIAccountRepo{accounts: []Account{latest}}
	settings := &SettingService{settingRepo: &panelRateLimitSettingRepo{}}
	for _, terminal := range []func(context.Context, *Account) (*Account, bool, string){
		(&OpenAIGatewayService{accountRepo: repo, settingService: settings}).ProfitControlVetoLatest,
		(&GatewayService{accountRepo: repo, settingService: settings}).GatewayProfitControlVetoLatest,
	} {
		_, vetoed, reason := terminal(ctx, &selected)
		require.True(t, vetoed, "a removed declaration cannot pass admission on a stale selection")
		require.Equal(t, "model_not_declared", reason)
	}
}

type smartChannelWaitCache struct {
	ConcurrencyCache
	entered int
	left    int
}

func (c *smartChannelWaitCache) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	c.entered++
	return true, nil
}

func (c *smartChannelWaitCache) DecrementAccountWaitCount(context.Context, int64) error {
	c.left++
	return nil
}

func TestSmartChannelCapacityWaitCancellationAndSharedBudget(t *testing.T) {
	base := smartChannelTestContext()
	ctx, cancel := context.WithCancel(base)
	cancel()
	cache := &smartChannelWaitCache{}
	concurrency := NewConcurrencyService(cache)
	busy := func(context.Context, int64, int) (*AcquireResult, error) { return &AcquireResult{Acquired: false}, nil }
	_, err := acquireSmartChannelSlot(ctx, &Account{ID: 1}, concurrency, 10, busy)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, cache.entered)
	require.Equal(t, 1, cache.left, "cancellation must release the Redis waiting counter")
	result, err := acquireSmartChannelSlot(base, &Account{ID: 2}, concurrency, 10, busy)
	require.NoError(t, err)
	require.False(t, result.Acquired)
	require.Equal(t, 1, cache.entered, "backups and retries cannot start another capacity wait")
}

type smartChannelRateLimitRepo struct {
	AccountRepository
	until  time.Time
	writes int
}

func (r *smartChannelRateLimitRepo) SetRateLimited(_ context.Context, _ int64, until time.Time) error {
	r.until = until
	r.writes++
	return nil
}

func TestRelay429DoesNotAdoptInternalCodexWeek(t *testing.T) {
	for _, pool := range []bool{false, true} {
		repo := &smartChannelRateLimitRepo{}
		svc := NewRateLimitService(repo, nil, nil, nil, nil)
		account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": pool}}
		headers := http.Header{}
		headers.Set("x-codex-primary-used-percent", "100")
		headers.Set("x-codex-primary-reset-after-seconds", "604800")
		headers.Set("x-codex-primary-window-minutes", "10080")
		body := []byte(`{"error":{"type":"usage_limit_reached","resets_in_seconds":604800}}`)
		svc.HandleUpstreamError(smartChannelTestContext(), account, 429, headers, body)
		require.Equal(t, 1, repo.writes)
		require.WithinDuration(t, time.Now().Add(5*time.Second), repo.until, time.Second)
		repo.writes = 0
		(&AccountTestService{accountRepo: repo}).reconcileOpenAI429State(context.Background(), account, headers, body)
		require.Zero(t, repo.writes, "admin tests must not persist the supplier's individual account reset")
	}
}

func TestRelayRequestErrorsDoNotDisableChannels(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, body := range []string{
		`{"error":{"code":"session_blocked_by_cyber_policy","message":"start a new session"}}`,
		`{"error":{"code":"content_policy_violation"}}`,
		`{"error":{"type":"upstream_error","message":"failed to WebSocket dial: expected handshake response status code 101 but got 403"}}`,
	} {
		require.True(t, isOpenAIRelayRequestError(account, 403, []byte(body)))
		rate := &RateLimitService{}
		require.Equal(t, ErrorPolicySkipped, rate.CheckErrorPolicy(context.Background(), account, 403, []byte(body)))
		require.False(t, rate.HandleUpstreamError(context.Background(), account, 403, nil, []byte(body)))
		require.False(t, (&OpenAIGatewayService{}).handleOpenAIAccountUpstreamError(context.Background(), account, 403, nil, []byte(body)))
	}
	for _, body := range []string{`{"error":{"code":"invalid_api_key"}}`, `{"input":"session_blocked_by_cyber_policy"}`, `{"error":{"message":"content_policy_violation"}}`} {
		require.False(t, isOpenAIRelayRequestError(account, 403, []byte(body)))
	}
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	body := `{"error":{"code":"session_blocked_by_cyber_policy","type":"permission_error","message":"start a new session"}}`
	require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(account, 502, "", []byte(body)))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	_, err := svc.handleErrorResponse(context.Background(), &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, c, account, nil)
	require.Error(t, err)
	require.Equal(t, 403, w.Code)
	require.Contains(t, w.Body.String(), "session_blocked_by_cyber_policy")
	require.True(t, IsOpsRequestScopedError(c))
	events, exists := c.Get(OpsUpstreamErrorsKey)
	require.True(t, exists)
	typedEvents, ok := events.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.NotEmpty(t, typedEvents)
	require.Equal(t, "request", typedEvents[0].Scope)
	require.False(t, shouldFailoverOpenAIPassthroughResponse(account, 502, []byte(body)))
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	err = svc.handleErrorResponsePassthrough(context.Background(), &http.Response{StatusCode: 403, Header: http.Header{}}, c, account, nil, []byte(body))
	require.Error(t, err)
	require.Contains(t, w.Body.String(), "session_blocked_by_cyber_policy")
	require.True(t, IsOpsRequestScopedError(c))
	require.Equal(t, "provider", openAIRelayErrorScope(account, 403, []byte(`{"error":{"type":"upstream_error","message":"failed to WebSocket dial: expected handshake response status code 101 but got 403"}}`)))
}
