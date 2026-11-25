package service

import (
	"context"
	"crypto/sha256"
	"fmt"
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

func TestMonitorProbeAuthenticationBindsExactRequest(t *testing.T) {
	const secret = "test-monitor-secret-1234567890123456789"
	const payload = `{"model":"sol","input":"1+1"}`
	for _, mutation := range []string{"valid", "forged", "body", "key", "host", "path", "expired"} {
		t.Run(mutation, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/responses", strings.NewReader(payload))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer test-key")
			stamp := fmt.Sprint(time.Now().Unix())
			if mutation == "expired" {
				stamp = fmt.Sprint(time.Now().Add(-2 * time.Minute).Unix())
			}
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
			req.Header.Set(monitorProbeHeader, stamp+"."+digest+"."+monitorProbeSignature(req, stamp, digest, secret))
			switch mutation {
			case "forged":
				req.Header.Set(monitorProbeHeader, "true")
			case "body":
				req.Body = io.NopCloser(strings.NewReader(`{"model":"other"}`))
			case "key":
				req.Header.Set("Authorization", "Bearer other")
			case "host":
				req.Host = "other.example.com"
			case "path":
				req.URL.Path = "/other"
			}
			AuthenticateMonitorProbe(req, secret)
			require.Equal(t, mutation == "valid", MonitorProbe(req.Context()))
			require.Empty(t, req.Header.Get(monitorProbeHeader))
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			if mutation != "body" {
				require.Equal(t, payload, string(body))
			}
		})
	}
}

type monitorProbeTestRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip monitorProbeTestRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return roundTrip(req)
}

func TestMonitorProbeCallProviderSignsExactRequest(t *testing.T) {
	const secret = "test-monitor-secret-1234567890123456789"
	originalTransport := monitorHTTPClient.Transport
	monitorHTTPClient.Transport = monitorProbeTestRoundTripper(func(req *http.Request) (*http.Response, error) {
		AuthenticateMonitorProbe(req, secret)
		if !MonitorProbe(req.Context()) {
			return nil, fmt.Errorf("request was not authenticated as a monitor probe")
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(body), `"model":"gpt-test"`) {
			return nil, fmt.Errorf("signed request body missing model: %s", body)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"2"}}]}`)), Request: req}, nil
	})
	defer func() { monitorHTTPClient.Transport = originalTransport }()

	_, _, status, err := callProvider(context.Background(), MonitorProviderOpenAI, "https://example.com", "test-key", "gpt-test", "1+1", &CheckOptions{monitorSecret: secret})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
}

func TestMonitorProbeCannotTempUnscheduleRetryableAccount(t *testing.T) {
	repo := &capacityShedAccountRepoStub{}
	ctx := context.WithValue(context.Background(), monitorProbeKey{}, true)
	(&GatewayService{accountRepo: repo}).TempUnscheduleRetryableError(ctx, 1, &UpstreamFailoverError{
		StatusCode: http.StatusBadGateway, RetryableOnSameAccount: true,
	})
	require.Zero(t, repo.tempUnschedCalls)
}

func TestMonitorProbeTimeoutDoesNotCoolOrChangeBindings(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 71, AccountSchedulingMode: AccountSchedulingModeSmartChannel})
	ctx = context.WithValue(ctx, monitorProbeKey{}, true)
	ctx = context.WithValue(ctx, qualitySchedulingReadOnlyKey{}, true)
	cache := &accountSchedulingCacheStub{bindings: map[string]int64{groupModelSchedulingStickyKey("sol"): 1, "session": 1, "openai:session": 1}}
	ctx = prepareFastestFailoverBinding(ctx, cache, "sol")
	account := &Account{ID: 1, Platform: PlatformOpenAI, Extra: map[string]any{}}
	ctx, attempt := beginFastestFailoverAttempt(ctx, account)
	require.Equal(t, monitorProbeAttemptTimeout, attempt.firstTimeout)
	require.Equal(t, monitorProbeTotalTimeout, attempt.totalBudget)
	require.Equal(t, 1, attempt.maxSubmissions)
	repo := &schedulingTimeoutRepo{}
	_ = attempt.bind(ctx)
	expireSchedulingAttempt(attempt)
	err := finishFastestFailoverAttempt(ctx, repo, nil, account, []byte(`{"model":"sol"}`), attempt, context.Canceled)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.True(t, failover.ShouldRetryNextAccount())
	require.Empty(t, repo.cooledModel)
	confirmGroupModelSchedulingAccount(ctx, "sol", 2)
	require.NoError(t, (&GatewayService{cache: cache}).BindStickySession(ctx, ptr(int64(71)), "session", 2))
	require.NoError(t, (&OpenAIGatewayService{cache: cache}).setStickySessionAccountID(ctx, ptr(int64(71)), "session", 2, time.Hour))
	for _, id := range cache.bindings {
		require.EqualValues(t, 1, id)
	}
	state := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	require.Empty(t, state.switchReasons)
	require.EqualValues(t, 0, state.smartFailure.accountID)
	stream := WithSchedulingAgentRequestType(ctx, RequestTypeStream)
	require.Nil(t, stream.Value(schedulingAgentStreamRequestKey{}))
}

type fixedStrikeCache struct {
	*poolSessionCache
	strikes map[int64]bool
}

func (c *fixedStrikeCache) SmartChannelStrike(_ context.Context, _ int64, _ string, id, _ int64, success bool) (bool, error) {
	first := !c.strikes[id]
	c.strikes[id] = !success
	return first && !success, nil
}

func TestSmartChannelFixedSessionSoftFailuresAndRecovery(t *testing.T) {
	group := &Group{ID: 71, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
	cache := &fixedStrikeCache{&poolSessionCache{&accountSchedulingCacheStub{bindings: map[string]int64{}}}, map[int64]bool{}}
	p := testSchedulingPoolPolicy()
	p.AgentMode = "off"
	scope := fixedSchedulingScope{groupID: group.ID, model: "sol", policy: p, order: p.order("sol")}
	all := []*Account{{ID: 1}, {ID: 3}, {ID: 4}}
	pick := func(session string, candidates []*Account) context.Context {
		ctx := withModelAutoSession(context.WithValue(context.Background(), ctxkey.Group, group), session)
		ctx = prepareFastestFailoverBinding(ctx, cache, "sol")
		orderFixedScheduling(ctx, scope, cache, candidates, nil)
		return ctx
	}
	ctx := pick("affected", all)
	confirmGroupModelSchedulingAccount(ctx, "sol", 1)
	warm := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState).fixedSession.key
	other := pick("healthy", all)
	confirmGroupModelSchedulingAccount(other, "sol", 1)
	otherKey := other.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState).fixedSession.key
	for turn := 1; turn <= 2; turn++ {
		ctx = pick("affected", all)
		for retry := 0; retry < 2; retry++ {
			recordSmartChannelFailure(ctx, 1, &UpstreamFailoverError{StatusCode: 502})
		}
		orderFixedScheduling(ctx, scope, cache, all[1:], nil)
		confirmGroupModelSchedulingAccount(ctx, "sol", 3)
		want := int64(1)
		if turn == 2 {
			want = 3
		}
		require.Equal(t, want, cache.bindings[warm])
		require.EqualValues(t, 1, cache.bindings[otherKey])
	}
	ctx = pick("affected", all) // Original account recovered; keep the warm backup.
	state := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	require.EqualValues(t, 3, state.fixedSession.account)
	require.EqualValues(t, 0, cache.bindings[groupModelSchedulingStickyKey("sol")])
}

type monitorRecoveryUpstream struct {
	HTTPUpstream
	allFail bool
	calls   []int64
}

func (u *monitorRecoveryUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.calls = append(u.calls, id)
	if id == 1 || u.allFail {
		expireSchedulingAttempt(fastestFailoverAttemptFromContext(req.Context()))
		return nil, req.Context().Err()
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"id":"resp-backup","object":"response","status":"completed","model":"upstream-a","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"2"}]}],"usage":{"input_tokens":4,"output_tokens":1}}`))}, nil
}

func TestMonitorProbeForwardPrimaryTimeoutBackupAndAllFail(t *testing.T) {
	for _, allFail := range []bool{false, true} {
		t.Run(fmt.Sprint(allFail), func(t *testing.T) {
			group := &Group{ID: 71, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
			ctx := context.WithValue(context.Background(), ctxkey.Group, group)
			ctx = context.WithValue(ctx, monitorProbeKey{}, true)
			ctx = context.WithValue(ctx, qualitySchedulingReadOnlyKey{}, true)
			ctx = WithFastestFailoverRequestState(ctx)
			cache := &accountSchedulingCacheStub{bindings: map[string]int64{groupModelSchedulingStickyKey("model-a"): 1}}
			upstream := &monitorRecoveryUpstream{allFail: allFail}
			svc := &OpenAIGatewayService{cache: cache, httpUpstream: upstream, cfg: &config.Config{}}
			body := []byte(`{"model":"model-a","stream":false,"input":"1+1","reasoning":{"effort":"high"}}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body))).WithContext(ctx)
			var lastErr error
			for _, id := range []int64{1, 2} {
				account := &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
					Extra: map[string]any{}, Credentials: map[string]any{"api_key": "test-key", "base_url": "https://example.com", "model_mapping": map[string]any{"model-a": "upstream-a"}}}
				result, err := svc.Forward(ctx, c, account, body)
				lastErr = err
				if err == nil {
					require.NotNil(t, result)
					break
				}
				require.Empty(t, rec.Body.String(), "failed attempt must not expose partial output")
				var failure *UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.True(t, failure.ShouldRetryNextAccount())
			}
			require.Equal(t, []int64{1, 2}, upstream.calls)
			if allFail {
				require.Error(t, lastErr)
				require.Empty(t, rec.Body.String())
			} else {
				require.NoError(t, lastErr)
				require.Contains(t, rec.Body.String(), `"text":"2"`)
			}
			require.EqualValues(t, 1, cache.bindings[groupModelSchedulingStickyKey("model-a")])
		})
	}
}
