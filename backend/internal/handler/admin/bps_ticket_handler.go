package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) bpsTicketService(c *gin.Context) *service.OpenAIGatewayService {
	s := h.accountTestService.BPSTicketGateway()
	if s == nil {
		response.Error(c, 503, "BPS/ticket service unavailable")
	}
	return s
}

func (h *AccountHandler) GetBPSTickets(c *gin.Context) {
	s := h.bpsTicketService(c)
	if s == nil {
		return
	}
	settings, err := s.GetBPSTicketSettings(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	accounts, err := s.BPSTicketAccounts(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"settings": settings, "accounts": accounts, "limitation": service.OpenAICodexStateProbeLimitation})
}

func (h *AccountHandler) SaveBPSTicketSettings(c *gin.Context) {
	s := h.bpsTicketService(c)
	if s == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var settings service.BPSTicketSettings
	if c.ShouldBindJSON(&settings) != nil {
		response.Error(c, 400, "Invalid BPS/ticket settings")
		return
	}
	if err := s.SaveBPSTicketSettings(c.Request.Context(), settings); err != nil {
		response.Error(c, 400, err.Error())
		return
	}
	response.Success(c, gin.H{"saved": true})
}

func bpsTicketAccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, 400, "Invalid account ID")
		return 0, false
	}
	return id, true
}

func (h *AccountHandler) SaveBPSTicketAccount(c *gin.Context) {
	s := h.bpsTicketService(c)
	if s == nil {
		return
	}
	id, ok := bpsTicketAccountID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var cfg service.BPSTicketAccountConfig
	if c.ShouldBindJSON(&cfg) != nil {
		response.Error(c, 400, "Invalid account settings")
		return
	}
	if err := s.SaveBPSTicketAccount(c.Request.Context(), id, cfg); err != nil {
		response.Error(c, 400, err.Error())
		return
	}
	response.Success(c, gin.H{"saved": true})
}

func (h *AccountHandler) ProbeBPSTicket(c *gin.Context)   { h.runBPSTicket(c, false) }
func (h *AccountHandler) HarvestBPSTicket(c *gin.Context) { h.runBPSTicket(c, true) }

func (h *AccountHandler) runBPSTicket(c *gin.Context, harvest bool) {
	s := h.bpsTicketService(c)
	if s == nil {
		return
	}
	id, ok := bpsTicketAccountID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		Model string `json:"model" binding:"required,max=128"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.Error(c, 400, "Select a model")
		return
	}
	var result any
	var err error
	if harvest {
		result, err = s.HarvestBPSTicketAccount(c.Request.Context(), id, req.Model)
	} else {
		result, err = s.ProbeBPSTicketAccount(c.Request.Context(), id, req.Model)
	}
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}
