package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type degradationTestRepo struct {
	*groupTestRepoFake
	work    []PelicanDegradationWork
	due     time.Time
	phase   int
	limits  []int
	blocked bool
}

func (r *degradationTestRepo) ListDegradationDue(_ context.Context, now time.Time) ([]PelicanDegradationWork, error) {
	if r.phase >= len(r.work) || now.Before(r.due) {
		return nil, nil
	}
	return r.work[r.phase : r.phase+1], nil
}
func (r *degradationTestRepo) ClaimDegradation(_ context.Context, _ PelicanDegradationWork, _, _ time.Time, limit int) (bool, error) {
	r.limits = append(r.limits, limit)
	return !r.blocked, nil
}
func (r *degradationTestRepo) FinishDegradation(context.Context, PelicanDegradationWork, time.Time, time.Time, bool) error {
	r.phase++
	return nil
}

type degradationTestRouter struct {
	t      *testing.T
	events *[]string
}

func (r degradationTestRouter) route(ctx context.Context, _ *Group, model string, _ map[int64]struct{}) (*pelicanGroupRoute, error) {
	id := int64(1)
	if target, _ := ctx.Value(pelicanDegradationTargetKey{}).(int64); target > 0 {
		require.EqualValues(r.t, 1, target, "the confirmation must target the original account")
		require.True(r.t, qualityProbe(ctx), "confirmation must not alter the primary binding")
	} else {
		id = 2
		require.False(r.t, qualityProbe(ctx), "replacement uses user scheduling, not least-tested-account rotation")
	}
	return &pelicanGroupRoute{account: account(id, "test"), model: model, release: func() {}, confirm: func() bool {
		*r.events = append(*r.events, "switch")
		return true
	}}, nil
}

func TestPelicanDegradationRetestsOriginalThenTestsReplacementImmediately(t *testing.T) {
	plan := groupTestPlan(8)
	plan.PelicanConfig.ReasoningEffort = "medium"
	now := groupTestNow.Add(pelicanDegradationCooldown)
	repo := &degradationTestRepo{groupTestRepoFake: newGroupTestRepoFake(plan), due: now, work: []PelicanDegradationWork{
		{PlanID: plan.ID, GroupID: plan.GroupID, AccountID: 1, Model: plan.ModelID, Action: "retry"},
		{PlanID: plan.ID, GroupID: plan.GroupID, AccountID: 1, Model: plan.ModelID, Action: "replacement"},
	}}
	var events []string
	var called []int64
	svc, _ := newGroupTestService(repo.groupTestRepoFake, degradationTestRouter{t, &events}, func(_ context.Context, id int64, _ string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
		require.Equal(t, "medium", cfg.ReasoningEffort)
		called = append(called, id)
		if id == 1 {
			events = append(events, "retest-a")
		} else {
			events = append(events, "test-b")
		}
		return &ScheduledTestResult{Status: "success", ResponseText: "<html>drawing</html>"}, nil
	})
	svc.repo = repo
	svc.groups = groupTestGroupsFake{4: {ID: 4, Status: StatusActive, AccountSchedulingMode: AccountSchedulingModeQualityPreferred}}
	svc.now = func() time.Time { return now }
	svc.assessOutput = func(_ context.Context, output string) (string, *PelicanAssessment) {
		verdict := "normal"
		if len(called) == 1 {
			verdict = "degraded"
		}
		return output, &PelicanAssessment{Quality: verdict}
	}
	svc.runDegradationDue(context.Background(), now.Add(-time.Second), 0)
	require.Empty(t, called, "five-minute cooldown must elapse before calling the original account")
	svc.runDegradationDue(context.Background(), now, 0)
	require.Equal(t, []int64{1, 2}, called, "append one retest and one replacement test despite parallel_count=8")
	require.Equal(t, []string{"retest-a", "switch", "test-b"}, events)
	require.Len(t, repo.results, 2)
	require.Equal(t, "degraded", repo.results[0].QualityVerdict)
	require.Equal(t, "normal", repo.results[1].QualityVerdict)
	require.Equal(t, []int{10, 10}, repo.limits)
}

func TestPelicanDegradationHonorsWindowDisabledPlansAndHourlyLimit(t *testing.T) {
	for _, scenario := range []string{"window", "disabled", "limit"} {
		t.Run(scenario, func(t *testing.T) {
			plan := groupTestPlan(1)
			repo := &degradationTestRepo{groupTestRepoFake: newGroupTestRepoFake(plan), due: groupTestNow,
				work: []PelicanDegradationWork{{PlanID: plan.ID, GroupID: 4, AccountID: 1, Action: "retry"}}}
			switch scenario {
			case "window":
				plan.PelicanConfig.DailyStart, plan.PelicanConfig.DailyEnd = "06:00", "10:00"
			case "disabled":
				plan.Enabled = false
			case "limit":
				repo.blocked = true
			}
			svc, _ := newGroupTestService(repo.groupTestRepoFake, nil, func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
				t.Fatal("a blocked extra test must not call the upstream")
				return nil, nil
			})
			svc.repo = repo
			svc.runDegradationDue(context.Background(), groupTestNow, 0)
			require.Zero(t, repo.phase)
			require.Empty(t, repo.results)
		})
	}
}
