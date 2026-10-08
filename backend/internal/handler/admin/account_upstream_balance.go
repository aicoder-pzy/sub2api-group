package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) GetUpstreamBalanceSettings(c *gin.Context) {
	if h.upstreamBillingProbe == nil {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeUnavailable)
		return
	}
	settings, err := h.upstreamBillingProbe.GetBalanceSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *AccountHandler) UpdateUpstreamBalanceSettings(c *gin.Context) {
	if h.upstreamBillingProbe == nil {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeUnavailable)
		return
	}
	var settings service.UpstreamBalanceSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		response.BadRequest(c, "Invalid balance settings")
		return
	}
	if err := h.upstreamBillingProbe.SetBalanceSettings(c.Request.Context(), settings); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *AccountHandler) GetUpstreamBalance(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if account.Type != service.AccountTypeAPIKey {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeAccountInvalid)
		return
	}
	state := service.DecodeUpstreamBalanceState(account.Extra)
	response.Success(c, state)
}

func (h *AccountHandler) UpdateUpstreamBalanceConfig(c *gin.Context) {
	if h.upstreamBillingProbe == nil {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeUnavailable)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	config := service.DefaultUpstreamBalanceConfig()
	if err := c.ShouldBindJSON(&config); err != nil {
		response.BadRequest(c, "Invalid balance configuration")
		return
	}
	state, err := h.upstreamBillingProbe.SetBalanceConfig(c.Request.Context(), id, config)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

func (h *AccountHandler) ProbeUpstreamBalance(c *gin.Context) {
	if h.upstreamBillingProbe == nil {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeUnavailable)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	state, err := h.upstreamBillingProbe.ProbeBalance(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, service.UpstreamBalanceProbeResult{AccountID: id, State: state})
}

func (h *AccountHandler) ProbeUpstreamBalanceBatch(c *gin.Context) {
	if h.upstreamBillingProbe == nil {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeUnavailable)
		return
	}
	var payload upstreamBillingProbeBatchRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.BadRequest(c, "Invalid account IDs")
		return
	}
	if len(payload.AccountIDs) == 0 || len(payload.AccountIDs) > service.UpstreamBillingProbeMaxBatchSize {
		response.BadRequest(c, "account_ids must contain between 1 and 20 items")
		return
	}
	seen := make(map[int64]bool, len(payload.AccountIDs))
	ids := make([]int64, 0, len(payload.AccountIDs))
	for _, id := range payload.AccountIDs {
		if id <= 0 {
			response.BadRequest(c, "account_ids must be positive")
			return
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	results := h.upstreamBillingProbe.ProbeBalances(c.Request.Context(), ids)
	response.Success(c, gin.H{"results": results})
}
