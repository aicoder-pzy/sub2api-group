package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type switchTestRepo struct {
	*groupTestRepoFake
	eligible bool
	released int
}

func (r *switchTestRepo) ClaimSwitchTest(ctx context.Context, plan *PelicanGroupTestPlan, _ int64, _ string, now, until time.Time) (bool, error) {
	if !r.eligible {
		return false, nil
	}
	return r.Claim(ctx, plan, now, until, nil)
}

func (r *switchTestRepo) ReleaseSwitchTest(context.Context, int64, time.Time) error {
	r.released++
	return nil
}

func (r *switchTestRepo) HasRecentSwitchTest(context.Context, int64, int64, string, string, time.Time) (bool, error) {
	return !r.eligible, nil
}

type switchTestCache struct {
	change *GroupModelSchedulingTransition
	claims int
	allow  bool
}

func (c *switchTestCache) GetGroupModelSchedulingTransition(context.Context, int64, string) (*GroupModelSchedulingTransition, error) {
	return c.change, nil
}

func (c *switchTestCache) ClaimPelicanSwitchTest(context.Context, int64, string, string, string, *GroupModelSchedulingTransition, time.Time, int) (bool, error) {
	c.claims++
	if !c.allow {
		return false, nil
	}
	c.allow = false
	return true, nil
}

type switchTestRouter struct {
	*groupTestRouterFake
	cache *switchTestCache
}

func (r switchTestRouter) switchTestRoute(context.Context, *Group, string) (*pelicanSwitchTestRoute, error) {
	return &pelicanSwitchTestRoute{cache: r.cache, model: "upstream"}, nil
}

func TestSwitchTestRunsOneFixedChannelSampleWithoutEditingPlan(t *testing.T) {
	for _, outcome := range []string{"success", "failure"} {
		t.Run(outcome, func(t *testing.T) {
			plan := groupTestPlan(8)
			next := groupTestNow.Add(6 * time.Hour)
			plan.NextRunAt = &next
			repo := &switchTestRepo{groupTestRepoFake: newGroupTestRepoFake(plan), eligible: true}
			cache := &switchTestCache{allow: true, change: &GroupModelSchedulingTransition{PreviousAccountID: 1, AccountID: 2, ChangedAt: groupTestNow.Add(-time.Second)}}
			router := switchTestRouter{groupTestRouterFake: &groupTestRouterFake{steps: []routeStep{{account: account(2, "new")}, {account: account(3, "other")}}}, cache: cache}
			calls := 0
			svc, _ := newGroupTestService(repo.groupTestRepoFake, router, func(ctx context.Context, id int64, _ string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
				calls++
				require.EqualValues(t, 2, id)
				require.EqualValues(t, 2, ctx.Value(pelicanDegradationTargetKey{}))
				require.True(t, qualityProbe(ctx), "test must not change the primary")
				require.Equal(t, "high", cfg.ReasoningEffort)
				if outcome == "failure" {
					return nil, errors.New("upstream 503")
				}
				return &ScheduledTestResult{Status: "success", ResponseText: "<html>drawing</html>"}, nil
			})
			svc.repo = repo
			svc.groups = groupTestGroupsFake{4: {ID: 4, Status: StatusActive, AccountSchedulingMode: AccountSchedulingModeSmartChannel}}
			svc.RunSwitchTests(context.Background(), groupTestNow)
			svc.runs.Wait()
			svc.RunSwitchTests(context.Background(), groupTestNow.Add(time.Minute))
			svc.runs.Wait()
			require.Equal(t, 1, calls, "one sample despite parallel_count=8, repeated scans, or failure")
			require.Len(t, repo.results, 1)
			require.Empty(t, repo.results[0].Attempts, "never retry this test on another channel")
			require.Equal(t, "*/30 * * * *", plan.CronExpression)
			require.Equal(t, next, *plan.NextRunAt)
			require.Equal(t, 8, plan.PelicanConfig.ParallelCount)
			require.Equal(t, "draw", plan.PelicanConfig.Prompt)
			require.True(t, plan.Enabled)
			require.Equal(t, 1, repo.released, "denied duplicate releases the lease without recording a run")
		})
	}
}

func TestSwitchTestsHonorFreshnessPlansAndOnlyCommittedSmartPrimaries(t *testing.T) {
	for _, scenario := range []string{"fresh", "disabled", "window", "running", "old", "initial", "unchanged", "legacy"} {
		t.Run(scenario, func(t *testing.T) {
			plan := groupTestPlan(1)
			repo := &switchTestRepo{groupTestRepoFake: newGroupTestRepoFake(plan), eligible: true}
			cache := &switchTestCache{allow: true, change: &GroupModelSchedulingTransition{PreviousAccountID: 1, AccountID: 2, ChangedAt: groupTestNow.Add(-time.Second)}}
			group := &Group{ID: 4, Status: StatusActive, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
			switch scenario {
			case "fresh":
				repo.eligible = false
			case "disabled":
				plan.Enabled = false
			case "window":
				plan.PelicanConfig.DailyStart, plan.PelicanConfig.DailyEnd = "06:00", "10:00"
			case "running":
				until := groupTestNow.Add(time.Minute)
				plan.RunningUntil = &until
			case "old":
				cache.change.ChangedAt = groupTestNow.Add(-time.Hour)
			case "initial":
				cache.change.PreviousAccountID = 0
			case "unchanged":
				cache.change.PreviousAccountID = 2
			case "legacy":
				group.AccountSchedulingMode = AccountSchedulingModeModelAuto
			}
			svc, _ := newGroupTestService(repo.groupTestRepoFake, switchTestRouter{cache: cache}, func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
				t.Fatal("ineligible switch must not call an upstream")
				return nil, nil
			})
			svc.repo, svc.groups = repo, groupTestGroupsFake{4: group}
			svc.RunSwitchTests(context.Background(), groupTestNow)
			svc.runs.Wait()
			require.Empty(t, repo.results)
			require.Zero(t, cache.claims)
		})
	}
}

func TestSmartDegradationReplacementSharesSwitchFreshnessAndCooldown(t *testing.T) {
	for _, recent := range []bool{true, false} {
		t.Run(fmt.Sprint("recent_", recent), func(t *testing.T) {
			plan := groupTestPlan(1)
			repo := &switchTestRepo{groupTestRepoFake: newGroupTestRepoFake(plan), eligible: !recent}
			cache := &switchTestCache{allow: true, change: &GroupModelSchedulingTransition{PreviousAccountID: 1, AccountID: 2, ChangedAt: groupTestNow.Add(-time.Second)}}
			router := switchTestRouter{cache: cache}
			svc, _ := newGroupTestService(repo.groupTestRepoFake, router, nil)
			svc.repo = repo
			group := &Group{ID: 4, Status: StatusActive, AccountSchedulingMode: AccountSchedulingModeSmartChannel}
			ok, err := svc.claimReplacementTest(context.Background(), plan, group, 2, "upstream")
			require.NoError(t, err)
			require.Equal(t, !recent, ok)
			ok, err = svc.claimReplacementTest(context.Background(), plan, group, 2, "upstream")
			require.NoError(t, err)
			require.False(t, ok, "replacement and background worker must share their reservation")
		})
	}
}
