package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type schedulingTimeoutRepo struct {
	schedulerTestOpenAIAccountRepo
	cooledModel string
}

func (repo *schedulingTimeoutRepo) SetModelRateLimit(ctx context.Context, accountID int64, model string, until time.Time, reason ...string) error {
	account, err := repo.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	account.Extra[modelRateLimitsKey] = map[string]any{model: map[string]any{"rate_limit_reset_at": until.Format(time.RFC3339)}}
	repo.cooledModel = model
	return nil
}

type schedulingTimeoutUpstream struct {
	HTTPUpstream
	headers   bool
	output    bool
	success   bool
	keepalive bool
	stream    *schedulingTimeoutBody
}

func expireSchedulingAttempt(attempt *fastestFailoverAttempt) {
	attempt.mutex.Lock()
	attempt.deadline = time.Now().Add(-time.Second)
	attempt.mutex.Unlock()
	attempt.expire()
}

func (upstream *schedulingTimeoutUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	attempt := fastestFailoverAttemptFromContext(request.Context())
	if upstream.headers {
		expireSchedulingAttempt(attempt)
		return nil, request.Context().Err()
	}
	payload := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-timeout\"}}\n\n"
	if upstream.output {
		payload += "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
	}
	if strings.HasSuffix(request.URL.Path, "/chat/completions") {
		payload = "data: {\"id\":\"chat-timeout\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n"
		if upstream.output {
			payload += "data: {\"id\":\"chat-timeout\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n"
		}
		if upstream.success {
			payload += "data: {\"id\":\"chat-timeout\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
		}
	} else if upstream.success {
		payload += "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-timeout\",\"status\":\"completed\",\"output\":[]}}\n\n"
	}
	if upstream.success {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
	}
	upstream.stream = &schedulingTimeoutBody{Reader: strings.NewReader(payload), attempt: attempt, waitForOutput: upstream.output, keepalive: upstream.keepalive}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: upstream.stream}, nil
}

type schedulingTimeoutBody struct {
	*strings.Reader
	attempt       *fastestFailoverAttempt
	mutex         sync.Mutex
	closed        bool
	waitForOutput bool
	keepalive     bool
}

func (body *schedulingTimeoutBody) Read(buffer []byte) (int, error) {
	count, err := body.Reader.Read(buffer)
	if err == io.EOF {
		if body.keepalive {
			time.Sleep(1100 * time.Millisecond)
		}
		if body.waitForOutput {
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				body.attempt.mutex.Lock()
				started := body.attempt.outputStarted
				body.attempt.mutex.Unlock()
				if started {
					break
				}
				time.Sleep(time.Millisecond)
			}
		}
		expireSchedulingAttempt(body.attempt)
		return 0, context.DeadlineExceeded
	}
	return count, err
}

func (body *schedulingTimeoutBody) Close() error {
	body.mutex.Lock()
	body.closed = true
	body.mutex.Unlock()
	return nil
}

func TestFastestFailoverTimeoutForwardAndReconnect(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		passthrough bool
		headers     bool
		output      bool
		chat        bool
		raw         bool
		nonstream   bool
		success     bool
		keepalive   bool
	}{
		{name: "native_headers", headers: true},
		{name: "passthrough_headers", passthrough: true, headers: true},
		{name: "native_preamble"},
		{name: "passthrough_preamble", passthrough: true},
		{name: "native_after_output", output: true},
		{name: "passthrough_after_output", passthrough: true, output: true},
		{name: "chat_headers", chat: true, headers: true},
		{name: "chat_preamble", chat: true},
		{name: "chat_preamble_keepalive", chat: true, keepalive: true},
		{name: "chat_after_output", chat: true, output: true},
		{name: "chat_nonstream", chat: true, nonstream: true},
		{name: "responses_nonstream", nonstream: true},
		{name: "passthrough_nonstream", passthrough: true, nonstream: true},
		{name: "raw_chat_headers", chat: true, raw: true, headers: true},
		{name: "raw_chat_preamble", chat: true, raw: true},
		{name: "raw_chat_after_output", chat: true, raw: true, output: true},
		{name: "raw_chat_nonstream", chat: true, raw: true, nonstream: true},
		{name: "responses_via_chat_preamble", raw: true},
		{name: "responses_via_chat_after_output", raw: true, output: true},
		{name: "responses_via_chat_nonstream", raw: true, nonstream: true},
		{name: "native_success", output: true, success: true},
		{name: "passthrough_success", passthrough: true, output: true, success: true},
		{name: "chat_success", chat: true, output: true, success: true},
		{name: "raw_chat_success", chat: true, raw: true, output: true, success: true},
		{name: "responses_via_chat_success", raw: true, output: true, success: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			groupID := int64(103)
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, AccountSchedulingMode: AccountSchedulingModeFastestFailover})
			repo := &schedulingTimeoutRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{
				{ID: 1, Priority: 1}, {ID: 2, Priority: 2},
			}}}
			for index := range repo.accounts {
				account := &repo.accounts[index]
				account.Platform, account.Type, account.Status = PlatformOpenAI, AccountTypeAPIKey, StatusActive
				account.Schedulable, account.Concurrency = true, 1
				account.GroupIDs = []int64{groupID}
				account.Extra = map[string]any{"openai_passthrough": scenario.passthrough, "scheduling_preferred": index == 0}
				if scenario.raw {
					account.Extra["openai_responses_supported"] = false
				}
				account.Credentials = map[string]any{"api_key": "test-key", "base_url": "https://api.example.com", "model_mapping": map[string]any{"model-a": "upstream-a", "model-b": "upstream-b"}}
			}
			upstream := &schedulingTimeoutUpstream{headers: scenario.headers, output: scenario.output, success: scenario.success, keepalive: scenario.keepalive}
			cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
			svc := &OpenAIGatewayService{accountRepo: repo, cache: cache, httpUpstream: upstream, cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
			if scenario.keepalive {
				svc.cfg.Gateway.StreamKeepaliveInterval = 1
			}
			rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "model-a", 1)
			body := []byte(`{"model":"model-a","stream":true,"input":"hello","reasoning":{"effort":"high"}}`)
			if scenario.nonstream {
				body = []byte(`{"model":"model-a","stream":false,"input":"hello"}`)
			}
			if scenario.chat {
				body = []byte(`{"model":"model-a","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
				if scenario.nonstream {
					body = []byte(`{"model":"model-a","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
				}
			}
			recorder := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(recorder)
			ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body))).WithContext(ctx)
			var err error
			if scenario.chat {
				_, err = svc.ForwardAsChatCompletions(ctx, ginCtx, &repo.accounts[0], body, "", "")
			} else {
				_, err = svc.Forward(ctx, ginCtx, &repo.accounts[0], body)
			}
			if scenario.success {
				require.NoError(t, err)
				require.Contains(t, recorder.Body.String(), "hello")
				require.Empty(t, repo.cooledModel)
				return
			}
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusGatewayTimeout, failoverErr.StatusCode)
			require.False(t, failoverErr.RetryableOnSameAccount)
			require.False(t, failoverErr.SafeToFailoverAfterWrite)
			require.Equal(t, "upstream-a", repo.cooledModel)
			require.False(t, repo.accounts[0].IsSchedulableForModel("model-a"))
			require.True(t, repo.accounts[0].IsSchedulableForModel("model-b"))
			if scenario.output {
				require.Contains(t, recorder.Body.String(), "hello")
			} else if scenario.keepalive {
				require.Contains(t, recorder.Body.String(), ":\n\n")
				require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(ginCtx))
			} else {
				require.Empty(t, recorder.Body.String())
			}
			for _, model := range []string{"model-a", "model-b"} {
				capability := OpenAIEndpointCapabilityResponses
				if scenario.raw {
					capability = OpenAIEndpointCapabilityChatCompletions
				}
				selected, _, _ := svc.selectBestAccount(ctx, &groupID, PlatformOpenAI, repo.accounts, model, nil, false, capability, false)
				require.NotNil(t, selected)
				if model == "model-a" {
					require.EqualValues(t, 2, selected.ID)
				} else {
					require.EqualValues(t, 1, selected.ID)
				}
			}
		})
	}
}

func TestFastestFailoverTimeoutOptInAndTimer(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 1, Platform: PlatformOpenAI}
	ctx, attempt := svc.beginFastestFailoverAttempt(context.Background(), account)
	require.Nil(t, attempt)
	ctx = context.WithValue(ctx, ctxkey.Group, &Group{AccountSchedulingMode: AccountSchedulingModeFastestFailover})
	ctx, attempt = svc.beginFastestFailoverAttempt(ctx, account)
	require.Equal(t, 60*time.Second, attempt.firstTimeout)
	require.Equal(t, 120*time.Second, attempt.idleTimeout)
	attempt.firstTimeout = 20 * time.Millisecond
	bound := attempt.bind(ctx)
	select {
	case <-bound.Done():
	case <-time.After(time.Second):
		t.Fatal("deadline did not cancel upstream")
	}
	require.ErrorIs(t, bound.Err(), context.Canceled)
	_ = svc.finishFastestFailoverAttempt(ctx, nil, account, nil, attempt, nil)
}

func TestFastestFailoverTimeoutOutputSwitchesToIdle(t *testing.T) {
	attempt := &fastestFailoverAttempt{firstTimeout: time.Hour, idleTimeout: 30 * time.Millisecond}
	ctx := context.WithValue(context.Background(), fastestFailoverAttemptKey{}, attempt)
	bound := attempt.bind(ctx)
	attempt.progress(false)
	require.False(t, attempt.outputStarted)
	markFastestFailoverOutput(ctx)
	attempt.expire()
	select {
	case <-bound.Done():
	case <-time.After(time.Second):
		t.Fatal("idle deadline did not cancel upstream")
	}
}

func TestFastestFailoverTimeoutSkipsStaleSnapshot(t *testing.T) {
	for _, loadBatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "load_aware"}[loadBatch], func(t *testing.T) {
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(104)
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, AccountSchedulingMode: AccountSchedulingModeFastestFailover})
			primary := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID}, Extra: map[string]any{"scheduling_preferred": true}}
			backup := primary
			backup.ID, backup.Extra = 2, nil
			stalePrimary := primary
			stalePrimary.Extra = map[string]any{"scheduling_preferred": true}
			repo := &schedulingTimeoutRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{primary, backup}}}
			require.NoError(t, repo.SetModelRateLimit(ctx, 1, "model-a", time.Now().Add(fastestFailoverTimeoutCooldown)))
			cache := &schedulerTestGatewayCache{sessionBindings: make(map[string]int64)}
			rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "model-a", 1)
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatch
			svc := &OpenAIGatewayService{
				accountRepo: repo, cache: cache, cfg: cfg,
				rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
				schedulerSnapshot: &SchedulerSnapshotService{cache: &openAISnapshotCacheStub{
					snapshotAccounts: []*Account{&stalePrimary, &backup},
					accountsByID:     map[int64]*Account{1: &stalePrimary, 2: &backup},
				}},
			}
			for _, model := range []string{"model-a", "model-b"} {
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "reconnect", model, nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.NotNil(t, selection)
				if model == "model-a" {
					require.EqualValues(t, 2, selection.Account.ID)
				} else {
					require.EqualValues(t, 1, selection.Account.ID)
				}
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
			}
		})
	}
}

func TestFastestFailoverTimeoutDoesNotCoolHealthyOrCanceledRequests(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "client_canceled"}[canceled], func(t *testing.T) {
			repo := &schedulingTimeoutRepo{}
			svc := &OpenAIGatewayService{accountRepo: repo}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = context.WithValue(ctx, ctxkey.Group, &Group{AccountSchedulingMode: AccountSchedulingModeFastestFailover})
			account := &Account{ID: 1, Platform: PlatformOpenAI}
			ctx, attempt := svc.beginFastestFailoverAttempt(ctx, account)
			bound := attempt.bind(ctx)
			var forwardErr error
			if canceled {
				cancel()
				forwardErr = context.DeadlineExceeded
			}
			err := svc.finishFastestFailoverAttempt(ctx, nil, account, []byte(`{"model":"model-a"}`), attempt, forwardErr)
			require.Equal(t, forwardErr, err)
			require.Empty(t, repo.cooledModel)
			require.ErrorIs(t, bound.Err(), context.Canceled)
		})
	}
}
