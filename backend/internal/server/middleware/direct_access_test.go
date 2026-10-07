package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDirectAccessMiddleware(test *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := service.NewSettingService(&panelRateLimitStubRepo{}, &config.Config{Server: config.ServerConfig{DirectAccessHost: "direct.easygpt.top"}})
	_, err := settings.SetDirectAccessSettings(context.Background(), service.DirectAccessSettings{Entries: []service.DirectAccessEntry{{CIDR: "47.239.86.227"}}})
	require.NoError(test, err)
	router := gin.New()
	router.Use(DirectAccess(settings))
	router.NoRoute(func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	for _, scenario := range []struct {
		name, host, source, method string
		status                     int
	}{
		{"allowed", "direct.easygpt.top", "47.239.86.227", "GET", 204},
		{"host normalization", "DIRECT.EASYGPT.TOP.:443", "47.239.86.227", "GET", 204},
		{"outside allowlist", "direct.easygpt.top", "203.0.113.1", "GET", 403},
		{"missing proxy header", "direct.easygpt.top", "", "GET", 403},
		{"multiple sources", "direct.easygpt.top", "47.239.86.227, 203.0.113.1", "GET", 403},
		{"preflight denied", "direct.easygpt.top", "203.0.113.1", "OPTIONS", 403},
		{"original hostname unchanged", "easygpt.top", "203.0.113.1", "GET", 204},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			request := httptest.NewRequest(scenario.method, "https://"+scenario.host+"/v1/models", nil)
			request.Header.Set("X-Sub2-Direct-Client-IP", scenario.source)
			request.Header.Set("X-Forwarded-For", "47.239.86.227")
			request.Header.Set("CF-Connecting-IP", "47.239.86.227")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(test, scenario.status, recorder.Code)
		})
	}
}
