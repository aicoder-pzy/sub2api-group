package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) GetUpstreamAccountPool(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	data, err := h.upstreamBillingProbe.AccountPoolDisplay(c.Request.Context(), c.Param("site_id"), true)
	if !response.ErrorFrom(c, err) {
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
	page, size := response.ParsePagination(c)
	data, err := h.upstreamBillingProbe.ExportGroupAccountPools(c.Request.Context(), groupID, page, size)
	if !response.ErrorFrom(c, err) {
		response.Success(c, data)
	}
}
