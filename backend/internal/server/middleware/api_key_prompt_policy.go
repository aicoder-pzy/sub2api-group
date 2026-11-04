package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Inject once at ingress, after Key aliases and before protocol conversion and
// retries. Upstream/account retries therefore cannot accumulate instructions.
func APIKeyPromptPolicy() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := GetAPIKeyFromContext(c)
		if !ok || key == nil || len(key.PromptPolicy) == 0 || c.Request == nil || c.Request.Method != http.MethodPost || isResponsesWebSocketRoute(c) {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		if !strings.HasSuffix(path, "/responses") && !strings.HasSuffix(path, "/responses/compact") && !strings.HasSuffix(path, "/chat/completions") && !strings.HasSuffix(path, "/messages") && !strings.HasSuffix(path, "/messages/count_tokens") {
			c.Next()
			return
		}
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err == nil {
			body, err = service.ApplyAPIKeyPromptPolicy(body, path, key.PromptPolicy)
		}
		if err != nil {
			groupModelAllowlistErrorWriter(c)(c, http.StatusBadRequest, "Unable to apply this Key's prompt policy: check model, instructions, system and messages")
			c.Abort()
			return
		}
		requestmodel.ResetRequestBody(c.Request, body)
		c.Next()
	}
}
