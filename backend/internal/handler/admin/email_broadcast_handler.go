package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) SetEmailBroadcastService(s *service.EmailBroadcastService) {
	h.emailBroadcastService = s
}

func bindEmailBroadcast(c *gin.Context, value any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	if err := c.ShouldBindJSON(value); err != nil {
		response.BadRequest(c, "Invalid email broadcast request (maximum 2 MiB)")
		return false
	}
	return true
}

func emailBroadcastPage(c *gin.Context) int {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 || page > 100000 {
		return 1
	}
	return page
}

func (h *SettingHandler) PreviewEmailBroadcast(c *gin.Context) {
	var input struct {
		Recipients string `json:"recipients"`
	}
	if !bindEmailBroadcast(c, &input) {
		return
	}
	preview, err := service.ParseEmailBroadcastRecipients(input.Recipients)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, preview)
}

func (h *SettingHandler) CreateEmailBroadcast(c *gin.Context) {
	var input service.EmailBroadcastInput
	if !bindEmailBroadcast(c, &input) {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Authentication required")
		return
	}
	item, err := h.emailBroadcastService.Create(c.Request.Context(), subject.UserID, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *SettingHandler) TestEmailBroadcast(c *gin.Context) {
	var input struct {
		Recipient      string `json:"recipient"`
		Subject        string `json:"subject"`
		Content        string `json:"content"`
		ContentFormat  string `json:"content_format"`
		UnsubscribeURL string `json:"unsubscribe_url"`
	}
	if !bindEmailBroadcast(c, &input) {
		return
	}
	if err := h.emailBroadcastService.TestWithFormat(c.Request.Context(), input.Recipient, input.Subject, input.Content, input.ContentFormat, input.UnsubscribeURL); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"sent": true})
}

func (h *SettingHandler) ListEmailBroadcasts(c *gin.Context) {
	items, err := h.emailBroadcastService.List(c.Request.Context(), emailBroadcastPage(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *SettingHandler) GetEmailBroadcast(c *gin.Context) {
	item, err := h.emailBroadcastService.Detail(c.Request.Context(), c.Param("id"), c.Query("status"), emailBroadcastPage(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *SettingHandler) CancelEmailBroadcast(c *gin.Context) {
	if err := h.emailBroadcastService.Cancel(c.Request.Context(), c.Param("id")); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"cancelled": true})
}
