package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestQualityPreferenceEvidenceRankingAndRecovery(t *testing.T) {
	now := time.Now()
	accounts := []*Account{{ID: 1, Priority: 0}, {ID: 2, Priority: 100}, {ID: 3, Priority: 1}}
	quality := map[int64]GroupModelAccountQuality{
		1: {Successes: 100, LatencyMS: 1, Quality: &AccountQualityEvidence{LatestVerdict: "degraded", LatestAt: &now, Degraded: 1}},
		2: {Successes: 300, Quality: &AccountQualityEvidence{LatestVerdict: "normal", LatestAt: &now, Normal: 10, NormalStreak: 10, LatencyP95MS: 100}},
		3: {Successes: 18, Quality: &AccountQualityEvidence{LatencyP95MS: 1}},
	}
	require.Equal(t, int64(2), orderQualityCandidates(accounts, quality, 10)[0].ID, "verified quality beats an unknown fast channel and manual priority")
	quality[1].Quality.LatestVerdict = "normal"
	quality[1].Quality.NormalStreak = 1
	require.Equal(t, 1, quality[1].Quality.tier(now), "a normal confirmation cancels degradation handling")
	quality[1].Quality.NormalStreak = 2
	require.Equal(t, 0, quality[1].Quality.tier(now))
	expired := now.Add(-25 * time.Hour)
	quality[1].Quality.LatestAt = &expired
	require.Equal(t, 2, quality[1].Quality.tier(now))
	require.Greater(t, QualitySuccessLowerBound(300, 2), QualitySuccessLowerBound(18, 0), "small 100% samples do not prove superiority")
	require.Zero(t, QualitySuccessLowerBound(0, 0))
	for _, q := range quality {
		q.Quality.LatestVerdict = "degraded"
		q.Quality.LatestAt = &now
	}
	require.Equal(t, int64(3), orderQualityCandidates(accounts, quality, 10)[0].ID, "all degraded uses speed")
}

func TestQualityPreferenceHonorsManualPreferenceWithoutBypassingHealth(t *testing.T) {
	now := time.Now()
	accounts := []*Account{{ID: 1}, {ID: 2, Priority: 2, Extra: map[string]any{"scheduling_preferred": true}}, {ID: 3, Priority: 1}}
	quality := map[int64]GroupModelAccountQuality{
		1: {Successes: 300, Quality: &AccountQualityEvidence{LatestVerdict: "normal", LatestAt: &now, NormalStreak: 2, LatencyP95MS: 100}},
		2: {Successes: 100, Quality: &AccountQualityEvidence{LatencyP95MS: 200}},
		3: {Successes: 100, Quality: &AccountQualityEvidence{LatestVerdict: "normal", LatestAt: &now, NormalStreak: 2, LatencyP95MS: 300}},
	}
	first := func() int64 { return orderQualityCandidates(accounts, quality, 10)[0].ID }
	require.Equal(t, int64(2), first(), "manual preference precedes automatic ranking even with unknown quality")
	quality[2].Quality.LatestVerdict, quality[2].Quality.LatestAt = "degraded", &now
	require.Equal(t, int64(1), first(), "preference cannot bypass degradation")
	quality[2].Quality.LatestVerdict = "normal"
	quality[2].Quality.NormalStreak = 2
	failed := quality[2]
	failed.Successes, failed.Failures = 0, 10
	quality[2] = failed
	require.Equal(t, int64(1), first(), "preference cannot bypass known transport failures")
	failed.Successes, failed.Failures = 100, 0
	quality[2] = failed
	accounts[2].Extra = map[string]any{"scheduling_preferred": true}
	require.Equal(t, int64(3), first(), "multiple preferred accounts honor the lower priority number")
	for _, sample := range quality {
		sample.Quality.LatestVerdict, sample.Quality.LatestAt = "degraded", &now
	}
	require.Equal(t, int64(1), first(), "all degraded uses speed before manual preference or priority")
}

func TestQualityPreferenceStableBindingReadOnlyAndRotation(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Hour)
	groupID := int64(6)
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, AccountSchedulingMode: AccountSchedulingModeQualityPreferred})
	accounts := []*Account{{ID: 1}, {ID: 2}, {ID: 3}}
	quality := map[int64]GroupModelAccountQuality{
		1: {Successes: 100, Quality: &AccountQualityEvidence{LatestVerdict: "normal", LatestAt: &now, NormalStreak: 2, LastTestAt: &now, LatencyP95MS: 300, LatencySamples: 100}},
		2: {Successes: 100, Quality: &AccountQualityEvidence{LatestVerdict: "normal", LatestAt: &now, NormalStreak: 2, LastTestAt: &older, LatencyP95MS: 100, LatencySamples: 100}},
	}
	order := func(retainBinding bool) []*Account {
		return orderSchedulingCandidates(ctx, accounts, quality, 1, DefaultFastestFailoverSettings(), retainBinding)
	}
	require.Equal(t, int64(2), order(false)[0].ID)
	require.Equal(t, int64(1), order(true)[0].ID, "a faster candidate cannot preempt a healthy primary")
	sessionCtx := context.WithValue(ctx, qualitySessionBindingKey{}, int64(1))
	require.Equal(t, int64(1), orderSchedulingCandidates(sessionCtx, accounts, quality, 2, DefaultFastestFailoverSettings(), true)[0].ID, "a normal existing session keeps its channel")
	quality[1].Quality.LatestVerdict = "degraded"
	require.Equal(t, int64(2), order(true)[0].ID, "degradation immediately selects a replacement")
	accounts[0].Extra = map[string]any{"scheduling_preferred": true}
	ctx = context.WithValue(ctx, qualitySchedulingProbeKey{}, &qualityProbeReservations{accounts: make(map[int64]bool)})
	require.Equal(t, int64(3), order(false)[0].ID, "never-tested channels are tested first")
	require.Equal(t, int64(2), order(false)[0].ID, "parallel samples reserve distinct candidates")
	require.Equal(t, int64(1), order(false)[0].ID)
}

func TestQualityPreferencePreservesHealthyPrimaryAndReplacesBadPrimary(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		verdict       string
		failures      int64
		omitPrimary   bool
		preferPrimary bool
		want          int64
	}{
		{name: "manual preference cannot preempt primary", verdict: "normal", want: 2},
		{name: "lower priority preferred account cannot preempt primary", verdict: "normal", preferPrimary: true, want: 2},
		{name: "unknown evidence preserves primary", want: 2},
		{name: "degraded primary yields", verdict: "degraded", preferPrimary: true, want: 1},
		{name: "unstable primary yields", verdict: "normal", failures: 40, want: 1},
		{name: "ineligible primary yields", verdict: "normal", omitPrimary: true, want: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			now := time.Now()
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 6, AccountSchedulingMode: AccountSchedulingModeQualityPreferred})
			accounts := []*Account{
				{ID: 1, Priority: 1, Extra: map[string]any{"scheduling_preferred": true}},
				{ID: 2, Priority: 2, Extra: map[string]any{"scheduling_preferred": scenario.preferPrimary}},
			}
			samples := map[int64]GroupModelAccountQuality{
				1: {Successes: 1000, Quality: &AccountQualityEvidence{LatestVerdict: "normal", LatestAt: &now, NormalStreak: 10, QualityLowerBound: 0.9, LatencyP95MS: 10, LatencySamples: 100}},
				2: {Successes: 100, Failures: scenario.failures, Quality: &AccountQualityEvidence{LatestVerdict: scenario.verdict, LatestAt: &now, NormalStreak: 2, QualityLowerBound: 0.7, LatencyP95MS: 100, LatencySamples: 100}},
			}
			if scenario.omitPrimary {
				accounts = accounts[:1]
			}
			for _, sessionID := range []int64{0, 2} {
				sessionCtx := context.WithValue(ctx, qualitySessionBindingKey{}, sessionID)
				ordered := orderSchedulingCandidates(sessionCtx, accounts, samples, 2, DefaultFastestFailoverSettings(), true)
				require.Equal(t, scenario.want, ordered[0].ID, "new and existing sessions honor primary health")
			}
		})
	}
}

func TestFailoverRecoveryDoesNotReclaimPrimary(t *testing.T) {
	for _, mode := range []string{AccountSchedulingModeFastestFailover, AccountSchedulingModeQualityPreferred} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now()
			groupID := int64(6)
			base := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, AccountSchedulingMode: mode})
			accounts := []*Account{
				{ID: 1, Priority: 1, Extra: map[string]any{"scheduling_preferred": true}},
				{ID: 2, Priority: 2, Extra: map[string]any{"scheduling_preferred": true}},
			}
			repo := accountSchedulingQualityStub{quality: map[int64]GroupModelAccountQuality{
				1: {Successes: 1000, LatencyMS: 10, IntelligenceVerdict: "normal", Quality: &AccountQualityEvidence{LatestVerdict: "normal", LatestAt: &now, NormalStreak: 10, QualityLowerBound: 0.9, LatencyP95MS: 10, LatencySamples: 100}},
				2: {Successes: 100, LatencyMS: 100, IntelligenceVerdict: "normal", Quality: &AccountQualityEvidence{LatestVerdict: "normal", LatestAt: &now, NormalStreak: 2, QualityLowerBound: 0.7, LatencyP95MS: 100, LatencySamples: 100}},
			}}
			cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
			key := groupModelSchedulingStickyKey("m")
			rememberGroupModelSchedulingAccount(base, cache, &groupID, "m", 1)
			late := prepareFastestFailoverBinding(base, cache, "m")
			request := prepareFastestFailoverBinding(base, cache, "m")
			winner := fastestFailoverCandidateOrder(request, repo, cache, &groupID, "m", accounts[1:])[0]
			require.EqualValues(t, 2, winner.ID, "A unavailable: choose B")
			require.EqualValues(t, 1, cache.bindings[key], "selection alone cannot change the primary")
			confirmGroupModelSchedulingAccount(request, "m", winner.ID)
			require.EqualValues(t, 2, cache.bindings[key], "successful B becomes primary")
			revision := cache.revisions[key]
			for i := 0; i < 3; i++ {
				request = prepareFastestFailoverBinding(base, cache, "m")
				winner = fastestFailoverCandidateOrder(request, repo, cache, &groupID, "m", accounts)[0]
				require.EqualValues(t, 2, winner.ID, "recovered, preferred, faster A cannot reclaim healthy B")
				confirmGroupModelSchedulingAccount(request, "m", winner.ID)
			}
			confirmGroupModelSchedulingAccount(late, "m", 1)
			require.EqualValues(t, 2, cache.bindings[key], "a late response cannot restore A")
			require.Equal(t, revision, cache.revisions[key], "healthy requests produce no binding change")
			rememberGroupModelSchedulingAccount(base, cache, &groupID, "other-model", 1)
			require.EqualValues(t, 1, fastestFailoverCandidateOrder(base, repo, cache, &groupID, "other-model", accounts)[0].ID, "model bindings stay independent")
			require.EqualValues(t, 1, fastestFailoverCandidateOrder(base, repo, cache, &groupID, "new-model", accounts)[0].ID, "preference still applies to initial selection")
		})
	}
}

func TestPelicanQualityChecksVerifyAnswersWithoutTrustingModelBadges(t *testing.T) {
	cfg := &PelicanTestConfig{Prompt: "Draw a pelican", VerificationTasks: true}
	generated, check, err := withPelicanQualityChecks(cfg)
	require.NoError(t, err)
	require.Equal(t, "Draw a pelican", cfg.Prompt, "the saved plan remains unchanged")
	require.Contains(t, generated.Prompt, check.nonce)
	normal := &PelicanAssessment{Quality: "normal"}
	correct := `<html><!--quality-check:` + check.nonce + `:{"paths":` + strconv.Itoa(check.paths) + `,"code":` + strconv.Itoa(check.code) + `}--></html>`
	require.Same(t, normal, check.assess(correct, normal))
	require.Equal(t, "unknown", check.assess(`<html data-sub2api-quality="normal"></html>`, normal).Quality)
	require.Equal(t, "degraded", check.assess(`<!--quality-check:`+check.nonce+`:{"paths":-1,"code":-1}-->`, normal).Quality)
	require.Equal(t, "unknown", check.assess(correct+correct, normal).Quality, "ambiguous duplicate answers are not trusted")
}

func TestQualityProbeAndStatusNeverChangeBusinessBinding(t *testing.T) {
	groupID := int64(6)
	base := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, AccountSchedulingMode: AccountSchedulingModeQualityPreferred})
	for _, key := range []any{qualitySchedulingProbeKey{}, qualitySchedulingReadOnlyKey{}} {
		cache := &accountSchedulingCacheStub{bindings: map[string]int64{groupModelSchedulingStickyKey("m"): 1}}
		ctx := WithFastestFailoverRequestState(base)
		groupModelSchedulingActiveAccount(ctx, cache, &groupID, "m")
		ctx = context.WithValue(ctx, key, true)
		confirmGroupModelSchedulingAccount(ctx, "m", 2)
		rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "m", 2)
		require.Equal(t, int64(1), cache.bindings[groupModelSchedulingStickyKey("m")])
		require.Nil(t, WithFastestFailoverRequestState(context.WithValue(base, key, true)).Value(groupModelSchedulingRequestKey{}))
	}
}
