package repository

import (
	"context"

	"github.com/lib/pq"
)

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
