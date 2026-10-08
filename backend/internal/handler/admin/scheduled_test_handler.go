package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ScheduledTestHandler handles admin scheduled-test-plan management.
type ScheduledTestHandler struct {
	scheduledTestSvc *service.ScheduledTestService
}

// NewScheduledTestHandler creates a new ScheduledTestHandler.
func NewScheduledTestHandler(scheduledTestSvc *service.ScheduledTestService) *ScheduledTestHandler {
	return &ScheduledTestHandler{scheduledTestSvc: scheduledTestSvc}
}

type createScheduledTestPlanRequest struct {
	PelicanConfig   *service.PelicanTestConfig `json:"pelican_config"`
	AccountID       int64                      `json:"account_id" binding:"required"`
	ModelID         string                     `json:"model_id"`
	TestPrompt      string                     `json:"test_prompt"`
	ExpectedAnswer  string                     `json:"expected_answer"`
	ReasoningEffort string                     `json:"reasoning_effort"`
	JudgeGroupID    int64                      `json:"judge_group_id"`
	JudgeModelID    string                     `json:"judge_model_id"`
	JudgePrompt     string                     `json:"judge_prompt"`
	CronExpression  string                     `json:"cron_expression" binding:"required"`
	Enabled         *bool                      `json:"enabled"`
	MaxResults      int                        `json:"max_results"`
	AutoRecover     *bool                      `json:"auto_recover"`
}

type updateScheduledTestPlanRequest struct {
	PelicanConfig   *service.PelicanTestConfig `json:"pelican_config"`
	ModelID         string                     `json:"model_id"`
	TestPrompt      *string                    `json:"test_prompt"`
	ExpectedAnswer  *string                    `json:"expected_answer"`
	ReasoningEffort *string                    `json:"reasoning_effort"`
	JudgeGroupID    *int64                     `json:"judge_group_id"`
	JudgeModelID    *string                    `json:"judge_model_id"`
	JudgePrompt     *string                    `json:"judge_prompt"`
	CronExpression  string                     `json:"cron_expression"`
	Enabled         *bool                      `json:"enabled"`
	MaxResults      int                        `json:"max_results"`
	AutoRecover     *bool                      `json:"auto_recover"`
}

// ListByAccount GET /admin/accounts/:id/scheduled-test-plans
func (h *ScheduledTestHandler) ListByAccount(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid account id")
		return
	}

	plans, err := h.scheduledTestSvc.ListPlansByAccount(c.Request.Context(), accountID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, plans)
}

// Create POST /admin/scheduled-test-plans
func (h *ScheduledTestHandler) Create(c *gin.Context) {
	var req createScheduledTestPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	plan := &service.ScheduledTestPlan{
		PelicanConfig:   req.PelicanConfig,
		AccountID:       req.AccountID,
		ModelID:         req.ModelID,
		TestPrompt:      req.TestPrompt,
		ExpectedAnswer:  req.ExpectedAnswer,
		ReasoningEffort: req.ReasoningEffort,
		JudgeGroupID:    req.JudgeGroupID,
		JudgeModelID:    req.JudgeModelID,
		JudgePrompt:     req.JudgePrompt,
		CronExpression:  req.CronExpression,
		Enabled:         true,
		MaxResults:      req.MaxResults,
	}
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}
	if req.AutoRecover != nil {
		plan.AutoRecover = *req.AutoRecover
	}

	created, err := h.scheduledTestSvc.CreatePlan(c.Request.Context(), plan)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, created)
}

// Update PUT /admin/scheduled-test-plans/:id
func (h *ScheduledTestHandler) Update(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	existing, err := h.scheduledTestSvc.GetPlan(c.Request.Context(), planID)
	if err != nil {
		response.NotFound(c, "plan not found")
		return
	}

	var req updateScheduledTestPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.PelicanConfig != nil {
		existing.PelicanConfig = req.PelicanConfig
	}
	if req.ModelID != "" {
		existing.ModelID = req.ModelID
	}
	if req.TestPrompt != nil {
		existing.TestPrompt = *req.TestPrompt
	}
	if req.ExpectedAnswer != nil {
		existing.ExpectedAnswer = *req.ExpectedAnswer
	}
	if req.ReasoningEffort != nil {
		existing.ReasoningEffort = *req.ReasoningEffort
	}
	if req.JudgeGroupID != nil {
		existing.JudgeGroupID = *req.JudgeGroupID
	}
	if req.JudgeModelID != nil {
		existing.JudgeModelID = *req.JudgeModelID
	}
	if req.JudgePrompt != nil {
		existing.JudgePrompt = *req.JudgePrompt
	}
	if req.CronExpression != "" {
		existing.CronExpression = req.CronExpression
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.MaxResults > 0 {
		existing.MaxResults = req.MaxResults
	}
	if req.AutoRecover != nil {
		existing.AutoRecover = *req.AutoRecover
	}

	updated, err := h.scheduledTestSvc.UpdatePlan(c.Request.Context(), existing)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, updated)
}

// Delete DELETE /admin/scheduled-test-plans/:id
func (h *ScheduledTestHandler) Delete(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	if err := h.scheduledTestSvc.DeletePlan(c.Request.Context(), planID); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// ListResults GET /admin/scheduled-test-plans/:id/results
func (h *ScheduledTestHandler) ListResults(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}

	results, err := h.scheduledTestSvc.ListResults(c.Request.Context(), planID, limit, c.Query("include_content") != "false")
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, results)
}
