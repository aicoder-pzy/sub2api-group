package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *APIKeyHandler) PromptProfiles(c *gin.Context) {
	response.Success(c, service.APIKeyPromptProfiles())
}
