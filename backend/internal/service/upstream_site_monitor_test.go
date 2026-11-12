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

func TestUpstreamSiteMonitoringHistoryPreservesSourceSemantics(t *testing.T) {
	for _, mode := range []string{"v1", "v2"} {
		t.Run(mode, func(t *testing.T) {
			upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, req.Method)
				if req.URL.Path == "/api/v1/settings/public" {
					return siteImportResponse(`{"channel_monitor_enabled":true,"channel_monitor_mode":"` + mode + `"}`), nil
				}
				if mode == "v1" {
					return siteImportResponse(`{"items":[{"id":1,"primary_status":"operational","timeline":[{"checked_at":"2026-11-12T06:00:00Z","status":"operational","latency_ms":500},{"checked_at":"invalid","status":"operational"},{"checked_at":"2026-11-12T05:59:00Z","status":"error","latency_ms":12000}],"extra_models":[{"model":"extra","status":"operational"}]}]}`), nil
				}
				return siteImportResponse(`{"coverage":{"requested_start":"2026-11-12T05:45:00Z","requested_end":"2026-11-12T06:05:00Z","data_through":"2026-11-12T06:00:00Z","bucket_seconds":300},"items":[{"platform":"openai","group_id":1,"health":{"overall":"healthy"},"metrics":{"error_rate":0.05,"success_rate":0.8},"buckets":[{"bucket_start":"2026-11-12T05:55:00Z","health":{"overall":"healthy","score":92},"metrics":{"success_rate":0.8,"error_rate":0.05,"cache_rate":0.85,"ttft":{"p50_ms":1500}}},{"bucket_start":"2026-11-12T05:50:00Z","health":{"overall":"warning","score":60},"metrics":{"success_rate":0.7,"error_rate":0.2}},{"bucket_start":"2026-11-12T05:45:00Z","health":{"overall":"unknown","score":100},"metrics":{"request_count":0,"error_rate":0,"cache_rate":0}}]}]}`), nil
			}}
			svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
			result, err := svc.UpstreamSiteMonitoring(context.Background(), &UpstreamSite{ID: mode, Kind: "sub2api", BaseURL: "https://example.com", AccessToken: "management"})
			require.NoError(t, err)
			points := result.Rows[0].Points
			for i := 1; i < len(points); i++ {
				require.True(t, points[i].At.After(points[i-1].At), "upstream history can arrive newest first")
			}
			if mode == "v1" {
				require.Len(t, points, 2)
				require.Equal(t, "critical", points[0].State)
				require.Equal(t, "healthy", points[1].State)
				require.Equal(t, 500.0, *points[1].LatencyMS)
				require.Nil(t, points[1].SuccessRate, "a probe result is not a statistical success percentage")
				require.Empty(t, result.Rows[1].Points, "extra models must not inherit the primary model's history")
			} else {
				require.Len(t, points, 3)
				require.Nil(t, points[0].Score, "unknown buckets must not become green from an unqualified score")
				require.Nil(t, points[0].SuccessRate)
				require.Equal(t, "unknown", points[0].State)
				require.Equal(t, 92.0, *points[2].Score)
				require.Equal(t, 80.0, *points[2].SuccessRate, "use upstream success_rate, not one minus error_rate")
				require.InDelta(t, 5.0, *points[2].ErrorRate, 0.001)
				require.Equal(t, 80.0, *result.Rows[0].SuccessRate)
				require.Equal(t, 300, result.BucketSeconds)
				require.Equal(t, "2026-11-12T06:05:00Z", result.TrendEnd.Format(time.RFC3339))
			}
		})
	}
}
