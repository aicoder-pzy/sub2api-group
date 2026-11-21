package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPoolExportRequiresDedicatedBearer(t *testing.T) {
	h := &AccountHandler{}
	router := gin.New()
	router.GET("/pool", h.ExportUpstreamAccountPool)
	t.Setenv("SUB2_ACCOUNT_POOL_SITE_ID", "d5fe06a4-d164-4b71-a204-ce79a0f043a5")
	token := strings.Repeat("a", 64)
	for _, test := range []struct {
		token, auth string
		status      int
	}{
		{"", "", http.StatusNotFound},
		{token, "", http.StatusUnauthorized},
		{token, "Bearer wrong", http.StatusUnauthorized},
		// Correct auth proceeds to the unconfigured storage, never to broader admin access.
		{token, "Bearer " + token, http.StatusServiceUnavailable},
	} {
		t.Setenv("SUB2_ACCOUNT_POOL_EXPORT_TOKEN", test.token)
		r := httptest.NewRequest(http.MethodGet, "/pool", nil)
		r.Header.Set("Authorization", test.auth)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, test.status, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}

func TestGroupPoolExportRequiresDedicatedBearerAndAllowedGroup(t *testing.T) {
	h := &AccountHandler{}
	router := gin.New()
	router.GET("/account-pool", h.ExportUpstreamAccountPool)
	router.GET("/account-pools/:group_id", h.ExportGroupAccountPools)
	legacy, token := strings.Repeat("a", 64), strings.Repeat("b", 64)
	t.Setenv("SUB2_ACCOUNT_POOL_SITE_ID", "d5fe06a4-d164-4b71-a204-ce79a0f043a5")
	t.Setenv("SUB2_ACCOUNT_POOL_EXPORT_TOKEN", legacy)
	for _, tc := range []struct {
		allow, configured, supplied, path string
		status                            int
	}{
		{"", token, token, "/account-pools/24", 404},
		{"24", "short", "short", "/account-pools/24", 404},
		{"24", legacy, legacy, "/account-pools/24", 404},
		{"24", token, legacy, "/account-pools/24", 401},
		{"24", token, token, "/account-pool", 401},
		{"124, 42", token, token, "/account-pools/24", 404},
		{"24", token, token, "/account-pools/024", 404},
		{"24", token, token, "/account-pools/9223372036854775808", 404},
		{"124, 24", token, token, "/account-pools/24", 503},
	} {
		t.Setenv("SUB2_ACCOUNT_POOLS_GROUP_IDS", tc.allow)
		t.Setenv("SUB2_ACCOUNT_POOLS_EXPORT_TOKEN", tc.configured)
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+tc.supplied)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, tc.status, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}

func TestPublishedPoolStatusRequiresItsOwnBearer(t *testing.T) {
	h := &AccountHandler{}
	router := gin.New()
	router.GET("/pool-status", h.ExportAccountPoolStatus)
	legacy, group, token := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	t.Setenv("SUB2_ACCOUNT_POOL_EXPORT_TOKEN", legacy)
	t.Setenv("SUB2_ACCOUNT_POOLS_EXPORT_TOKEN", group)
	for _, tc := range []struct {
		configured, supplied string
		status               int
	}{
		{"", token, 404}, {legacy, legacy, 404}, {group, group, 404},
		{token, legacy, 401}, {token, group, 401}, {token, "", 401}, {token, token, 503},
	} {
		t.Setenv("SUB2_ACCOUNT_POOL_STATUS_TOKEN", tc.configured)
		r := httptest.NewRequest(http.MethodGet, "/pool-status", nil)
		r.Header.Set("Authorization", "Bearer "+tc.supplied)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, tc.status, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}
