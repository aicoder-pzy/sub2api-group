package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCachePreferredSurvivesSnapshotAndUpdates(t *testing.T) {
	for _, platform := range []string{
		service.PlatformOpenAI, service.PlatformGrok, service.PlatformAnthropic,
		service.PlatformGemini, service.PlatformAntigravity, service.PlatformKimi,
		service.PlatformZhipu, service.PlatformDeepseek, service.PlatformMiniMax, service.PlatformOpenCodeGo,
	} {
		t.Run(platform, func(t *testing.T) {
			ctx := context.Background()
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			cache := NewSchedulerCache(client)
			rate := 0.065
			account := service.Account{
				ID: 121, Platform: platform, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, Priority: 1, RateMultiplier: &rate,
				GroupIDs:    []int64{23, 24},
				Credentials: map[string]any{"model_mapping": map[string]any{"model-a": "upstream-a"}},
				Extra:       map[string]any{"scheduling_preferred": true, "unrelated_payload": "drop-me"},
			}
			for _, groupID := range account.GroupIDs {
				bucket := service.SchedulerBucket{GroupID: groupID, Platform: platform, Mode: service.SchedulerModeSingle}
				token, err := cache.CaptureBucketWriteToken(ctx, bucket)
				require.NoError(t, err)
				require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))
			}
			for _, preferred := range []any{true, false, nil, true} {
				if preferred == nil {
					delete(account.Extra, "scheduling_preferred")
				} else {
					account.Extra["scheduling_preferred"] = preferred
				}
				require.NoError(t, cache.SetAccount(ctx, &account))
				full, err := cache.GetAccount(ctx, account.ID)
				require.NoError(t, err)
				require.Equal(t, preferred, full.Extra["scheduling_preferred"])
				for _, groupID := range account.GroupIDs {
					bucket := service.SchedulerBucket{GroupID: groupID, Platform: platform, Mode: service.SchedulerModeSingle}
					candidates, hit, err := cache.GetSnapshot(ctx, bucket)
					require.NoError(t, err)
					require.True(t, hit)
					require.Len(t, candidates, 1)
					require.Equal(t, preferred, candidates[0].Extra["scheduling_preferred"])
					require.Equal(t, account.RateMultiplier, candidates[0].RateMultiplier)
					require.True(t, candidates[0].IsModelSupported("model-a"))
					require.False(t, candidates[0].IsModelSupported("unsupported-model"))
					require.NotContains(t, candidates[0].Extra, "unrelated_payload")
				}
			}
		})
	}
}

func TestFilterSchedulerCredentialsKeepsSubscriptionPlanType(t *testing.T) {
	filtered := filterSchedulerCredentials(map[string]any{
		"plan_type":     "plus",
		"access_token":  "secret-access-token",
		"refresh_token": "secret-refresh-token",
	})

	require.Equal(t, "plus", filtered["plan_type"])
	require.NotContains(t, filtered, "access_token")
	require.NotContains(t, filtered, "refresh_token")
}

func TestSchedulerMetadataAccountKeepsOpenAISubscriptionIdentity(t *testing.T) {
	account := service.Account{
		ID:       24,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"plan_type":    "plus",
			"access_token": "secret-access-token",
		},
	}

	metadata := buildSchedulerMetadataAccount(account)

	require.True(t, metadata.IsOpenAIChatGPTSubscription())
	require.Empty(t, metadata.GetCredential("access_token"))
}

func TestSchedulerMetadataAccountProjectsUpstreamBillingProbe(t *testing.T) {
	lastError := strings.Repeat("upstream diagnostic ", 512)
	probe := map[string]any{
		"status": "ok",
		"data": map[string]any{
			"billing_scope":             "token",
			"resolved_rate_multiplier":  0.03,
			"peak_rate_enabled":         true,
			"peak_start":                "09:00",
			"peak_end":                  "18:00",
			"peak_rate_multiplier":      2.0,
			"timezone":                  "Asia/Shanghai",
			"effective_rate_multiplier": 0.03,
			"remote_diagnostic":         lastError,
		},
		"received_at":   "2026-07-13T10:00:00Z",
		"fresh_until":   "2026-07-13T11:00:00Z",
		"next_probe_at": "2026-07-13T10:30:00Z",
		"http_status":   502,
		"last_error":    lastError,
	}
	account := service.Account{
		ID: 42,
		Extra: map[string]any{
			"upstream_billing_probe": probe,
			"unused_large_field":     "drop-me",
		},
	}

	metadata := buildSchedulerMetadataAccount(account)
	fullPayload, metaPayload, err := marshalSchedulerCacheAccount(account)
	require.NoError(t, err)

	filtered, ok := metadata.Extra["upstream_billing_probe"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ok", filtered["status"])
	require.Equal(t, "2026-07-13T10:00:00Z", filtered["received_at"])
	require.Equal(t, "2026-07-13T11:00:00Z", filtered["fresh_until"])
	require.Equal(t, "2026-07-13T10:30:00Z", filtered["next_probe_at"])
	require.NotContains(t, filtered, "http_status")
	require.NotContains(t, filtered, "last_error")
	filteredData, ok := filtered["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "token", filteredData["billing_scope"])
	require.Equal(t, 0.03, filteredData["resolved_rate_multiplier"])
	require.Equal(t, true, filteredData["peak_rate_enabled"])
	require.Equal(t, "09:00", filteredData["peak_start"])
	require.Equal(t, "18:00", filteredData["peak_end"])
	require.Equal(t, 2.0, filteredData["peak_rate_multiplier"])
	require.Equal(t, "Asia/Shanghai", filteredData["timezone"])
	require.NotContains(t, filteredData, "effective_rate_multiplier")
	require.NotContains(t, filteredData, "remote_diagnostic")
	require.NotContains(t, metadata.Extra, "unused_large_field")
	require.Contains(t, string(fullPayload), lastError)
	require.NotContains(t, string(metaPayload), "last_error")
	require.Less(t, len(metaPayload)*4, len(fullPayload))
}

func TestSchedulerMetadataAccountDropsInvalidUpstreamBillingProbe(t *testing.T) {
	for _, probe := range []any{
		"invalid",
		map[string]any{},
		map[string]any{"status": ""},
	} {
		metadata := buildSchedulerMetadataAccount(service.Account{
			Extra: map[string]any{service.UpstreamBillingProbeExtraKey: probe},
		})

		require.NotContains(t, metadata.Extra, service.UpstreamBillingProbeExtraKey)
	}
}
