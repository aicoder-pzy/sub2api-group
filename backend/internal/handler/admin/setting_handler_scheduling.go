package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
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
	settings, err := h.settingService.GetModelAccountRouting(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) UpdateModelAccountRouting(c *gin.Context) {
	var settings service.ModelAccountRoutingSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		response.BadRequest(c, "Invalid model account routing settings")
		return
	}
	if err := h.settingService.SetModelAccountRouting(c.Request.Context(), settings); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}
