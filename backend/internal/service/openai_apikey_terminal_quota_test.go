//go:build unit

package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAPIKeyTerminalQuota_StreamReaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, quota := range []string{"usage_limit_reached", "insufficient_quota", "quota_exceeded"} {
			for _, field := range []string{"code", "type"} {
				for _, pool := range []string{"off", "default", "explicit429", "explicit502"} {
					t.Run(fmt.Sprintf("passthrough=%t/%s/%s/pool=%s", passthrough, quota, field, pool), func(t *testing.T) {
						repo := &oauth429RateLimitRepo{}
						svc := &OpenAIGatewayService{cfg: &config.Config{}, rateLimitService: &RateLimitService{accountRepo: repo}}
						account := &Account{ID: 71, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
							Credentials: map[string]any{"pool_mode": pool != "off"}}
						if pool == "explicit429" || pool == "explicit502" {
							status := 429
							if pool == "explicit502" {
								status = 502
							}
							account.Credentials["pool_mode_retry_status_codes"] = []any{float64(status)}
						}
						payload := fmt.Sprintf(`{"type":"response.failed","response":{"error":{%q:%q,"message":"Quota exhausted"}}}`, field, " "+strings.ToUpper(quota)+" ")
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(smartChannelTestContext())
						response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + payload + "\n\n"))}
						var err error
						if passthrough {
							_, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), response, c, account, time.Now(), "gpt-6.2-sol", "gpt-6.2-sol")
						} else {
							_, err = svc.handleStreamingResponse(c.Request.Context(), response, c, account, time.Now(), "gpt-6.2-sol", "gpt-6.2-sol")
						}
						var failure *UpstreamFailoverError
						require.ErrorAs(t, err, &failure)
						require.Equal(t, 429, failure.StatusCode)
						require.False(t, failure.RetryableOnSameAccount)
						require.True(t, failure.ShouldRetryNextAccount())
						require.True(t, failure.SameAccountRetryDeadline.IsZero())
						require.NotNil(t, failure.StreamFailedFrame)
						require.Equal(t, payload, string(failure.StreamFailedFrame.Payload))
						require.False(t, c.Writer.Written(), "a failure before output may use existing bounded failover")
						require.Zero(t, repo.setRateLimitedCalls, "a relay pool member is not the API key's quota")
						require.Zero(t, repo.setModelRateLimitCalls)
						require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
						require.Equal(t, StatusActive, account.Status)
					})
				}
			}
		}
	}
}

func TestOpenAIAPIKeyTerminalQuota_ClassificationBoundaries(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}}
	for _, payload := range []string{
		`{"type":"error","error":{"code":"insufficient_quota"}}`,
		`{"type":"response.failed","response":{"error":{"type":"insufficient_quota","code":"rate_limit_exceeded"}}}`,
		`{"type":"response.failed","response":{"error":{"type":"insufficient_quota","code":"credit_balance_exhausted"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"quota_exceeded","code":"server_error"}}}`,
		`{"type":"response.failed","response":{"error":{"type":"insufficient_quota","type":"server_error"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"quota_exceeded","\u0063ode":"server_error"}}}`,
		`{"type":"response.failed","response":{"error":{"type":"authentication_error"}},"error":{"code":"quota_exceeded"}}`,
		`{"type":"response.failed","response":{"error":{"code":"quota_exceeded"}}`,
	} {
		require.False(t, isOpenAIAPIKeyTerminalQuotaExhausted(account, []byte(payload)), payload)
	}
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"insufficient_quota"}}}`)
	require.False(t, isOpenAIAPIKeyTerminalQuotaExhausted(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, payload))
	require.Equal(t, 502, openAIStreamFailureStatus(payload, "Quota exhausted"), "do not change OAuth/default callers")
	for _, status := range []int{401, 403, 529} {
		body := []byte(fmt.Sprintf(`{"type":"response.failed","response":{"error":{"code":"quota_exceeded","status_code":%d}}}`, status))
		require.Equal(t, status, openAIStreamFailureStatus(body, "Quota exhausted", account))
		failure := (&OpenAIGatewayService{}).newOpenAIAccountFailoverErrorWithClassificationHeaders(account, status, nil, nil, body, "Quota exhausted", false, true)
		require.True(t, failure.RetryableOnSameAccount, "the new veto applies only to semantic quota 429")
	}
	for _, body := range []string{
		`{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded"}}}`,
		`{"type":"response.failed","response":{"error":{"type":"insufficient_quota","code":"rate_limit_exceeded"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"server_is_overloaded"}}}`,
	} {
		require.True(t, openAIStreamFailedEventRetryableOnSameAccount(account, []byte(body), "Try again later"))
	}
	failure := (&OpenAIGatewayService{}).newOpenAIAccountFailoverErrorWithClassificationHeaders(account, 429, nil, nil, payload, "Quota exhausted", false, true)
	require.False(t, failure.RetryableOnSameAccount, "final construction cannot re-enable futile retries")
	payload = []byte(`{"type":"response.failed","response":{"error":{"type":"invalid_request_error","code":"insufficient_quota"}}}`)
	require.Equal(t, 429, openAIStreamFailureStatus(payload, "Quota exhausted", account))
	require.True(t, openAIStreamFailedEventShouldFailover(payload, "Quota exhausted", account))
}

func TestOpenAIAPIKeyTerminalQuota_DeliveryBoundaries(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, terminal := range []string{"failed", "completed", "recovered"} {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, terminal), func(t *testing.T) {
				completed := terminal != "failed"
				account := &Account{ID: 71, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}}
				repo := &oauth429RateLimitRepo{}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, rateLimitService: &RateLimitService{accountRepo: repo}}
				body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\",\"output_index\":0}\n\n" +
					"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"quota_exceeded\"}}}\n\n"
				if completed {
					body = "data: {\"type\":\"error\",\"error\":{\"code\":\"quota_exceeded\"}}\n\n"
					if terminal == "recovered" {
						body += "data: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered\",\"output_index\":0}\n\n"
					}
					body += "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\n"
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
				var err error
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), response, c, account, time.Now(), "gpt-6.2-sol", "gpt-6.2-sol")
				} else {
					_, err = svc.handleStreamingResponse(c.Request.Context(), response, c, account, time.Now(), "gpt-6.2-sol", "gpt-6.2-sol")
				}
				if completed {
					require.NoError(t, err)
					require.Contains(t, rec.Body.String(), `"type":"response.completed"`)
					if terminal == "recovered" {
						require.Contains(t, rec.Body.String(), "recovered", "preserve output after recovery")
					}
					require.NotContains(t, rec.Body.String(), `"type":"error"`)
					require.NotContains(t, rec.Body.String(), `"type":"response.failed"`)
				} else {
					require.Error(t, err)
					var failure *UpstreamFailoverError
					require.False(t, errors.As(err, &failure), "never replay delivered output")
					require.Contains(t, rec.Body.String(), "partial")
				}
				require.Zero(t, repo.setRateLimitedCalls)
			})
		}
	}
}

func TestOpenAIAPIKeyTerminalQuota_WebSocketHealth(t *testing.T) {
	repo := &oauth429RateLimitRepo{}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
	account := &Account{ID: 71, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}}
	payload := []byte(`{"type":"response.failed","response":{"error":{"type":"insufficient_quota"}}}`)
	require.True(t, svc.handleOpenAIWSFailureAccountSideEffects(smartChannelTestContext(), account, "gpt-6.2-sol", nil, payload))
	require.Zero(t, repo.setRateLimitedCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIAPIKeyTerminalQuota_ExplicitModelPolicy(t *testing.T) {
	account := openAICapacityShedModelRuleAccount(AccountTypeAPIKey)
	rule := account.Credentials["temp_unschedulable_rules"].([]any)[0].(map[string]any)
	rule["error_code"] = 429
	rule["keywords"] = []any{"Quota exhausted"}
	repo := &modelNotFoundAccountRepoStub{}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"quota_exceeded","message":"Quota exhausted"}}}`)
	failure := svc.newOpenAIStreamFailoverErrorWithModel(nil, account, false, "", payload, "Quota exhausted", "gpt-6.2-sol")
	require.False(t, failure.RetryableOnSameAccount)
	require.Len(t, repo.modelRateLimitCalls, 1, "preserve administrator-configured model rules")
	require.Equal(t, "gpt-6.2-sol", repo.modelRateLimitCalls[0].scope)
	require.Zero(t, repo.tempCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}
