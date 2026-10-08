package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func schedulingAvailabilityContext() context.Context {
	return WithFastestFailoverRequestState(context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 77, AccountSchedulingMode: AccountSchedulingModeFastestFailover}))
}

func TestFastestFailoverAvailabilityEvidence(t *testing.T) {
	now := time.Now()
	quality := map[int64]GroupModelAccountQuality{
		1: {Successes: 8, Failures: 4, ProbeSucceeded: true},
		2: {},
		3: {Successes: 20, Failures: 1, LatencyMS: 200},
		4: {ProbeSucceeded: true, LatencyMS: 10},
		5: {Successes: 20, RecentFailures: 2, LastFailureAt: &now},
	}
	candidates := []*Account{{ID: 1, Priority: 0, Extra: map[string]any{"scheduling_preferred": true}}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}
	ordered := orderFastestFailoverCandidates(candidates, quality, 0, DefaultFastestFailoverSettings())
	var ids []int64
	for _, account := range ordered {
		ids = append(ids, account.ID)
	}
	require.Equal(t, []int64{3, 4, 2, 5, 1}, ids)
	require.Equal(t, "unknown", quality[2].confidence(10))
	require.Equal(t, "degraded", quality[1].confidence(10), "one probe cannot erase a measured history of failures")
	require.EqualValues(t, 1, orderFastestFailoverCandidates(candidates, quality, 1, DefaultFastestFailoverSettings())[0].ID, "historical ranking cannot displace an eligible active channel")
}

func TestFastestFailoverDiagnosticsDoesNotProbeOrWrite(t *testing.T) {
	group := &Group{ID: 77, Platform: PlatformAnthropic, Hydrated: true, Status: StatusActive, AccountSchedulingMode: AccountSchedulingModeFastestFailover}
	accounts := &schedulingRefreshRepo{accounts: []Account{
		{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{77}, AccountGroups: []AccountGroup{{GroupID: 77}}},
		{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{77}, AccountGroups: []AccountGroup{{GroupID: 77}}},
	}}
	cache := &accountSchedulingCacheStub{bindings: map[string]int64{groupModelSchedulingStickyKey("model-a"): 1}}
	svc := &GatewayService{accountRepo: accounts, groupRepo: schedulingRefreshGroups{group: group}, cache: cache,
		usageLogRepo: accountSchedulingQualityStub{quality: map[int64]GroupModelAccountQuality{2: {Successes: 20}}}}
	status, err := svc.GroupSchedulingStatus(context.Background(), nil, 77, "model-a")
	require.NoError(t, err)
	require.EqualValues(t, 1, status.CurrentAccountID)
	require.Len(t, status.Candidates, 2)
	require.True(t, status.Candidates[0].Current)
	require.Equal(t, "unknown", status.Candidates[0].Confidence)
	require.Nil(t, status.Candidates[0].SuccessRate)
	require.Equal(t, 1.0, *status.Candidates[1].SuccessRate)
	require.Empty(t, cache.revisions)
	require.Empty(t, accounts.cooled)
}

func TestFastestFailoverSharedSubmissionAndTimeBudget(t *testing.T) {
	base := schedulingAvailabilityContext()
	account := &Account{ID: 1, Platform: PlatformOpenAI}
	ctx, attempt := beginFastestFailoverAttempt(base, account)
	require.NoError(t, reserveFastestFailoverSubmission(ctx, 1))
	attempt.bind(ctx)
	require.NoError(t, reserveFastestFailoverSubmission(ctx, 1))
	ctx2, attempt2 := beginFastestFailoverAttempt(base, account)
	require.Error(t, reserveFastestFailoverSubmission(ctx2, 1), "inner and outer retries share the same count")
	var retryErr *UpstreamFailoverError
	require.ErrorAs(t, limitFastestFailoverRetry(ctx2, &UpstreamFailoverError{RetryableOnSameAccount: true}, 1, 1), &retryErr)
	require.False(t, retryErr.RetryableOnSameAccount)
	state := attempt.state
	state.mu.Lock()
	state.deadline = time.Now().Add(-time.Second)
	state.mu.Unlock()
	var failover *UpstreamFailoverError
	require.ErrorAs(t, reserveFastestFailoverSubmission(ctx2, 2), &failover)
	require.Equal(t, NextAccountStop, failover.NextAccountAction)
	require.NoError(t, finishFastestFailoverAttempt(ctx, nil, nil, account, nil, attempt, nil))
	require.NoError(t, finishFastestFailoverAttempt(ctx2, nil, nil, account, nil, attempt2, nil))

	disabled := &Account{ID: 3, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true, "pool_mode_retry_count": 0}}
	ctx, attempt = beginFastestFailoverAttempt(schedulingAvailabilityContext(), disabled)
	require.NoError(t, reserveFastestFailoverSubmission(ctx, 3))
	require.Error(t, reserveFastestFailoverSubmission(ctx, 3))
	require.NoError(t, finishFastestFailoverAttempt(ctx, nil, nil, disabled, nil, attempt, nil))
}

func TestFastestFailoverTotalBudgetDoesNotCoolLastChannel(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Extra: map[string]any{}}
	repo := &schedulingTimeoutRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}}
	ctx, attempt := beginFastestFailoverAttempt(schedulingAvailabilityContext(), account)
	attempt.state.deadline = time.Now().Add(10 * time.Millisecond)
	bound := attempt.bind(ctx)
	<-bound.Done()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	transportErr := (&OpenAIGatewayService{}).handleOpenAIUpstreamTransportError(ctx, c, account, bound.Err(), false)
	err := finishFastestFailoverAttempt(ctx, repo, c, account, []byte(`{"model":"model-a"}`), attempt, transportErr)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, "fastest_failover_budget_exhausted", string(failover.Reason))
	require.False(t, failover.ShouldReportAccountScheduleFailure())
	require.Empty(t, repo.cooledModel)

	ctx, attempt = beginFastestFailoverAttempt(schedulingAvailabilityContext(), account)
	attempt.totalBudget = 10 * time.Millisecond
	attempt.idleTimeout = time.Hour
	bound = attempt.bind(ctx)
	markFastestFailoverOutput(ctx)
	time.Sleep(20 * time.Millisecond)
	attempt.expire()
	require.NoError(t, bound.Err(), "healthy output may outlive the initial failover budget")
	require.NoError(t, finishFastestFailoverAttempt(ctx, repo, nil, account, nil, attempt, nil))
}

func TestFastestFailoverUnknownGrok403HasOneAlternate(t *testing.T) {
	base := schedulingAvailabilityContext()
	account := &Account{ID: 1, Platform: PlatformGrok}
	for _, body := range []string{`invalid api key`, `{"error":{"code":"invalid_token"}}`, `{"error":{"message":"token has expired"}}`, `account suspended`, `{"error":{"code":"content_policy_violation"}}`, `{"error":{"message":"insufficient credits"}}`} {
		require.False(t, isFastestFailoverUnknownGrokForbidden(base, account, 403, []byte(body)), body)
	}
	ctx, attempt := beginFastestFailoverAttempt(base, account)
	require.NoError(t, reserveFastestFailoverSubmission(ctx, 1))
	var err *UpstreamFailoverError
	require.ErrorAs(t, classifyFastestFailoverRequestError(ctx, nil, account, &UpstreamFailoverError{StatusCode: 403, ResponseBody: []byte("Forbidden"), RetryableOnSameAccount: true}), &err)
	require.Equal(t, GatewayFailureScopeRequest, err.Scope)
	require.False(t, err.RetryableOnSameAccount)
	require.False(t, err.ShouldReportAccountScheduleFailure())
	_ = finishFastestFailoverAttempt(ctx, nil, nil, account, nil, attempt, err)
	backup := &Account{ID: 2, Platform: PlatformGrok}
	ctx, attempt = beginFastestFailoverAttempt(base, backup)
	require.NoError(t, reserveFastestFailoverSubmission(ctx, 2))
	require.Error(t, reserveFastestFailoverSubmission(ctx, 2))
	require.Error(t, reserveFastestFailoverSubmission(ctx, 3))
	require.ErrorAs(t, classifyFastestFailoverRequestError(ctx, nil, backup, &UpstreamFailoverError{StatusCode: 500}), &err)
	require.Equal(t, NextAccountStop, err.NextAccountAction)
	require.True(t, err.ShouldReportAccountScheduleFailure(), "a real backup fault still retains its health classification")
	_ = finishFastestFailoverAttempt(ctx, nil, nil, backup, nil, attempt, err)
}

type schedulingProxyRepoStub struct {
	ProxyRepository
	proxies map[int64]*Proxy
}

func (r schedulingProxyRepoStub) GetByID(_ context.Context, id int64) (*Proxy, error) {
	return r.proxies[id], nil
}

func TestFastestFailoverProxyReplayRequiresUnsentProof(t *testing.T) {
	backupID := int64(2)
	primary := &Proxy{ID: 1, Name: "primary", Protocol: "http", Host: "primary.example", Port: 3128, Status: StatusActive, FallbackMode: FallbackModeProxy, BackupProxyID: &backupID}
	backup := &Proxy{ID: 2, Name: "backup", Protocol: "http", Host: "backup.example", Port: 3128, Status: StatusActive, FallbackMode: FallbackModeDirect}
	account := &Account{ID: 7, Platform: PlatformOpenAI, ProxyID: &primary.ID, Proxy: primary}
	repo := schedulingProxyRepoStub{proxies: map[int64]*Proxy{2: backup}}
	for _, mode := range []string{"safe", "no_trace", "connected", "wrote", "response", "fresh_trace", "quarantine"} {
		t.Run(mode, func(t *testing.T) {
			req, err := http.NewRequestWithContext(schedulingAvailabilityContext(), "POST", "https://upstream.example", strings.NewReader("body"))
			require.NoError(t, err)
			var calls []string
			blocked := func(id int64) bool { return mode == "quarantine" && id == 1 }
			resp, err := doSchedulingProxyFallback(req, account, primary.URL(), repo, blocked, func(r *http.Request, url string) (*http.Response, error) {
				calls = append(calls, url)
				trace := httptrace.ContextClientTrace(r.Context())
				if mode != "no_trace" && !(mode == "fresh_trace" && len(calls) > 1) {
					trace.GetConn("upstream.example:443")
				}
				if mode == "connected" {
					trace.GotConn(httptrace.GotConnInfo{})
				}
				if mode == "wrote" {
					trace.WroteHeaders()
				}
				if mode == "response" || url == backup.URL() && mode != "fresh_trace" {
					return &http.Response{StatusCode: 200}, nil
				}
				return nil, errors.New("connection refused")
			})
			if mode == "safe" {
				require.NoError(t, err)
				require.Equal(t, []string{primary.URL(), backup.URL()}, calls)
				id, _ := openAIProxyStreamCircuitProxyID(account, resp)
				require.EqualValues(t, 2, id)
			}
			if mode == "quarantine" {
				require.NoError(t, err)
				require.Equal(t, []string{backup.URL()}, calls)
			}
			if mode == "fresh_trace" {
				require.Len(t, calls, 2)
				id, name := schedulingProxyErrorAttribution(account, err)
				require.Equal(t, backupID, *id)
				require.Equal(t, "backup", name)
			}
			if mode == "no_trace" || mode == "connected" || mode == "wrote" || mode == "response" {
				require.Len(t, calls, 1)
			}
			require.Same(t, primary, account.Proxy)
		})
	}
	chain := newSchedulingProxyChain(primary)
	backup.Status = "inactive"
	next, ok := chain.next(context.Background(), repo, nil)
	require.True(t, ok)
	require.Zero(t, next.id, "only explicit direct is permitted")
	backup.FallbackMode, backup.BackupProxyID = FallbackModeProxy, &primary.ID
	_, ok = newSchedulingProxyChain(primary).next(context.Background(), repo, nil)
	require.False(t, ok, "cycles stop without implicit direct fallback")
}
