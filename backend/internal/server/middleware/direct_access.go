package middleware

import (
	"net"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func DirectAccess(settings *service.SettingService) gin.HandlerFunc {
	hostname := settings.DirectAccessHost()
	return func(ctx *gin.Context) {
		host := ctx.Request.Host
		if parsedHost, _, err := net.SplitHostPort(host); err == nil {
			host = parsedHost
		}
		if hostname == "" || !strings.EqualFold(strings.TrimSuffix(host, "."), hostname) {
			ctx.Next()
			return
		}
		allowed, err := settings.IsDirectAccessAllowed(ctx.Request.Context(), ctx.GetHeader("X-Sub2-Direct-Client-IP"))
		if err != nil {
			AbortWithError(ctx, http.StatusServiceUnavailable, "DIRECT_ACCESS_UNAVAILABLE", "Direct access policy is unavailable")
			return
		}
		if !allowed {
			AbortWithError(ctx, http.StatusForbidden, "DIRECT_ACCESS_DENIED", "Your IP address is not allowed to use this direct endpoint")
			return
		}
		ctx.Next()
	}
}
