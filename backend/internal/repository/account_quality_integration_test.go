//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualityHistorySurvivesPruningAndSharesModelVerdictsAcrossEfforts(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "quality-history"})
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID) })
	groups := createPelicanGroupTestGroups(t, "quality-history", "other-quality-history")
	repo := NewPelicanGroupTestRepository(integrationDB)
	plan, err := repo.CreatePlan(ctx, newPelicanGroupTestPlan(groups[0].ID, false, now))
	require.NoError(t, err)
	other, err := repo.CreatePlan(ctx, newPelicanGroupTestPlan(groups[1].ID, false, now))
	require.NoError(t, err)
	for i, input := range []struct {
		plan            int64
		effort, verdict string
	}{{plan.ID, "xhigh", "degraded"}, {plan.ID, "xhigh", "normal"}, {plan.ID, "xhigh", "normal"}, {plan.ID, "medium", "degraded"}, {other.ID, "xhigh", "degraded"}} {
		_, err = repo.CreateResult(ctx, &service.PelicanGroupTestResult{PlanID: input.plan, AccountID: account.ID, Status: "success", QualityModelID: "quality-model", QualityVerdict: input.verdict, QualityReason: "test", PelicanConfig: &service.PelicanTestConfig{ModelID: "quality-model", ReasoningEffort: input.effort}, StartedAt: now.Add(time.Duration(i) * time.Second), FinishedAt: now.Add(time.Duration(i) * time.Second), LatencyMs: 100})
		require.NoError(t, err)
	}
	require.NoError(t, repo.PruneResults(ctx, plan.ID, 1))
	usage := newUsageLogRepositoryWithSQL(nil, integrationDB)
	read := func(group int64, model, effort string) map[int64]service.GroupModelAccountQuality {
		data, err := usage.GetQualityPreferredAccountQuality(ctx, group, model, effort)
		require.NoError(t, err)
		return data
	}
	e := read(groups[0].ID, "quality-model", "xhigh")[account.ID].Quality
	require.NotNil(t, e)
	require.EqualValues(t, 2, e.Normal)
	require.EqualValues(t, 2, e.Degraded)
	require.Zero(t, e.NormalStreak)
	require.Equal(t, "degraded", e.Tier, "medium degradation must also protect xhigh requests")
	require.Equal(t, "medium", e.TestEffort)
	require.Zero(t, e.TestLatencyMS, "medium generation time cannot stand in for xhigh speed")
	require.EqualValues(t, 1, read(groups[1].ID, "quality-model", "xhigh")[account.ID].Quality.Degraded)
	medium := read(groups[0].ID, "quality-model", "medium")[account.ID].Quality
	require.Equal(t, e.Tier, medium.Tier)
	require.Equal(t, float64(100), medium.TestLatencyMS)
	require.Empty(t, read(groups[0].ID, "other-model", "xhigh"))
	for i := 6; i < 8; i++ {
		_, err = repo.CreateResult(ctx, &service.PelicanGroupTestResult{PlanID: plan.ID, AccountID: account.ID, Status: "success", QualityModelID: "quality-model", QualityVerdict: "normal", PelicanConfig: &service.PelicanTestConfig{ModelID: "quality-model", ReasoningEffort: "medium"}, StartedAt: now.Add(time.Duration(i) * time.Second), FinishedAt: now.Add(time.Duration(i) * time.Second)})
		require.NoError(t, err)
	}
	e = read(groups[0].ID, "quality-model", "xhigh")[account.ID].Quality
	require.EqualValues(t, 2, e.NormalStreak)
	require.Equal(t, "verified", e.Tier, "a cached xhigh selection sees newly saved medium recovery immediately")
	_, err = repo.CreateResult(ctx, &service.PelicanGroupTestResult{PlanID: plan.ID, AccountID: account.ID, Status: "success", QualityModelID: "quality-model", QualityVerdict: "degraded", PelicanConfig: &service.PelicanTestConfig{ModelID: "quality-model", ReasoningEffort: "medium"}, StartedAt: now.Add(8 * time.Second), FinishedAt: now.Add(8 * time.Second)})
	require.NoError(t, err)
	require.Equal(t, "degraded", read(groups[0].ID, "quality-model", "xhigh")[account.ID].Quality.Tier, "new degradation also bypasses a warm statistics cache")
	manual := &accountRepository{sql: integrationDB}
	require.NoError(t, manual.SavePelicanAccountQuality(ctx, account.ID, "manual-model", &service.PelicanAssessment{Quality: "degraded"}, now.Add(-time.Hour), 100))
	require.NoError(t, manual.SavePelicanAccountQuality(ctx, account.ID, "manual-model", &service.PelicanAssessment{Quality: "normal"}, now, 100))
	e = read(groups[0].ID, "manual-model", "")[account.ID].Quality
	require.EqualValues(t, 1, e.Normal)
	require.EqualValues(t, 1, e.Degraded)
	require.Equal(t, "provisional", e.Tier, "manual recovery cancels degradation while history remains retained")
}
