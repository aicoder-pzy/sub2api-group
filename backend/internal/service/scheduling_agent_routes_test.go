package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
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

type agentInvestigationFixture struct {
	agentRouteObservations
	activity []SchedulingAgentActivity
	queries  []schedulingAgentDecision
	traffic  []SchedulingAgentTraffic
}

func (o *agentInvestigationFixture) GetSchedulingAgentActivity(context.Context, int64, []string) ([]SchedulingAgentActivity, error) {
	return o.activity, nil
}

func (o *agentInvestigationFixture) GetSchedulingAgentTraffic(context.Context, int64, string) ([]SchedulingAgentTraffic, error) {
	return o.traffic, nil
}

func (o *agentInvestigationFixture) GetSchedulingAgentCohorts(_ context.Context, _ int64, model, effort string, hours int, _ []int64) (map[int64][]SchedulingAgentSpeedWindow, error) {
	o.queries = append(o.queries, schedulingAgentDecision{Model: model, Effort: effort, Hours: hours})
	return map[int64][]SchedulingAgentSpeedWindow{1: agentRouteWindows(9000, 12345)}, nil
}

func TestSchedulingAgentInvestigatesNewEvidenceAndStopsTrial(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	g := &Group{ID: 24, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
	p := testSchedulingPoolPolicy()
	quality := GroupModelAccountQuality{Successes: 35, Quality: &AccountQualityEvidence{LatestAt: &now, LatestVerdict: "normal", NormalStreak: 2, TestEffort: "xhigh"}}
	o := &agentInvestigationFixture{agentRouteObservations: agentRouteObservations{
		samples: map[int64]GroupModelAccountQuality{1: quality, 3: quality},
		windows: map[int64][]SchedulingAgentSpeedWindow{1: agentRouteWindows(32000, 55000)[:1]},
	}}
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		d := schedulingAgentDecision{Model: "sol", Effort: "xhigh", AccountID: 3}
		switch calls.Add(1) {
		case 1:
			d.Action, d.Hours, d.Reason = "inspect", 6, "check same-effort baseline"
		case 2:
			if !bytes.Contains(body, []byte("12345")) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			d.Action, d.Reason = schedulingTrialRoute, "recent slowdown versus baseline, gather a sample"
		default:
			if !bytes.Contains(body, []byte("gather a sample")) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			d.Action, d.Reason = "stop_trial", "new evidence changes the assessment"
		}
		data, _ := json.Marshal(d)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(data)}}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 20}})
	}))
	defer model.Close()
	repo := &agentTestSettings{values: map[string]string{}}
	s := &SettingService{settingRepo: repo}
	r := &schedulingAgentRuntime{settings: s, observations: o, secrets: agentTestEncryptor{}, client: model.Client(), lock: &fakeLeaderLockCache{}}
	s.schedulingAgent = r
	cfg := SchedulingAgentConnection{Version: 7, APIKey: "encrypted:model-key", URL: model.URL, Model: "judge", DailyCalls: 12}
	require.NoError(t, writeSchedulingJSON(ctx, repo, schedulingAgentConnectionKey, cfg))
	require.NoError(t, writeSchedulingJSON(ctx, repo, schedulingPolicyKey(g.ID), p))
	_, err := r.runForActivity(ctx, g, p, cfg, true)
	require.NoError(t, err)
	require.Zero(t, calls.Load(), "old aggregates alone cannot wake the agent")
	o.activity = []SchedulingAgentActivity{{Model: "sol", At: now}}
	run, err := r.runForActivity(ctx, g, p, cfg, true)
	require.NoError(t, err)
	require.Equal(t, "route_trial", run.State)
	require.EqualValues(t, 3, run.Routes["sol"].TrialID)
	require.Equal(t, 2, run.Calls)
	require.Equal(t, 200, run.InputTokens)
	require.Equal(t, []schedulingAgentDecision{{Model: "sol", Effort: "xhigh", Hours: 6}}, o.queries)
	run.At = run.At.Add(-2 * time.Minute)
	require.NoError(t, r.storeRun(ctx, g.ID, run))
	_, err = r.runForActivity(ctx, g, p, cfg, true)
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load(), "the same traffic must not be billed again")
	o.activity[0].At = now.Add(time.Second)
	run, err = r.runForActivity(ctx, g, p, cfg, true)
	require.NoError(t, err)
	require.Equal(t, "trial_stopped", run.State)
	require.Zero(t, run.Routes["sol"].TrialID)
	require.True(t, run.Routes["sol"].TrialUntil.After(now), "stopping does not clear the cooldown")
}

func TestSchedulingAgentPrioritizes502AndSavesLastCallForDecision(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	g := &Group{ID: 24, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
	p := testSchedulingPoolPolicy()
	p.DailyCalls = 10
	o := &agentInvestigationFixture{
		agentRouteObservations: agentRouteObservations{samples: map[int64]GroupModelAccountQuality{1: {Successes: 10}}},
		activity: []SchedulingAgentActivity{
			{Model: "astra", At: now},
			{Model: "sol", At: now.Add(-10 * time.Second)},
		},
		traffic: []SchedulingAgentTraffic{{AccountID: 1, Endpoint: "/v1/chat/completions", Stream: true, Samples: 12}},
	}
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var body struct {
			Messages []map[string]string `json:"messages"`
		}
		if json.NewDecoder(req.Body).Decode(&body) != nil || len(body.Messages) < 3 ||
			!bytes.Contains([]byte(body.Messages[1]["content"]), []byte(`"stream":true,"effort":"","requests":12`)) ||
			!bytes.Contains([]byte(body.Messages[len(body.Messages)-1]["content"]), []byte("0 inspections left")) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"action":"hold","model":"sol","reason":"Need comparable samples"}`}}}})
	}))
	defer model.Close()
	repo := &agentTestSettings{values: map[string]string{}}
	r := &schedulingAgentRuntime{settings: &SettingService{settingRepo: repo}, observations: o, secrets: agentTestEncryptor{}, client: model.Client(), lock: &fakeLeaderLockCache{}}
	cfg := SchedulingAgentConnection{Version: 7, APIKey: "encrypted:model-key", URL: model.URL, Model: "judge", DailyCalls: 2, IntervalMinutes: 10}
	last := SchedulingAgentRun{At: now.Add(-2 * time.Minute), Day: now.UTC().Format(time.DateOnly), Version: p.Version, ConnectionVersion: cfg.Version,
		Reviews: map[string]schedulingAgentReview{"sol": {Through: now.Add(-3 * time.Minute)}}}
	require.NoError(t, r.storeRun(ctx, g.ID, last))
	activity := o.activity
	o.activity = nil
	r.queue = map[schedulingQueueKey]schedulingQueueSample{{g.ID, "sol", 1}: {started: now, at: now, full: 4}}
	_, err := r.runForActivity(ctx, g, p, cfg, true)
	require.NoError(t, err)
	require.Zero(t, calls.Load(), "queue observations alone do not start an analysis")
	o.activity = activity
	_, err = r.runForActivity(ctx, g, p, cfg, true)
	require.NoError(t, err)
	require.Zero(t, calls.Load(), "ordinary traffic respects the interval")

	o.activity[1].FailureSince, o.activity[1].FailedAccount, o.activity[1].Failures = now.Add(-time.Minute), 1, 2
	run, err := r.runForActivity(ctx, g, p, cfg, true)
	require.NoError(t, err)
	require.Equal(t, "model_http_503", run.State)
	require.Equal(t, "sol", run.Signal.Model, "the 502 burst wins over newer ordinary traffic")
	require.True(t, run.Reviews["sol"].Through.Equal(last.Reviews["sol"].Through), "failed analysis does not consume activity")
	run.At = run.At.Add(-11 * time.Minute)
	require.NoError(t, r.storeRun(ctx, g.ID, run))
	run, err = r.runForActivity(ctx, g, p, cfg, true)
	require.NoError(t, err)
	require.Equal(t, "observed", run.State)
	require.Equal(t, "hold", run.Decision.Action)
	require.True(t, run.Reviews["sol"].Through.Equal(o.activity[1].At))
	require.Equal(t, 2, run.Calls)
	require.Empty(t, o.queries, "with one slot left, decide using existing evidence")

	run.At = run.At.Add(-11 * time.Minute)
	require.NoError(t, r.storeRun(ctx, g.ID, run))
	run, err = r.runForActivity(ctx, g, p, cfg, true)
	require.NoError(t, err)
	require.Equal(t, "budget_exhausted", run.State)
	run.At = run.At.Add(-11 * time.Minute)
	require.NoError(t, r.storeRun(ctx, g.ID, run))
	again, err := r.runForActivity(ctx, g, p, cfg, true)
	require.NoError(t, err)
	require.True(t, again.At.Equal(run.At), "repeated budget blocks do not replace review history")
	require.EqualValues(t, 2, calls.Load())
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
		if session != "sync" {
			c = WithSchedulingAgentRequestType(c, RequestTypeStream)
		}
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
	require.Equal(t, []int64{1, 3}, request(g.ID, "sol", "xhigh", "sync", cache, first, second))
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

func TestSchedulingAgentTrialsAfterFaultUsingOtherModelMonitor(t *testing.T) {
	p := testSchedulingPoolPolicy()
	now := time.Now()
	d := schedulingAgentDecision{Action: schedulingTrialRoute, Model: "sol", AccountID: 3, Effort: "xhigh", Reason: "same pool monitoring after a user failure"}
	snapshot := &UpstreamSiteMonitor{Status: "ok", Rows: []UpstreamSiteMonitorRow{
		{Model: "gpt-6.1-sol", GroupID: "42", CheckedAt: &now, State: "healthy", LatencyKind: "check"},
	}}
	link := schedulingAgentMonitorLink{model: "gpt-6.2-sol", group: "42"}
	monitors := schedulingMonitorRows(link, "backup", snapshot)
	require.Len(t, monitors, 1)
	require.Equal(t, "other_model", monitors[0].Scope)
	evidence := []schedulingAgentEvidence{
		{Model: "sol", AccountID: 1, Current: true, Eligible: true},
		{Model: "sol", AccountID: 3, Eligible: true, Monitors: monitors},
	}
	failures := []SchedulingAgentFailure{{AccountID: 1, Phase: "upstream", Status: 502, Upstream: 502, LastAt: now}}
	route, ok := schedulingRouteChange(p, SchedulingAgentRoute{}, d, evidence, now, failures, SchedulingAgentActivity{})
	require.True(t, ok)
	require.EqualValues(t, 3, route.TrialID)
	require.True(t, route.TrialFromMonitor)
	require.Zero(t, route.AccountID)

	check := func(change func([]schedulingAgentEvidence, []SchedulingAgentFailure, *schedulingAgentDecision)) {
		t.Helper()
		e := append([]schedulingAgentEvidence(nil), evidence...)
		e[1].Monitors = append([]schedulingAgentMonitor(nil), monitors...)
		f, decision := append([]SchedulingAgentFailure(nil), failures...), d
		change(e, f, &decision)
		_, allowed := schedulingRouteChange(p, SchedulingAgentRoute{}, decision, e, now, f, SchedulingAgentActivity{})
		require.False(t, allowed)
	}
	check(func(_ []schedulingAgentEvidence, f []SchedulingAgentFailure, _ *schedulingAgentDecision) {
		f[0].Status = 200
	})
	check(func(_ []schedulingAgentEvidence, f []SchedulingAgentFailure, _ *schedulingAgentDecision) {
		f[0].AccountID = 0
	})
	check(func(e []schedulingAgentEvidence, _ []SchedulingAgentFailure, _ *schedulingAgentDecision) {
		e[1].Eligible = false
	})
	check(func(e []schedulingAgentEvidence, _ []SchedulingAgentFailure, _ *schedulingAgentDecision) {
		e[1].Monitors[0].GroupMatch = false
	})
	check(func(e []schedulingAgentEvidence, _ []SchedulingAgentFailure, _ *schedulingAgentDecision) {
		e[1].Monitors[0].Stale = true
	})
	check(func(_ []schedulingAgentEvidence, _ []SchedulingAgentFailure, d *schedulingAgentDecision) {
		d.Action = schedulingPreferRoute
	})

	ctx := WithRequestedReasoningEffort(context.Background(), "xhigh")
	cache := &agentRouteTrialCache{}
	scope := fixedSchedulingScope{groupID: 24, model: "sol"}
	accounts := []*Account{{ID: 1}, {ID: 3}}
	samples := map[int64]GroupModelAccountQuality{}
	ordered := orderSchedulingAgentRoute(ctx, scope, fixedSessionSnapshot{cache: cache, key: "new"}, slices.Clone(accounts), samples, route)
	require.Equal(t, []int64{3, 1}, poolAccountIDs(ordered), "a candidate without local quality evidence gets one limited fault trial")
	ordered = orderSchedulingAgentRoute(ctx, scope, fixedSessionSnapshot{cache: cache, key: "newer"}, accounts, samples, route)
	require.Equal(t, []int64{1, 3}, poolAccountIDs(ordered))
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
			_, ok := schedulingRouteChange(p, SchedulingAgentRoute{}, d, e, now, nil, SchedulingAgentActivity{})
			require.False(t, ok)
		})
	}
}

// Replay a cache trial follow-up while an unrelated failure awaits review.
func TestSchedulingAgentReviewsCacheTrialWithoutNewTrialErrors(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	group := &Group{ID: 24, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
	policy := testSchedulingPoolPolicy()
	quality := GroupModelAccountQuality{Successes: 35, Quality: &AccountQualityEvidence{LatestAt: &now, LatestVerdict: "normal", NormalStreak: 2, TestEffort: "xhigh"}}
	window := now.Add(-time.Minute).Truncate(10 * time.Minute)
	low := SchedulingAgentCacheSession{Session: "first", Effort: "xhigh", Baseline: .93, Previous: .74, Latest: .72}
	observations := &agentCacheFixture{
		agentInvestigationFixture: agentInvestigationFixture{agentRouteObservations: agentRouteObservations{samples: map[int64]GroupModelAccountQuality{1: quality, 3: quality}}},
		incidents:                 []SchedulingAgentCacheIncident{{Model: "sol", At: window, AccountID: 1, Affected: 1, Comparable: 10, Low: []SchedulingAgentCacheSession{low}}},
	}
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decision := schedulingAgentDecision{Model: "sol", Effort: "xhigh", AccountID: 3, Reason: "fixture"}
		switch calls.Add(1) {
		case 1:
			if !bytes.Contains(body, []byte(`\"affected_sessions\":1`)) {
				w.WriteHeader(400)
				return
			}
			decision.Action = schedulingTrialRoute // Even a bad model answer cannot move the group.
		case 2:
			if !bytes.Contains(body, []byte(`\"affected_sessions\":2`)) {
				w.WriteHeader(400)
				return
			}
			decision.Action = schedulingTrialRoute
		default:
			if !bytes.Contains(body, []byte(`\"trial_warm_requests\":4`)) {
				w.WriteHeader(400)
				return
			}
			decision.Action = "stop_trial"
		}
		data, _ := json.Marshal(decision)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(data)}}}})
	}))
	defer endpoint.Close()
	repo := &agentTestSettings{values: map[string]string{}}
	settings := &SettingService{settingRepo: repo}
	r := &schedulingAgentRuntime{settings: settings, observations: observations, trials: observations, client: endpoint.Client(), secrets: agentTestEncryptor{}, lock: &fakeLeaderLockCache{}}
	cfg := SchedulingAgentConnection{Version: 7, APIKey: "encrypted:model-key", URL: endpoint.URL, Model: "judge", DailyCalls: 12}
	require.NoError(t, writeSchedulingJSON(ctx, repo, schedulingAgentConnectionKey, cfg))
	require.NoError(t, writeSchedulingJSON(ctx, repo, schedulingPolicyKey(group.ID), policy))
	run, err := r.runForActivity(ctx, group, policy, cfg, true)
	require.NoError(t, err)
	require.Equal(t, "observed", run.State)
	require.Equal(t, 10, run.Signal.Cache.Comparable)
	advance := func() {
		run.At = run.At.Add(-11 * time.Minute)
		require.NoError(t, r.storeRun(ctx, group.ID, run))
	}
	advance()
	_, err = r.runForActivity(ctx, group, policy, cfg, true)
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load(), "the same cache window is reviewed once")
	low.Session = "second"
	observations.incidents[0].At = window.Add(10 * time.Minute)
	observations.incidents[0].Affected = 2
	observations.incidents[0].Low = append(observations.incidents[0].Low, low)
	clear(r.cacheScans)
	run, err = r.runForActivity(ctx, group, policy, cfg, true)
	require.NoError(t, err)
	require.Equal(t, "route_trial", run.State)
	route := run.Routes["sol"]
	route.TrialStartedAt = now.Add(-12 * time.Minute)
	run.Routes["sol"] = route
	observations.cohorts = []SchedulingAgentTrialCohort{{Effort: "xhigh", TrialSessions: 2, ControlSessions: 2, TrialRequests: 4, ControlRequests: 5, TrialCache: .66, ControlCache: .76, TrialP95: 3000, ControlP95: 2000}}
	observations.activity = []SchedulingAgentActivity{{Model: "sol", At: now}}
	advance()
	run, err = r.runForActivity(ctx, group, policy, cfg, true)
	require.NoError(t, err)
	require.Equal(t, "trial_stopped", run.State)
	require.NotNil(t, run.Signal.Trial)
	require.Zero(t, run.Routes["sol"].TrialID)
	require.EqualValues(t, 3, calls.Load())
}

type agentCacheFixture struct {
	agentInvestigationFixture
	incidents []SchedulingAgentCacheIncident
	cohorts   []SchedulingAgentTrialCohort
}

func (o *agentCacheFixture) GetSchedulingAgentCacheIncidents(context.Context, int64, []string) ([]SchedulingAgentCacheIncident, error) {
	return o.incidents, nil
}
func (o *agentCacheFixture) GetSchedulingAgentCacheRatios(context.Context, int64, string, []int64) (map[int64]*float64, error) {
	cache := .94
	return map[int64]*float64{3: &cache}, nil
}
func (o *agentCacheFixture) GetSchedulingAgentTrialAssignments(context.Context, int64, string, time.Time) (map[string]int64, error) {
	return map[string]int64{"test1": 3, "test2": 3, "control1": 1, "control2": 1}, nil
}
func (o *agentCacheFixture) GetSchedulingAgentTrialCohorts(context.Context, int64, string, SchedulingAgentRoute, map[string]int64) ([]SchedulingAgentTrialCohort, error) {
	return o.cohorts, nil
}
