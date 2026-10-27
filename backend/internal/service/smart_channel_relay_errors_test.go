//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSmartChannelModelMissingHasBoundedModelOnlyCooldown(t *testing.T) {
	for _, tc := range []struct {
		name                string
		smart, pool, custom bool
		want                time.Duration
	}{
		{"smart relay", true, false, false, 30 * time.Second},
		{"smart pool", true, true, false, 30 * time.Second},
		{"explicit operator policy", true, true, true, upstreamModelNotFoundCooldown},
		{"legacy pool", false, true, false, 0},
		{"legacy account", false, false, false, upstreamModelNotFoundCooldown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &modelNotFoundAccountRepoStub{}
			svc := &RateLimitService{accountRepo: repo}
			account := &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"pool_mode": tc.pool, "custom_error_codes_enabled": tc.custom,
					"custom_error_codes": []any{float64(http.StatusNotFound)},
					"model_mapping":      map[string]any{"public": "actual"}}}
			ctx := context.Background()
			if tc.smart {
				ctx = context.WithValue(ctx, ctxkey.Group, &Group{ID: 24, AccountSchedulingMode: AccountSchedulingModeSmartChannel})
			}
			handled := svc.HandleUpstreamModelNotFound(ctx, account, "public", 404, []byte(`{"error":{"code":"model_not_found","message":"model not found"}}`))
			require.Equal(t, tc.want > 0, handled)
			require.Zero(t, repo.tempCalls)
			if tc.want == 0 {
				require.Empty(t, repo.modelRateLimitCalls)
				return
			}
			require.Len(t, repo.modelRateLimitCalls, 1)
			require.Equal(t, "actual", repo.modelRateLimitCalls[0].scope)
			require.WithinDuration(t, time.Now().Add(tc.want), repo.modelRateLimitCalls[0].resetAt, 2*time.Second)
		})
	}
}

func TestRelayCapacityDoesNotOverrideAuthoritativeErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		capacity bool
	}{
		{"model capacity", 429, `{"error":{"message":"Selected model is at capacity"}}`, true},
		{"relay capacity code", 503, `{"error":{"code":"upstream_capacity_exhausted"}}`, true},
		{"quota type", 429, `{"error":{"type":"insufficient_quota","message":"server is overloaded"}}`, false},
		{"quota code", 429, `{"error":{"code":"usage_limit_reached","message":"server is overloaded"}}`, false},
		{"quota reset", 429, `{"error":{"code":"server_is_overloaded","resets_at":9999999999}}`, false},
		{"credential status", 401, `{"error":{"code":"server_is_overloaded"}}`, false},
		{"policy status", 403, `{"error":{"code":"server_is_overloaded"}}`, false},
		{"safety frame", 503, `{"response":{"error":{"type":"permission_error","code":"server_is_overloaded"}}}`, false},
		{"echoed content", 503, `{"error":{"code":"upstream_error","message":"unavailable"},"input":"server is overloaded"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			err := newOpenAIUpstreamFailoverError(tc.status, nil, body, "", false)
			require.Equal(t, tc.capacity, err.RequestScopedTransient)
			require.Equal(t, tc.capacity, err.RetryableOnSameAccount)
			if tc.capacity {
				repo := &modelNotFoundAccountRepoStub{}
				svc := &RateLimitService{accountRepo: repo}
				require.False(t, svc.HandleUpstreamError(context.Background(), &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, tc.status, http.Header{}, body, "gpt-6.1-sol"))
				require.Empty(t, repo.modelRateLimitCalls)
				require.Zero(t, repo.tempCalls)
				gateway := &OpenAIGatewayService{rateLimitService: svc}
				require.False(t, gateway.handleOpenAIAccountUpstreamError(context.Background(), &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, tc.status, http.Header{}, body, "gpt-6.1-sol"))
				require.Empty(t, repo.modelRateLimitCalls)
				require.Zero(t, repo.tempCalls)
			}
		})
	}
}

func TestSmartChannelExplicitModelCooldownPolicyTakesPrecedence(t *testing.T) {
	for _, pool := range []bool{false, true} {
		for _, gatewayPath := range []bool{false, true} {
			for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusServiceUnavailable} {
				t.Run(fmt.Sprintf("pool=%t/gateway=%t/status=%d", pool, gatewayPath, status), func(t *testing.T) {
					repo := &modelNotFoundAccountRepoStub{}
					svc := &RateLimitService{accountRepo: repo}
					gateway := &OpenAIGatewayService{rateLimitService: svc}
					account := &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
						Credentials: map[string]any{"pool_mode": pool, "temp_unschedulable_enabled": true,
							"temp_unschedulable_rules": []any{map[string]any{
								"error_code": float64(status), "keywords": []any{"model not found", "server is overloaded"},
								"duration_minutes": float64(7), "scope": "model",
							}}}}
					body := []byte(`{"error":{"code":"model_not_found","message":"model not found"}}`)
					if status != http.StatusNotFound {
						body = []byte(`{"error":{"message":"server is overloaded"}}`)
					}
					ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 24, AccountSchedulingMode: AccountSchedulingModeSmartChannel})
					if gatewayPath {
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
						failover := gateway.failoverOpenAIUpstreamHTTPError(ctx, c, account, &http.Response{StatusCode: status, Header: http.Header{}}, body, "", "gpt-6.1-sol")
						require.NotNil(t, failover)
						require.False(t, failover.RetryableOnSameAccount)
					} else {
						require.True(t, svc.HandleUpstreamError(ctx, account, status, nil, body, "gpt-6.1-sol"))
					}
					require.Zero(t, repo.tempCalls)
					require.Len(t, repo.modelRateLimitCalls, 1)
					require.Equal(t, "gpt-6.1-sol", repo.modelRateLimitCalls[0].scope)
					require.WithinDuration(t, time.Now().Add(7*time.Minute), repo.modelRateLimitCalls[0].resetAt, 2*time.Second)
					require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
				})
			}
		}
	}
}
