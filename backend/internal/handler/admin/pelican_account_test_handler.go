package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) PelicanTest(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "invalid account id")
		return
	}
	var req struct {
		ModelID         string `json:"model_id" binding:"required,max=100"`
		Prompt          string `json:"prompt" binding:"required,max=32000"`
		ReasoningEffort string `json:"reasoning_effort" binding:"oneof=none minimal low medium high xhigh max ultra"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid test configuration")
		return
	}
	_ = h.accountTestService.TestPelicanAccountConnection(c, accountID, req.ModelID, req.Prompt, req.ReasoningEffort)
}
func (h *ScheduledTestHandler) GetResult(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || planID <= 0 {
		response.BadRequest(c, "invalid plan id")
		return
	}
	resultID, err := strconv.ParseInt(c.Param("resultId"), 10, 64)
	if err != nil || resultID <= 0 {
		response.BadRequest(c, "invalid result id")
		return
	}
	result, err := h.scheduledTestSvc.GetResult(c.Request.Context(), planID, resultID)
	if err != nil {
		response.NotFound(c, "result not found")
		return
	}
	c.JSON(http.StatusOK, result)
}
func (h *ScheduledTestHandler) ListPelicanHistory(c *gin.Context) {
	beforeID, err := strconv.ParseInt(c.DefaultQuery("before_id", "0"), 10, 64)
	if err != nil || beforeID < 0 {
		response.BadRequest(c, "invalid history cursor")
		return
	}
	page, err := h.scheduledTestSvc.ListPelicanHistory(c.Request.Context(), beforeID)
	if err != nil {
		response.InternalError(c, "failed to read test history")
		return
	}
	c.JSON(http.StatusOK, page)
}
