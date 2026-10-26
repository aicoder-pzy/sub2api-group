package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerIPRiskRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	risk := admin.Group("/settings/ip-risk")
	risk.GET("", h.Admin.Setting.GetIPRisk)
	risk.PUT("", h.Admin.Setting.SaveIPRisk)
	risk.GET("/events", h.Admin.Setting.GetIPRiskEvents)
	risk.DELETE("/events", h.Admin.Setting.ClearIPRiskEvents)
	risk.GET("/bans", h.Admin.Setting.GetIPRiskBans)
	risk.POST("/unban", h.Admin.Setting.UnbanIPRisk)
	risk.POST("/check", h.Admin.Setting.CheckIPRisk)
	risk.GET("/anonymous-bans", h.Admin.Setting.GetAnonymousAPIBans)
	risk.POST("/anonymous-unban", h.Admin.Setting.UnbanAnonymousAPI)
}
