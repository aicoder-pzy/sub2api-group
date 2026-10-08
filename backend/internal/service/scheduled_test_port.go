package service

import (
	"context"
	"time"
)

// ScheduledTestPlan represents a scheduled test plan domain model.
type PelicanTestConfig struct {
	QuestionKind    string `json:"question_kind,omitempty"`
	Prompt          string `json:"prompt"`
	ReasoningEffort string `json:"reasoning_effort"`
	ParallelCount   int    `json:"parallel_count"`
	ModelID         string `json:"model_id,omitempty"`
}

type ScheduledTestPlan struct {
	PelicanConfig   *PelicanTestConfig `json:"pelican_config,omitempty"`
	RunningUntil    *time.Time         `json:"running_until,omitempty"`
	ID              int64              `json:"id"`
	AccountID       int64              `json:"account_id"`
	ModelID         string             `json:"model_id"`
	TestPrompt      string             `json:"test_prompt"`
	ExpectedAnswer  string             `json:"expected_answer"`
	ReasoningEffort string             `json:"reasoning_effort"`
	JudgeGroupID    int64              `json:"judge_group_id"`
	JudgeModelID    string             `json:"judge_model_id"`
	JudgePrompt     string             `json:"judge_prompt"`
	CronExpression  string             `json:"cron_expression"`
	Enabled         bool               `json:"enabled"`
	MaxResults      int                `json:"max_results"`
	AutoRecover     bool               `json:"auto_recover"`
	LastRunAt       *time.Time         `json:"last_run_at"`
	NextRunAt       *time.Time         `json:"next_run_at"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

// ScheduledTestResult represents a single test execution result.
type ScheduledTestResult struct {
	PelicanConfig  *PelicanTestConfig `json:"pelican_config,omitempty"`
	ID             int64              `json:"id"`
	PlanID         int64              `json:"plan_id"`
	Status         string             `json:"status"`
	ResponseText   string             `json:"response_text"`
	ErrorMessage   string             `json:"error_message"`
	QualityVerdict string             `json:"quality_verdict,omitempty"`
	QualityReason  string             `json:"quality_reason,omitempty"`
	JudgeAccountID int64              `json:"judge_account_id,omitempty"`
	LatencyMs      int64              `json:"latency_ms"`
	StartedAt      time.Time          `json:"started_at"`
	FinishedAt     time.Time          `json:"finished_at"`
	CreatedAt      time.Time          `json:"created_at"`
}

// ScheduledTestPlanRepository defines the data access interface for test plans.
type ScheduledTestPlanRepository interface {
	ClaimPelican(ctx context.Context, plan *ScheduledTestPlan, now, until, next time.Time) (bool, error)
	FinishPelican(ctx context.Context, id int64, until, finished time.Time) error
	Create(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error)
	GetByID(ctx context.Context, id int64) (*ScheduledTestPlan, error)
	ListByAccountID(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error)
	ListDue(ctx context.Context, now time.Time) ([]*ScheduledTestPlan, error)
	Update(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error)
	Delete(ctx context.Context, id int64) error
	UpdateAfterRun(ctx context.Context, id int64, lastRunAt time.Time, nextRunAt time.Time) error
}

// ScheduledTestResultRepository defines the data access interface for test results.
type ScheduledTestResultRepository interface {
	GetResult(ctx context.Context, planID, resultID int64) (*ScheduledTestResult, error)
	ListPelicanHistory(ctx context.Context, beforeID int64, limit int) ([]*PelicanHistoryResult, error)
	PruneExpiredPelican(ctx context.Context, before time.Time) error
	Create(ctx context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error)
	ListByPlanID(ctx context.Context, planID int64, limit int, includeContent ...bool) ([]*ScheduledTestResult, error)
	PruneOldResults(ctx context.Context, planID int64, keepCount int) error
}

type PelicanHistoryResult struct {
	ScheduledTestResult
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
}
type PelicanHistoryPage struct {
	Items      []*PelicanHistoryResult `json:"items"`
	NextCursor int64                   `json:"next_cursor"`
}
