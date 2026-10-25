package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKeyMappingBeforeGroupAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		path, body, target string
		status             int
	}{
		{"/v1/responses", `{"model":"alias"}`, "target", 200},
		{"/v1/chat/completions", `{"model":"alias"}`, "target", 200},
		{"/v1/messages", `{"model":"alias"}`, "target", 200},
		{"/v1/live", `{"session":{"model":"alias"}}`, "target", 200},
		{"/v1/responses", `{"model":"alias"}`, "blocked", 404},
		{"/v1/responses", `{"model":"alias","Model":"blocked"}`, "target", 404},
		{"/v1/responses", `{"model":"alias","model":"blocked"}`, "target", 404},
		{"/v1beta/models/alias:generateContent", `{}`, "target", 200},
		{"/v1beta/models/alias:generateContent", `{}`, "blocked", 404},
	} {
		t.Run(tc.path+tc.body+tc.target, func(t *testing.T) {
			key := &service.APIKey{ModelMapping: map[string]string{"alias": tc.target, "target": "blocked"}, Group: &service.Group{ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"target"}}}}
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set(string(ContextKeyAPIKey), key) })
			router.Use(APIKeyModelMapping(), GroupModelAllowlist())
			handler := func(c *gin.Context) {
				if c.Param("modelAction") != "" {
					require.Equal(t, "/target:generateContent", c.Param("modelAction"))
					require.Equal(t, "/v1beta/models/target:generateContent", c.Request.URL.Path)
				} else {
					body, err := io.ReadAll(c.Request.Body)
					require.NoError(t, err)
					require.Equal(t, "target", requestmodel.FromBodyForRoute(c.FullPath(), c.GetHeader("Content-Type"), body))
				}
				c.Status(http.StatusOK)
			}
			router.POST("/v1/responses", handler)
			router.POST("/v1/chat/completions", handler)
			router.POST("/v1/messages", handler)
			router.POST("/v1/live", handler)
			router.POST("/v1beta/models/*modelAction", handler)
			request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code, response.Body.String())
		})
	}
}
