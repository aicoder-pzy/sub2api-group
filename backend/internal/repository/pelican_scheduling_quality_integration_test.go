//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPelicanSchedulingQualitySourcesAndExpiry(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "pelican-scheduling-quality"})
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID) })
	group := createPelicanGroupTestGroups(t, "quality")[0]
	now := time.Now().Truncate(time.Microsecond)
	model := "pelican-quality-model"
	read := func() service.GroupModelAccountQuality {
		t.Helper()
		// Each repository has a 30-second cache: use a fresh instance to exercise SQL.
		quality, err := newUsageLogRepositoryWithSQL(nil, integrationDB).GetGroupModelAccountQuality(ctx, group.ID, model, now.Add(-24*time.Hour))
		require.NoError(t, err)
		return quality[account.ID]
	}
	manual := &accountRepository{sql: integrationDB}
	require.NoError(t, manual.SavePelicanAccountQuality(ctx, account.ID, model, &service.PelicanAssessment{Quality: "degraded"}, now.Add(-25*time.Hour), 100))
	require.Empty(t, read().IntelligenceVerdict, "expired samples do not influence selection")
	require.NoError(t, manual.SavePelicanAccountQuality(ctx, account.ID, "other-model", &service.PelicanAssessment{Quality: "normal"}, now, 1))
	require.Empty(t, read().IntelligenceVerdict, "models are isolated")
	require.NoError(t, manual.SavePelicanAccountQuality(ctx, account.ID, model, &service.PelicanAssessment{Quality: "degraded", Reason: "manual"}, now.Add(-3*time.Hour), 100))
	require.Equal(t, "degraded", read().IntelligenceVerdict)
	require.Equal(t, "manual", read().IntelligenceReason)

	plans := NewScheduledTestPlanRepository(integrationDB)
	cfg := &service.PelicanTestConfig{QuestionKind: "pelican", ModelID: model}
	plan, err := plans.Create(ctx, &service.ScheduledTestPlan{AccountID: account.ID, ModelID: model, CronExpression: "*/30 * * * *", MaxResults: 100, PelicanConfig: cfg})
	require.NoError(t, err)
	_, err = NewScheduledTestResultRepository(integrationDB).Create(ctx, &service.ScheduledTestResult{PlanID: plan.ID, Status: "success", QualityVerdict: "normal", PelicanConfig: cfg, StartedAt: now.Add(-2 * time.Hour), FinishedAt: now.Add(-2 * time.Hour), LatencyMs: 200})
	require.NoError(t, err)
	require.Equal(t, "normal", read().IntelligenceVerdict, "newer account schedule recovers manual degradation")

	groups := NewPelicanGroupTestRepository(integrationDB)
	groupPlan, err := groups.CreatePlan(ctx, newPelicanGroupTestPlan(group.ID, false, now))
	require.NoError(t, err)
	saved, err := groups.CreateResult(ctx, &service.PelicanGroupTestResult{PlanID: groupPlan.ID, AccountID: account.ID, Status: "success", QualityVerdict: "degraded", QualityReason: "group", QualityModelID: model, PelicanConfig: cfg, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-time.Hour), LatencyMs: 300})
	require.NoError(t, err)
	loaded, err := groups.GetResult(ctx, saved.ID)
	require.NoError(t, err)
	require.Equal(t, "degraded", loaded.QualityVerdict)
	require.Equal(t, model, loaded.QualityModelID)
	require.Equal(t, "group", read().IntelligenceReason, "latest group test identifies its actual answering account/model")
	require.EqualValues(t, 300, read().LatencyMS, "generation latency is available if there is no real traffic yet")

	require.NoError(t, manual.SavePelicanAccountQuality(ctx, account.ID, model, &service.PelicanAssessment{Quality: "unknown"}, now, 400))
	require.Equal(t, "unknown", read().IntelligenceVerdict)
	require.NoError(t, manual.SavePelicanAccountQuality(ctx, account.ID, model, &service.PelicanAssessment{Quality: "degraded"}, now.Add(-time.Minute), 100))
	require.Equal(t, "unknown", read().IntelligenceVerdict, "late assessments cannot overwrite newer samples")
}
