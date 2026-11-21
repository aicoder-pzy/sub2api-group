package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) GetUpstreamAccountPool(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	data, err := h.upstreamBillingProbe.AccountPoolDisplay(c.Request.Context(), c.Param("site_id"), true)
	if !response.ErrorFrom(c, err) {
		if c.Query("page") != "" {
			page, size := response.ParsePagination(c)
			data.Paginate(page, size, c.Query("pool_id"), c.Query("platform"))
		}
		response.Success(c, data)
	}
}

func (h *AccountHandler) RefreshUpstreamAccountPool(c *gin.Context) {
	site, err := h.loadUpstreamSite(c)
	if response.ErrorFrom(c, err) {
		return
	}
	err = h.upstreamBillingProbe.StartAccountPoolRefresh(site)
	if !response.ErrorFrom(c, err) {
		response.Accepted(c, gin.H{"refreshing": true})
	}
}

func (h *AccountHandler) RenameUpstreamAccountPool(c *gin.Context) {
	var input struct {
		Name *string `json:"name"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
	if c.ShouldBindJSON(&input) != nil || input.Name == nil {
		response.BadRequest(c, "Display name is required")
		return
	}
	site, err := h.loadUpstreamSite(c)
	if response.ErrorFrom(c, err) {
		return
	}
	err = h.upstreamBillingProbe.RenameAccountPool(c.Request.Context(), site, c.Param("pool_id"), *input.Name)
	if !response.ErrorFrom(c, err) {
		h.GetUpstreamAccountPool(c)
	}
}

func (h *AccountHandler) ExportUpstreamAccountPool(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	siteID, token := os.Getenv("SUB2_ACCOUNT_POOL_SITE_ID"), os.Getenv("SUB2_ACCOUNT_POOL_EXPORT_TOKEN")
	if siteID == "" || len(token) < 32 {
		response.NotFound(c, "Account pool export is disabled")
		return
	}
	if !validAccountPoolBearer(c, token) {
		response.Unauthorized(c, "Invalid account pool export credential")
		return
	}
	data, err := h.upstreamBillingProbe.AccountPoolDisplay(c.Request.Context(), siteID, false)
	if !response.ErrorFrom(c, err) {
		response.Success(c, data)
	}
}

func validAccountPoolBearer(c *gin.Context, token string) bool {
	want := sha256.Sum256([]byte("Bearer " + token))
	got := sha256.Sum256([]byte(c.GetHeader("Authorization")))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

func (h *AccountHandler) GetAccountPoolStatusConfig(c *gin.Context) {
	site, err := h.loadUpstreamSite(c)
	if response.ErrorFrom(c, err) {
		return
	}
	cfg, err := h.upstreamBillingProbe.AccountPoolStatusConfig(c.Request.Context(), site)
	if !response.ErrorFrom(c, err) {
		response.Success(c, cfg)
	}
}

func (h *AccountHandler) SaveAccountPoolStatusConfig(c *gin.Context) {
	var input struct {
		Config *service.AccountPoolStatusConfig `json:"config"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 24<<10)
	if c.ShouldBindJSON(&input) != nil || input.Config == nil {
		response.BadRequest(c, "Invalid account pool status configuration")
		return
	}
	site, err := h.loadUpstreamSite(c)
	if response.ErrorFrom(c, err) {
		return
	}
	cfg, err := h.upstreamBillingProbe.SaveAccountPoolStatusConfig(c.Request.Context(), site, *input.Config)
	if !response.ErrorFrom(c, err) {
		response.Success(c, cfg)
	}
}

func (h *AccountHandler) ExportAccountPoolStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	digest, enabled, err := h.poolStatusCredential(c)
	if response.ErrorFrom(c, err) {
		return
	}
	if !enabled {
		response.NotFound(c, "Account pool status export is disabled")
		return
	}
	got := sha256.Sum256([]byte(c.GetHeader("Authorization")))
	if subtle.ConstantTimeCompare(digest, got[:]) != 1 {
		response.Unauthorized(c, "Invalid account pool status credential")
		return
	}
	h.PreviewAccountPoolStatus(c)
}

func (h *AccountHandler) poolStatusCredential(c *gin.Context) ([]byte, bool, error) {
	stored, err := h.upstreamBillingProbe.AccountPoolStatusCredential(c.Request.Context())
	if err != nil || stored == "disabled" {
		return nil, false, err
	}
	if stored != "" {
		digest, err := hex.DecodeString(stored)
		return digest, err == nil && len(digest) == sha256.Size, nil
	}
	token := os.Getenv("SUB2_ACCOUNT_POOL_STATUS_TOKEN")
	if len(token) < 32 || token == os.Getenv("SUB2_ACCOUNT_POOL_EXPORT_TOKEN") || token == os.Getenv("SUB2_ACCOUNT_POOLS_EXPORT_TOKEN") {
		return nil, false, nil
	}
	digest := sha256.Sum256([]byte("Bearer " + token))
	return digest[:], true, nil
}

func (h *AccountHandler) GetAccountPoolStatusExport(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	_, enabled, err := h.poolStatusCredential(c)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"enabled": enabled})
	}
}

func (h *AccountHandler) IssueAccountPoolStatusToken(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var secret [32]byte
	if _, err := rand.Read(secret[:]); response.ErrorFrom(c, err) {
		return
	}
	token := "pool_" + hex.EncodeToString(secret[:])
	digest := sha256.Sum256([]byte("Bearer " + token))
	err := h.upstreamBillingProbe.SetAccountPoolStatusCredential(c.Request.Context(), hex.EncodeToString(digest[:]))
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"enabled": true, "token": token})
	}
}

func (h *AccountHandler) DisableAccountPoolStatusExport(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	err := h.upstreamBillingProbe.SetAccountPoolStatusCredential(c.Request.Context(), "disabled")
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"enabled": false})
	}
}

func (h *AccountHandler) PreviewAccountPoolStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	page, size := response.ParsePagination(c)
	pools, err := h.upstreamBillingProbe.PublishedAccountPools(c.Request.Context(), page, size)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"pools": pools, "page": page, "page_size": size})
	}
}

func (h *AccountHandler) ExportGroupAccountPools(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	allow := strings.TrimSpace(os.Getenv("SUB2_ACCOUNT_POOLS_GROUP_IDS"))
	token := os.Getenv("SUB2_ACCOUNT_POOLS_EXPORT_TOKEN")
	if allow == "" || len(token) < 32 || token == os.Getenv("SUB2_ACCOUNT_POOL_EXPORT_TOKEN") {
		response.NotFound(c, "Group account pool export is disabled")
		return
	}
	if !validAccountPoolBearer(c, token) {
		response.Unauthorized(c, "Invalid account pool export credential")
		return
	}
	rawID := c.Param("group_id")
	groupID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || groupID <= 0 || strconv.FormatInt(groupID, 10) != rawID ||
		!slices.ContainsFunc(strings.Split(allow, ","), func(id string) bool { return strings.TrimSpace(id) == rawID }) {
		response.NotFound(c, "Group account pool not found")
		return
	}
	h.serveGroupAccountPools(c, groupID)
}

func (h *AccountHandler) PreviewGroupAccountPools(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.NotFound(c, "Group account pool not found")
		return
	}
	h.serveGroupAccountPools(c, groupID)
}

func (h *AccountHandler) serveGroupAccountPools(c *gin.Context, groupID int64) {
	page, size := response.ParsePagination(c)
	data, err := h.upstreamBillingProbe.ExportGroupAccountPools(c.Request.Context(), groupID, page, size)
	if !response.ErrorFrom(c, err) {
		response.Success(c, data)
	}
}
