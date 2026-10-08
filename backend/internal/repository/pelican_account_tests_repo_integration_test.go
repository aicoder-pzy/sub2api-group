//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPelicanAccountRepo_PersistenceAndExclusiveLease(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "pelican-account-lease"})
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID) })
	plans := NewScheduledTestPlanRepository(integrationDB)
	results := NewScheduledTestResultRepository(integrationDB)
	now := time.Now().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)
	cfg := &service.PelicanTestConfig{QuestionKind: "pelican", Prompt: "draw a pelican", ReasoningEffort: "high", ParallelCount: 2}
	create := func() *service.ScheduledTestPlan {
		plan, err := plans.Create(ctx, &service.ScheduledTestPlan{AccountID: account.ID, ModelID: "claude-opus-5-5", CronExpression: "*/30 * * * *", Enabled: true, MaxResults: 100, NextRunAt: &due, PelicanConfig: cfg})
		require.NoError(t, err)
		require.Equal(t, cfg, plan.PelicanConfig)
		return plan
	}
	a, b := create(), create()
	until, next := now.Add(15*time.Minute), now.Add(time.Hour)
	claimed, err := plans.ClaimPelican(ctx, a, now, until, next)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = plans.ClaimPelican(ctx, b, now, until, next)
	require.NoError(t, err)
	require.False(t, claimed, "another plan cannot run on the same account while its lease is held")
	loaded, err := plans.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, cfg, loaded.PelicanConfig)
	require.Equal(t, until, *loaded.RunningUntil)
	require.Equal(t, next, *loaded.NextRunAt)
	require.NoError(t, plans.FinishPelican(ctx, a.ID, until.Add(time.Second), now))
	loaded, err = plans.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.RunningUntil, "a stale lease owner cannot finish another run")
	require.NoError(t, plans.FinishPelican(ctx, a.ID, until, now))
	claimed, err = plans.ClaimPelican(ctx, b, now, until, next)
	require.NoError(t, err)
	require.True(t, claimed)

	saved, err := results.Create(ctx, &service.ScheduledTestResult{PlanID: a.ID, Status: "success", ResponseText: "<svg></svg>", StartedAt: now, FinishedAt: now, PelicanConfig: cfg})
	require.NoError(t, err)
	require.Equal(t, cfg, saved.PelicanConfig)
	summary, err := results.ListByPlanID(ctx, a.ID, 10, false)
	require.NoError(t, err)
	require.Len(t, summary, 1)
	require.Empty(t, summary[0].ResponseText)
	full, err := results.GetResult(ctx, a.ID, saved.ID)
	require.NoError(t, err)
	require.Equal(t, "<svg></svg>", full.ResponseText)
	history, err := results.ListPelicanHistory(ctx, 0, 100)
	require.NoError(t, err)
	require.Contains(t, func() []int64 {
		ids := []int64{}
		for _, item := range history {
			ids = append(ids, item.ID)
		}
		return ids
	}(), saved.ID)
	_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_results SET created_at=$2 WHERE id=$1`, saved.ID, now.Add(-8*24*time.Hour))
	require.NoError(t, err)
	require.NoError(t, results.PruneExpiredPelican(ctx, now.Add(-7*24*time.Hour)))
	_, err = results.GetResult(ctx, a.ID, saved.ID)
	require.Error(t, err, "expired drawing history is removed")
}
