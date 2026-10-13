//go:build integration

package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPelicanDegradationConfirmationAndRollingBudget(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	base := now
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "confirmation-a"})
	other := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "confirmation-b"})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id IN ($1,$2)`, account.ID, other.ID)
	})
	groups := createPelicanGroupTestGroups(t, "confirmation", "confirmation-other")
	for _, group := range groups {
		_, err := integrationDB.ExecContext(ctx, `UPDATE groups SET account_scheduling_mode='quality_preferred' WHERE id=$1`, group.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES ($1,$3),($2,$3)`, account.ID, other.ID, group.ID)
		require.NoError(t, err)
	}
	repo := NewPelicanGroupTestRepository(integrationDB).(*pelicanGroupTestRepository)
	plans := []*service.PelicanGroupTestPlan{}
	for _, group := range groups {
		plan, err := repo.CreatePlan(ctx, newPelicanGroupTestPlan(group.ID, true, base))
		require.NoError(t, err)
		plans = append(plans, plan)
	}
	usage := newUsageLogRepositoryWithSQL(nil, integrationDB)
	read := func(group, id int64) *service.PelicanDegradationState {
		states, err := usage.GetPelicanDegradationStates(ctx, group, "gpt-6-astra")
		require.NoError(t, err)
		require.NotNil(t, states[id])
		return states[id]
	}
	save := func(plan, id int64, verdict string, start, finish time.Time) {
		_, err := repo.CreateResult(ctx, &service.PelicanGroupTestResult{PlanID: plan, AccountID: id,
			Status: "success", QualityModelID: "gpt-6-astra", QualityVerdict: verdict,
			PelicanConfig: &service.PelicanTestConfig{ModelID: "gpt-6-astra", ReasoningEffort: "medium"}, StartedAt: start, FinishedAt: finish})
		require.NoError(t, err)
	}
	save(plans[0].ID, account.ID, "degraded", base.Add(-time.Minute), base)
	require.Equal(t, "pending", read(groups[0].ID, account.ID).Status)
	require.WithinDuration(t, base.Add(5*time.Minute), *read(groups[0].ID, account.ID).DueAt, 10*time.Second)
	work, err := repo.ListDegradationDue(ctx, base.Add(4*time.Minute))
	require.NoError(t, err)
	require.Empty(t, work)
	// A slow parallel sample which started before the cooldown is not a retest.
	save(plans[0].ID, account.ID, "degraded", base.Add(4*time.Minute), base.Add(8*time.Minute))
	require.Equal(t, "pending", read(groups[0].ID, account.ID).Status)
	save(plans[0].ID, account.ID, "unknown", base.Add(8*time.Minute), base.Add(9*time.Minute))
	require.Equal(t, "pending", read(groups[0].ID, account.ID).Status)
	require.True(t, base.Add(14*time.Minute).Equal(*read(groups[0].ID, account.ID).DueAt), "compare the instant independently of the SQL driver's timezone location")
	save(plans[0].ID, account.ID, "degraded", base.Add(14*time.Minute), base.Add(15*time.Minute))
	require.Equal(t, "confirmed", read(groups[0].ID, account.ID).Status)
	work, err = repo.ListDegradationDue(ctx, base.Add(16*time.Minute))
	require.NoError(t, err)
	require.Len(t, work, 1)
	require.Equal(t, "replacement", work[0].Action)
	// A different group gets its own confirmation cycle for the same account.
	save(plans[1].ID, account.ID, "degraded", base, base.Add(time.Minute))
	require.Equal(t, "pending", read(groups[1].ID, account.ID).Status)
	save(plans[0].ID, account.ID, "normal", base.Add(15*time.Minute), base.Add(16*time.Minute))
	require.Equal(t, "normal", read(groups[0].ID, account.ID).Status, "one normal retest cancels handling")
	save(plans[0].ID, account.ID, "degraded", base, base.Add(time.Minute))
	require.Equal(t, "normal", read(groups[0].ID, account.ID).Status, "late older samples cannot overwrite recovery")
	// Restart the first group's cycle, and exercise concurrent claims with a cap
	// of one. Keep fixture connections bounded on CI.
	save(plans[0].ID, account.ID, "degraded", base.Add(17*time.Minute), base.Add(17*time.Minute))
	save(plans[0].ID, other.ID, "degraded", base.Add(17*time.Minute), base.Add(17*time.Minute))
	at := base.Add(23 * time.Minute)
	work, err = repo.ListDegradationDue(ctx, at)
	require.NoError(t, err)
	var targets []service.PelicanDegradationWork
	for _, item := range work {
		if item.GroupID == groups[0].ID {
			targets = append(targets, item)
		}
	}
	require.Len(t, targets, 2)
	until := at.Add(15 * time.Minute)
	var claimed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(item service.PelicanDegradationWork) {
			defer wg.Done()
			ok, err := repo.ClaimDegradation(ctx, item, at, until, 1)
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			if ok {
				claimed.Add(1)
			}
		}(targets[i%len(targets)])
	}
	wg.Wait()
	require.EqualValues(t, 1, claimed.Load())
	for _, item := range targets {
		require.NoError(t, repo.FinishDegradation(ctx, item, until, at, true))
	}
	require.NoError(t, repo.Finish(ctx, plans[0].ID, until, at))
	ok, err := repo.ClaimDegradation(ctx, targets[0], at.Add(6*time.Minute), until.Add(6*time.Minute), 1)
	require.NoError(t, err)
	require.False(t, ok, "the persisted rolling-hour budget survives new worker instances")
	ok, err = (&pelicanGroupTestRepository{db: integrationDB}).ClaimDegradation(ctx, targets[0], at.Add(time.Hour), until.Add(time.Hour), 1)
	require.NoError(t, err)
	require.True(t, ok, "an expired hour slot becomes available without resetting at a clock-hour boundary")
}
