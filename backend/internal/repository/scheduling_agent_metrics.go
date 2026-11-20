package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const schedulingAgentActivitySQL = `SELECT model, max(created_at) FROM (
 SELECT COALESCE(NULLIF(BTRIM(requested_model),''),model) AS model, created_at
 FROM usage_logs WHERE group_id=$1 AND created_at>=NOW()-INTERVAL '15 minutes'
 UNION ALL
 SELECT COALESCE(NULLIF(BTRIM(requested_model),''),model), created_at
 FROM ops_error_logs WHERE group_id=$1 AND api_key_id IS NOT NULL AND created_at>=NOW()-INTERVAL '15 minutes'
 AND error_phase IN ('upstream','account_auth','concurrency') AND COALESCE(is_count_tokens,false)=false
 AND COALESCE(error_owner,'') NOT IN ('client','user')
) AS activity WHERE model=ANY($2) GROUP BY model`

func (r *usageLogRepository) GetSchedulingAgentActivity(ctx context.Context, groupID int64, models []string) ([]service.SchedulingAgentActivity, error) {
	rows, err := r.sql.QueryContext(ctx, schedulingAgentActivitySQL, groupID, pq.Array(models))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []service.SchedulingAgentActivity
	for rows.Next() {
		var signal service.SchedulingAgentActivity
		if err := rows.Scan(&signal.Model, &signal.At); err != nil {
			return nil, err
		}
		result = append(result, signal)
	}
	return result, rows.Err()
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
