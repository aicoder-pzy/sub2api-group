package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type groupTestClaim struct {
	planID int64
	until  time.Time
	next   *time.Time
}

type groupTestRepoFake struct {
	mu            sync.Mutex
	plans         map[int64]*PelicanGroupTestPlan
	due           []*PelicanGroupTestPlan
	claimOK       bool
	claims        []groupTestClaim
	finished      []time.Time
	results       []*PelicanGroupTestResult
	pruned        []int64
	expiredBefore time.Time
	listed        []*PelicanGroupTestResult
	listedOffset  int
	listedLimit   int
	nextID        int64
	traffic       []PelicanTrafficAccount
	trafficErr    error
}

func newGroupTestRepoFake(plans ...*PelicanGroupTestPlan) *groupTestRepoFake {
	repo := &groupTestRepoFake{plans: map[int64]*PelicanGroupTestPlan{}, claimOK: true, nextID: 1000,
		traffic: []PelicanTrafficAccount{{AccountID: 11, LastSeenAt: groupTestNow}, {AccountID: 12, LastSeenAt: groupTestNow}}}
	for _, plan := range plans {
		repo.plans[plan.ID] = plan
	}
	return repo
}

func (r *groupTestRepoFake) ListTrafficAccounts(context.Context, *PelicanGroupTestPlan, time.Time) ([]PelicanTrafficAccount, error) {
	return r.traffic, r.trafficErr
}

func (r *groupTestRepoFake) ListPlans(context.Context) ([]*PelicanGroupTestPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*PelicanGroupTestPlan, 0, len(r.plans))
	for _, plan := range r.plans {
		out = append(out, plan)
	}
	return out, nil
}
func (r *groupTestRepoFake) GetPlan(_ context.Context, id int64) (*PelicanGroupTestPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.plans[id], nil
}
func (r *groupTestRepoFake) CreatePlan(_ context.Context, plan *PelicanGroupTestPlan) (*PelicanGroupTestPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved := *plan
	r.nextID++
	saved.ID = r.nextID
	r.plans[saved.ID] = &saved
	return &saved, nil
}
func (r *groupTestRepoFake) UpdatePlan(_ context.Context, plan *PelicanGroupTestPlan) (*PelicanGroupTestPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plans[plan.ID] == nil {
		return nil, nil
	}
	saved := *plan
	r.plans[plan.ID] = &saved
	return &saved, nil
}
func (r *groupTestRepoFake) DeletePlan(_ context.Context, id int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.plans[id]
	delete(r.plans, id)
	return ok, nil
}
func (r *groupTestRepoFake) ListDue(context.Context, time.Time) ([]*PelicanGroupTestPlan, error) {
	return r.due, nil
}
func (r *groupTestRepoFake) Claim(_ context.Context, plan *PelicanGroupTestPlan, _, until time.Time, next *time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claims = append(r.claims, groupTestClaim{plan.ID, until, next})
	return r.claimOK, nil
}
func (r *groupTestRepoFake) Finish(_ context.Context, _ int64, until, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished = append(r.finished, until)
	return nil
}
func (r *groupTestRepoFake) CreateResult(_ context.Context, result *PelicanGroupTestResult) (*PelicanGroupTestResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved := *result
	r.nextID++
	saved.ID = r.nextID
	r.results = append(r.results, &saved)
	return &saved, nil
}
func (r *groupTestRepoFake) PruneResults(_ context.Context, planID int64, _ int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruned = append(r.pruned, planID)
	return nil
}
func (r *groupTestRepoFake) PruneExpiredResults(_ context.Context, before time.Time) error {
	r.expiredBefore = before
	return nil
}
func (r *groupTestRepoFake) ListResults(_ context.Context, _ int64, offset, limit int) ([]*PelicanGroupTestResult, int64, error) {
	r.listedOffset, r.listedLimit = offset, limit
	start := min(offset, len(r.listed))
	end := min(start+limit, len(r.listed))
	return r.listed[start:end], int64(len(r.listed)), nil
}
func (r *groupTestRepoFake) GetResult(_ context.Context, id int64) (*PelicanGroupTestResult, error) {
	for _, result := range r.results {
		if result.ID == id {
			return result, nil
		}
	}
	return nil, nil
}

type groupTestGroupsFake map[int64]*Group

func (g groupTestGroupsFake) GetByID(_ context.Context, id int64) (*Group, error) {
	if group, ok := g[id]; ok {
		return group, nil
	}
	return nil, ErrGroupNotFound
}

type routeStep struct {
	account *Account
	err     error
}

type groupTestRouterFake struct {
	mu           sync.Mutex
	steps        []routeStep
	excluded     [][]int64
	released     []int64
	ignoreTarget bool
}

func (r *groupTestRouterFake) route(ctx context.Context, _ *Group, model string, excluded map[int64]struct{}) (*pelicanGroupRoute, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := []int64{}
	for id := range excluded {
		seen = append(seen, id)
	}
	r.excluded = append(r.excluded, seen)
	if len(r.steps) == 0 {
		return nil, ErrNoAvailableAccounts
	}
	index := 0
	if target, _ := ctx.Value(pelicanDegradationTargetKey{}).(int64); target > 0 && !r.ignoreTarget && r.steps[0].err == nil {
		index = -1
		for i, step := range r.steps {
			if step.account != nil && step.account.ID == target {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, ErrNoAvailableAccounts
		}
	}
	step := r.steps[index]
	r.steps = append(r.steps[:index], r.steps[index+1:]...)
	if step.err != nil {
		return nil, step.err
	}
	id := step.account.ID
	return &pelicanGroupRoute{account: step.account, model: model, release: func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.released = append(r.released, id)
	}}, nil
}

var groupTestNow = time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)

func groupTestPlan(parallel int) *PelicanGroupTestPlan {
	return &PelicanGroupTestPlan{ID: 7, GroupID: 4, ModelID: "gpt-6-astra", CronExpression: "*/30 * * * *", Enabled: true,
		PelicanConfig: &PelicanTestConfig{QuestionKind: "pelican", Prompt: "draw", ReasoningEffort: "high", ParallelCount: parallel, ModelID: "gpt-6-astra"}}
}

func newGroupTestService(repo *groupTestRepoFake, router pelicanGroupRouter,
	run func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error)) (*PelicanGroupTestService, *showcaseRepoStub) {
	showcaseRepo := &showcaseRepoStub{}
	return &PelicanGroupTestService{
		repo:       repo,
		groups:     groupTestGroupsFake{4: {ID: 4, Name: "GPT PRO", Platform: PlatformOpenAI, Status: StatusActive}},
		router:     router,
		runAccount: run,
		showcase:   &PelicanShowcaseService{repo: showcaseRepo, settings: enabledShowcase()},
		now:        func() time.Time { return groupTestNow },
	}, showcaseRepo
}

func account(id int64, name string) *Account { return &Account{ID: id, Name: name} }

// answers scripts the account test: each account answers with the given result.
func answers(byAccount map[int64]ScheduledTestResult) func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
	return func(_ context.Context, accountID int64, _ string, _ *PelicanTestConfig) (*ScheduledTestResult, error) {
		result := byAccount[accountID]
		return &result, nil
	}
}

func TestPelicanGroupTestPlanInputValidation(t *testing.T) {
	ctx := context.Background()
	repo := newGroupTestRepoFake()
	svc, _ := newGroupTestService(repo, &groupTestRouterFake{}, nil)
	valid := PelicanGroupTestPlanInput{GroupID: 4, ModelID: " gpt-6-astra ", Prompt: "draw a pelican", ReasoningEffort: "HIGH", Enabled: true}

	for name, input := range map[string]PelicanGroupTestPlanInput{
		"no group":          {ModelID: "m", Prompt: "p", ReasoningEffort: "high"},
		"unknown group":     {GroupID: 99, ModelID: "m", Prompt: "p", ReasoningEffort: "high"},
		"no model":          {GroupID: 4, Prompt: "p", ReasoningEffort: "high"},
		"long model":        {GroupID: 4, ModelID: strings.Repeat("m", 101), Prompt: "p", ReasoningEffort: "high"},
		"no prompt":         {GroupID: 4, ModelID: "m", Prompt: "  ", ReasoningEffort: "high"},
		"unknown mode":      {GroupID: 4, ModelID: "m", Prompt: "p", ReasoningEffort: "high", PromptMode: "invalid"},
		"long prompt":       {GroupID: 4, ModelID: "m", Prompt: strings.Repeat("p", 32001), ReasoningEffort: "high"},
		"unknown effort":    {GroupID: 4, ModelID: "m", Prompt: "p", ReasoningEffort: "extreme"},
		"too many parallel": {GroupID: 4, ModelID: "m", Prompt: "p", ReasoningEffort: "high", ParallelCount: 9},
		"negative parallel": {GroupID: 4, ModelID: "m", Prompt: "p", ReasoningEffort: "high", ParallelCount: -1},
		"bad schedule":      {GroupID: 4, ModelID: "m", Prompt: "p", ReasoningEffort: "high", CronExpression: "every minute"},
		"long candy retry":  {GroupID: 4, ModelID: "m", QuestionKind: "candy", Prompt: CandyPrompt, ReasoningEffort: "high", RetryIntervalMinutes: 61},
	} {
		_, err := svc.CreatePlan(ctx, input)
		require.Error(t, err, name)
	}
	require.Empty(t, repo.plans)

	plan, err := svc.CreatePlan(ctx, valid)
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", plan.ModelID)
	require.Equal(t, "*/30 * * * *", plan.CronExpression, "the default schedule is every 30 minutes")
	require.True(t, plan.Enabled)
	require.Equal(t, &PelicanTestConfig{QuestionKind: "pelican", Prompt: "draw a pelican", ReasoningEffort: "high", ParallelCount: 1, ModelID: "gpt-6-astra"}, plan.PelicanConfig)
	require.Equal(t, groupTestNow.Add(30*time.Minute), *plan.NextRunAt)

	creative := valid
	creative.Prompt, creative.PromptMode = "", pelicanRandomCreative
	randomPlan, err := svc.CreatePlan(ctx, creative)
	require.NoError(t, err, "random questions do not require a fixed prompt")
	require.Equal(t, pelicanRandomCreative, randomPlan.PelicanConfig.PromptMode)
	require.Nil(t, randomPlan.PelicanConfig.CreativeRound, "only execution snapshots a round")

	candy, err := svc.CreatePlan(ctx, PelicanGroupTestPlanInput{GroupID: 4, ModelID: "gpt-6-astra", QuestionKind: "candy", Prompt: "original candy wording", RetryIntervalMinutes: 7, ReasoningEffort: "medium"})
	require.NoError(t, err)
	require.Equal(t, "original candy wording", candy.PelicanConfig.Prompt)
	require.Equal(t, 7, candy.PelicanConfig.RetryIntervalMinutes)
	defaultRetry, err := svc.CreatePlan(ctx, PelicanGroupTestPlanInput{GroupID: 4, ModelID: "gpt-6-astra", QuestionKind: "candy", ReasoningEffort: "medium"})
	require.NoError(t, err)
	require.Equal(t, CandyPrompt, defaultRetry.PelicanConfig.Prompt)
	require.Equal(t, 2, defaultRetry.PelicanConfig.RetryIntervalMinutes)

	// An edit keeps the plan's group: moving it would mix two groups' history.
	updated, err := svc.UpdatePlan(ctx, plan.ID, PelicanGroupTestPlanInput{GroupID: 99, ModelID: "claude-opus-5-5", Prompt: "p", ReasoningEffort: "low", CronExpression: "0 * * * *", ParallelCount: 3})
	require.NoError(t, err)
	require.EqualValues(t, 4, updated.GroupID)
	require.Equal(t, "claude-opus-5-5", updated.ModelID)
	require.False(t, updated.Enabled)
	require.Equal(t, 3, updated.PelicanConfig.ParallelCount)
	require.Equal(t, groupTestNow.Add(time.Hour), *updated.NextRunAt)

	_, err = svc.UpdatePlan(ctx, 404, valid)
	require.ErrorIs(t, err, ErrPelicanGroupTestPlanNotFound)
	require.NoError(t, svc.DeletePlan(ctx, plan.ID))
	require.ErrorIs(t, svc.DeletePlan(ctx, plan.ID), ErrPelicanGroupTestPlanNotFound)
}

func TestPelicanGroupTestFollowsStreamingAccountWithoutFailover(t *testing.T) {
	for _, outcome := range []string{"normal", "wrong", "429", "timeout", "unavailable", "wrong_account", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			repo := newGroupTestRepoFake()
			repo.traffic = []PelicanTrafficAccount{{AccountID: 11, AccountName: "actual", LastSeenAt: groupTestNow}}
			router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "actual")}, {account: account(12, "other")}}}
			if outcome == "unavailable" {
				router.steps[0] = routeStep{err: ErrNoAvailableAccounts}
			}
			if outcome == "wrong_account" {
				router.ignoreTarget = true
				router.steps[0] = routeStep{account: account(12, "other")}
			}
			calls := 0
			svc, _ := newGroupTestService(repo, router, func(ctx context.Context, id int64, model string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
				calls++
				require.EqualValues(t, 11, id)
				require.Equal(t, "gpt-6-astra", model)
				require.Equal(t, "high", cfg.ReasoningEffort)
				require.True(t, qualityProbe(ctx))
				switch outcome {
				case "429":
					return &ScheduledTestResult{Status: "failed", ErrorMessage: "API returned 429"}, nil
				case "timeout":
					return nil, errors.New("timeout")
				case "panic":
					panic("adapter")
				case "wrong":
					return &ScheduledTestResult{Status: "failed", ResponseText: "29", ErrorMessage: "answer_mismatch: expected 21"}, nil
				default:
					return &ScheduledTestResult{Status: "success", ResponseText: "21"}, nil
				}
			})
			result := svc.runSample(context.Background(), groupTestPlan(1), &Group{ID: 4})
			require.EqualValues(t, 11, result.AccountID)
			require.Empty(t, result.Attempts, "another account must never mask this one's failure")
			require.Len(t, router.excluded, 1)
			if outcome == "normal" {
				require.Equal(t, "success", result.Status)
			} else {
				require.Equal(t, "failed", result.Status)
			}
			if outcome == "unavailable" || outcome == "wrong_account" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestPelicanGroupTestSkipsWithoutRealStreamingEvidence(t *testing.T) {
	for _, problem := range []error{nil, errors.New("evidence unavailable")} {
		repo := newGroupTestRepoFake()
		repo.traffic, repo.trafficErr = nil, problem
		router := &groupTestRouterFake{}
		svc, _ := newGroupTestService(repo, router, func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
			t.Fatal("no paid call without evidence")
			return nil, nil
		})
		require.Empty(t, svc.runSamples(context.Background(), groupTestPlan(1)))
		require.Nil(t, svc.runSample(context.Background(), groupTestPlan(1), &Group{ID: 4}))
		require.Empty(t, router.excluded)
	}
}

func TestPelicanGroupTestCoversActiveAccountsWithinSampleBudget(t *testing.T) {
	plan := groupTestPlan(2)
	repo := newGroupTestRepoFake()
	repo.traffic = []PelicanTrafficAccount{{AccountID: 11}, {AccountID: 12}, {AccountID: 13}}
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(12, "b")}, {account: account(13, "c")}}}
	svc, _ := newGroupTestService(repo, router, answers(map[int64]ScheduledTestResult{
		11: {Status: "failed", ResponseText: "29", ErrorMessage: "answer_mismatch"},
		12: {Status: "success", ResponseText: "21"},
	}))
	results := svc.runSamples(context.Background(), plan)
	require.Len(t, results, 2, "do not multiply the user's configured budget by the account count")
	require.EqualValues(t, 11, results[0].AccountID)
	require.Equal(t, "failed", results[0].Status)
	require.EqualValues(t, 12, results[1].AccountID)
	require.Equal(t, "success", results[1].Status)
	require.Len(t, router.steps, 1)
}

func TestPelicanGroupTestSampleSurvivesAccountTestFailures(t *testing.T) {
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}}}
	svc, _ := newGroupTestService(newGroupTestRepoFake(), router, func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
		panic("adapter bug")
	})
	result := svc.runSample(context.Background(), groupTestPlan(1), &Group{ID: 4})
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "pelican_group_test_panic: sample failed", result.ErrorMessage)
	require.Equal(t, []int64{11}, router.released, "panic must release the account concurrency slot")

	router = &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(12, "b")}}}
	calls := 0
	svc, _ = newGroupTestService(newGroupTestRepoFake(), router, func(_ context.Context, id int64, _ string, _ *PelicanTestConfig) (*ScheduledTestResult, error) {
		calls++
		if id == 11 {
			return nil, errors.New("dial tcp: timeout")
		}
		return &ScheduledTestResult{Status: "success", ResponseText: "<svg></svg>"}, nil
	})
	result = svc.runSample(context.Background(), groupTestPlan(1), &Group{ID: 4})
	require.Equal(t, "failed", result.Status)
	require.Equal(t, 1, calls)
	require.Empty(t, result.Attempts)
	require.EqualValues(t, 11, result.AccountID)
}

func TestPelicanGroupTestExecuteSavesPublishesAndFinishes(t *testing.T) {
	repo := newGroupTestRepoFake()
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(12, "b")}}}
	svc, showcase := newGroupTestService(repo, router, answers(map[int64]ScheduledTestResult{
		11: {Status: "success", ResponseText: "<svg data-a></svg>", LatencyMs: 900},
		12: {Status: "failed", ResponseText: "not html", ErrorMessage: "Model did not return HTML or SVG"},
	}))
	until := groupTestNow.Add(15 * time.Minute)
	svc.execute(groupTestPlan(2), until)

	require.Len(t, repo.results, 2, "one result per parallel sample")
	require.Len(t, showcase.published, 1, "only the HTML answer reaches the gallery")
	published := showcase.published[0]
	require.EqualValues(t, 4, published.snapshot.GroupID)
	require.Equal(t, "<svg data-a></svg>", published.snapshot.ResponseText)
	require.Equal(t, "gpt-6-astra", published.snapshot.ModelID)
	require.Equal(t, "high", published.snapshot.ReasoningEffort)
	require.EqualValues(t, 900, published.snapshot.LatencyMs)
	require.Equal(t, 12, published.maxItems)
	for _, result := range repo.results {
		if result.Status == "success" {
			require.Equal(t, result.ID, published.snapshot.SourceResultID)
		}
	}
	require.Equal(t, []int64{7}, repo.pruned)
	require.Equal(t, []time.Time{until}, repo.finished)
}

func TestPelicanGroupTestUnavailableGroupRecordsOneFailure(t *testing.T) {
	repo := newGroupTestRepoFake()
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}}}
	svc, showcase := newGroupTestService(repo, router, answers(nil))
	svc.groups = groupTestGroupsFake{4: {ID: 4, Status: StatusDisabled}}
	svc.execute(groupTestPlan(3), groupTestNow)
	require.Len(t, repo.results, 1)
	require.Equal(t, "group_unavailable: group is disabled", repo.results[0].ErrorMessage)
	require.Empty(t, router.excluded, "the scheduler is not asked for a disabled group")
	require.Empty(t, showcase.published)

	svc.groups = groupTestGroupsFake{}
	svc.execute(groupTestPlan(1), groupTestNow)
	require.Contains(t, repo.results[1].ErrorMessage, "group_unavailable")
}

func TestPelicanGroupRunEnabledQuestions(t *testing.T) {
	ctx := context.Background()
	drawing := groupTestPlan(1)
	drawing.GroupStatus = StatusActive
	repo := newGroupTestRepoFake(drawing)
	upstream := account(11, "a")
	router := &groupTestRouterFake{steps: []routeStep{{account: upstream}, {account: upstream}}}
	svc, showcase := newGroupTestService(repo, router, func(_ context.Context, _ int64, _ string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
		if cfg.QuestionKind == "pelican" {
			return &ScheduledTestResult{Status: "success", ResponseText: "<svg></svg>"}, nil
		}
		if cfg.QuestionKind != "custom" || !strings.HasPrefix(intelligenceTestPrompt(cfg), "2+2?") {
			return nil, errors.New("wrong question")
		}
		output := "3"
		message := intelligenceTestOutputError(cfg, output)
		if message == "" {
			return &ScheduledTestResult{Status: "success", ResponseText: output}, nil
		}
		return &ScheduledTestResult{Status: "failed", ResponseText: output, ErrorMessage: message}, nil
	})
	input := PelicanGroupTestPlanInput{GroupID: 4, ModelID: drawing.ModelID, QuestionKind: "custom", QuestionName: "加法",
		Prompt: "2+2?", ReasoningEffort: "high", Enabled: true}
	_, err := svc.CreatePlan(ctx, input)
	require.Error(t, err, "missing grading answer")
	input.ExpectedAnswer = " 4 "
	input.RetryIntervalMinutes = 7
	custom, err := svc.CreatePlan(ctx, input)
	require.NoError(t, err)
	custom.GroupStatus = StatusActive
	require.Equal(t, "4", custom.PelicanConfig.ExpectedAnswer)
	require.Equal(t, 7, custom.PelicanConfig.RetryIntervalMinutes)
	require.Empty(t, intelligenceTestOutputError(custom.PelicanConfig, "  4\n"))
	paused := groupTestPlan(1)
	paused.ID, paused.Enabled, paused.GroupStatus = 8, false, StatusActive
	otherModel := groupTestPlan(1)
	otherModel.ID, otherModel.ModelID, otherModel.GroupStatus = 9, "gpt-other", StatusActive
	repo.plans[paused.ID], repo.plans[otherModel.ID] = paused, otherModel

	require.NoError(t, svc.RunEnabled(ctx, drawing.GroupID, drawing.ModelID))
	svc.runs.Wait()
	require.Len(t, repo.claims, 2)
	require.Len(t, repo.results, 2)
	require.Len(t, showcase.published, 1)
	for _, result := range repo.results {
		if result.PlanID == custom.ID {
			require.Equal(t, "failed", result.Status)
			require.Equal(t, "answer_mismatch", result.ErrorMessage)
			require.Equal(t, "加法", result.PelicanConfig.QuestionName)
			require.Empty(t, result.QualityVerdict)
		} else {
			require.Equal(t, drawing.ID, result.PlanID)
			require.Equal(t, "success", result.Status)
		}
	}
}

func TestPelicanGroupRunEnabledCanCoverAllModels(t *testing.T) {
	ctx := context.Background()
	first := groupTestPlan(1)
	first.GroupStatus = StatusActive
	second := groupTestPlan(1)
	second.ID, second.ModelID, second.PelicanConfig.ModelID, second.GroupStatus = 8, "gpt-6.2-astra", "gpt-6.2-astra", StatusActive
	repo := newGroupTestRepoFake(first, second)
	var ran []string
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(11, "a")}}}
	svc, _ := newGroupTestService(repo, router, func(_ context.Context, _ int64, model string, _ *PelicanTestConfig) (*ScheduledTestResult, error) {
		ran = append(ran, model)
		return &ScheduledTestResult{Status: "success", ResponseText: "<svg></svg>"}, nil
	})
	svc.groups = groupTestGroupsFake{4: {ID: 4, Status: StatusActive}}
	require.NoError(t, svc.RunEnabled(ctx, first.GroupID, ""))
	svc.runs.Wait()
	require.ElementsMatch(t, []string{first.ModelID, second.ModelID}, ran)
}

func TestPelicanGroupTestRunNowAndRunDue(t *testing.T) {
	ctx := context.Background()
	plan := groupTestPlan(1)
	repo := newGroupTestRepoFake(plan)
	run := answers(map[int64]ScheduledTestResult{11: {Status: "success", ResponseText: "<svg></svg>"}})
	svc, _ := newGroupTestService(repo, &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(11, "a")}}}, run)

	require.ErrorIs(t, svc.RunNow(ctx, 404), ErrPelicanGroupTestPlanNotFound)
	noModel := groupTestPlan(1)
	noModel.ID, noModel.ModelID = 8, ""
	repo.plans[noModel.ID] = noModel
	require.ErrorIs(t, svc.RunNow(ctx, noModel.ID), ErrPelicanGroupTestModelRequired, "a carried-over plan needs a model first")
	repo.due = []*PelicanGroupTestPlan{noModel}
	svc.RunDue(ctx, groupTestNow)
	require.Empty(t, repo.claims, "neither path claims a plan without a model")
	repo.claimOK = false
	require.ErrorIs(t, svc.RunNow(ctx, plan.ID), ErrPelicanGroupTestPlanRunning)
	repo.claimOK = true
	require.NoError(t, svc.RunNow(ctx, plan.ID))
	svc.runs.Wait()
	require.Nil(t, repo.claims[1].next, "a manual run keeps the schedule")
	require.Len(t, repo.results, 1)

	repo.due = []*PelicanGroupTestPlan{plan}
	svc.RunDue(ctx, groupTestNow)
	require.Len(t, repo.claims, 3)
	require.Equal(t, groupTestNow.Add(30*time.Minute), *repo.claims[2].next, "a scheduled claim moves to the next slot")
	require.Equal(t, groupTestNow.Add(pelicanGroupTestLease), repo.claims[2].until)
	require.Len(t, repo.results, 2)

	repo.claimOK = false
	svc.RunDue(ctx, groupTestNow)
	require.Len(t, repo.results, 2, "a plan claimed elsewhere is skipped")

	var nilService *PelicanGroupTestService
	require.NotPanics(t, func() {
		nilService.RunDue(ctx, groupTestNow)
		nilService.Cleanup(ctx, groupTestNow)
	})
}

func TestPelicanGroupTestCleanupAndHistoryPaging(t *testing.T) {
	ctx := context.Background()
	repo := newGroupTestRepoFake()
	svc, showcase := newGroupTestService(repo, &groupTestRouterFake{}, nil)
	svc.Cleanup(ctx, groupTestNow)
	require.Equal(t, groupTestNow.Add(-7*24*time.Hour), repo.expiredBefore)
	require.Len(t, showcase.pruned, 1, "the gallery limits are applied on the same tick")

	for id := int64(10); id > 0; id-- {
		repo.listed = append(repo.listed, &PelicanGroupTestResult{ID: id})
	}
	items, total, err := svc.ListResults(ctx, 0, 1, 3)
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.EqualValues(t, 10, total)
	require.EqualValues(t, 10, items[0].ID)
	items, total, err = svc.ListResults(ctx, 0, 2, 3)
	require.NoError(t, err)
	require.EqualValues(t, 7, items[0].ID)
	require.EqualValues(t, 10, total)
	items, total, err = svc.ListResults(ctx, 0, 4, 3)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.EqualValues(t, 10, total, "the last page still reports the full total")
	require.EqualValues(t, 1, items[0].ID)
	items, total, err = svc.ListResults(ctx, 0, 5, 3)
	require.NoError(t, err)
	require.Empty(t, items)
	require.EqualValues(t, 10, total, "an empty page still reports the total")
	_, _, err = svc.ListResults(ctx, 0, 0, 0)
	require.NoError(t, err)
	require.Equal(t, 0, repo.listedOffset)
	require.Equal(t, 20, repo.listedLimit)
	_, _, err = svc.ListResults(ctx, 0, 2, 2000)
	require.NoError(t, err)
	require.Equal(t, 1000, repo.listedOffset)
	require.Equal(t, 1000, repo.listedLimit)

	_, err = svc.GetResult(ctx, 12345)
	require.ErrorIs(t, err, ErrPelicanGroupTestResultNotFound)
}

func TestPelicanGroupRouterRoutesThroughTheOpenAIScheduler(t *testing.T) {
	ctx := context.Background()
	accounts := []Account{
		{ID: 11, Name: "pool-a", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0},
		{ID: 12, Name: "pool-b", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 5},
	}
	newRouter := func(cache schedulerTestConcurrencyCache) *gatewayPelicanGroupRouter {
		cfg := &config.Config{}
		cfg.Gateway.Scheduling.LoadBatchEnabled = false
		concurrency := NewConcurrencyService(cache)
		openai := &OpenAIGatewayService{
			accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
			cache:              &schedulerTestGatewayCache{},
			cfg:                cfg,
			concurrencyService: concurrency,
		}
		return &gatewayPelicanGroupRouter{openai: openai, concurrency: concurrency, slotWait: 50 * time.Millisecond}
	}
	group := &Group{ID: 4, Platform: PlatformOpenAI, Status: StatusActive}
	t.Run("confirmation pins the same account without bypassing scheduler eligibility", func(t *testing.T) {
		router := newRouter(schedulerTestConcurrencyCache{})
		pinned := context.WithValue(ctx, pelicanDegradationTargetKey{}, int64(12))
		selected, err := router.route(pinned, group, "gpt-6-astra", nil)
		require.NoError(t, err)
		require.EqualValues(t, 12, selected.account.ID)
		selected.release()
		_, err = router.route(pinned, group, "gpt-6-astra", map[int64]struct{}{12: {}})
		require.Error(t, err, "an unavailable original account must not turn a retest into another account's test")
	})

	acquired, released := []int64{}, []int64{}
	router := newRouter(schedulerTestConcurrencyCache{acquiredIDs: &acquired, releasedIDs: &released})
	first, err := router.route(ctx, group, "gpt-6-astra", map[int64]struct{}{})
	require.NoError(t, err)
	require.Equal(t, int64(11), first.account.ID, "the scheduler's priority decides, not the test")
	require.Equal(t, "gpt-6-astra", first.model)
	require.Equal(t, []int64{11}, acquired, "the sample holds a concurrency slot like a user request")
	first.release()
	first.release()
	require.Equal(t, []int64{11}, released, "the slot is released exactly once")

	second, err := router.route(ctx, group, "gpt-6-astra", map[int64]struct{}{11: {}})
	require.NoError(t, err)
	require.Equal(t, int64(12), second.account.ID, "a failed account is excluded like in failover")
	second.release()

	_, err = router.route(ctx, group, "gpt-6-astra", map[int64]struct{}{11: {}, 12: {}})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)

	busy := newRouter(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{11: false, 12: false}})
	_, err = busy.route(ctx, group, "gpt-6-astra", map[int64]struct{}{})
	require.ErrorIs(t, err, errPelicanGroupAccountBusy, "a sample waits for a slot like a queued request, then gives up")
}

func TestPelicanGroupRouterPlatformsMirrorGatewayRoutes(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo} {
		require.True(t, isOpenAIResponsesGatewayPlatform(platform), platform)
	}
	for _, platform := range []string{PlatformAnthropic, PlatformGemini, PlatformAntigravity, PlatformComposite, ""} {
		require.False(t, isOpenAIResponsesGatewayPlatform(platform), platform)
	}
}
