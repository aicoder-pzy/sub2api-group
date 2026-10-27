//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSwitchTestLeaseReusesRecentEvidenceAndPreservesSchedule(t *testing.T) {
	ctx := context.Background()
	group := createPelicanGroupTestGroups(t, "switch")[0]
	_, err := integrationDB.ExecContext(ctx, `UPDATE groups SET account_scheduling_mode='smart_channel' WHERE id=$1`, group.ID)
	require.NoError(t, err)
	repo := &pelicanGroupTestRepository{db: integrationDB}
	now := time.Now().UTC().Truncate(time.Microsecond)
	plan, err := repo.CreatePlan(ctx, newPelicanGroupTestPlan(group.ID, true, now.Add(6*time.Hour)))
	require.NoError(t, err)
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: fmt.Sprintf("switch-test-%d", now.UnixNano())})
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID) })
	until := now.Add(15 * time.Minute)
	claim := func() bool {
		t.Helper()
		ok, err := repo.ClaimSwitchTest(ctx, plan, account.ID, plan.ModelID, now, until)
		require.NoError(t, err)
		return ok
	}
	require.True(t, claim())
	require.False(t, claim(), "another replica must see the running lease")
	require.NoError(t, repo.ReleaseSwitchTest(ctx, plan.ID, until))
	for _, scenario := range []struct {
		name, verdict, effort, model string
		age                          time.Duration
		global                       bool
		blocks                       bool
	}{
		{"normal", "normal", "high", plan.ModelID, 30 * time.Minute, false, true},
		{"degraded", "degraded", "high", plan.ModelID, 30 * time.Minute, false, true},
		{"manual", "normal", "high", plan.ModelID, 30 * time.Minute, true, true},
		{"expired", "normal", "high", plan.ModelID, 61 * time.Minute, false, false},
		{"other effort", "normal", "medium", plan.ModelID, 30 * time.Minute, false, false},
		{"other model", "normal", "high", "other", 30 * time.Minute, false, false},
		{"recent failure", "failed", "high", plan.ModelID, 2 * time.Minute, false, true},
		{"older failure", "failed", "high", plan.ModelID, 10 * time.Minute, false, false},
		{"unknown", "unknown", "high", plan.ModelID, 10 * time.Minute, false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var groupID any = group.ID
			if scenario.global {
				groupID = nil
			}
			var sampleID int64
			err := integrationDB.QueryRowContext(ctx, `INSERT INTO upstream_quality_samples
 (source,source_id,account_id,group_id,model,effort,verdict,sampled_at) VALUES ('switch-test',$1,$2,$3,$4,$5,$6,$7) RETURNING id`,
				scenario.name, account.ID, groupID, scenario.model, scenario.effort, scenario.verdict, now.Add(-scenario.age)).Scan(&sampleID)
			require.NoError(t, err)
			t.Cleanup(func() {
				_, _ = integrationDB.ExecContext(ctx, `DELETE FROM upstream_quality_samples WHERE id=$1`, sampleID)
			})
			require.Equal(t, !scenario.blocks, claim(), "freshness must use the same channel, model, and effort")
			require.NoError(t, repo.ReleaseSwitchTest(ctx, plan.ID, until))
		})
	}
	current, err := repo.GetPlan(ctx, plan.ID)
	require.NoError(t, err)
	require.Equal(t, plan.CronExpression, current.CronExpression)
	require.Equal(t, plan.NextRunAt, current.NextRunAt)
	require.Equal(t, plan.PelicanConfig, current.PelicanConfig)
	require.Equal(t, plan.UpdatedAt, current.UpdatedAt)
	require.Nil(t, current.LastRunAt, "released claims are not completed tests")

	sibling, err := repo.CreatePlan(ctx, newPelicanGroupTestPlan(group.ID, true, now.Add(time.Hour)))
	require.NoError(t, err)
	ok, err := repo.Claim(ctx, sibling, now, until, nil)
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, claim(), "an already running regular/manual plan prevents a duplicate supplement")
	require.NoError(t, repo.ReleaseSwitchTest(ctx, sibling.ID, until))
	stale := *plan
	stale.UpdatedAt = stale.UpdatedAt.Add(-time.Second)
	ok, err = repo.ClaimSwitchTest(ctx, &stale, account.ID, plan.ModelID, now, until)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestSwitchTestConcurrentSiblingPlansOnlyClaimOnce(t *testing.T) {
	ctx := context.Background()
	group := createPelicanGroupTestGroups(t, "switch-concurrent")[0]
	_, err := integrationDB.ExecContext(ctx, `UPDATE groups SET account_scheduling_mode='smart_channel' WHERE id=$1`, group.ID)
	require.NoError(t, err)
	repo := &pelicanGroupTestRepository{db: integrationDB}
	now := time.Now().UTC().Truncate(time.Microsecond)
	plans := make([]*service.PelicanGroupTestPlan, 2)
	for i := range plans {
		plans[i], err = repo.CreatePlan(ctx, newPelicanGroupTestPlan(group.ID, true, now.Add(time.Hour)))
		require.NoError(t, err)
	}
	type result struct {
		ok  bool
		err error
	}
	results := make(chan result, 12)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			ok, err := (&pelicanGroupTestRepository{db: integrationDB}).ClaimSwitchTest(ctx, plans[i%2], 999999, plans[i%2].ModelID, now, now.Add(15*time.Minute))
			results <- result{ok, err}
		})
	}
	wg.Wait()
	close(results)
	wins := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.ok {
			wins++
		}
	}
	require.Equal(t, 1, wins)
}
