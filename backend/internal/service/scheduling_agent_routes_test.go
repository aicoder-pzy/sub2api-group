package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type agentRouteObservations struct {
	samples map[int64]GroupModelAccountQuality
	windows map[int64][]SchedulingAgentSpeedWindow
}

func (o *agentRouteObservations) GetModelAutoAccountQuality(context.Context, int64, string, string) (map[int64]GroupModelAccountQuality, error) {
	return o.samples, nil
}

func (o *agentRouteObservations) GetSchedulingAgentSpeedWindows(_ context.Context, _ int64, model string, _ []int64) (map[int64][]SchedulingAgentSpeedWindow, error) {
	if model != "sol" {
		return nil, nil
	}
	return o.windows, nil
}

type agentRouteTrialCache struct {
	agentTestSticky
	ModelAutoSchedulingCache
	calls int
}

func (c *agentRouteTrialCache) AssignModelAutoSession(_ context.Context, _ int64, _ string, _ string, primary int64, _ []int64, backups []int64, _ time.Duration) (int64, bool, error) {
	c.calls++
	if c.calls == 1 {
		return backups[0], true, nil
	}
	return primary, true, nil
}

func agentRouteWindows(p50, p95 float64) []SchedulingAgentSpeedWindow {
	w := SchedulingAgentSpeedWindow{ModelAutoSpeedBucket: ModelAutoSpeedBucket{ContextBucket: 2, Samples: 8, P50MS: p50, P95MS: p95, OutputTPS: 30, OutputSamples: 8}, Effort: "xhigh"}
	previous := w
	previous.Age = 1
	return []SchedulingAgentSpeedWindow{w, previous}
}

func TestSchedulingAgentTrialAndPreferenceStayWithExactModel(t *testing.T) {
	ctx := context.Background()
	p := testSchedulingPoolPolicy()
	g := &Group{ID: 24, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
	repo := &agentTestSettings{values: map[string]string{}}
	s := &SettingService{settingRepo: repo}
	now := time.Now()
	sample := GroupModelAccountQuality{Successes: 35, Quality: &AccountQualityEvidence{LatestAt: &now, LatestVerdict: "normal", NormalStreak: 2, TestEffort: "xhigh"}}
	o := &agentRouteObservations{samples: map[int64]GroupModelAccountQuality{1: sample, 3: sample}, windows: map[int64][]SchedulingAgentSpeedWindow{1: agentRouteWindows(32000, 55000)}}
	decisions := make(chan string, 3)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		action := <-decisions
		if action == "outage" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		data, _ := json.Marshal(schedulingAgentDecision{Action: action, Model: "sol", AccountID: 3, Reason: "matched windows"})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(data)}}}})
	}))
	defer model.Close()
	r := &schedulingAgentRuntime{settings: s, observations: o, secrets: agentTestEncryptor{}, client: model.Client(), lock: &fakeLeaderLockCache{}}
	s.schedulingAgent = r
	cfg := SchedulingAgentConnection{Version: 7, APIKey: "encrypted:model-key", URL: model.URL, Model: "judge", DailyCalls: 12}
	require.NoError(t, writeSchedulingJSON(ctx, repo, schedulingAgentConnectionKey, cfg))
	require.NoError(t, writeSchedulingJSON(ctx, repo, schedulingPolicyKey(g.ID), p))

	request := func(group int64, model, effort, session string, cache GatewayCache, accounts ...*Account) []int64 {
		c := WithFastestFailoverRequestState(context.WithValue(ctx, ctxkey.Group, g))
		c = withModelAutoSession(WithRequestedReasoningEffort(c, effort), session)
		scope := fixedSchedulingScope{s, group, model, p, p.order(model)}
		return poolAccountIDs(orderFixedScheduling(c, scope, cache, accounts, o.samples))
	}
	first, second := &Account{ID: 1}, &Account{ID: 3}
	advance := func(run SchedulingAgentRun) {
		run.At = run.At.Add(-2 * time.Minute)
		require.NoError(t, r.storeRun(ctx, g.ID, run))
	}
	decisions <- schedulingTrialRoute
	run, err := r.run(ctx, g, p, cfg)
	require.NoError(t, err)
	require.Equal(t, "route_trial", run.State)
	require.EqualValues(t, 3, run.Routes["sol"].TrialID)
	cache := &agentRouteTrialCache{}
	require.Equal(t, []int64{1, 3}, request(g.ID, "sol", "high", "wrong-effort", cache, first, second))
	require.Equal(t, 0, cache.calls)
	require.Equal(t, []int64{3, 1}, request(g.ID, "sol", "xhigh", "trial", cache, first, second))
	require.Equal(t, []int64{1, 3}, request(g.ID, "sol", "xhigh", "not-allocated", cache, first, second))

	advance(run)
	o.windows[3] = agentRouteWindows(12000, 25000)
	decisions <- schedulingPreferRoute
	run, err = r.run(ctx, g, p, cfg)
	require.NoError(t, err)
	require.Equal(t, "route_selected", run.State)
	require.Equal(t, []int64{1, 3, 4}, p.order("sol"), "administrator preference is not rewritten")
	require.Equal(t, []int64{3, 1}, request(g.ID, "sol", "xhigh", "new", cache, first, second))
	require.Equal(t, []int64{1, 3}, request(g.ID, "astra", "xhigh", "other-model", cache, first, second))
	require.Equal(t, []int64{1, 3}, request(25, "sol", "xhigh", "other-group", cache, first, second))
	cache.id = 1
	require.Equal(t, []int64{1, 3}, request(g.ID, "sol", "xhigh", "existing", cache, first, second))
	cache.id = 0

	advance(run)
	decisions <- "outage"
	run, err = r.run(ctx, g, p, cfg)
	require.NoError(t, err)
	require.Equal(t, "model_http_503", run.State)
	require.Equal(t, []int64{3, 1}, request(g.ID, "sol", "xhigh", "outage", cache, first, second))
	require.Equal(t, []int64{1}, request(g.ID, "sol", "xhigh", "ineligible", cache, first))
	p.Version++
	require.Equal(t, []int64{1, 3}, request(g.ID, "sol", "xhigh", "revoked", cache, first, second))
}

func TestSchedulingAgentRouteRejectsUnmatchedOrWeakEvidence(t *testing.T) {
	p := testSchedulingPoolPolicy()
	now := time.Now()
	d := schedulingAgentDecision{Action: schedulingPreferRoute, Model: "sol", AccountID: 3, Reason: "evidence"}
	for _, tc := range []struct {
		name string
		edit func([]schedulingAgentEvidence)
	}{
		{"one window only", func(e []schedulingAgentEvidence) { e[1].Windows = e[1].Windows[:1] }},
		{"different effort", func(e []schedulingAgentEvidence) { e[1].Windows[1].Effort = "high" }},
		{"different context", func(e []schedulingAgentEvidence) { e[1].Windows[0].ContextBucket++ }},
		{"quality unknown", func(e []schedulingAgentEvidence) { e[1].QualityVerified = false }},
		{"unstable route", func(e []schedulingAgentEvidence) { e[1].RecentFailures = 1 }},
		{"not materially better", func(e []schedulingAgentEvidence) { e[1].Windows = agentRouteWindows(31000, 54000) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := []schedulingAgentEvidence{
				{Model: "sol", AccountID: 1, Eligible: true, Current: true, Windows: agentRouteWindows(32000, 55000)},
				{Model: "sol", AccountID: 3, Eligible: true, Successes: 20, QualityEffort: "xhigh", QualityVerified: true, Windows: agentRouteWindows(12000, 25000)},
			}
			tc.edit(e)
			_, ok := schedulingRouteChange(p, SchedulingAgentRoute{}, d, e, now)
			require.False(t, ok)
		})
	}
}
