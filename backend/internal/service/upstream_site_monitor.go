package service

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// These are upstream-reported results. Reading them never runs a model probe.
type UpstreamSiteMonitorRow struct {
	ID             string                     `json:"id"`
	Name           string                     `json:"name"`
	GroupID        string                     `json:"group_id,omitempty"`
	GroupName      string                     `json:"group_name,omitempty"`
	Model          string                     `json:"model,omitempty"`
	State          string                     `json:"state"`
	LatencyKind    string                     `json:"latency_kind"`
	LatencyMS      *float64                   `json:"latency_ms"`
	Availability7d *float64                   `json:"availability_7d"`
	ErrorRate      *float64                   `json:"error_rate"`
	SuccessRate    *float64                   `json:"success_rate"`
	CacheRate      *float64                   `json:"cache_rate"`
	CacheWindowHrs *float64                   `json:"cache_window_hours,omitempty"`
	CheckedAt      *time.Time                 `json:"checked_at,omitempty"`
	Points         []UpstreamSiteMonitorPoint `json:"points,omitempty"`
}

type UpstreamSiteMonitorPoint struct {
	At          time.Time `json:"at"`
	State       string    `json:"state"`
	Score       *float64  `json:"score"`
	LatencyMS   *float64  `json:"latency_ms"`
	SuccessRate *float64  `json:"success_rate"`
	ErrorRate   *float64  `json:"error_rate"`
	CacheRate   *float64  `json:"cache_rate"`
}

type UpstreamSiteMonitor struct {
	Status         string                   `json:"status"`
	Mode           string                   `json:"mode,omitempty"`
	SourceURL      string                   `json:"source_url,omitempty"`
	AttemptAt      time.Time                `json:"attempt_at"`
	ReceivedAt     *time.Time               `json:"received_at,omitempty"`
	DataAt         *time.Time               `json:"data_at,omitempty"`
	TrendStart     *time.Time               `json:"trend_start,omitempty"`
	TrendEnd       *time.Time               `json:"trend_end,omitempty"`
	BucketSeconds  int                      `json:"bucket_seconds,omitempty"`
	RefreshSeconds int                      `json:"refresh_seconds"`
	Stale          bool                     `json:"stale"`
	Error          string                   `json:"error,omitempty"`
	Rows           []UpstreamSiteMonitorRow `json:"rows"`
}

type upstreamSiteMonitorCache struct {
	Revision int64
	Snapshot *UpstreamSiteMonitor
	RetryAt  time.Time
}

// Short in-memory caching and the existing singleflight group coalesce readers.
// No timer, probe runner, inference key, or database migration is needed.
func (s *UpstreamBillingProbeService) UpstreamSiteMonitoring(ctx context.Context, site *UpstreamSite) (*UpstreamSiteMonitor, error) {
	key := site.ID + ":" + strconv.FormatInt(site.Revision, 10)
	value, err, _ := s.probeGroup.Do("site-monitor:"+key, func() (any, error) {
		now := s.now()
		var previous *UpstreamSiteMonitor
		if cached, ok := s.siteMonitors.Load(site.ID); ok {
			if entry, valid := cached.(upstreamSiteMonitorCache); valid && entry.Revision == site.Revision {
				if now.Before(entry.RetryAt) {
					return entry.Snapshot, nil
				}
				previous = entry.Snapshot
			}
		}
		snapshot, readErr := s.readUpstreamSiteMonitoring(ctx, site, now)
		if readErr != nil {
			if previous != nil && previous.ReceivedAt != nil {
				copy := *previous
				snapshot = &copy
				snapshot.AttemptAt = now
				snapshot.Status, snapshot.Stale = "error", true
			}
			// Transport and upstream bodies may contain secrets; expose neither.
			snapshot.Error = "Cannot read upstream monitoring; check site authorization and connection"
		}
		s.siteMonitors.Store(site.ID, upstreamSiteMonitorCache{Revision: site.Revision, Snapshot: snapshot, RetryAt: now.Add(time.Duration(snapshot.RefreshSeconds) * time.Second)})
		return snapshot, nil
	})
	if err != nil {
		return nil, err
	}
	snapshot, ok := value.(*UpstreamSiteMonitor)
	if !ok {
		return nil, upstreamSiteError("Invalid monitoring cache")
	}
	return snapshot, nil
}

func (s *UpstreamBillingProbeService) readUpstreamSiteMonitoring(ctx context.Context, site *UpstreamSite, now time.Time) (*UpstreamSiteMonitor, error) {
	out := &UpstreamSiteMonitor{Status: "unsupported", AttemptAt: now, RefreshSeconds: 60, Rows: []UpstreamSiteMonitorRow{}}
	if site.Kind != "sub2api" {
		return out, nil
	}
	out.SourceURL = strings.TrimRight(site.BaseURL, "/") + "/monitor"
	settings, err := s.upstreamSiteRequestWithToken(ctx, site, http.MethodGet, "/api/v1/settings/public", nil, "")
	if err != nil {
		out.Status = "error"
		return out, err
	}
	flags := gjson.ParseBytes(settings)
	if flags.Get("channel_monitor_enabled").Type != gjson.True && flags.Get("channel_monitor_enabled").Type != gjson.False {
		return out, nil
	}
	if !flags.Get("channel_monitor_enabled").Bool() {
		out.Status = "disabled"
		return out, nil
	}
	out.Mode = flags.Get("channel_monitor_mode").String()
	if out.Mode != "v1" && out.Mode != "v2" {
		return out, nil
	}
	interval := flags.Get("channel_monitor_default_interval_seconds").Int()
	if interval > 60 {
		out.RefreshSeconds = int(min(interval, 300))
	}
	path := "/api/v1/channel-monitors"
	if out.Mode == "v2" {
		path = "/api/v1/channel-monitor-v2/matrix?range=90m&group_by=platform_group"
	}
	data, err := s.upstreamSiteRequest(ctx, site, http.MethodGet, path, nil)
	if err != nil {
		out.Status = "error"
		return out, err
	}
	result := gjson.ParseBytes(data)
	if !result.Get("items").IsArray() || len(result.Get("items").Array()) > 1000 {
		out.Status = "error"
		return out, upstreamSiteError("Invalid upstream monitoring response")
	}
	out.DataAt = upstreamMonitorTime(result.Get("coverage.data_through"))
	if out.Mode == "v2" {
		out.TrendStart = upstreamMonitorTime(result.Get("coverage.requested_start"))
		out.TrendEnd = upstreamMonitorTime(result.Get("coverage.requested_end"))
		if seconds := result.Get("coverage.bucket_seconds").Int(); seconds >= 60 && seconds <= 86400 {
			out.BucketSeconds = int(seconds)
		}
	}
	for _, row := range result.Get("items").Array() {
		if out.Mode == "v1" {
			item := UpstreamSiteMonitorRow{
				ID: row.Get("id").String(), Name: row.Get("name").String(), GroupID: row.Get("group_id").String(), GroupName: row.Get("group_name").String(),
				Model: row.Get("primary_model").String(), State: upstreamMonitorState(row.Get("primary_status").String()), LatencyKind: "check",
				LatencyMS: upstreamMonitorNumber(row.Get("primary_latency_ms"), math.MaxFloat64, 1), Availability7d: upstreamMonitorNumber(row.Get("availability_7d"), 100, 1),
				CacheRate: upstreamMonitorNumber(row.Get("usage_metrics.cache_hit_rate"), 100, 1), CacheWindowHrs: upstreamMonitorNumber(row.Get("usage_metrics.window_hours"), math.MaxFloat64, 1),
				CheckedAt: upstreamMonitorTime(row.Get("checked_at")),
			}
			for _, point := range row.Get("timeline").Array() {
				if checked := upstreamMonitorTime(point.Get("checked_at")); checked != nil {
					item.Points = append(item.Points, UpstreamSiteMonitorPoint{At: *checked, State: upstreamMonitorState(point.Get("status").String()), LatencyMS: upstreamMonitorNumber(point.Get("latency_ms"), math.MaxFloat64, 1)})
					if item.CheckedAt == nil || checked.After(*item.CheckedAt) {
						item.CheckedAt = checked
					}
				}
			}
			item.Points = upstreamMonitorPoints(item.Points)
			if item.CheckedAt != nil && (out.DataAt == nil || item.CheckedAt.After(*out.DataAt)) {
				out.DataAt = item.CheckedAt
			}
			out.Rows = append(out.Rows, item)
			for _, extra := range row.Get("extra_models").Array() {
				out.Rows = append(out.Rows, UpstreamSiteMonitorRow{
					ID: item.ID + ":" + extra.Get("model").String(), Name: item.Name, GroupID: item.GroupID, GroupName: item.GroupName, Model: extra.Get("model").String(),
					State: upstreamMonitorState(extra.Get("status").String()), LatencyKind: "check", LatencyMS: upstreamMonitorNumber(extra.Get("latency_ms"), math.MaxFloat64, 1), CheckedAt: item.CheckedAt,
				})
			}
			continue
		}
		groupID := row.Get("group_id").String()
		item := UpstreamSiteMonitorRow{
			ID: row.Get("platform").String() + ":" + groupID, Name: row.Get("group_name").String(), GroupID: groupID, GroupName: row.Get("group_name").String(),
			State: upstreamMonitorState(row.Get("health.overall").String()), LatencyKind: "ttft_p50", CheckedAt: out.DataAt,
		}
		values := upstreamMonitorV2Values(row)
		item.LatencyMS, item.ErrorRate, item.SuccessRate, item.CacheRate = values.LatencyMS, values.ErrorRate, values.SuccessRate, values.CacheRate
		for _, bucket := range row.Get("buckets").Array() {
			if at := upstreamMonitorTime(bucket.Get("bucket_start")); at != nil {
				point := upstreamMonitorV2Values(bucket)
				point.At = *at
				item.Points = append(item.Points, point)
			}
		}
		item.Points = upstreamMonitorPoints(item.Points)
		out.Rows = append(out.Rows, item)
	}
	out.Status = "ok"
	received := s.now().UTC()
	out.ReceivedAt = &received
	return out, nil
}

func upstreamMonitorV2Values(source gjson.Result) UpstreamSiteMonitorPoint {
	point := UpstreamSiteMonitorPoint{State: upstreamMonitorState(source.Get("health.overall").String())}
	// Some sites hide counts while returning valid rates. Unknown health alone
	// cannot establish a healthy bucket, even if the source includes a score.
	metrics := source.Get("metrics")
	hasData := point.State != "unknown" || metrics.Get("has_requests").Bool() || metrics.Get("request_count").Int() > 0 || metrics.Get("ttft.p50_ms").Float() > 0 || metrics.Get("cache_rate").Float() > 0
	if hasData {
		point.LatencyMS = upstreamMonitorNumber(metrics.Get("ttft.p50_ms"), math.MaxFloat64, 1)
		point.ErrorRate = upstreamMonitorNumber(metrics.Get("error_rate"), 1, 100)
		point.SuccessRate = upstreamMonitorNumber(metrics.Get("success_rate"), 1, 100)
		point.CacheRate = upstreamMonitorNumber(metrics.Get("cache_rate"), 1, 100)
	}
	if point.State != "unknown" {
		point.Score = upstreamMonitorNumber(source.Get("health.score"), 100, 1)
	}
	return point
}

func upstreamMonitorPoints(points []UpstreamSiteMonitorPoint) []UpstreamSiteMonitorPoint {
	sort.SliceStable(points, func(i, j int) bool { return points[i].At.Before(points[j].At) })
	if len(points) > 256 {
		points = points[len(points)-256:]
	}
	return points
}

func upstreamMonitorNumber(value gjson.Result, maximum, scale float64) *float64 {
	if value.Type != gjson.Number {
		return nil
	}
	number := value.Float()
	if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > maximum {
		return nil
	}
	number *= scale
	return &number
}

func upstreamMonitorTime(value gjson.Result) *time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value.String())
	if err != nil {
		return nil
	}
	return &parsed
}

func upstreamMonitorState(state string) string {
	switch state {
	case "operational", "healthy":
		return "healthy"
	case "degraded", "warning":
		return "warning"
	case "error", "outage", "critical":
		return "critical"
	default:
		return "unknown"
	}
}
