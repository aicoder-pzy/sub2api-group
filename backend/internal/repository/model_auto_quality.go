package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageLogRepository) GetModelAutoAccountQuality(ctx context.Context, groupID int64, model, effort string) (map[int64]service.GroupModelAccountQuality, error) {
	quality, err := r.GetQualityPreferredAccountQuality(ctx, groupID, model, effort)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("model-auto-speed:%d:%s:%s", groupID, model, effort)
	speeds, ok := r.schedulingQuality.Get(key)
	var evidence map[int64]*service.ModelAutoAccountEvidence
	if ok {
		evidence, _ = speeds.(map[int64]*service.ModelAutoAccountEvidence)
	}
	if evidence == nil {
		rows, err := r.sql.QueryContext(ctx, modelAutoSpeedSQL, groupID, model, effort)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		evidence = make(map[int64]*service.ModelAutoAccountEvidence)
		for rows.Next() {
			var id int64
			var selectedEffort string
			var bucket service.ModelAutoSpeedBucket
			if err := rows.Scan(&id, &selectedEffort, &bucket.ContextBucket, &bucket.Samples, &bucket.P50MS, &bucket.P95MS, &bucket.OutputTPS, &bucket.CostRatio, &bucket.OutputSamples); err != nil {
				return nil, err
			}
			if evidence[id] == nil {
				evidence[id] = &service.ModelAutoAccountEvidence{Effort: selectedEffort, Buckets: []service.ModelAutoSpeedBucket{}}
			}
			evidence[id].Buckets = append(evidence[id].Buckets, bucket)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		r.schedulingQuality.SetDefault(key, evidence)
	}
	result := make(map[int64]service.GroupModelAccountQuality, len(quality))
	for id, sample := range quality {
		sample.Auto = evidence[id]
		result[id] = sample
	}
	return result, nil
}

// Complete requests only, one effort, fixed context-size buckets. An upstream
// error on this account removes a partial response from the speed comparison.
// Cost is account expense / standard expense, so cache hits and output length
// cannot make a cheaper workload look like a cheaper provider.
const modelAutoSpeedSQL = `WITH chosen AS (
 SELECT COALESCE(NULLIF($3,''),(SELECT COALESCE(reasoning_effort,'') FROM usage_logs
 WHERE group_id=$1 AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=$2
 AND created_at>=NOW()-INTERVAL '24 hours' GROUP BY reasoning_effort ORDER BY COUNT(*) DESC,reasoning_effort LIMIT 1),'') AS effort
), requests AS (
 SELECT u.*,c.effort,CASE WHEN input_tokens+cache_creation_tokens+cache_read_tokens<8192 THEN 0
 WHEN input_tokens+cache_creation_tokens+cache_read_tokens<32768 THEN 1
 WHEN input_tokens+cache_creation_tokens+cache_read_tokens<131072 THEN 2 ELSE 3 END AS bucket
 FROM usage_logs u CROSS JOIN chosen c WHERE u.group_id=$1
 AND COALESCE(NULLIF(BTRIM(u.requested_model),''),u.model)=$2
 AND u.created_at>=NOW()-INTERVAL '24 hours' AND u.stream AND u.first_token_ms>0
 AND u.duration_ms>=u.first_token_ms AND u.output_tokens>0 AND COALESCE(u.reasoning_effort,'')=c.effort
 AND NOT EXISTS(SELECT 1 FROM ops_error_logs e
 LEFT JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(e.upstream_errors)='array' THEN e.upstream_errors ELSE '[]'::jsonb END) event ON true
 WHERE e.group_id=$1 AND e.api_key_id=u.api_key_id AND e.request_id=u.request_id
 AND COALESCE(NULLIF(BTRIM(e.requested_model),''),e.model)=$2 AND e.created_at>=NOW()-INTERVAL '24 hours'
 AND COALESCE(NULLIF((event->>'account_id')::bigint,0),e.account_id)=u.account_id AND e.error_phase IN ('upstream','account_auth')
 AND COALESCE(e.is_count_tokens,false)=false AND COALESCE(e.error_owner,'') NOT IN ('client','user')
 AND COALESCE(event->>'scope','') NOT IN ('request','provider','proxy'))
)
 SELECT account_id,effort,bucket,COUNT(*),
 percentile_cont(0.5) WITHIN GROUP(ORDER BY first_token_ms),
 percentile_cont(0.95) WITHIN GROUP(ORDER BY first_token_ms),
 COALESCE(percentile_cont(0.5) WITHIN GROUP(ORDER BY output_tokens*1000.0/NULLIF(duration_ms-first_token_ms,0)) FILTER(WHERE output_tokens>=100 AND duration_ms>first_token_ms),0),
 COALESCE(SUM(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1))/NULLIF(SUM(total_cost),0),0),
 COUNT(*) FILTER(WHERE output_tokens>=100 AND duration_ms>first_token_ms)
 FROM requests GROUP BY account_id,effort,bucket ORDER BY account_id,bucket`
