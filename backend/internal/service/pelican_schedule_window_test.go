//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPelicanDailyWindow(t *testing.T) {
	cfg := &PelicanTestConfig{DailyStart: "06:00", DailyEnd: "01:00"}
	for clock, allowed := range map[string]bool{"00:59": true, "01:00": false, "05:59": false, "06:00": true, "23:59": true} {
		now, err := time.ParseInLocation("2006-01-02 15:04", "2026-10-11 "+clock, pelicanScheduleLocation)
		require.NoError(t, err)
		require.Equal(t, allowed, pelicanWindowAllows(cfg, now.UTC()), clock)
	}
	from := time.Date(2026, 10, 11, 1, 0, 0, 0, pelicanScheduleLocation)
	next, err := computeNextPelicanRun("*/30 * * * *", from, cfg)
	require.NoError(t, err)
	require.Equal(t, "2026-10-11 06:00", next.In(pelicanScheduleLocation).Format("2006-01-02 15:04"))
	_, err = computeNextPelicanRun("0 2 * * *", from, cfg)
	require.ErrorContains(t, err, "no run inside")
	for _, invalid := range []*PelicanTestConfig{{DailyStart: "6:00", DailyEnd: "01:00"}, {DailyStart: "24:00", DailyEnd: "01:00"}, {DailyStart: "06:00"}, {DailyStart: "06:00", DailyEnd: "06:00"}} {
		require.Error(t, validatePelicanWindow(invalid))
	}
	require.NoError(t, validatePelicanWindow(&PelicanTestConfig{}))
	daytime := &PelicanTestConfig{DailyStart: "09:00", DailyEnd: "17:00"}
	require.False(t, pelicanWindowAllows(daytime, from))
	require.True(t, pelicanWindowAllows(daytime, from.Add(8*time.Hour)))
}

func TestPelicanScheduledWindowSkipsGenerationButAllowsManual(t *testing.T) {
	plan := groupTestPlan(1)
	plan.PelicanConfig.DailyStart, plan.PelicanConfig.DailyEnd = "13:00", "14:00" // Fake clock is noon in Beijing.
	repo := newGroupTestRepoFake(plan)
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}}}
	svc, _ := newGroupTestService(repo, router, answers(map[int64]ScheduledTestResult{11: {Status: "success", ResponseText: "<svg></svg>"}}))
	svc.runScheduled(context.Background(), plan, groupTestNow)
	require.Empty(t, repo.results)
	require.Empty(t, router.excluded)
	require.Empty(t, repo.finished, "skipping must preserve last_run_at")
	require.Equal(t, groupTestNow, repo.claims[0].until)
	require.Equal(t, "13:00", repo.claims[0].next.In(pelicanScheduleLocation).Format("15:04"))
	require.NoError(t, svc.RunNow(context.Background(), plan.ID))
	svc.runs.Wait()
	require.Len(t, repo.results, 1)
	require.Nil(t, repo.claims[1].next)
}
