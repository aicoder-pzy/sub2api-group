package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type textIntelligenceRetryRepo struct {
	*groupTestRepoFake
	work     []TextIntelligenceRetry
	claimed  bool
	finished []TextIntelligenceRetry
}

func TestTextIntelligenceRetryCannotUseAnotherActiveAccount(t *testing.T) {
	for _, originalActive := range []bool{true, false} {
		plan := groupTestPlan(1)
		plan.PelicanConfig.QuestionKind, plan.PelicanConfig.Prompt = "candy", CandyPrompt
		repo := &textIntelligenceRetryRepo{groupTestRepoFake: newGroupTestRepoFake(plan), claimed: true,
			work: []TextIntelligenceRetry{{PlanID: plan.ID, AccountID: 11, ResultID: 4, PlanUpdatedAt: plan.UpdatedAt}}}
		if !originalActive {
			repo.traffic = repo.traffic[1:]
		}
		router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(12, "b")}}}
		calls := 0
		svc, _ := newGroupTestService(repo.groupTestRepoFake, router, func(_ context.Context, id int64, model string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
			calls++
			require.EqualValues(t, 11, id)
			require.Equal(t, plan.ModelID, model)
			require.Equal(t, CandyPrompt, cfg.Prompt)
			return nil, errors.New("upstream 429")
		})
		svc.repo = repo
		svc.runTextIntelligenceDue(context.Background(), groupTestNow)
		if originalActive {
			require.Equal(t, 1, calls)
			require.Len(t, repo.results, 1)
			require.EqualValues(t, 11, repo.results[0].AccountID)
			require.Equal(t, "failed", repo.results[0].Status)
			require.Empty(t, repo.results[0].Attempts)
		} else {
			require.Zero(t, calls)
			require.Empty(t, repo.results)
		}
	}
}

func (r *textIntelligenceRetryRepo) ListTextIntelligenceDue(context.Context, time.Time) ([]TextIntelligenceRetry, error) {
	return r.work, nil
}
func (r *textIntelligenceRetryRepo) ClaimTextIntelligenceRetry(context.Context, TextIntelligenceRetry, time.Time, time.Time) (bool, error) {
	return r.claimed, nil
}
func (r *textIntelligenceRetryRepo) FinishTextIntelligenceRetry(_ context.Context, work TextIntelligenceRetry, _ int64, _, _ time.Time) error {
	r.finished = append(r.finished, work)
	return nil
}

func TestTextIntelligenceRetryPinsOriginalAccountAndModel(t *testing.T) {
	plan := groupTestPlan(1)
	plan.PelicanConfig.QuestionKind = "candy"
	plan.PelicanConfig.Prompt = CandyPrompt
	repo := &textIntelligenceRetryRepo{
		groupTestRepoFake: newGroupTestRepoFake(plan), claimed: true,
		work: []TextIntelligenceRetry{{PlanID: plan.ID, AccountID: 1, ResultID: 4, PlanUpdatedAt: plan.UpdatedAt}},
	}
	repo.traffic = []PelicanTrafficAccount{{AccountID: 1, LastSeenAt: groupTestNow}}
	called := false
	svc, _ := newGroupTestService(repo.groupTestRepoFake, degradationTestRouter{t: t}, func(_ context.Context, id int64, model string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
		called = true
		require.EqualValues(t, 1, id)
		require.Equal(t, plan.ModelID, model)
		require.Equal(t, "candy", cfg.QuestionKind)
		return &ScheduledTestResult{Status: "success", ResponseText: "21"}, nil
	})
	svc.repo = repo
	svc.groups = groupTestGroupsFake{4: {ID: 4, Status: StatusActive, AccountSchedulingMode: AccountSchedulingModeQualityPreferred}}
	now := groupTestNow
	svc.now = func() time.Time { return now }
	svc.runTextIntelligenceDue(context.Background(), now)
	require.True(t, called)
	require.Len(t, repo.results, 1)
	require.Equal(t, "21", repo.results[0].ResponseText)
	require.Equal(t, repo.work, repo.finished)
}
