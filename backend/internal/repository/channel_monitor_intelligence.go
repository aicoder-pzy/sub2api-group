package repository

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Read only the matching public model from the immutable result snapshot. A
// plan may have changed models since an earlier run. Never publish prompts,
// account identities, credentials or raw errors in the user monitor response.
const channelMonitorIntelligenceSQL = `
SELECT m.id, CASE
    WHEN g.id IS NULL THEN 'unknown'
    WHEN NOT EXISTS (SELECT 1 FROM pelican_group_test_plans p
      WHERE p.group_id=g.id AND p.model_id=m.primary_model AND p.enabled) THEN
      CASE WHEN EXISTS (SELECT 1 FROM pelican_group_test_plans p
        WHERE p.group_id=g.id AND p.model_id=m.primary_model) THEN 'paused'
        WHEN h.latest IS NOT NULL THEN 'historical' ELSE 'no_plan' END
    WHEN h.latest IS NULL THEN 'pending'
    WHEN h.latest < now() - interval '24 hours' THEN 'stale'
    ELSE h.last_status
  END,
  COALESCE(h.history, '[]'::jsonb)
FROM channel_monitors m
LEFT JOIN groups g ON g.id=m.intelligence_group_id AND g.deleted_at IS NULL
LEFT JOIN LATERAL (
  SELECT max(x.finished_at) AS latest,
    (array_agg(x.status ORDER BY x.finished_at DESC, x.id DESC))[1] AS last_status,
    jsonb_agg(jsonb_build_object('checked_at', x.finished_at, 'status', x.status,
      'latency_ms', x.latency_ms, 'effort', x.effort) ORDER BY x.finished_at, x.id) AS history
  FROM (
    SELECT r.id, r.finished_at, r.latency_ms, r.pelican_config->>'reasoning_effort' AS effort,
      CASE WHEN r.status <> 'success' THEN 'failed'
        WHEN r.quality_verdict IN ('normal','degraded') THEN r.quality_verdict
        ELSE 'unknown' END AS status
    FROM pelican_group_test_plans p
    JOIN pelican_group_test_results r ON r.plan_id=p.id
    WHERE p.group_id=g.id AND r.pelican_config->>'model_id'=m.primary_model
      AND r.finished_at IS NOT NULL
    ORDER BY r.finished_at DESC, r.id DESC LIMIT 20
  ) x
) h ON true
WHERE m.id=ANY($1) AND m.enabled AND m.intelligence_group_id IS NOT NULL`

func (r *channelMonitorRepository) ListMonitorIntelligence(ctx context.Context, ids []int64) (map[int64]*service.MonitorIntelligence, error) {
	rows, err := r.db.QueryContext(ctx, channelMonitorIntelligenceSQL, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]*service.MonitorIntelligence, len(ids))
	for rows.Next() {
		var id int64
		var summary service.MonitorIntelligence
		var history []byte
		if err := rows.Scan(&id, &summary.State, &history); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(history, &summary.History); err != nil {
			return nil, err
		}
		out[id] = &summary
	}
	return out, rows.Err()
}
