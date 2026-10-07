package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var scheduledTestCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ScheduledTestService provides CRUD operations for scheduled test plans and results.
type ScheduledTestService struct {
	planRepo   ScheduledTestPlanRepository
	resultRepo ScheduledTestResultRepository
	accounts   AccountRepository
}

// NewScheduledTestService creates a new ScheduledTestService.
func NewScheduledTestService(
	planRepo ScheduledTestPlanRepository,
	resultRepo ScheduledTestResultRepository,
	accounts AccountRepository,
) *ScheduledTestService {
	return &ScheduledTestService{
		planRepo:   planRepo,
		resultRepo: resultRepo,
		accounts:   accounts,
	}
}

// CreatePlan validates the cron expression, computes next_run_at, and persists the plan.
func (s *ScheduledTestService) CreatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := validateScheduledTestPlan(plan); err != nil {
		return nil, err
	}
	if err := s.validateQualityAccount(ctx, plan); err != nil {
		return nil, err
	}
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	if plan.MaxResults <= 0 {
		plan.MaxResults = 50
	}

	return s.planRepo.Create(ctx, plan)
}

// GetPlan retrieves a plan by ID.
func (s *ScheduledTestService) GetPlan(ctx context.Context, id int64) (*ScheduledTestPlan, error) {
	return s.planRepo.GetByID(ctx, id)
}

// ListPlansByAccount returns all plans for a given account.
func (s *ScheduledTestService) ListPlansByAccount(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error) {
	return s.planRepo.ListByAccountID(ctx, accountID)
}

// UpdatePlan validates cron and updates the plan.
func (s *ScheduledTestService) UpdatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := validateScheduledTestPlan(plan); err != nil {
		return nil, err
	}
	if err := s.validateQualityAccount(ctx, plan); err != nil {
		return nil, err
	}
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	return s.planRepo.Update(ctx, plan)
}

// DeletePlan removes a plan and its results (via CASCADE).
func (s *ScheduledTestService) DeletePlan(ctx context.Context, id int64) error {
	return s.planRepo.Delete(ctx, id)
}

// ListResults returns the most recent results for a plan.
func (s *ScheduledTestService) ListResults(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.resultRepo.ListByPlanID(ctx, planID, limit)
}

// SaveResult inserts a result and prunes old entries beyond maxResults.
func (s *ScheduledTestService) SaveResult(ctx context.Context, planID int64, maxResults int, result *ScheduledTestResult) error {
	result.PlanID = planID
	if _, err := s.resultRepo.Create(ctx, result); err != nil {
		return err
	}
	return s.resultRepo.PruneOldResults(ctx, planID, maxResults)
}

func computeNextRun(cronExpr string, from time.Time) (time.Time, error) {
	sched, err := scheduledTestCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from), nil
}

func validateScheduledTestPlan(plan *ScheduledTestPlan) error {
	if plan == nil {
		return fmt.Errorf("test plan is required")
	}
	plan.ModelID = strings.TrimSpace(plan.ModelID)
	plan.TestPrompt = strings.TrimSpace(plan.TestPrompt)
	plan.ExpectedAnswer = strings.TrimSpace(plan.ExpectedAnswer)
	plan.ReasoningEffort = strings.ToLower(strings.TrimSpace(plan.ReasoningEffort))
	plan.JudgeModelID = strings.TrimSpace(plan.JudgeModelID)
	plan.JudgePrompt = strings.TrimSpace(plan.JudgePrompt)
	if plan.TestPrompt != "" && len(plan.TestPrompt) > 32000 {
		return fmt.Errorf("test prompt must be at most 32000 bytes")
	}
	if plan.ExpectedAnswer == "" {
		if plan.JudgeGroupID != 0 || plan.JudgeModelID != "" || plan.JudgePrompt != "" {
			return fmt.Errorf("expected answer is required when judge settings are configured")
		}
	} else {
		if plan.TestPrompt == "" || plan.ModelID == "" || len(plan.ExpectedAnswer) > 32000 {
			return fmt.Errorf("quality test requires a prompt and an expected answer (maximum 32000 bytes)")
		}
		if plan.JudgeGroupID <= 0 || plan.JudgeModelID == "" || len(plan.JudgeModelID) > 100 {
			return fmt.Errorf("quality test requires a judge group and model")
		}
		if err := ValidateSchedulingProbeModel(plan.ModelID); err != nil {
			return err
		}
		if err := ValidateSchedulingProbeModel(plan.JudgeModelID); err != nil {
			return err
		}
		if plan.JudgePrompt == "" {
			plan.JudgePrompt = "Compare the candidate answer with the reference answer. Accept equivalent wording and formatting differences, but reject materially incorrect answers. Return only JSON with verdict (correct, incorrect, or unknown) and a brief reason."
		}
		if len(plan.JudgePrompt) > 8000 {
			return fmt.Errorf("judge prompt must be at most 8000 bytes")
		}
	}
	if len(plan.ModelID) > 100 {
		return fmt.Errorf("model ID must be at most 100 bytes")
	}
	if plan.ReasoningEffort != "" && plan.ReasoningEffort != "minimal" && plan.ReasoningEffort != "low" && plan.ReasoningEffort != "medium" && plan.ReasoningEffort != "high" && plan.ReasoningEffort != "xhigh" {
		return fmt.Errorf("reasoning effort must be minimal, low, medium, high, or xhigh")
	}
	return nil
}

func (s *ScheduledTestService) validateQualityAccount(ctx context.Context, plan *ScheduledTestPlan) error {
	if plan.ExpectedAnswer == "" || !plan.Enabled {
		return nil
	}
	if s == nil || s.accounts == nil {
		return fmt.Errorf("quality test account repository unavailable")
	}
	account, err := s.accounts.GetByID(ctx, plan.AccountID)
	if err != nil {
		return fmt.Errorf("quality test account unavailable: %w", err)
	}
	return validateScheduledQualityAccount(account, plan.ModelID)
}

func validateScheduledQualityAccount(account *Account, model string) error {
	if account == nil || !account.IsOpenAI() || account.IsSyntheticUITest() {
		return fmt.Errorf("quality checks currently support OpenAI text accounts only")
	}
	return ValidateSchedulingProbeModel(account.GetMappedModel(model))
}
