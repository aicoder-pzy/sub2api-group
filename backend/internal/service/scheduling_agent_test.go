package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type agentTestSettings struct {
	SettingRepository
	values map[string]string
}

func (s *agentTestSettings) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (s *agentTestSettings) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

type agentTestEncryptor struct{}

func (agentTestEncryptor) Encrypt(value string) (string, error) { return "encrypted:" + value, nil }
func (agentTestEncryptor) Decrypt(value string) (string, error) {
	return value[len("encrypted:"):], nil
}

type agentTestObservations struct{}

func (agentTestObservations) GetModelAutoAccountQuality(context.Context, int64, string, string) (map[int64]GroupModelAccountQuality, error) {
	return map[int64]GroupModelAccountQuality{1: {Successes: 20}, 2: {Successes: 10}}, nil
}

type agentTestSticky struct {
	GatewayCache
	id int64
}

func (s *agentTestSticky) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	if s.id == 0 {
		return 0, ErrStickySessionNotFound
	}
	return s.id, nil
}
func (s *agentTestSticky) CompareAndSwapFixedSession(_ context.Context, _ int64, _ string, previous, id int64, _ time.Duration) error {
	if previous == s.id {
		s.id = id
	}
	return nil
}

func TestSchedulingAgentPolicyAndOutage(t *testing.T) {
	ctx := context.Background()
	repo := &agentTestSettings{values: map[string]string{}}
	settings := &SettingService{settingRepo: repo}
	var status atomic.Int32
	status.Store(http.StatusOK)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer model-key" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(int(status.Load()))
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"action\":\"shorten_capacity_wait\",\"model\":\"astra\",\"account_id\":1,\"wait_ms\":500,\"reason\":\"test\"}"}}],"usage":{"prompt_tokens":100,"completion_tokens":20}}`)
	}))
	defer model.Close()
	runtime := &schedulingAgentRuntime{settings: settings, observations: agentTestObservations{}, secrets: agentTestEncryptor{}, client: model.Client(), lock: &fakeLeaderLockCache{}, queue: make(map[schedulingQueueKey]schedulingQueueSample), actions: make(map[int64]schedulingActionCount)}
	settings.schedulingAgent = runtime
	cfg := SchedulingAgentConnection{Version: 8, URL: model.URL + "/v1", Model: "judge", APIKey: "model-key", DailyCalls: 10}
	cfg.APIKey, _ = runtime.secrets.Encrypt(cfg.APIKey)
	require.NoError(t, writeSchedulingJSON(ctx, repo, schedulingAgentConnectionKey, cfg))
	group := &Group{ID: 31, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
	policy := SchedulingPolicy{Version: 7, Accounts: []int64{3, 1}, Models: map[string][]int64{"astra": {1, 2}}, AgentMode: "auto", DailyCalls: 2, MinWaitMS: 400}
	require.NoError(t, policy.validate([]int64{1, 2, 3}))
	require.Error(t, policy.validate([]int64{1, 3}))
	require.NoError(t, writeSchedulingJSON(ctx, repo, schedulingPolicyKey(group.ID), policy))
	runtime.queue[schedulingQueueKey{group.ID, "astra", 1}] = schedulingQueueSample{started: time.Now(), at: time.Now(), full: 3, waited: 6000}
	run, err := runtime.run(ctx, group, policy, cfg)
	require.NoError(t, err)
	require.Equal(t, "applied", run.State)
	require.Equal(t, 100, run.InputTokens)
	require.Equal(t, 1, run.Calls)
	request := func(g *Group, model string, cache GatewayCache, candidates ...*Account) (context.Context, []*Account) {
		c := WithFastestFailoverRequestState(context.WithValue(ctx, ctxkey.Group, g))
		c = withModelAutoSession(settings.withFixedScheduling(c, &g.ID, model), "conversation")
		return c, fastestFailoverCandidateOrder(c, accountSchedulingQualityStub{quality: map[int64]GroupModelAccountQuality{2: {Successes: 10}}}, cache, &g.ID, model, candidates)
	}
	cache := &agentTestSticky{}
	primary, backup, other := &Account{ID: 1}, &Account{ID: 2}, &Account{ID: 3}
	c, ordered := request(group, "astra", cache, other, backup, primary)
	require.Equal(t, []*Account{primary, backup}, ordered)
	require.Equal(t, 500*time.Millisecond, schedulingAgentWait(c, 1))
	// A completed first request binds the session to its successful account.
	confirmGroupModelSchedulingAccount(c, "astra", 2)
	c, ordered = request(group, "astra", cache, other, backup, primary)
	require.Equal(t, []*Account{backup, primary}, ordered)
	require.Equal(t, 2*time.Second, schedulingAgentWait(c, 1))
	preserveSmartChannelCapacitySelection(c, &AccountSelectionResult{Account: primary})
	confirmGroupModelSchedulingAccount(c, "astra", 1)
	require.Equal(t, int64(2), cache.id)
	_, ordered = request(group, "astra", &agentTestSticky{}, other, backup)
	require.Equal(t, []*Account{backup}, ordered)
	_, ordered = request(group, "other", &agentTestSticky{}, backup, primary, other)
	require.Equal(t, []*Account{other, primary}, ordered)
	alternate := &Group{ID: 32, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
	c, _ = request(alternate, "astra", &agentTestSticky{}, other, backup, primary)
	require.Equal(t, 2*time.Second, schedulingAgentWait(c, 1))
	run.At = run.At.Add(-2 * time.Minute)
	require.NoError(t, runtime.storeRun(ctx, group.ID, run))
	status.Store(503)
	run, err = runtime.run(ctx, group, policy, cfg)
	require.NoError(t, err)
	require.Equal(t, "model_http_503", run.State)
	c, ordered = request(group, "astra", &agentTestSticky{}, other, backup, primary)
	require.Equal(t, []*Account{primary, backup}, ordered)
	require.Equal(t, 2*time.Second, schedulingAgentWait(c, 1))
	run.At = run.At.Add(-2 * time.Minute)
	require.NoError(t, runtime.storeRun(ctx, group.ID, run))
	run, err = runtime.run(ctx, group, policy, cfg)
	require.NoError(t, err)
	require.Equal(t, "budget_exhausted", run.State)
}
