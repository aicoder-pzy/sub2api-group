package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

const CandyPrompt = `在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）
苹果味 桃子味 西瓜味
圆形 7 9 8
五角星形 7 6 4`
const PelicanDeliveryContract = "所有账号使用相同交付约定：直接返回独立 HTML，不使用 Markdown 代码块或外部依赖。只输出 HTML，不要解释。"

var pelicanHTMLPattern = regexp.MustCompile(`(?i)<(?:!doctype\s+html|html|svg)[\s>]`)
var candyAnswerPattern = regexp.MustCompile(`^21\s*(?:个)?[。.!！]?$`)

const (
	pelicanErrEmptyOutput  = "Model returned empty output"
	pelicanErrCaptureLimit = "Response exceeds 4 MiB capture limit"
	pelicanErrHistoryLimit = "Output exceeds 2 MiB history limit"
	pelicanErrMaxTokens    = "Model output hit max_tokens before finishing"
	pelicanErrRefused      = "Model refused the request"
)

func (s *AccountTestService) RunPelicanBackground(ctx context.Context, accountID int64, model string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
	if cfg == nil {
		return nil, fmt.Errorf("pelican configuration required")
	}
	started := time.Now()
	deferred, _ := ctx.Value(pelicanDeferAssessmentKey{}).(bool)
	ctx = context.WithValue(ctx, pelicanDeferAssessmentKey{}, true)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &pelicanRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	c, _ := gin.CreateTestContext(w)
	c.Request = (&http.Request{Header: make(http.Header)}).WithContext(ctx)
	err := s.TestPelicanAccountConnection(c, accountID, model, intelligenceTestPrompt(cfg), cfg.ReasoningEffort)
	output, message := parsePelicanOutput(w.Body.String())
	if w.overflow {
		output, message = "", pelicanErrCaptureLimit
	}
	if err != nil && message == "" {
		message = err.Error()
	}
	if message == "" {
		message = intelligenceTestOutputError(cfg, output)
	}
	if len(output) > 2<<20 {
		output, message = "", pelicanErrHistoryLimit
	}
	status := "success"
	if message != "" {
		status = "failed"
	}
	finished := time.Now()
	snapshot := *cfg
	snapshot.ModelID = model
	if status == "success" && cfg.QuestionKind != "candy" && !isBuiltinCandyPlan(cfg) && !deferred {
		output = s.pelicanAssessment.AssessOutput(ctx, output)
	}
	return &ScheduledTestResult{Status: status, ResponseText: output, ErrorMessage: message, LatencyMs: finished.Sub(started).Milliseconds(), StartedAt: started, FinishedAt: finished, PelicanConfig: &snapshot}, nil
}

func (s *ScheduledTestRunnerService) runPelicanPlan(ctx context.Context, plan *ScheduledTestPlan) {
	now := time.Now()
	next, err := computeNextRun(plan.CronExpression, now)
	if err != nil {
		return
	}
	until := now.Add(15 * time.Minute).Truncate(time.Microsecond)
	claimed, err := s.planRepo.ClaimPelican(ctx, plan, now, until, next)
	if err != nil || !claimed {
		return
	}
	runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	results := make([]*ScheduledTestResult, plan.PelicanConfig.ParallelCount)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(index int) { defer wg.Done(); results[index] = s.runPelicanSample(runCtx, plan) }(i)
	}
	wg.Wait()
	saveCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	succeeded := false
	for _, result := range results {
		succeeded = succeeded || result.Status == "success"
		if err := s.scheduledSvc.SaveResult(saveCtx, plan.ID, plan.MaxResults, result); err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "pelican plan=%d save failed: %v", plan.ID, err)
		}
	}
	if succeeded && plan.AutoRecover && !isBuiltinCandyPlan(plan.PelicanConfig) {
		s.tryRecoverAccount(saveCtx, plan.AccountID, plan.ID)
	}
	if err := s.planRepo.FinishPelican(saveCtx, plan.ID, until, time.Now()); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "pelican plan=%d finish failed: %v", plan.ID, err)
	}
}

func (s *ScheduledTestRunnerService) runPelicanSample(ctx context.Context, plan *ScheduledTestPlan) (result *ScheduledTestResult) {
	started := time.Now()
	failure := func(message string) *ScheduledTestResult {
		return &ScheduledTestResult{Status: "failed", ErrorMessage: message, StartedAt: started, FinishedAt: time.Now(), PelicanConfig: plan.PelicanConfig}
	}
	defer func() {
		if recover() != nil {
			result = failure("scheduled_test_panic: background sample failed")
		}
	}()
	var err error
	result, err = s.accountTestSvc.RunPelicanBackground(ctx, plan.AccountID, plan.ModelID, plan.PelicanConfig)
	if err != nil {
		return failure(err.Error())
	}
	if result == nil {
		return failure("scheduled_test_empty_result")
	}
	return result
}

type pelicanRecorder struct {
	*httptest.ResponseRecorder
	cancel   context.CancelFunc
	overflow bool
}

func (w *pelicanRecorder) Write(data []byte) (int, error) {
	if w.overflow || w.Body.Len()+len(data) > 4<<20 {
		w.overflow = true
		w.cancel()
		return 0, io.ErrShortWrite
	}
	return w.ResponseRecorder.Write(data)
}
func (w *pelicanRecorder) WriteString(data string) (int, error) { return w.Write([]byte(data)) }
func parsePelicanOutput(body string) (string, string) {
	output, message := parseTestSSEOutput(body)
	complete := false
	for _, line := range strings.Split(body, "\n") {
		data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:")
		if !ok {
			continue
		}
		var event TestEvent
		if json.Unmarshal([]byte(strings.TrimSpace(data)), &event) != nil {
			continue
		}
		if event.Type == "test_complete" {
			complete = event.Success
		}
	}
	if !complete && message == "" {
		message = "Generation stream ended before completion"
	}
	return output, message
}
func isBuiltinCandyPlan(cfg *PelicanTestConfig) bool {
	return cfg != nil && strings.TrimSpace(cfg.Prompt) == strings.TrimSpace(CandyPrompt)
}
func intelligenceTestPrompt(cfg *PelicanTestConfig) string {
	contract := PelicanDeliveryContract
	if cfg.QuestionKind == "candy" || isBuiltinCandyPlan(cfg) {
		contract = "只输出最终整数，不要解释。"
	}
	return cfg.Prompt + "\n\n" + contract
}
func intelligenceTestOutputError(cfg *PelicanTestConfig, output string) string {
	if strings.TrimSpace(output) == "" {
		return pelicanErrEmptyOutput
	}
	if isBuiltinCandyPlan(cfg) && !candyAnswerPattern.MatchString(strings.TrimSpace(output)) {
		return "answer_mismatch: expected 21"
	}
	if cfg.QuestionKind != "candy" && !isBuiltinCandyPlan(cfg) && !pelicanHTMLPattern.MatchString(output) {
		return "Model did not return HTML or SVG"
	}
	return ""
}
