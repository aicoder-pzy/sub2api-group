package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
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

type poolStatusSettingRepo struct {
	service.SettingRepository
	value string
}

func (r *poolStatusSettingRepo) GetValue(context.Context, string) (string, error) {
	if r.value == "" {
		return "", service.ErrSettingNotFound
	}
	return r.value, nil
}

func (r *poolStatusSettingRepo) Set(_ context.Context, _, value string) error {
	r.value = value
	return nil
}

func TestPoolStatusManagedTokenCanRotateAndRevoke(t *testing.T) {
	t.Setenv("SUB2_ACCOUNT_POOL_STATUS_TOKEN", strings.Repeat("c", 64))
	t.Setenv("SUB2_ACCOUNT_POOL_EXPORT_TOKEN", strings.Repeat("a", 64))
	t.Setenv("SUB2_ACCOUNT_POOLS_EXPORT_TOKEN", strings.Repeat("b", 64))
	repo := &poolStatusSettingRepo{}
	svc := service.NewUpstreamBillingProbeService(nil, nil, service.NewSettingService(repo, nil))
	t.Cleanup(svc.Stop)
	h := &AccountHandler{upstreamBillingProbe: svc}
	router := gin.New()
	router.GET("/export", h.ExportAccountPoolStatus)
	router.GET("/preview", h.PreviewAccountPoolStatus)
	router.GET("/config", h.GetAccountPoolStatusExport)
	router.POST("/config", h.IssueAccountPoolStatusToken)
	router.DELETE("/config", h.DisableAccountPoolStatusExport)
	call := func(method, path, token string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, status, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		return w
	}
	issue := func() string {
		w := call(http.MethodPost, "/config", "", 200)
		var result struct{ Data struct{ Token string } }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.Regexp(t, `^pool_[a-f0-9]{64}$`, result.Data.Token)
		require.NotContains(t, repo.value, result.Data.Token)
		require.NotContains(t, call(http.MethodGet, "/config", "", 200).Body.String(), result.Data.Token)
		return result.Data.Token
	}
	first, second := issue(), issue()
	require.NotEqual(t, first, second)
	call(http.MethodGet, "/export", first, 401)
	call(http.MethodGet, "/export", strings.Repeat("c", 64), 401)
	call(http.MethodGet, "/export", second, 503) // Auth succeeded; no snapshot database in this test.
	call(http.MethodDelete, "/config", "", 200)
	call(http.MethodGet, "/export", second, 404)
	call(http.MethodGet, "/export", strings.Repeat("c", 64), 404)
	call(http.MethodGet, "/preview", "", 503) // Admin preview does not depend on export being open.
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
