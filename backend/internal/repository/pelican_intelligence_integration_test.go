//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestPelicanIntelligenceWindow(t *testing.T) {
	ctx := context.Background()
	groups := createPelicanGroupTestGroups(t, "intelligence", "isolated")
	plans := NewPelicanGroupTestRepository(integrationDB)
	showcase := NewPelicanShowcaseRepository(integrationDB)
	now := time.Now().Truncate(time.Microsecond)
	p1, err := plans.CreatePlan(ctx, newPelicanGroupTestPlan(groups[0].ID, true, now))
	require.NoError(t, err)
	p2, err := plans.CreatePlan(ctx, newPelicanGroupTestPlan(groups[0].ID, true, now))
	require.NoError(t, err)
	other, err := plans.CreatePlan(ctx, newPelicanGroupTestPlan(groups[1].ID, true, now))
	require.NoError(t, err)
	stats := func(groupID int64) service.PelicanIntelligenceProbe {
		t.Helper()
		listed, err := showcase.ListGroups(ctx)
		require.NoError(t, err)
		for _, group := range listed {
			if group.ID == groupID {
				return group.IntelligenceProbe
			}
		}
		t.Fatal("group missing")
		return service.PelicanIntelligenceProbe{}
	}
	makeResult := func(planID int64, index int, verdict, status string) *service.PelicanGroupTestResult {
		return &service.PelicanGroupTestResult{PlanID: planID, Status: status, QualityVerdict: verdict,
			ResponseText: "<svg/>", StartedAt: now.Add(time.Duration(index) * time.Second),
			FinishedAt: now.Add(time.Duration(index) * time.Second), PelicanConfig: p1.PelicanConfig}
	}
	insert := func(planID int64, index int, verdict, status string) *service.PelicanGroupTestResult {
		t.Helper()
		saved, err := plans.CreateResult(ctx, makeResult(planID, index, verdict, status))
		require.NoError(t, err)
		return saved
	}
	for i := 1; i <= 10; i++ {
		insert(p1.ID, i, "normal", "success")
	}
	require.Nil(t, stats(groups[0].ID).NormalRate)
	eleventh := insert(p2.ID, 11, "degraded", "success")
	got := stats(groups[0].ID)
	require.Equal(t, 11, got.SampleCount)
	require.InDelta(t, 1000.0/11, *got.NormalRate, 0.000001)
	for _, invalid := range [][2]string{{"unknown", "success"}, {"normal", "error"}, {"degraded", "failed"}, {"", "success"}} {
		insert(p1.ID, 500, invalid[0], invalid[1])
	}
	require.Equal(t, 11, stats(groups[0].ID).SampleCount)
	_, err = integrationDB.ExecContext(ctx, `UPDATE pelican_group_test_results SET quality_verdict='degraded' WHERE id=$1`, eleventh.ID)
	require.NoError(t, err)
	require.Equal(t, 11, stats(groups[0].ID).SampleCount, "repeated classification does not count twice")
	insert(other.ID, 1, "degraded", "success")
	require.Equal(t, 1, stats(groups[1].ID).DegradedCount)

	// Concurrent, out-of-order completions from both plans keep the true newest 100.
	errors := make(chan error, 104)
	// Exercise concurrent completion without exhausting PostgreSQL's connection
	// limit while other integration-test clients are also connected.
	slots := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := 12; i <= 115; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			planID := p1.ID
			if index%2 == 0 {
				planID = p2.ID
			}
			_, err := plans.CreateResult(ctx, makeResult(planID, index, "normal", "success"))
			errors <- err
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	got = stats(groups[0].ID)
	require.Equal(t, 100, got.SampleCount)
	require.Equal(t, 100, got.NormalCount)
	require.Zero(t, got.DegradedCount, "the older degraded test was evicted")
	require.Equal(t, 100.0, *got.NormalRate)
	require.NotNil(t, got.LastProbedAt)
	require.True(t, now.Add(115*time.Second).Equal(*got.LastProbedAt), "latest test timestamp is preserved across database timezone conversion")
	insert(p1.ID, -100, "degraded", "success")
	require.Zero(t, stats(groups[0].ID).DegradedCount, "late arrival of an older result cannot displace the latest window")

	// Clearing history, deleting a plan and trimming/deleting artwork do not erase statistics.
	require.NoError(t, plans.PruneResults(ctx, p1.ID, 1))
	_, err = plans.DeletePlan(ctx, p2.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM pelican_group_test_results WHERE plan_id=$1`, p1.ID)
	require.NoError(t, err)
	require.Equal(t, got, stats(groups[0].ID))
	require.Equal(t, 1, stats(groups[1].ID).DegradedCount)
}

func TestPelicanIntelligenceMigrationBackfill(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
CREATE TEMP TABLE groups (id bigint PRIMARY KEY);
CREATE TEMP TABLE pelican_group_test_plans (id bigint PRIMARY KEY, group_id bigint);
CREATE TEMP TABLE pelican_group_test_results (id bigint PRIMARY KEY, plan_id bigint, status text, quality_verdict text, finished_at timestamptz);
CREATE TEMP TABLE upstream_quality_samples (group_id bigint, source_id text, source text, kind text, verdict text, sampled_at timestamptz);
CREATE TEMP TABLE pelican_showcase_items (group_id bigint, source_result_id bigint, quality_verdict text, generated_at timestamptz);
CREATE TEMP TABLE pelican_group_intelligence_samples (group_id bigint, source_result_id bigint, verdict text, tested_at timestamptz, PRIMARY KEY(group_id, source_result_id));
INSERT INTO groups VALUES (1),(2);
INSERT INTO pelican_group_test_plans VALUES (1,1);
INSERT INTO pelican_group_test_results SELECT i,1,'success',CASE WHEN i<=5 THEN 'degraded' ELSE 'normal' END,
    '2026-10-12'::timestamptz + i*interval '1 second' FROM generate_series(1,105) i;
INSERT INTO pelican_group_test_results VALUES (200,1,'failed','normal','2026-10-13');
INSERT INTO upstream_quality_samples VALUES
 (1,'105','pelican_group_test_results','pelican','degraded','2026-10-14'),
 (2,'300','pelican_group_test_results','pelican','degraded','2026-10-12'),
 (2,'301','group_attempt','pelican','normal','2026-10-12'),
 (2,'302','scheduled_test_results','answer','normal','2026-10-12');
INSERT INTO pelican_showcase_items VALUES
 (1,200,'normal','2026-10-14'),(2,300,'normal','2026-10-13'),(2,400,'normal','2026-10-14');`)
	require.NoError(t, err)
	migration, err := dbmigrations.FS.ReadFile("252_custom_pelican_intelligence_probe.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = tx.ExecContext(ctx, string(migration))
		require.NoError(t, err)
		var count, degraded, earliest int
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(*) FILTER(WHERE verdict='degraded'),MIN(source_result_id)
 FROM pelican_group_intelligence_samples WHERE group_id=1`).Scan(&count, &degraded, &earliest))
		require.Equal(t, 100, count)
		require.Zero(t, degraded)
		require.Equal(t, 6, earliest)
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(*) FILTER(WHERE verdict='degraded')
 FROM pelican_group_intelligence_samples WHERE group_id=2`).Scan(&count, &degraded))
		require.Equal(t, 2, count)
		require.Equal(t, 1, degraded)
	}
}

func TestPelicanIntelligenceReset(t *testing.T) {
	ctx := context.Background()
	groups := createPelicanGroupTestGroups(t, "reset", "untouched")
	plans := NewPelicanGroupTestRepository(integrationDB)
	showcase := NewPelicanShowcaseRepository(integrationDB)
	old := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	p1, err := plans.CreatePlan(ctx, newPelicanGroupTestPlan(groups[0].ID, true, old))
	require.NoError(t, err)
	p2, err := plans.CreatePlan(ctx, newPelicanGroupTestPlan(groups[0].ID, true, old))
	require.NoError(t, err)
	other, err := plans.CreatePlan(ctx, newPelicanGroupTestPlan(groups[1].ID, true, old))
	require.NoError(t, err)
	insert := func(planID int64, started time.Time, verdict string) *service.PelicanGroupTestResult {
		t.Helper()
		saved, err := plans.CreateResult(ctx, &service.PelicanGroupTestResult{PlanID: planID,
			Status: "success", QualityVerdict: verdict, StartedAt: started, FinishedAt: time.Now(), PelicanConfig: p1.PelicanConfig})
		require.NoError(t, err)
		return saved
	}
	stats := func(groupID int64) service.PelicanIntelligenceProbe {
		t.Helper()
		listed, err := showcase.ListGroups(ctx)
		require.NoError(t, err)
		for _, group := range listed {
			if group.ID == groupID {
				return group.IntelligenceProbe
			}
		}
		t.Fatal("group missing")
		return service.PelicanIntelligenceProbe{}
	}
	r1 := insert(p1.ID, old, "degraded")
	r2 := insert(p2.ID, old, "unknown")
	insert(other.ID, old, "degraded")
	require.NoError(t, showcase.Publish(ctx, service.PelicanShowcaseSnapshot{
		GroupID: groups[0].ID, SourceResultID: r1.ID, GeneratedAt: old, ResponseText: "<svg/>", QualityVerdict: "degraded"}, 20))
	resetAt, err := showcase.ResetIntelligence(ctx, groups[0].ID)
	require.NoError(t, err)
	require.False(t, resetAt.IsZero())
	require.Equal(t, service.NewPelicanIntelligenceProbe(0, 0, nil), stats(groups[0].ID))
	require.Equal(t, 1, stats(groups[1].ID).DegradedCount)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pelican_group_test_results WHERE plan_id IN ($1,$2)`, p1.ID, p2.ID).Scan(&count))
	require.Equal(t, 2, count, "history is preserved")
	items, err := showcase.ListItems(ctx, []int64{groups[0].ID}, 20, time.Time{})
	require.NoError(t, err)
	require.Len(t, items, 1, "artwork is preserved")
	// Reassessment and tests already running at reset must not resurrect old samples.
	_, err = integrationDB.ExecContext(ctx, `UPDATE pelican_group_test_results SET quality_verdict='normal' WHERE id IN ($1,$2)`, r1.ID, r2.ID)
	require.NoError(t, err)
	insert(p2.ID, old, "degraded")
	require.Zero(t, stats(groups[0].ID).SampleCount)
	for range 10 {
		insert(p1.ID, time.Now(), "normal")
	}
	require.Nil(t, stats(groups[0].ID).NormalRate)
	insert(p2.ID, time.Now(), "degraded")
	got := stats(groups[0].ID)
	require.Equal(t, 11, got.SampleCount)
	require.Equal(t, 1, got.DegradedCount)
	require.InDelta(t, 1000.0/11, *got.NormalRate, 0.000001)
	_, err = showcase.ResetIntelligence(ctx, groups[0].ID)
	require.NoError(t, err)
	require.Zero(t, stats(groups[0].ID).SampleCount, "reset can be repeated")
	require.Equal(t, 1, stats(groups[1].ID).SampleCount)
	_, err = showcase.ResetIntelligence(ctx, -1)
	require.ErrorIs(t, err, service.ErrGroupNotFound)
}
