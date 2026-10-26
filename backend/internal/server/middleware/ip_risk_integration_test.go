//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/iprisk"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type ipRiskIntegrationStore struct{ config iprisk.Config }

func (s *ipRiskIntegrationStore) Load(context.Context) (iprisk.Config, error) { return s.config, nil }
func (s *ipRiskIntegrationStore) Save(_ context.Context, c iprisk.Config) error {
	s.config = c
	return nil
}

type ipRiskLegacyCounter struct{ failures int }

func (s *ipRiskLegacyCounter) CheckInvalidAuthAbuse(string) (time.Duration, bool) {
	return time.Hour, true
}
func (s *ipRiskLegacyCounter) RecordInvalidAuthFailure(string) { s.failures++ }

func TestIPRiskAnonymousBansOnlyRejectProvenInvalidCredentials(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	config := iprisk.DefaultConfig()
	config.AnonymousAPI.Enabled = true
	config.AnonymousAPI.Threshold = 2
	store := &ipRiskIntegrationStore{config: config}
	manager := iprisk.New(store, client, nil, false)
	t.Cleanup(manager.Stop)
	legacy := &ipRiskLegacyCounter{}
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(manager.Middleware())
	router.GET("/test", func(c *gin.Context) {
		require.False(t, rejectInvalidAuthAbuse(c, legacy), "shared guard must not reject a valid credential before lookup")
		if c.GetHeader("Authorization") == "valid" {
			c.Set("api_key", true)
			c.Status(http.StatusOK)
			return
		}
		recordInvalidAuthFailure(c, legacy)
		AbortWithError(c, http.StatusUnauthorized, "INVALID_API_KEY", "Invalid API key")
	})
	request := func(valid bool) int {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "8.8.8.8:12345"
		req.Header.Set("X-Forwarded-For", "1.1.1.1")
		if valid {
			req.Header.Set("Authorization", "valid")
		}
		writer := httptest.NewRecorder()
		router.ServeHTTP(writer, req)
		return writer.Code
	}
	require.Equal(t, http.StatusUnauthorized, request(false))
	require.Equal(t, http.StatusTooManyRequests, request(false))
	require.Equal(t, http.StatusOK, request(true), "a shared NAT must not lock out valid users")
	require.Equal(t, http.StatusTooManyRequests, request(false))
	require.Zero(t, legacy.failures, "do not stack the old local ban")
	bans, err := manager.AnonymousBans(context.Background())
	require.NoError(t, err)
	require.Len(t, bans, 1)
	require.Equal(t, "8.8.8.8/32", bans[0].Network, "untrusted XFF must not choose the punished IP")
	config.AnonymousAPI.ResetOnSuccess = true
	require.NoError(t, manager.Save(context.Background(), config))
	require.Equal(t, http.StatusOK, request(true))
	require.Equal(t, http.StatusUnauthorized, request(false), "explicit reset-on-success clears only anonymous state")
}
