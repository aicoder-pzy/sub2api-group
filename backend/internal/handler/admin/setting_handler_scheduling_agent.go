package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) SchedulingAgentConnection(c *gin.Context) {
	if adminaccess.RestrictedFromContext(c.Request.Context()) != nil {
		response.Error(c, http.StatusForbidden, "Full administrator access required")
		return
	}
	ctx := c.Request.Context()
	switch c.Request.Method {
	case http.MethodPut:
		var input service.SchedulingAgentConnection
		if c.ShouldBindJSON(&input) != nil {
			response.BadRequest(c, "Invalid connection settings")
			return
		}
		if err := h.settingService.SetSchedulingAgentConnection(ctx, input); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	case http.MethodPost:
		test, err := h.settingService.TestSchedulingAgentConnection(ctx)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, test)
		return
	}
	connection, err := h.settingService.GetSchedulingAgentConnection(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, connection)
}

func (h *SettingHandler) SchedulingAgentGroup(c *gin.Context) {
	if adminaccess.RestrictedFromContext(c.Request.Context()) != nil {
		response.Error(c, http.StatusForbidden, "Full administrator access required")
		return
	}
	ctx := c.Request.Context()
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid group ID")
		return
	}
	switch c.Request.Method {
	case http.MethodPut:
		var input service.SchedulingPolicy
		if c.ShouldBindJSON(&input) != nil {
			response.BadRequest(c, "Invalid scheduling policy")
			return
		}
		if err := h.settingService.SetSchedulingPolicy(ctx, id, input); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	case http.MethodPost:
		if _, err := h.settingService.RunSchedulingAgent(ctx, id); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	case http.MethodDelete:
		if err := h.settingService.RevokeSchedulingAgentRun(ctx, id); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	status, err := h.settingService.GetSchedulingAgentStatus(ctx, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}
