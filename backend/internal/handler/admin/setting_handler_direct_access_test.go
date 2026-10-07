//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type directAccessHandlerRepo struct {
	service.SettingRepository
	values map[string]string
}

func (repo *directAccessHandlerRepo) GetValue(_ context.Context, key string) (string, error) {
	value, exists := repo.values[key]
	if !exists {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (repo *directAccessHandlerRepo) Set(_ context.Context, key, value string) error {
	repo.values[key] = value
	return nil
}

func TestDirectAccessSettingsHandler(test *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := service.NewSettingService(&directAccessHandlerRepo{values: map[string]string{}}, &config.Config{Server: config.ServerConfig{DirectAccessHost: "direct.easygpt.top"}})
	handler := &SettingHandler{settingService: settings}
	router := gin.New()
	router.GET("/settings", handler.GetDirectAccessSettings)
	router.PUT("/settings", handler.UpdateDirectAccessSettings)
	for _, scenario := range []struct {
		body   string
		status int
	}{
		{`{}`, 400}, {`{"entries":null}`, 400}, {`{"entries":[{"cidr":"bad"}]}`, 400},
		{`{"entries":[{"cidr":"47.239.86.227","note":"customer"}]}`, 200},
		{`{"entries":[]}`, 200},
	} {
		request := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(scenario.body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(test, scenario.status, recorder.Code, recorder.Body.String())
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/settings", nil))
	require.Equal(test, 200, recorder.Code)
	require.Contains(test, recorder.Body.String(), `"hostname":"direct.easygpt.top"`)
	require.Contains(test, recorder.Body.String(), `"entries":[]`)
}
