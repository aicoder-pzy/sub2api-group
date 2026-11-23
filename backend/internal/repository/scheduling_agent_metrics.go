package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Use the inbound protocol, even when an error occurs before opening the stream.
// Known request types override the legacy stream flag. Recovered attempts have
// a successful client status and can inform diagnosis without starting it.
const schedulingAgentFinalFailure = `status_code >= 400
 AND api_key_id IS NOT NULL AND COALESCE(is_count_tokens,false)=false
 AND COALESCE(error_owner,'') NOT IN ('client','user')
 AND ((error_phase IN ('upstream','account_auth','network','routing','internal') AND NOT COALESCE(is_business_limited,false))
      OR (error_phase='concurrency' AND (account_id>0 OR status_code>=500)))`

func schedulingAgentStreamSQL(prefix string) string {
	return `(` + prefix + `request_type IN (2,3) OR (COALESCE(` + prefix + `request_type,0)=0 AND ` + prefix + `stream))`
}

var schedulingAgentActivitySQL = `WITH failures AS (
 SELECT COALESCE(NULLIF(BTRIM(requested_model),''),model) AS model, created_at,
        COALESCE(upstream_status_code,status_code) AS upstream_status_code, account_id, api_key_id, request_id, client_request_id, error_phase
 FROM ops_error_logs WHERE group_id=$1 AND created_at>=NOW()-INTERVAL '15 minutes'
 AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=ANY($2)
 AND ` + schedulingAgentStreamSQL("") + ` AND ` + schedulingAgentFinalFailure + `
), request_502s AS (
 SELECT model, account_id, max(created_at) AS at FROM failures
 WHERE upstream_status_code=502 AND account_id>0 AND error_phase IN ('upstream','account_auth')
 AND COALESCE(NULLIF(client_request_id,''),NULLIF(request_id,'')) IS NOT NULL
 AND created_at>=NOW()-INTERVAL '5 minutes'
 GROUP BY model, account_id, api_key_id, COALESCE('client:'||NULLIF(client_request_id,''),request_id)
), bursts AS (
 SELECT model, account_id, count(*) AS requests, (array_agg(at ORDER BY at DESC))[2] AS second_latest_at
 FROM request_502s GROUP BY model, account_id HAVING count(*)>=2
), urgent AS (
 SELECT DISTINCT ON (model) model, account_id, requests, second_latest_at FROM bursts
 ORDER BY model, second_latest_at DESC, account_id
)
SELECT f.model, max(f.created_at), u.second_latest_at, COALESCE(u.account_id,0), COALESCE(u.requests,0)
FROM failures f LEFT JOIN urgent u USING (model)
GROUP BY f.model, u.second_latest_at, u.account_id, u.requests`

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

var schedulingAgentTrafficSQL = `SELECT account_id, COALESCE(inbound_endpoint,''), true,
 COALESCE(NULLIF(LOWER(BTRIM(reasoning_effort)),''),''), count(*),
 percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms>0)
 FROM usage_logs WHERE group_id=$1 AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=$2
 AND created_at>=NOW()-INTERVAL '1 hour' AND ` + schedulingAgentStreamSQL("") + ` GROUP BY 1,2,4 ORDER BY count(*) DESC,1,2,4 LIMIT 24`

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

func (r *usageLogRepository) GetSchedulingAgentFailures(ctx context.Context, groupID int64, model string) ([]service.SchedulingAgentFailure, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT COALESCE(account_id,0), COALESCE(error_phase,''), COALESCE(status_code,0), COALESCE(upstream_status_code,0), count(*), max(created_at)
 FROM ops_error_logs WHERE group_id=$1 AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=$2
 AND created_at>=NOW()-INTERVAL '30 minutes' AND api_key_id IS NOT NULL AND COALESCE(is_count_tokens,false)=false
 AND `+schedulingAgentStreamSQL("")+` AND (`+schedulingAgentFinalFailure+`
 OR (status_code BETWEEN 200 AND 299 AND error_phase IN ('upstream','account_auth')))
 GROUP BY 1,2,3,4 ORDER BY count(*) DESC,1,2,3,4 LIMIT 24`, groupID, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []service.SchedulingAgentFailure{}
	for rows.Next() {
		var row service.SchedulingAgentFailure
		if err := rows.Scan(&row.AccountID, &row.Phase, &row.Status, &row.Upstream, &row.Count, &row.LastAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// Read a few recent exceptional requests on demand, using existing group/time
// and usage request/key indexes. Only whitelisted attempt fields leave this reader.
var schedulingAgentRequestTracesSQL = `WITH recent AS (
 SELECT api_key_id, request_id, client_request_id, created_at, status_code, error_phase,
        account_id, upstream_status_code, upstream_errors
 FROM ops_error_logs WHERE group_id=$1 AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=$2
 AND created_at>=NOW()-INTERVAL '30 minutes' AND api_key_id IS NOT NULL AND COALESCE(is_count_tokens,false)=false
 AND ` + schedulingAgentStreamSQL("") + ` AND (` + schedulingAgentFinalFailure + `
 OR (status_code BETWEEN 200 AND 299 AND error_phase IN ('upstream','account_auth')))
 ORDER BY created_at DESC, id DESC LIMIT 8
)
SELECT e.created_at, e.status_code, COALESCE(e.error_phase,''), COALESCE(e.account_id,0),
       COALESCE(e.upstream_status_code,0), u.account_id, u.first_token_ms,
       COALESCE(e.upstream_errors,'[]'::jsonb)
FROM recent e LEFT JOIN LATERAL (
 SELECT account_id, first_token_ms FROM usage_logs u
 WHERE e.status_code BETWEEN 200 AND 299 AND u.api_key_id=e.api_key_id AND u.group_id=$1
 AND COALESCE(NULLIF(BTRIM(u.requested_model),''),u.model)=$2 AND ` + schedulingAgentStreamSQL("u.") + `
 AND u.request_id IN ('client:'||NULLIF(e.client_request_id,''),'local:'||NULLIF(e.request_id,''),NULLIF(e.request_id,''))
 AND u.created_at BETWEEN e.created_at-INTERVAL '2 minutes' AND e.created_at+INTERVAL '2 minutes'
 ORDER BY (u.request_id = 'client:'||NULLIF(e.client_request_id,'')) DESC NULLS LAST,
          (u.request_id = 'local:'||NULLIF(e.request_id,'')) DESC NULLS LAST LIMIT 1
) u ON true ORDER BY e.created_at DESC`

func (r *usageLogRepository) GetSchedulingAgentRequestTraces(ctx context.Context, groupID int64, model string) ([]service.SchedulingAgentRequestTrace, error) {
	rows, err := r.sql.QueryContext(ctx, schedulingAgentRequestTracesSQL, groupID, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []service.SchedulingAgentRequestTrace{}
	for rows.Next() {
		var trace service.SchedulingAgentRequestTrace
		var raw []byte
		if err := rows.Scan(&trace.At, &trace.ClientStatus, &trace.Phase, &trace.LastFailedAccount, &trace.UpstreamStatus,
			&trace.ServedAccount, &trace.ServingFirstMs, &raw); err != nil {
			return nil, err
		}
		var attempts []service.SchedulingAgentAttempt
		if err := json.Unmarshal(raw, &attempts); err == nil {
			const maxAttempts = 6
			trace.Truncated = len(attempts) > maxAttempts || (len(attempts) > 0 && attempts[0].Omitted > 0)
			trace.Attempts = attempts[max(0, len(attempts)-maxAttempts):]
		}
		result = append(result, trace)
	}
	return result, rows.Err()
}

// Usage logs store uncached input and cache tokens in separate columns.
func (r *usageLogRepository) GetSchedulingAgentCacheRatios(ctx context.Context, groupID int64, model string, accounts []int64) (map[int64]*float64, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT account_id,
  SUM(cache_read_tokens)::double precision / NULLIF(SUM(input_tokens + cache_creation_tokens + cache_read_tokens),0)
  FROM usage_logs WHERE group_id=$1 AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=$2
  AND account_id=ANY($3) AND created_at>=NOW()-INTERVAL '24 hours' AND `+schedulingAgentStreamSQL("")+`
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
