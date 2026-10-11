package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type qualityChallengerStub struct {
	*accountSchedulingCacheStub
	ready        bool
	observations int
}

func (c *qualityChallengerStub) QualityChallengerReady(context.Context, int64, string, int64, int64, bool) (bool, error) {
	c.observations++
	return c.ready, nil
}

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
	require.Equal(t, 3, quality[1].Quality.tier(now), "one good test cannot erase measured degradation")
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

func TestQualityPreferencePromotionReadOnlyAndRotation(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Hour)
	groupID := int64(6)
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, AccountSchedulingMode: AccountSchedulingModeQualityPreferred})
	accounts := []*Account{{ID: 1}, {ID: 2}, {ID: 3}}
	quality := map[int64]GroupModelAccountQuality{
		1: {Successes: 100, Quality: &AccountQualityEvidence{LatestVerdict: "normal", LatestAt: &now, NormalStreak: 2, LastTestAt: &now, LatencyP95MS: 300, LatencySamples: 100}},
		2: {Successes: 100, Quality: &AccountQualityEvidence{LatestVerdict: "normal", LatestAt: &now, NormalStreak: 2, LastTestAt: &older, LatencyP95MS: 100, LatencySamples: 100}},
	}
	cache := &qualityChallengerStub{accountSchedulingCacheStub: &accountSchedulingCacheStub{bindings: make(map[string]int64)}}
	order := func(observe bool) []*Account {
		return orderSchedulingCandidates(ctx, cache, &groupID, "m", accounts, quality, 1, DefaultFastestFailoverSettings(), observe)
	}
	require.Equal(t, int64(2), order(false)[0].ID)
	require.Zero(t, cache.observations, "status views cannot train challenger state")
	require.Equal(t, int64(1), order(true)[0].ID, "a new challenger must wait")
	cache.ready = true
	require.Equal(t, int64(2), order(true)[0].ID)
	sessionCtx := context.WithValue(ctx, qualitySessionBindingKey{}, int64(1))
	require.Equal(t, int64(1), orderSchedulingCandidates(sessionCtx, cache, &groupID, "m", accounts, quality, 2, DefaultFastestFailoverSettings(), true)[0].ID, "a normal existing session keeps its channel")
	quality[1].Quality.LatestVerdict = "degraded"
	cache.ready = false
	require.Equal(t, int64(2), order(true)[0].ID, "degradation bypasses hold")
	ctx = context.WithValue(ctx, qualitySchedulingProbeKey{}, &qualityProbeReservations{accounts: make(map[int64]bool)})
	require.Equal(t, int64(3), order(false)[0].ID, "never-tested channels are tested first")
	require.Equal(t, int64(2), order(false)[0].ID, "parallel samples reserve distinct candidates")
	require.Equal(t, int64(1), order(false)[0].ID)
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
