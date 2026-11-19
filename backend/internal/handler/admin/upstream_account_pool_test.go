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
