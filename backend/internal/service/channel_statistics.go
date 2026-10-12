package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Channel statistics are retrospective only; they never select or probe accounts.
type ChannelStatisticsQuery struct {
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
	GroupID   int64  `form:"group_id"`
	Model     string `form:"model"`
	Effort    string `form:"effort"`
}

type ChannelStatisticsRow struct {
	Day                  string   `json:"day"`
	AccountID            int64    `json:"account_id"`
	AccountName          string   `json:"account_name"`
	AccountDeleted       bool     `json:"account_deleted"`
	Requests             int64    `json:"requests"`
	Successes            int64    `json:"successes"`
	Failures             int64    `json:"failures"`
	UnclassifiedFailures int64    `json:"unclassified_failures"`
	StreamRequests       int64    `json:"stream_requests"`
	NonstreamRequests    int64    `json:"nonstream_requests"`
	LatencySamples       int64    `json:"latency_samples"`
	TTFTAverageMS        *float64 `json:"ttft_average_ms"`
	TTFTP50MS            *float64 `json:"ttft_p50_ms"`
	TTFTP95MS            *float64 `json:"ttft_p95_ms"`
	Slow30               int64    `json:"slow_30"`
	Slow60               int64    `json:"slow_60"`
	DurationP50MS        *float64 `json:"duration_p50_ms"`
	DurationP95MS        *float64 `json:"duration_p95_ms"`
	OutputTPSP50         *float64 `json:"output_tps_p50"`
	ContextTokensP50     *float64 `json:"context_tokens_p50"`
	InputTokens          int64    `json:"input_tokens"`
	OutputTokens         int64    `json:"output_tokens"`
	CacheCreationTokens  int64    `json:"cache_creation_tokens"`
	CacheReadTokens      int64    `json:"cache_read_tokens"`
	StandardCost         float64  `json:"standard_cost"`
	AccountCost          float64  `json:"account_cost"`
	UserCost             float64  `json:"user_cost"`
	Normal               int64    `json:"normal"`
	Degraded             int64    `json:"degraded"`
	Unknown              int64    `json:"unknown"`
	FailedTests          int64    `json:"failed_tests"`
}

type ChannelStatistics struct {
	StartDate   string                 `json:"start_date"`
	EndDate     string                 `json:"end_date"`
	Timezone    string                 `json:"timezone"`
	GeneratedAt time.Time              `json:"generated_at"`
	Summary     ChannelStatisticsRow   `json:"summary"`
	Accounts    []ChannelStatisticsRow `json:"accounts"`
	Daily       []ChannelStatisticsRow `json:"daily"`
	Models      []string               `json:"models"`
}

func (q ChannelStatisticsQuery) Window(now time.Time) (time.Time, time.Time, error) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	today := now.In(loc).Format(time.DateOnly)
	if q.StartDate == "" {
		q.StartDate = today
	}
	if q.EndDate == "" {
		q.EndDate = q.StartDate
	}
	start, err := time.ParseInLocation(time.DateOnly, q.StartDate, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid start_date (YYYY-MM-DD)")
	}
	end, err := time.ParseInLocation(time.DateOnly, q.EndDate, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid end_date (YYYY-MM-DD)")
	}
	midnight, _ := time.ParseInLocation(time.DateOnly, today, loc)
	if start.Before(midnight.AddDate(0, 0, -6)) || end.After(midnight) || end.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("select dates within the latest 7 calendar days (Asia/Shanghai)")
	}
	if q.GroupID < 0 || len(q.Model) > 100 {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid group_id or model")
	}
	switch q.Effort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("invalid effort")
	}
	end = end.AddDate(0, 0, 1)
	if end.After(now) {
		end = now
	}
	return start, end, nil
}

func (s *UsageService) GetChannelStatistics(ctx context.Context, q ChannelStatisticsQuery) (*ChannelStatistics, error) {
	now := time.Now()
	q.Model, q.Effort = strings.TrimSpace(q.Model), strings.TrimSpace(q.Effort)
	start, end, err := q.Window(now)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_STATISTICS_RANGE", err.Error())
	}
	reader, ok := s.usageRepo.(interface {
		GetChannelStatistics(context.Context, ChannelStatisticsQuery, time.Time, time.Time) (*ChannelStatistics, error)
	})
	if !ok {
		return nil, fmt.Errorf("channel statistics unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := reader.GetChannelStatistics(ctx, q, start, end)
	if err != nil {
		return nil, err
	}
	result.StartDate = start.Format(time.DateOnly)
	result.EndDate = end.Add(-time.Nanosecond).In(start.Location()).Format(time.DateOnly)
	result.Timezone, result.GeneratedAt = "Asia/Shanghai", now
	// Include zero-usage days so daily comparisons don't silently skip gaps.
	byDay := make(map[string]ChannelStatisticsRow, len(result.Daily))
	for _, row := range result.Daily {
		byDay[row.Day] = row
	}
	result.Daily = []ChannelStatisticsRow{}
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		key := day.Format(time.DateOnly)
		row := byDay[key]
		row.Day = key
		result.Daily = append(result.Daily, row)
	}
	return result, nil
}
