package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func modelAutoSample(p95 float64) GroupModelAccountQuality {
	now := time.Now()
	return GroupModelAccountQuality{Successes: 100, Quality: &AccountQualityEvidence{LatestVerdict: "normal", LatestAt: &now, NormalStreak: 5, Normal: 5, LatencyP95MS: p95}, Auto: &ModelAutoAccountEvidence{Effort: "medium", Buckets: []ModelAutoSpeedBucket{{ContextBucket: 1, Samples: 30, P50MS: p95 / 2, P95MS: p95, OutputTPS: 50, OutputSamples: 30, CostRatio: 0.1}}}}
}

func TestModelAutoPromotionRequiresComparableSustainedEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*GroupModelAccountQuality)
		want   string
	}{
		{"clear speed advantage", func(s *GroupModelAccountQuality) {}, "latency"},
		{"small sample", func(s *GroupModelAccountQuality) { s.Auto.Buckets[0].Samples = 19 }, "insufficient_samples"},
		{"other effort", func(s *GroupModelAccountQuality) { s.Auto.Effort = "xhigh" }, "insufficient_samples"},
		{"other context size", func(s *GroupModelAccountQuality) { s.Auto.Buckets[0].ContextBucket = 3 }, "insufficient_samples"},
		{"unknown quality", func(s *GroupModelAccountQuality) { s.Quality.LatestVerdict = "unknown" }, "quality_unverified"},
		{"pending retest", func(s *GroupModelAccountQuality) {
			s.Degradation = &PelicanDegradationState{Status: "pending", SampledAt: time.Now()}
		}, "quality_unverified"},
		{"recent errors", func(s *GroupModelAccountQuality) { s.RecentFailures = 1 }, "stability_not_better"},
		{"less than one second", func(s *GroupModelAccountQuality) { s.Auto.Buckets[0].P95MS = 9500 }, "no_clear_advantage"},
		{"worse typical latency", func(s *GroupModelAccountQuality) { s.Auto.Buckets[0].P50MS = 8000 }, "no_clear_advantage"},
	} {
		t.Run(test.name, func(t *testing.T) {
			current, candidate := modelAutoSample(10000), modelAutoSample(7000)
			test.change(&candidate)
			require.Equal(t, test.want, modelAutoPromotionReason(current, candidate))
		})
	}
	current, candidate := modelAutoSample(10000), modelAutoSample(7000)
	current.Degradation = &PelicanDegradationState{Status: "pending", SampledAt: time.Now()}
	require.Equal(t, "quality_unverified", modelAutoPromotionReason(current, candidate), "first degraded result must get its five-minute opportunity")
	current, candidate = modelAutoSample(10000), modelAutoSample(10000)
	candidate.Auto.Buckets[0].OutputTPS = 70
	require.Equal(t, "output_speed", modelAutoPromotionReason(current, candidate))
	candidate.Auto.Buckets[0].OutputTPS = 50
	candidate.Auto.Buckets[0].CostRatio = 0.08
	require.Equal(t, "cost", modelAutoPromotionReason(current, candidate))
}

type modelAutoCacheFake struct {
	*accountSchedulingCacheStub
	sessions map[string]int64
	counts   map[string]int
	ready    bool
	reviews  int
}

func (c *modelAutoCacheFake) ReviewModelAutoCandidate(_ context.Context, _ int64, _ string, _ string, input ModelAutoReviewInput) (*ModelAutoReview, error) {
	c.reviews++
	return &ModelAutoReview{PrimaryID: input.PrimaryID, CandidateID: input.CandidateID, Ready: c.ready && input.CandidateID > 0}, nil
}
func (c *modelAutoCacheFake) GetModelAutoReview(context.Context, int64, string, string) (*ModelAutoReview, error) {
	return nil, nil
}
func (c *modelAutoCacheFake) AssignModelAutoSession(_ context.Context, _ int64, model, session string, primary int64, valid, backups []int64) (int64, bool, error) {
	key := model + ":" + session
	for _, id := range valid {
		if c.sessions[key] == id {
			return id, false, nil
		}
	}
	c.counts[model]++
	selected := primary
	if len(backups) > 0 && c.counts[model]%10 == 0 {
		selected = backups[(c.counts[model]/10-1)%len(backups)]
	}
	c.sessions[key] = selected
	return selected, true, nil
}

func TestModelAutoTrialsSessionsPromotionAndFailureAreSeparate(t *testing.T) {
	for _, mode := range []string{AccountSchedulingModeModelAuto, AccountSchedulingModeSmartChannel} {
		t.Run(mode, func(t *testing.T) { testModelAutoTrialsSessionsPromotionAndFailure(t, mode) })
	}
}

func testModelAutoTrialsSessionsPromotionAndFailure(t *testing.T, mode string) {
	groupID := int64(6)
	base := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, AccountSchedulingMode: mode})
	cache := &modelAutoCacheFake{accountSchedulingCacheStub: &accountSchedulingCacheStub{bindings: make(map[string]int64)}, sessions: make(map[string]int64), counts: make(map[string]int)}
	accounts := []*Account{{ID: 1}, {ID: 2}, {ID: 3}}
	repo := accountSchedulingQualityStub{quality: map[int64]GroupModelAccountQuality{1: modelAutoSample(10000), 2: modelAutoSample(7000), 3: modelAutoSample(12000)}}
	rememberGroupModelSchedulingAccount(base, cache, &groupID, "m", 1)
	selectRequest := func(model, session string, candidates []*Account) (int64, context.Context) {
		ctx := prepareFastestFailoverBinding(withModelAutoSession(base, session), cache, model)
		selected := fastestFailoverCandidateOrder(ctx, repo, cache, &groupID, model, candidates)[0].ID
		confirmGroupModelSchedulingAccount(ctx, model, selected)
		return selected, ctx
	}
	for i := 0; i < 9; i++ {
		id, _ := selectRequest("m", string(rune('a'+i)), accounts)
		require.EqualValues(t, 1, id)
	}
	id, trial := selectRequest("m", "trial", accounts)
	require.EqualValues(t, 2, id)
	require.EqualValues(t, 1, cache.bindings[groupModelSchedulingStickyKey("m")], "successful trial must not replace the primary")
	for i := 0; i < 3; i++ {
		id, _ = selectRequest("m", "trial", accounts)
		require.EqualValues(t, 2, id)
	}
	cache.ready = true
	id, upgrade := selectRequest("m", "upgrade", accounts)
	require.EqualValues(t, 2, id)
	require.Equal(t, "model_auto_upgrade", GroupModelSchedulingConfirmationReason(upgrade, "m", 2))
	require.EqualValues(t, 2, cache.bindings[groupModelSchedulingStickyKey("m")])
	confirmGroupModelSchedulingAccount(trial, "m", 3)
	require.EqualValues(t, 2, cache.bindings[groupModelSchedulingStickyKey("m")], "late trial cannot overwrite an upgraded binding")
	id, _ = selectRequest("m", "a", accounts)
	require.EqualValues(t, 1, id, "existing normal session stays on its original account")
	require.EqualValues(t, 2, cache.bindings[groupModelSchedulingStickyKey("m")])
	cache.ready = false
	id, _ = selectRequest("m", "a", accounts[1:])
	require.EqualValues(t, 2, id, "paused or over-cap original session must migrate")
	repo.quality[2].Quality.LatestVerdict = "degraded"
	sample := repo.quality[2]
	sample.Degradation = &PelicanDegradationState{Status: "pending", SampledAt: time.Now()}
	repo.quality[2] = sample
	id, _ = selectRequest("m", "pending", accounts)
	require.EqualValues(t, 2, id)
	sample.Degradation.Status = "confirmed"
	id, _ = selectRequest("m", "confirmed", accounts)
	require.EqualValues(t, 1, id, "confirmed degradation bypasses automatic promotion delay")
	require.EqualValues(t, 1, cache.bindings[groupModelSchedulingStickyKey("m")])
	rememberGroupModelSchedulingAccount(base, cache, &groupID, "other", 3)
	id, _ = selectRequest("other", "a", accounts)
	require.EqualValues(t, 3, id, "session and primary are isolated by model")
	reviews := cache.reviews
	ctx := context.WithValue(prepareFastestFailoverBinding(base, cache, "m"), modelAutoPrimaryOnlyKey{}, true)
	require.EqualValues(t, 1, fastestFailoverCandidateOrder(ctx, repo, cache, &groupID, "m", accounts)[0].ID)
	require.Equal(t, reviews, cache.reviews, "routine Pelican must not evaluate promotions or assign trials")
	readOnly := context.WithValue(ctx, qualitySchedulingReadOnlyKey{}, true)
	_ = fastestFailoverCandidateOrder(readOnly, repo, cache, &groupID, "m", accounts)
	require.Equal(t, reviews, cache.reviews, "status reads do not advance evaluations")
	for _, sample := range repo.quality {
		sample.Quality.LatestVerdict = "degraded"
	}
	id, _ = selectRequest("m", "a", accounts)
	require.EqualValues(t, 2, id, "all confirmed degraded moves old sessions to the fastest fallback")
	for i := 0; i < 3; i++ {
		id, _ = selectRequest("m", "a", accounts)
		require.EqualValues(t, 2, id, "all-degraded fallback must not rotate indefinitely")
	}
}
