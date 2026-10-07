package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) prismAdminService(c *gin.Context) *service.OpenAIGatewayService {
	s := h.accountTestService.BPSTicketGateway()
	if s == nil {
		response.Error(c, 503, "Prism administrator testing is unavailable")
	}
	return s
}

func (h *AccountHandler) GetPrismAdmin(c *gin.Context) {
	if s := h.prismAdminService(c); s != nil {
		data, err := s.GetPrismAdmin(c.Request.Context())
		if !response.ErrorFrom(c, err) {
			response.Success(c, data)
		}
	}
}

func (h *AccountHandler) SavePrismAdmin(c *gin.Context) {
	s := h.prismAdminService(c)
	if s == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input service.PrismAdminSettingsInput
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "Invalid Prism settings")
		return
	}
	data, err := s.SavePrismAdmin(c.Request.Context(), input)
	if !response.ErrorFrom(c, err) {
		response.Success(c, data)
	}
}

func (h *AccountHandler) CheckPrismAdmin(c *gin.Context) {
	if s := h.prismAdminService(c); s != nil {
		ready, err := s.CheckPrismAdmin(c.Request.Context())
		if !response.ErrorFrom(c, err) {
			response.Success(c, gin.H{"process_ready": ready})
		}
	}
}

func (h *AccountHandler) TestPrismAdmin(c *gin.Context) {
	s := h.prismAdminService(c)
	if s == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var input service.PrismAdminTestInput
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "Invalid Prism test request")
		return
	}
	data, err := s.TestPrismAdmin(c.Request.Context(), id, input)
	if !response.ErrorFrom(c, err) {
		response.Success(c, data)
	}
}
