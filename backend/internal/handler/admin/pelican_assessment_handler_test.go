//go:build unit

package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPelicanAssessmentRejectsCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewPelicanAssessmentHandler(nil)
	router := gin.New()
	router.POST("/assess", handler.Start)
	router.POST("/lookup", handler.Lookup)
	for _, route := range []string{"/assess", "/lookup"} {
		for _, body := range []string{
			`{"html":"<html></html>","api_key":"FORBIDDEN"}`,
			`{"html":"<html></html>","base_url":"https://private.example"}`,
			`{"html":"<html></html>","credentials":{"access_token":"FORBIDDEN"}}`,
			`{"html":"<html></html>","model":"gpt-6-astra"}`,
			`{"html":"<html></html>"} {"api_key":"FORBIDDEN"}`,
		} {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest("POST", route, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer DO_NOT_FORWARD")
			request.Header.Set("Cookie", "private-session=DO_NOT_FORWARD")
			router.ServeHTTP(recorder, request)
			require.Equal(t, 400, recorder.Code, "credentials must be rejected before accessing the service")
			require.NotContains(t, recorder.Body.String(), "FORBIDDEN")
		}
	}
}
