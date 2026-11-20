package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const schedulingAgentActivitySQL = `WITH failures AS (
 SELECT COALESCE(NULLIF(BTRIM(requested_model),''),model) AS model, created_at,
        upstream_status_code, account_id, api_key_id, request_id
 FROM ops_error_logs WHERE group_id=$1 AND api_key_id IS NOT NULL AND created_at>=NOW()-INTERVAL '15 minutes'
 AND error_phase IN ('upstream','account_auth','concurrency') AND COALESCE(is_count_tokens,false)=false
 AND COALESCE(error_owner,'') NOT IN ('client','user')
), activity AS (
 SELECT COALESCE(NULLIF(BTRIM(requested_model),''),model) AS model, created_at
 FROM usage_logs WHERE group_id=$1 AND created_at>=NOW()-INTERVAL '15 minutes'
 UNION ALL
 SELECT model, created_at FROM failures
), request_502s AS (
 SELECT model, account_id, max(created_at) AS at FROM failures
 WHERE model=ANY($2) AND upstream_status_code=502 AND account_id>0 AND NULLIF(request_id,'') IS NOT NULL
 AND created_at>=NOW()-INTERVAL '5 minutes' GROUP BY model, account_id, api_key_id, request_id
), bursts AS (
 SELECT model, account_id, count(*) AS requests, (array_agg(at ORDER BY at DESC))[2] AS second_latest_at
 FROM request_502s GROUP BY model, account_id HAVING count(*)>=2
), urgent AS (
 SELECT DISTINCT ON (model) model, account_id, requests, second_latest_at FROM bursts
 ORDER BY model, second_latest_at DESC, account_id
)
SELECT a.model, max(a.created_at), u.second_latest_at, COALESCE(u.account_id,0), COALESCE(u.requests,0)
FROM activity a LEFT JOIN urgent u USING (model) WHERE a.model=ANY($2)
GROUP BY a.model, u.second_latest_at, u.account_id, u.requests`

func (r *usageLogRepository) GetSchedulingAgentActivity(ctx context.Context, groupID int64, models []string) ([]service.SchedulingAgentActivity, error) {
	rows, err := r.sql.QueryContext(ctx, schedulingAgentActivitySQL, groupID, pq.Array(models))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []service.SchedulingAgentActivity
	for rows.Next() {
		var signal service.SchedulingAgentActivity
		var failureSince *time.Time
		if err := rows.Scan(&signal.Model, &signal.At, &failureSince, &signal.FailedAccount, &signal.Failures); err != nil {
			return nil, err
		}
		if failureSince != nil {
			signal.FailureSince = *failureSince
		}
		result = append(result, signal)
	}
	return result, rows.Err()
}

const schedulingAgentTrafficSQL = `SELECT account_id, COALESCE(inbound_endpoint,''), stream,
 COALESCE(NULLIF(LOWER(BTRIM(reasoning_effort)),''),''), count(*),
 percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms>0)
 FROM usage_logs WHERE group_id=$1 AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=$2
 AND created_at>=NOW()-INTERVAL '1 hour' GROUP BY 1,2,3,4 ORDER BY count(*) DESC,1,2,3,4 LIMIT 24`

func (r *usageLogRepository) GetSchedulingAgentTraffic(ctx context.Context, groupID int64, model string) ([]service.SchedulingAgentTraffic, error) {
	rows, err := r.sql.QueryContext(ctx, schedulingAgentTrafficSQL, groupID, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	traffic := []service.SchedulingAgentTraffic{}
	for rows.Next() {
		var row service.SchedulingAgentTraffic
		if err := rows.Scan(&row.AccountID, &row.Endpoint, &row.Stream, &row.Effort, &row.Samples, &row.DurationP95); err != nil {
			return nil, err
		}
		traffic = append(traffic, row)
	}
	return traffic, rows.Err()
}

// Usage logs store uncached input and cache tokens in separate columns.
func (r *usageLogRepository) GetSchedulingAgentCacheRatios(ctx context.Context, groupID int64, model string, accounts []int64) (map[int64]*float64, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT account_id,
  SUM(cache_read_tokens)::double precision / NULLIF(SUM(input_tokens + cache_creation_tokens + cache_read_tokens),0)
  FROM usage_logs WHERE group_id=$1 AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=$2
  AND account_id=ANY($3) AND created_at>=NOW()-INTERVAL '24 hours'
  GROUP BY account_id`, groupID, model, pq.Array(accounts))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ratios := make(map[int64]*float64)
	for rows.Next() {
		var id int64
		var ratio *float64
		if err := rows.Scan(&id, &ratio); err != nil {
			return nil, err
		}
		ratios[id] = ratio
	}
	return ratios, rows.Err()
}
