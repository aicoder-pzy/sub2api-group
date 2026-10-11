//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type rateLimitCacheStub struct{ *accountSchedulingCacheStub }

func (s *rateLimitCacheStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func TestAccountRateLimitSubmissionCompletesThenNextRequestIsRejected(t *testing.T) {
	limit, reads, sends := 0.21, 0, 0
	account := rateLimitProbeAccount(0.20, time.Now())
	gate := &openAIProfitControlGate{accountRateLimit: &limit, readAccount: func(context.Context, int64) (*Account, error) {
		reads++
		return account, nil
	}}
	ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, gate)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.invalid", nil)
	require.NoError(t, err)
	send := func(req *http.Request) (*http.Response, error) {
		sends++
		account = rateLimitProbeAccount(0.22, time.Now())
		require.NoError(t, req.Context().Err(), "a submitted request is allowed to finish")
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("complete response"))}, nil
	}
	response, err := doFastestFailoverUpstream(nil, request, "", account.ID, 1, nil, send)
	require.NoError(t, err)
	content, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "complete response", string(content))
	require.Equal(t, 1, reads, "no polling or cancellation during the response")
	response, err = doFastestFailoverUpstream(nil, request, "", account.ID, 1, nil, send)
	require.Nil(t, response)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, GatewayFailureScopeRequest, failover.Scope)
	require.EqualValues(t, accountRateLimitExceeded, failover.Reason)
	require.Equal(t, 1, sends, "the next request must not reach the frozen upstream")
	account = rateLimitProbeAccount(limit, time.Now())
	require.NoError(t, checkAccountRateLimitLatest(ctx, account.ID), "a fresh qualifying probe restores eligibility")
	gate.readAccount = func(context.Context, int64) (*Account, error) { return nil, errors.New("cache unavailable") }
	require.ErrorAs(t, checkAccountRateLimitLatest(ctx, account.ID), &failover)
	require.EqualValues(t, accountRateLimitUnknown, failover.Reason)
	gate.accountRateLimit = nil
	require.NoError(t, checkAccountRateLimitLatest(ctx, account.ID), "disabled groups retain their existing behavior")
}

func TestAccountRateLimitConfigValidation(t *testing.T) {
	for _, invalid := range []float64{0, -1, 0.00001, 1000000, math.NaN(), math.Inf(1)} {
		require.Error(t, ValidateAccountRateLimitConfig(PlatformOpenAI, true, invalid))
	}
	require.NoError(t, ValidateAccountRateLimitConfig(PlatformOpenAI, true, 0.21))
	require.Error(t, ValidateAccountRateLimitConfig(PlatformComposite, true, 0.21))
}

func rateLimitProbeAccount(rate float64, now time.Time) *Account {
	manual := 1.0
	snapshot := &UpstreamBillingProbeSnapshot{
		Status: UpstreamBillingProbeStatusOK, ReceivedAt: probeTimePtr(now.Add(-time.Minute)),
		FreshUntil: probeTimePtr(now.Add(time.Hour)), NextProbeAt: now.Add(30 * time.Minute),
		Data: map[string]any{"billing_scope": "token", "resolved_rate_multiplier": rate, "peak_rate_enabled": false},
	}
	encoded, _ := json.Marshal(snapshot)
	var data map[string]any
	_ = json.Unmarshal(encoded, &data)
	return &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: &manual,
		Extra: map[string]any{UpstreamBillingProbeExtraKey: data}}
}

func TestAccountRateLimitUsesActualRateAndInclusiveBoundary(t *testing.T) {
	now, limit := time.Now(), 0.21
	for _, test := range []struct {
		limit float64
		rate  float64
		veto  bool
	}{{0.21, 0, false}, {0.21, 0.20, false}, {0.21, 0.21, false}, {0.21, 0.22, true},
		{0.10, 0.09, false}, {0.10, 0.10, false}, {0.10, 0.1001, true}} {
		vetoed, _ := accountRateLimitVeto(&test.limit, rateLimitProbeAccount(test.rate, now), now)
		require.Equal(t, test.veto, vetoed, "rate=%v limit=%v", test.rate, test.limit)
	}
	account := rateLimitProbeAccount(0.15, now)
	snapshot := account.Extra[UpstreamBillingProbeExtraKey].(map[string]any)
	data := snapshot["data"].(map[string]any)
	data["peak_rate_enabled"], data["peak_start"], data["peak_end"] = true, "06:00", "22:00"
	data["peak_rate_multiplier"], data["timezone"] = 2.0, "Asia/Shanghai"
	beijing := time.FixedZone("Asia/Shanghai", 8*60*60)
	peak := time.Date(2026, 10, 11, 12, 0, 0, 0, beijing)
	snapshot["received_at"], snapshot["fresh_until"] = peak.Add(-time.Hour).Format(time.RFC3339), peak.Add(12*time.Hour).Format(time.RFC3339)
	vetoed, _ := accountRateLimitVeto(&limit, account, peak)
	require.True(t, vetoed, "peak factor must be included")
	vetoed, _ = accountRateLimitVeto(&limit, account, peak.Add(11*time.Hour))
	require.False(t, vetoed, "leaving a known peak window restores eligibility without waiting for another probe")
}

func TestAccountRateLimitCannotRecoverUsingStaleOrFailedEvidence(t *testing.T) {
	now, limit, cheap := time.Now(), 0.21, 0.01
	account := rateLimitProbeAccount(0.30, now)
	account.RateMultiplier = &cheap
	snapshot := account.Extra[UpstreamBillingProbeExtraKey].(map[string]any)
	snapshot["status"] = UpstreamBillingProbeStatusFailed
	vetoed, reason := accountRateLimitVeto(&limit, account, now)
	require.True(t, vetoed)
	require.Equal(t, accountRateLimitExceeded, reason)
	snapshot["fresh_until"] = now.Add(-time.Second).Format(time.RFC3339)
	vetoed, reason = accountRateLimitVeto(&limit, account, now)
	require.True(t, vetoed)
	require.Equal(t, accountRateLimitUnknown, reason, "stale declaration must not fall back to cheap manual cost")
	account.Extra = nil
	vetoed, _ = accountRateLimitVeto(&limit, account, now)
	require.False(t, vetoed, "unsupported/manual accounts use configured cost")
}

func TestAccountRateLimitAppliesAlongsideProfitControl(t *testing.T) {
	group := profitControlTestGroup(102, 0, 0)
	group.ProfitControlEnabled, group.AccountRateLimitEnabled, group.AccountRateLimitMultiplier = false, true, 0.21
	svc := &OpenAIGatewayService{}
	ctx, _ := svc.WithOpenAIRequestPricingContext(profitControlTestCtx(group), &group.ID)
	account := rateLimitProbeAccount(0.20, time.Now())
	vetoed, _ := OpenAIProfitControlVeto(ctx, account)
	require.False(t, vetoed, "probe actual rate is used even when stored manual multiplier is 1")
	group.ProfitControlEnabled, group.ProfitMinMargin = true, 0.5
	ctx, _ = svc.WithOpenAIRequestPricingContext(profitControlTestCtx(group), &group.ID)
	vetoed, reason := OpenAIProfitControlVeto(ctx, account)
	require.True(t, vetoed)
	require.Equal(t, openAIProfitFilterReasonThreshold, reason, "existing profit control remains enforced independently")
}

func TestAccountRateLimitLiveSelectionSwitchesAndRecovers(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
		for _, mode := range []string{AccountSchedulingModeFastestFailover, AccountSchedulingModePriority} {
			for _, loadBatch := range []bool{false, true} {
				t.Run(platform+"/"+mode+"/"+map[bool]string{false: "legacy", true: "load_aware"}[loadBatch], func(t *testing.T) {
					groupID, rateA, rateB := int64(105), 0.21, 0.10
					group := &Group{ID: groupID, Platform: platform, Status: StatusActive, Hydrated: true,
						AccountSchedulingMode: mode, AccountRateLimitEnabled: true, AccountRateLimitMultiplier: 0.21}
					ctx := context.WithValue(context.Background(), ctxkey.Group, group)
					ctx, _ = WithGatewayTokenRequestPricing(ctx)
					accounts := []Account{{ID: 1, Priority: 0, RateMultiplier: &rateA}, {ID: 2, Priority: 1, RateMultiplier: &rateB}}
					byID := make(map[int64]*Account)
					for i := range accounts {
						accounts[i].Platform, accounts[i].Type, accounts[i].Status = platform, AccountTypeAPIKey, StatusActive
						accounts[i].Schedulable, accounts[i].Concurrency = true, 1
						accounts[i].GroupIDs = []int64{groupID}
						accounts[i].AccountGroups = []AccountGroup{{GroupID: groupID}}
						accounts[i].Credentials = map[string]any{"model_mapping": map[string]any{"model-a": "model-a"}}
						byID[accounts[i].ID] = &accounts[i]
					}
					cache := &rateLimitCacheStub{accountSchedulingCacheStub: &accountSchedulingCacheStub{bindings: map[string]int64{"old-session": 1, groupModelSchedulingStickyKey("model-a"): 1}}}
					cfg := testConfig()
					cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatch
					concurrency := NewConcurrencyService(schedulerTestConcurrencyCache{})
					groups := &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: group}}
					openai := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: cache, cfg: cfg, concurrencyService: concurrency}
					gateway := &GatewayService{accountRepo: &mockAccountRepoForPlatform{accounts: accounts, accountsByID: byID}, groupRepo: groups, cache: cache, cfg: cfg, concurrencyService: concurrency}
					selectID := func(want int64) {
						requestCtx := WithFastestFailoverRequestState(ctx)
						var selected *AccountSelectionResult
						var err error
						if platform == PlatformOpenAI {
							selected, _, err = openai.SelectAccountWithScheduler(requestCtx, &groupID, "", "old-session", "model-a", nil, OpenAIUpstreamTransportAny, false)
						} else {
							selected, err = gateway.SelectAccountWithLoadAwareness(requestCtx, &groupID, "old-session", "model-a", nil, "", 0)
						}
						if want == 0 {
							require.Error(t, err)
							return
						}
						require.NoError(t, err)
						require.NotNil(t, selected)
						require.Equal(t, want, selected.Account.ID)
						if selected.ReleaseFunc != nil {
							selected.ReleaseFunc()
						}
						confirmGroupModelSchedulingAccount(requestCtx, "model-a", selected.Account.ID)
					}
					selectID(1)
					rateA = 0.22
					selectID(2)
					rateB = 0.25
					selectID(0)
					rateA = 0.21
					selectID(1)
				})
			}
		}
	}
}

func TestAccountRateLimitEnablesPeriodicProbeWithoutGlobalFreeze(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: false,
		Groups: []*Group{{Platform: PlatformOpenAI, Status: StatusActive, AccountRateLimitEnabled: true, AccountRateLimitMultiplier: 0.21}}}
	require.True(t, upstreamBillingProbeRequired(account))
	require.False(t, upstreamBillingRateSyncEnabled(account), "protection must not change cost accounting")
	account.Groups[0].AccountRateLimitEnabled = false
	require.False(t, upstreamBillingProbeRequired(account))
}
