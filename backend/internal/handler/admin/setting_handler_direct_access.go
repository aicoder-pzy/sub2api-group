package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (handler *SettingHandler) GetDirectAccessSettings(ctx *gin.Context) {
	settings, err := handler.settingService.GetDirectAccessSettings(ctx.Request.Context())
	if err != nil {
		response.ErrorFrom(ctx, err)
		return
	}
	response.Success(ctx, gin.H{"hostname": handler.settingService.DirectAccessHost(), "entries": settings.Entries})
}

func (handler *SettingHandler) UpdateDirectAccessSettings(ctx *gin.Context) {
	var request struct {
		Entries *[]service.DirectAccessEntry `json:"entries" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		response.BadRequest(ctx, "Invalid request: "+err.Error())
		return
	}
	settings, err := handler.settingService.SetDirectAccessSettings(ctx.Request.Context(), service.DirectAccessSettings{Entries: *request.Entries})
	if err != nil {
		response.ErrorFrom(ctx, err)
		return
	}
	response.Success(ctx, gin.H{"hostname": handler.settingService.DirectAccessHost(), "entries": settings.Entries})
}
