//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type qualityAccountRepo struct {
	AccountRepository
	accounts []Account
}

func (r *qualityAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			return &r.accounts[i], nil
		}
	}
	return nil, fmt.Errorf("account not found")
}

func (r *qualityAccountRepo) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return append([]Account(nil), r.accounts...), nil
}

type qualityGroupRepo struct {
	group *Group
}

func (r *qualityGroupRepo) GetByID(context.Context, int64) (*Group, error) {
	return r.group, nil
}

type qualitySlots struct {
	acquired []int64
	released int
	busy     bool
}

func (s *qualitySlots) AcquireAccountSlot(_ context.Context, id int64, _ int) (*AcquireResult, error) {
	if s.busy {
		return &AcquireResult{}, nil
	}
	s.acquired = append(s.acquired, id)
	return &AcquireResult{Acquired: true, ReleaseFunc: func() { s.released++ }}, nil
}

func qualityTestAccount(id int64) Account {
	return Account{
		ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://quality.example"},
		Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
	}
}

func qualityTestPlan() *ScheduledTestPlan {
	return &ScheduledTestPlan{
		ID: 10, AccountID: 1, ModelID: "gpt-6-astra", TestPrompt: "Test question only",
		ExpectedAnswer: "21", JudgeGroupID: 1, JudgeModelID: "gpt-4.1-mini",
		CronExpression: "*/30 * * * *", MaxResults: 100, Enabled: true,
	}
}

func qualityTextResponse(text, terminal string) *http.Response {
	delta, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": text})
	return newJSONResponse(http.StatusOK, "data: "+string(delta)+"\n\ndata: "+terminal+"\n\n")
}

func qualityTestJudge(accounts []Account, responses ...*http.Response) (*ScheduledTestQualityJudge, *queuedHTTPUpstream, *qualitySlots) {
	repo := &qualityAccountRepo{accounts: accounts}
	upstream := &queuedHTTPUpstream{responses: responses}
	slots := &qualitySlots{}
	tests := &AccountTestService{accountRepo: repo, httpUpstream: upstream,
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}}
	return &ScheduledTestQualityJudge{
		accounts: repo, groups: &qualityGroupRepo{group: &Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive}},
		slots: slots, tests: tests,
	}, upstream, slots
}

func TestScheduledQualityParser(t *testing.T) {
	for _, verdict := range []string{"correct", "incorrect", "unknown"} {
		judgment, err := parseScheduledTestJudgment(`{"verdict":"` + verdict + `","reason":"Equivalent wording"}`)
		require.NoError(t, err)
		require.Equal(t, verdict, judgment.Verdict)
	}
	for _, invalid := range []string{
		"```json\n{\"verdict\":\"correct\",\"reason\":\"ok\"}\n```",
		`{"verdict":"correct","verdict":"incorrect","reason":"ok"}`,
		`{"verdict":"correct","reason":"ok","extra":true}`,
		`{"verdict":"correct","reason":"ok"} {}`,
		`{"verdict":"correct","reason":null}`,
		`{"verdict":"correct"}`, `{"verdict":"yes","reason":"ok"}`, `[]`,
	} {
		_, err := parseScheduledTestJudgment(invalid)
		require.Error(t, err, invalid)
	}
}

func TestScheduledQualityIndependentJudgeAndRetries(t *testing.T) {
	parent := int64(1)
	shadow := qualityTestAccount(2)
	shadow.ParentAccountID = &parent
	judge, upstream, slots := qualityTestJudge(
		[]Account{qualityTestAccount(1), shadow, qualityTestAccount(3), qualityTestAccount(4)},
		qualityTextResponse("invalid JSON", `{"type":"response.completed"}`),
		qualityTextResponse(`{"verdict":"correct","reason":"21个 matches 21"}`, `{"type":"response.completed"}`),
	)
	plan := qualityTestPlan()
	judgment := judge.Judge(context.Background(), 1, plan, "21个。")
	require.Equal(t, "correct", judgment.Verdict)
	require.Equal(t, int64(4), judgment.AccountID)
	require.Equal(t, []int64{3, 4}, slots.acquired)
	require.Equal(t, 2, slots.released)
	body, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	prompt := gjson.GetBytes(body, "input.0.content.0.text").String()
	require.Contains(t, prompt, `"reference_answer":"21"`)
	require.Contains(t, prompt, `"candidate_answer":"21个。"`)
	require.NotContains(t, prompt, plan.TestPrompt)
}

func TestScheduledQualityUnknownDoesNotBecomeIncorrect(t *testing.T) {
	accounts := []Account{qualityTestAccount(1), qualityTestAccount(2), qualityTestAccount(3), qualityTestAccount(4), qualityTestAccount(5)}
	judge, upstream, slots := qualityTestJudge(accounts,
		newJSONResponse(http.StatusForbidden, "forbidden"),
		qualityTextResponse("invalid", `{"type":"response.completed"}`),
		qualityTextResponse("invalid", `{"type":"response.completed"}`),
		qualityTextResponse(`{"verdict":"incorrect","reason":"wrong"}`, `{"type":"response.completed"}`),
	)
	judgment := judge.Judge(context.Background(), 1, qualityTestPlan(), "21")
	require.Equal(t, "unknown", judgment.Verdict)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, 3, slots.released)
	result := &ScheduledTestResult{Status: "success"}
	applyScheduledTestJudgment(result, judgment)
	require.Equal(t, "unknown", result.Status)
	require.Empty(t, result.ErrorMessage)

	judge, upstream, slots = qualityTestJudge(accounts[:1])
	require.Equal(t, "judge_no_available_account", judge.Judge(context.Background(), 1, qualityTestPlan(), "21").Reason)
	require.Empty(t, upstream.requests)
	require.Empty(t, slots.acquired)
}

func TestScheduledQualityBackgroundPromptEffortAndCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, terminal string
		success        bool
	}{
		{"complete", `{"type":"response.completed","response":{"status":"completed"}}`, true},
		{"truncated", `{"type":"response.output_text.delta","delta":"partial"}`, false},
		{"incomplete", `{"type":"response.done","response":{"status":"incomplete"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			judge, upstream, slots := qualityTestJudge([]Account{qualityTestAccount(1)}, qualityTextResponse("21", tc.terminal))
			plan := qualityTestPlan()
			plan.ReasoningEffort = "xhigh"
			result, err := judge.Test(context.Background(), plan)
			require.NoError(t, err)
			require.Equal(t, tc.success, result.Status == "success")
			body, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.Equal(t, plan.TestPrompt, gjson.GetBytes(body, "input.0.content.0.text").String())
			require.Equal(t, "xhigh", gjson.GetBytes(body, "reasoning.effort").String())
			require.Equal(t, 1, slots.released)
		})
	}
	judge, _, _ := qualityTestJudge([]Account{qualityTestAccount(1)}, qualityTextResponse(strings.Repeat("x", 64001), `{"type":"response.completed"}`))
	result, err := judge.Test(context.Background(), qualityTestPlan())
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Empty(t, result.ResponseText)
	require.Contains(t, result.ErrorMessage, "64000")
}

type qualityResultRepo struct {
	ScheduledTestResultRepository
	saved *ScheduledTestResult
}

func (r *qualityResultRepo) Create(ctx context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.saved = result
	return result, nil
}

func (r *qualityResultRepo) PruneOldResults(context.Context, int64, int) error { return nil }

type qualityPlanRepo struct {
	ScheduledTestPlanRepository
	updated bool
}

func (r *qualityPlanRepo) UpdateAfterRun(context.Context, int64, time.Time, time.Time) error {
	r.updated = true
	return nil
}

func TestScheduledQualityRunnerPersistsMismatchAndUnknown(t *testing.T) {
	for _, tc := range []struct{ verdict, status, errorMessage string }{
		{"incorrect", "failed", "answer_mismatch"}, {"correct", "success", ""}, {"unknown", "unknown", ""},
	} {
		t.Run(tc.verdict, func(t *testing.T) {
			judge, _, slots := qualityTestJudge([]Account{qualityTestAccount(1), qualityTestAccount(2)},
				qualityTextResponse("21", `{"type":"response.completed"}`),
				qualityTextResponse(`{"verdict":"`+tc.verdict+`","reason":"comparison result"}`, `{"type":"response.completed"}`),
			)
			plans, results := &qualityPlanRepo{}, &qualityResultRepo{}
			runner := NewScheduledTestRunnerService(plans, NewScheduledTestService(plans, results, nil), judge.tests, judge, nil, nil)
			runner.runOnePlan(context.Background(), qualityTestPlan())
			require.NotNil(t, results.saved)
			require.Equal(t, tc.status, results.saved.Status)
			require.Equal(t, tc.verdict, results.saved.QualityVerdict)
			require.Equal(t, tc.errorMessage, results.saved.ErrorMessage)
			require.Equal(t, "21", results.saved.ResponseText)
			require.True(t, plans.updated)
			require.Equal(t, 2, slots.released)
		})
	}
	judge, upstream, slots := qualityTestJudge([]Account{qualityTestAccount(1)})
	slots.busy = true
	plans, results := &qualityPlanRepo{}, &qualityResultRepo{}
	runner := NewScheduledTestRunnerService(plans, NewScheduledTestService(plans, results, nil), judge.tests, judge, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner.runOnePlan(ctx, qualityTestPlan())
	require.Equal(t, "unknown", results.saved.QualityVerdict)
	require.Equal(t, "test_request_failed", results.saved.QualityReason)
	require.True(t, plans.updated)
	require.Empty(t, upstream.requests)
}

func TestScheduledQualityValidation(t *testing.T) {
	plan := qualityTestPlan()
	require.NoError(t, validateScheduledTestPlan(plan))
	require.NotEmpty(t, plan.JudgePrompt)
	for _, mutate := range []func(*ScheduledTestPlan){
		func(p *ScheduledTestPlan) { p.TestPrompt = "" },
		func(p *ScheduledTestPlan) { p.JudgeModelID = "gpt-image-2" },
		func(p *ScheduledTestPlan) { p.JudgeGroupID = 0 },
		func(p *ScheduledTestPlan) { p.ModelID = "gpt-*" },
		func(p *ScheduledTestPlan) { p.ReasoningEffort = "invalid" },
		func(p *ScheduledTestPlan) { p.ExpectedAnswer = "" },
	} {
		plan := qualityTestPlan()
		mutate(plan)
		require.Error(t, validateScheduledTestPlan(plan))
	}
	account := qualityTestAccount(1)
	account.Credentials["model_mapping"] = map[string]any{"text-alias": "gpt-image-2"}
	require.Error(t, validateScheduledQualityAccount(&account, "text-alias"))
	require.NoError(t, validateScheduledTestPlan(&ScheduledTestPlan{ModelID: "gpt-6-astra"}))
}
