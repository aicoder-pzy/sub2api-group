package admin

import (
	"context"
	"math"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) ListUpstreamSites(c *gin.Context) {
	if h.upstreamBillingProbe == nil {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeUnavailable)
		return
	}
	result, err := h.upstreamBillingProbe.ListUpstreamSites(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AccountHandler) loadUpstreamSite(c *gin.Context) (*service.UpstreamSite, error) {
	if h.upstreamBillingProbe == nil {
		return nil, service.ErrUpstreamBillingProbeUnavailable
	}
	site, err := h.upstreamBillingProbe.GetUpstreamSite(c.Request.Context(), c.Param("site_id"))
	if err != nil {
		return nil, err
	}
	if site.ProxyID != nil {
		site.Proxy, err = h.adminService.GetProxy(c.Request.Context(), *site.ProxyID)
	}
	return site, err
}

func (h *AccountHandler) SaveUpstreamSite(c *gin.Context) {
	if h.upstreamBillingProbe == nil {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeUnavailable)
		return
	}
	var input service.UpstreamSite
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid site configuration")
		return
	}
	input.ID = c.Param("site_id")
	if input.ProxyID != nil {
		proxy, err := h.adminService.GetProxy(c.Request.Context(), *input.ProxyID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		input.Proxy = proxy
	}
	result, err := h.upstreamBillingProbe.SaveUpstreamSite(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AccountHandler) DeleteUpstreamSite(c *gin.Context) {
	if h.upstreamBillingProbe == nil {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeUnavailable)
		return
	}
	if err := h.upstreamBillingProbe.DeleteUpstreamSite(c.Request.Context(), c.Param("site_id")); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *AccountHandler) UpstreamSiteInventory(c *gin.Context) {
	site, err := h.loadUpstreamSite(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	inventory, err := h.upstreamBillingProbe.SyncUpstreamSite(c.Request.Context(), site)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, inventory)
}

func (h *AccountHandler) UpstreamSiteAccounts(c *gin.Context) {
	site, err := h.loadUpstreamSite(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	accounts, err := h.upstreamBillingProbe.UpstreamSiteAccounts(c.Request.Context(), site.ID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, accounts)
}

func (h *AccountHandler) LinkUpstreamSiteAccount(c *gin.Context) {
	var input struct {
		AccountID int64  `json:"account_id"`
		KeyID     string `json:"key_id"`
		Revision  int64  `json:"revision"`
		Unlink    bool   `json:"unlink"`
	}
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "Invalid account association")
		return
	}
	site, err := h.loadUpstreamSite(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if input.Revision != site.Revision {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeIdentityChanged)
		return
	}
	if err = h.upstreamBillingProbe.LinkUpstreamSiteAccount(c.Request.Context(), site, input.AccountID, input.KeyID, input.Unlink); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"saved": true})
}

func (h *AccountHandler) UpstreamSiteAccountModels(c *gin.Context) {
	var input struct {
		Revision int64                             `json:"revision"`
		Apply    bool                              `json:"apply"`
		Preview  *service.UpstreamSiteModelPreview `json:"preview"`
	}
	accountID, err := strconv.ParseInt(c.Param("account_id"), 10, 64)
	if err != nil || accountID <= 0 || c.ShouldBindJSON(&input) != nil || (input.Apply && input.Preview == nil) {
		response.BadRequest(c, "A model preview is required before applying changes")
		return
	}
	site, err := h.loadUpstreamSite(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if input.Revision != site.Revision {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeIdentityChanged)
		return
	}
	if !input.Apply {
		input.Preview = nil
	}
	preview, err := h.upstreamBillingProbe.UpstreamSiteAccountModels(c.Request.Context(), site, accountID, input.Preview)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, preview)
}

func (h *AccountHandler) UpstreamSiteModels(c *gin.Context) {
	site, err := h.loadUpstreamSite(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	models, err := h.upstreamBillingProbe.UpstreamSiteModels(c.Request.Context(), site, c.Param("key_id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, models)
}

func (h *AccountHandler) CreateUpstreamSiteKey(c *gin.Context) {
	var input struct {
		Name      string `json:"name"`
		GroupID   string `json:"group_id"`
		Revision  int64  `json:"revision"`
		Confirmed bool   `json:"confirmed"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || !input.Confirmed {
		response.BadRequest(c, "Explicit confirmation is required to create an upstream key")
		return
	}
	site, err := h.loadUpstreamSite(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if site.Revision != input.Revision {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeIdentityChanged)
		return
	}
	// No idempotency retry wrapper: an ambiguous upstream POST must be resolved
	// by refreshing inventory, never by replaying creation automatically.
	if err = h.upstreamBillingProbe.CreateUpstreamSiteKey(c.Request.Context(), site, input.Name, input.GroupID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"created": true})
}

func (h *AccountHandler) ImportUpstreamSiteKey(c *gin.Context) {
	var input struct {
		KeyID          string   `json:"key_id"`
		Revision       int64    `json:"revision"`
		Name           string   `json:"name"`
		Platform       string   `json:"platform"`
		GroupID        int64    `json:"group_id"`
		Concurrency    int      `json:"concurrency"`
		RateMultiplier float64  `json:"rate_multiplier"`
		Models         []string `json:"models"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.GroupID <= 0 || input.Concurrency < 1 || input.Concurrency > 1000 || input.RateMultiplier < 0 || input.RateMultiplier > 100 || math.IsNaN(input.RateMultiplier) || math.IsInf(input.RateMultiplier, 0) || len(input.Models) == 0 || len(input.Models) > 1000 {
		response.BadRequest(c, "Choose a local group, models, concurrency (1-1000) and purchase multiplier (0-100)")
		return
	}
	if input.Platform != service.PlatformOpenAI && input.Platform != service.PlatformAnthropic {
		response.BadRequest(c, "Choose OpenAI-compatible or Anthropic-compatible account protocol")
		return
	}
	if input.Name == "" || len(input.Name) > 100 {
		response.BadRequest(c, "Account name must contain 1-100 bytes")
		return
	}
	for _, model := range input.Models {
		if err := service.ValidateAPIKeyModelMapping(map[string]string{model: model}); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	site, err := h.loadUpstreamSite(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if site.Revision != input.Revision {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeIdentityChanged)
		return
	}
	result, err := executeAdminIdempotent(c, "admin.upstream-sites.import."+site.ID, input, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		credentials, extra, resolveErr := h.upstreamBillingProbe.UpstreamSiteImportCredentials(ctx, site, input.KeyID, input.Models)
		if resolveErr != nil {
			return nil, resolveErr
		}
		account, createErr := h.adminService.CreateAccount(ctx, &service.CreateAccountInput{Name: input.Name, Platform: input.Platform, Type: service.AccountTypeAPIKey, Credentials: credentials, Extra: extra, ProxyID: site.ProxyID, Concurrency: input.Concurrency, RateMultiplier: &input.RateMultiplier, GroupIDs: []int64{input.GroupID}, SkipDefaultGroupBind: true})
		if createErr != nil {
			return nil, createErr
		}
		return gin.H{"account_id": account.ID, "name": account.Name}, nil
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if result != nil && result.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	response.Success(c, result.Data)
}
