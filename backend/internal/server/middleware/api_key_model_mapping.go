package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/gin-gonic/gin"
)

// APIKeyModelMapping runs before group admission and every routing stage.
// Responses WebSocket frames are rewritten by the ingress hooks instead.
func APIKeyModelMapping() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := GetAPIKeyFromContext(c)
		if !ok || key == nil || len(key.ModelMapping) == 0 || c.Request == nil || isResponsesWebSocketRoute(c) {
			c.Next()
			return
		}
		for i := range c.Params {
			param := &c.Params[i]
			if param.Key != "model" && param.Key != "modelAction" {
				continue
			}
			model := strings.TrimPrefix(param.Value, "/")
			action := ""
			if param.Key == "modelAction" {
				if pos := strings.LastIndex(model, ":"); pos >= 0 {
					action, model = model[pos:], model[:pos]
				}
			}
			if mapped := key.ResolveModel(model); mapped != model {
				prefix := ""
				if strings.HasPrefix(param.Value, "/") {
					prefix = "/"
				}
				newValue := prefix + mapped + action
				c.Request.URL.Path = strings.TrimSuffix(c.Request.URL.Path, param.Value) + newValue
				c.Request.URL.RawPath = ""
				param.Value = newValue
			}
		}
		query := c.Request.URL.Query()
		if model := query.Get("model"); model != "" && key.ResolveModel(model) != model {
			query.Set("model", key.ResolveModel(model))
			c.Request.URL.RawQuery = query.Encode()
		}
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
			if err == nil {
				body, err = requestmodel.RewriteBody(c.GetHeader("Content-Type"), body, key.ModelMapping)
			}
			if err != nil {
				status := http.StatusBadRequest
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					status = http.StatusRequestEntityTooLarge
				}
				groupModelAllowlistErrorWriter(c)(c, status, "Failed to read model mapping request body")
				c.Abort()
				return
			}
			requestmodel.ResetRequestBody(c.Request, body)
		}
		c.Next()
	}
}
