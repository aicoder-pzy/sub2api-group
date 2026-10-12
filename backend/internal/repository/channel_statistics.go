package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// One statement gives the cards, account comparison and daily totals the same
// database snapshot. Historical costs never join the current account multiplier.
const channelStatisticsSQL = `WITH usage AS MATERIALIZED (
 SELECT u.*, COALESCE(NULLIF(BTRIM(u.requested_model),''),u.model) AS requested,
 COALESCE(u.reasoning_effort,'') AS effort,
 (u.duration_ms>0 AND (u.total_cost>0 OR u.actual_cost>0 OR u.input_tokens>0
 OR u.output_tokens>0 OR u.cache_creation_tokens>0 OR u.cache_read_tokens>0
 OR u.image_count>0 OR u.image_output_tokens>0)) AS completed
 FROM usage_logs u WHERE u.created_at >= $1 AND u.created_at < $2
 AND ($3::bigint=0 OR u.group_id=$3)
), request_efforts AS MATERIALIZED (
 SELECT DISTINCT ON (request_id,api_key_id,group_id,requested)
 request_id,api_key_id,group_id,requested,effort FROM usage WHERE request_id<>''
 ORDER BY request_id,api_key_id,group_id,requested,created_at DESC,id DESC
), errors AS MATERIALIZED (
 SELECT e.*,COALESCE(NULLIF(BTRIM(e.requested_model),''),e.model) AS requested,
 COALESCE(NULLIF(e.request_id,''),'error:'||e.id::text) AS request_key,
 u.effort FROM ops_error_logs e LEFT JOIN request_efforts u
 ON u.request_id=e.request_id AND u.api_key_id=e.api_key_id
 AND u.group_id IS NOT DISTINCT FROM e.group_id
 AND u.requested=COALESCE(NULLIF(BTRIM(e.requested_model),''),e.model)
 WHERE e.created_at >= $1 AND e.created_at < $2
 AND ($3::bigint=0 OR e.group_id=$3)
 AND e.error_phase IN ('upstream','account_auth')
 AND COALESCE(e.is_count_tokens,false)=false AND COALESCE(e.error_owner,'') NOT IN ('client','user')
), failure_events AS (
 -- Keep the root account even when the event array lists only earlier attempts.
 SELECT e.*,e.account_id AS failed_account FROM errors e
 WHERE e.account_id>0 AND (e.upstream_status_code>=400 OR e.error_phase='account_auth'
 OR (e.status_code>=400 AND COALESCE(jsonb_array_length(CASE WHEN jsonb_typeof(e.upstream_errors)='array'
 THEN e.upstream_errors ELSE '[]'::jsonb END),0)=0)) AND NOT EXISTS (
 SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(e.upstream_errors)='array'
 THEN e.upstream_errors ELSE '[]'::jsonb END) v WHERE v->>'account_id'=e.account_id::text)
 UNION ALL
 SELECT e.*,COALESCE(NULLIF(v->>'account_id','')::bigint,e.account_id) FROM errors e
 CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(e.upstream_errors)='array'
 THEN e.upstream_errors ELSE '[]'::jsonb END) v
 WHERE COALESCE(v->>'scope','') NOT IN ('request','provider','proxy')
 AND (COALESCE((v->>'upstream_status_code')::integer,0)>=400
 OR v->>'kind' IN ('request_error','stream_error','fastest_failover_timeout'))
), failures AS MATERIALIZED (
 SELECT failed_account AS account_id,api_key_id,group_id,requested,request_key,
 MAX(created_at) AS at,MAX(effort) AS effort FROM failure_events WHERE failed_account>0
 GROUP BY failed_account,api_key_id,group_id,requested,request_key
), quality AS MATERIALIZED (
 SELECT * FROM upstream_quality_samples WHERE sampled_at >= $1 AND sampled_at < $2
 AND ($3::bigint=0 OR group_id=$3) AND kind='pelican'
), events AS (
 SELECT u.account_id,u.created_at AS at,CASE WHEN completed THEN 1 ELSE 0 END AS requests,
 CASE WHEN completed AND f.account_id IS NULL THEN 1 ELSE 0 END AS successes,
 0 AS failures,0 AS unclassified_failures,CASE WHEN completed THEN u.stream END AS stream,
 CASE WHEN completed AND u.stream AND u.first_token_ms>0 THEN u.first_token_ms END AS ttft,
 CASE WHEN completed AND u.duration_ms>0 THEN u.duration_ms END AS duration,
 CASE WHEN completed AND u.stream AND u.first_token_ms>0 AND u.duration_ms>u.first_token_ms AND u.output_tokens>0
 THEN u.output_tokens*1000.0/(u.duration_ms-u.first_token_ms) END AS tps,
 u.input_tokens,u.output_tokens,u.cache_creation_tokens,u.cache_read_tokens,
 u.total_cost AS standard_cost,
 COALESCE(u.account_stats_cost,u.total_cost)*COALESCE(u.account_rate_multiplier,1) AS account_cost,
 u.actual_cost AS user_cost,NULL::text AS verdict,
 CASE WHEN completed THEN u.input_tokens+u.cache_creation_tokens+u.cache_read_tokens END AS context_tokens
 FROM usage u LEFT JOIN failures f ON f.account_id=u.account_id
 AND f.request_key=u.request_id AND f.api_key_id=u.api_key_id
 AND f.group_id IS NOT DISTINCT FROM u.group_id AND f.requested=u.requested
 WHERE ($4::text='' OR u.requested=$4) AND ($5::text='' OR u.effort=$5)
 UNION ALL
 SELECT account_id,at,0,0,CASE WHEN $5::text='' OR effort=$5 THEN 1 ELSE 0 END,
 CASE WHEN COALESCE(effort,'')='' THEN 1 ELSE 0 END,NULL,NULL,NULL,NULL,0,0,0,0,0,0,0,NULL,NULL
 FROM failures WHERE ($4::text='' OR requested=$4) AND ($5::text='' OR effort=$5 OR COALESCE(effort,'')='')
 UNION ALL
 SELECT account_id,sampled_at,0,0,0,0,NULL,NULL,NULL,NULL,0,0,0,0,0,0,0,verdict,NULL
 FROM quality WHERE ($4::text='' OR model=$4) AND ($5::text='' OR effort=$5)
), aggregate AS (
 SELECT CASE WHEN GROUPING(account_id)=0 THEN 'account' WHEN GROUPING((at AT TIME ZONE 'Asia/Shanghai')::date)=0
 THEN 'day' ELSE 'summary' END AS scope,COALESCE(account_id,0) AS account_id,
 COALESCE(((at AT TIME ZONE 'Asia/Shanghai')::date)::text,'') AS day,
 COALESCE(SUM(requests),0) AS requests,COALESCE(SUM(successes),0) AS successes,
 COALESCE(SUM(failures),0) AS failures,COALESCE(SUM(unclassified_failures),0) AS unclassified_failures,
 COUNT(*) FILTER(WHERE stream=true) AS stream_requests,COUNT(*) FILTER(WHERE stream=false) AS nonstream_requests,
 COUNT(ttft) AS latency_samples,AVG(ttft) AS ttft_average_ms,
 percentile_cont(0.5) WITHIN GROUP(ORDER BY ttft) AS ttft_p50_ms,
 percentile_cont(0.95) WITHIN GROUP(ORDER BY ttft) AS ttft_p95_ms,
 COUNT(*) FILTER(WHERE ttft>30000) AS slow_30,COUNT(*) FILTER(WHERE ttft>60000) AS slow_60,
 percentile_cont(0.5) WITHIN GROUP(ORDER BY duration) AS duration_p50_ms,
 percentile_cont(0.95) WITHIN GROUP(ORDER BY duration) AS duration_p95_ms,
 percentile_cont(0.5) WITHIN GROUP(ORDER BY tps) AS output_tps_p50,
 percentile_cont(0.5) WITHIN GROUP(ORDER BY context_tokens) AS context_tokens_p50,
 COALESCE(SUM(input_tokens),0) AS input_tokens,COALESCE(SUM(output_tokens),0) AS output_tokens,
 COALESCE(SUM(cache_creation_tokens),0) AS cache_creation_tokens,COALESCE(SUM(cache_read_tokens),0) AS cache_read_tokens,
 COALESCE(SUM(standard_cost),0) AS standard_cost,COALESCE(SUM(account_cost),0) AS account_cost,
 COALESCE(SUM(user_cost),0) AS user_cost,COUNT(*) FILTER(WHERE verdict='normal') AS normal,
 COUNT(*) FILTER(WHERE verdict='degraded') AS degraded,COUNT(*) FILTER(WHERE verdict='unknown') AS unknown,
 COUNT(*) FILTER(WHERE verdict='failed') AS failed_tests
 FROM events GROUP BY GROUPING SETS ((),(account_id),((at AT TIME ZONE 'Asia/Shanghai')::date))
), named AS (
 SELECT g.*,COALESCE(a.name,'#'||g.account_id::text) AS account_name,
 (a.id IS NULL OR a.deleted_at IS NOT NULL) AS account_deleted
 FROM aggregate g LEFT JOIN accounts a ON a.id=g.account_id
)
SELECT jsonb_build_object(
 'summary',(SELECT to_jsonb(g) FROM aggregate g WHERE scope='summary'),
 'accounts',COALESCE((SELECT jsonb_agg(to_jsonb(g) ORDER BY requests DESC,account_id) FROM named g WHERE scope='account'),'[]'::jsonb),
 'daily',COALESCE((SELECT jsonb_agg(to_jsonb(g) ORDER BY day) FROM aggregate g WHERE scope='day'),'[]'::jsonb),
 'models',COALESCE((SELECT jsonb_agg(model ORDER BY model) FROM (
 SELECT requested AS model FROM usage UNION SELECT requested FROM errors UNION SELECT model FROM quality
 ) m WHERE model<>''),'[]'::jsonb))`

func (r *usageLogRepository) GetChannelStatistics(ctx context.Context, q service.ChannelStatisticsQuery, start, end time.Time) (*service.ChannelStatistics, error) {
	var raw []byte
	if err := scanSingleRow(ctx, r.sql, channelStatisticsSQL, []any{start, end, q.GroupID, q.Model, q.Effort}, &raw); err != nil {
		return nil, err
	}
	var result service.ChannelStatistics
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
