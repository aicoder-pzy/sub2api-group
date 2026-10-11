package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"strconv"
)

func (h *SettingHandler) GetFastestFailoverSettings(c *gin.Context) {
	settings, err := h.settingService.GetFastestFailoverSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) UpdateFastestFailoverSettings(c *gin.Context) {
	settings := service.DefaultFastestFailoverSettings()
	settings.FirstOutputTimeoutSeconds, settings.StreamIdleTimeoutSeconds, settings.ModelCooldownSeconds = 0, 0, 0
	if err := c.ShouldBindJSON(&settings); err != nil {
		response.BadRequest(c, "Invalid scheduling settings")
		return
	}
	if err := h.settingService.SetFastestFailoverSettings(c.Request.Context(), settings); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) GetModelAccountRouting(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Query("group_id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "A positive group_id is required")
		return
	}
	settings, err := h.settingService.GetModelAccountRouting(c.Request.Context(), groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) UpdateModelAccountRouting(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Query("group_id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "A positive group_id is required")
		return
	}
	var settings service.ModelAccountRoutingSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		response.BadRequest(c, "Invalid model account routing settings")
		return
	}
	if err := h.settingService.SetModelAccountRouting(c.Request.Context(), groupID, settings); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}
