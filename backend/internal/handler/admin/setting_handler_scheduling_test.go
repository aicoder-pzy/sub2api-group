//go:build unit

package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFastestFailoverSettingsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := service.NewSettingService(&directAccessHandlerRepo{values: map[string]string{}}, &config.Config{})
	h := &SettingHandler{settingService: svc}
	router := gin.New()
	router.GET("/settings", h.GetFastestFailoverSettings)
	router.PUT("/settings", h.UpdateFastestFailoverSettings)
	for _, scenario := range []struct {
		body   string
		status int
	}{
		{`{}`, 400}, {`{"first_output_timeout_seconds":1.5}`, 400},
		{`{"first_output_timeout_seconds":1,"stream_idle_timeout_seconds":0,"model_cooldown_seconds":120}`, 400},
		{`{"first_output_timeout_seconds":15,"stream_idle_timeout_seconds":40,"model_cooldown_seconds":80}`, 200},
	} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(scenario.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		require.Equal(t, scenario.status, recorder.Code, recorder.Body.String())
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/settings", nil))
	require.Equal(t, 200, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"first_output_timeout_seconds":15`)
}
