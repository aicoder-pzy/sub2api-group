package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPromptPolicyAfterAliasesAndGroupAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := &service.APIKey{ModelMapping: map[string]string{"alias": "target"}, PromptPolicy: map[string]string{"target": "custom:selected"}, Group: &service.Group{ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"target"}}}}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(ContextKeyAPIKey), key) })
	r.Use(APIKeyModelMapping(), GroupModelAllowlist(), APIKeyPromptPolicy())
	r.POST("/v1/responses", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, "target", gjson.GetBytes(body, "model").String())
		require.Equal(t, "selected\n\nclient", gjson.GetBytes(body, "instructions").String())
		c.Status(200)
	})
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"model":"alias","instructions":"client"}`, 200},
		{`{"model":"blocked","model":"alias","instructions":"client"}`, 404},
		{`{"model":"alias","Model":"blocked","instructions":"client"}`, 404},
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, tc.status, rec.Code, rec.Body.String())
	}
}
