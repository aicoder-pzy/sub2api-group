package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUpstreamSiteMonitoringReadOnlyCacheAndFailure(t *testing.T) {
	now := time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)
	requests, fail := 0, false
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		requests++
		require.Equal(t, http.MethodGet, req.Method)
		require.Nil(t, req.Body)
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		switch req.URL.RequestURI() {
		case "/api/v1/settings/public":
			require.Empty(t, req.Header.Get("Authorization"))
			return siteImportResponse(`{"code":0,"data":{"channel_monitor_enabled":true,"channel_monitor_mode":"v2","channel_monitor_default_interval_seconds":60}}`), nil
		case "/api/v1/channel-monitor-v2/matrix?range=90m&group_by=platform_group":
			require.Equal(t, "Bearer management-only", req.Header.Get("Authorization"))
			if fail {
				response := siteImportResponse(`{"secret":"must-not-leak"}`)
				response.StatusCode = http.StatusInternalServerError
				return response, nil
			}
			return siteImportResponse(`{"data":{"coverage":{"data_through":"2026-11-12T05:59:00Z"},"items":[{"platform":"openai","group_id":141,"group_name":"Group","health":{"overall":"warning"},"metrics":{"request_count":0,"error_rate":0.05,"cache_rate":0.82,"ttft":{"sample_count":0,"p50_ms":1200}}},{"platform":"openai","group_id":142,"health":{"overall":"unknown"},"metrics":{"error_rate":0,"cache_rate":0,"ttft":{"p50_ms":0}}},{"group_id":143,"health":{"overall":"healthy"},"metrics":{"error_rate":2,"cache_rate":-1}}]}}`), nil
		default:
			t.Fatalf("unexpected upstream request: %s", req.URL)
			return nil, nil
		}
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	svc.now = func() time.Time { return now }
	site := &UpstreamSite{ID: "site", Kind: "sub2api", Revision: 1, BaseURL: "https://example.com", AccessToken: "management-only"}
	first, err := svc.UpstreamSiteMonitoring(context.Background(), site)
	require.NoError(t, err)
	require.Equal(t, "ok", first.Status)
	require.Equal(t, "warning", first.Rows[0].State)
	require.Equal(t, "141", first.Rows[0].GroupID)
	require.InDelta(t, 82, *first.Rows[0].CacheRate, 0.001)
	require.InDelta(t, 5, *first.Rows[0].ErrorRate, 0.001)
	require.Equal(t, 1200.0, *first.Rows[0].LatencyMS)
	require.Nil(t, first.Rows[1].CacheRate)
	require.Nil(t, first.Rows[1].ErrorRate)
	require.Nil(t, first.Rows[2].CacheRate)
	require.Nil(t, first.Rows[2].ErrorRate)
	_, err = svc.UpstreamSiteMonitoring(context.Background(), site)
	require.NoError(t, err)
	require.Equal(t, 2, requests, "repeated readers must not poll upstream")

	now = now.Add(61 * time.Second)
	fail = true
	stale, err := svc.UpstreamSiteMonitoring(context.Background(), site)
	require.NoError(t, err)
	require.True(t, stale.Stale)
	require.Equal(t, "error", stale.Status)
	require.Equal(t, first.ReceivedAt, stale.ReceivedAt)
	require.Equal(t, first.DataAt, stale.DataAt)
	require.Len(t, stale.Rows, 3)
	encoded, err := json.Marshal(stale)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "must-not-leak")
	require.NotContains(t, string(encoded), "management-only")
	_, err = svc.UpstreamSiteMonitoring(context.Background(), site)
	require.NoError(t, err)
	require.Equal(t, 4, requests, "failures must also be cached")

	site.Revision++
	changed, err := svc.UpstreamSiteMonitoring(context.Background(), site)
	require.NoError(t, err)
	require.Empty(t, changed.Rows, "changed credentials or URL must not reuse another revision's data")
	require.Nil(t, changed.ReceivedAt)
	require.Equal(t, 6, requests)
}

func TestUpstreamSiteMonitoringV1DoesNotConfuseMonitorAndGroupIDs(t *testing.T) {
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		if req.URL.Path == "/api/v1/settings/public" {
			return siteImportResponse(`{"data":{"channel_monitor_enabled":true,"channel_monitor_mode":"v1","channel_monitor_default_interval_seconds":900}}`), nil
		}
		require.Equal(t, "/api/v1/channel-monitors", req.URL.Path)
		return siteImportResponse(`{"data":{"items":[{"id":29,"name":"Monitor","primary_model":"test-model","primary_status":"operational","primary_latency_ms":2500,"availability_7d":99.5,"usage_metrics":{"cache_hit_rate":84,"window_hours":24},"timeline":[{"checked_at":"2026-11-12T05:58:00Z"},{"checked_at":"2026-11-12T05:59:00Z"}],"extra_models":[{"model":"extra","status":"degraded","latency_ms":3000}]}]}}`), nil
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	result, err := svc.UpstreamSiteMonitoring(context.Background(), &UpstreamSite{ID: "v1", Kind: "sub2api", BaseURL: "https://example.com", AccessToken: "management"})
	require.NoError(t, err)
	require.Len(t, result.Rows, 2)
	require.Empty(t, result.Rows[0].GroupID, "monitor ID is not an upstream group ID")
	require.Equal(t, "healthy", result.Rows[0].State)
	require.Equal(t, "warning", result.Rows[1].State)
	require.Equal(t, "check", result.Rows[0].LatencyKind)
	require.Equal(t, 84.0, *result.Rows[0].CacheRate)
	require.Equal(t, result.DataAt, result.Rows[1].CheckedAt)
	require.Equal(t, 300, result.RefreshSeconds)
}

func TestUpstreamSiteMonitoringUnavailableDoesNotProbeModels(t *testing.T) {
	for _, test := range []struct{ kind, settings, status string }{
		{"newapi", "", "unsupported"},
		{"sub2api", `{"channel_monitor_enabled":false}`, "disabled"},
		{"sub2api", `{}`, "unsupported"},
	} {
		t.Run(test.kind+test.status+test.settings, func(t *testing.T) {
			calls := 0
			upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, http.MethodGet, req.Method)
				require.Equal(t, "/api/v1/settings/public", req.URL.Path)
				require.Empty(t, req.Header.Get("Authorization"))
				return siteImportResponse(test.settings), nil
			}}
			svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
			result, err := svc.UpstreamSiteMonitoring(context.Background(), &UpstreamSite{ID: "disabled", Kind: test.kind, BaseURL: "https://example.com"})
			require.NoError(t, err)
			require.Equal(t, test.status, result.Status)
			require.Empty(t, result.Rows)
			if test.kind == "newapi" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}
