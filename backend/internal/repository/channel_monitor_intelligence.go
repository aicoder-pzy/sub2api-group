package repository

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Only finished results for the configured group and exact public model count.
// Text results also have to match the current question and grading rule. Private
// prompts, custom answers, errors and accounts never leave the repository here.
// Only the built-in candy question exposes a bounded answer and its grading value.
const channelMonitorIntelligenceSQL = `
SELECT m.id, COALESCE(p.id, 0), q.kind,
  COALESCE(p.pelican_config->>'question_name', ''), CASE
    WHEN g.id IS NULL THEN 'unknown'
    WHEN p.id IS NULL THEN 'no_plan'
    WHEN NOT p.enabled OR g.status <> 'active' THEN 'paused'
    WHEN h.latest IS NULL THEN 'pending'
    WHEN h.latest < now() - interval '24 hours' THEN 'stale'
    ELSE h.last_status
  END, COALESCE(h.history, '[]'::jsonb)
FROM channel_monitors m
LEFT JOIN groups g ON g.id=m.intelligence_group_id AND g.deleted_at IS NULL
LEFT JOIN pelican_group_test_plans p ON p.group_id=g.id AND p.model_id=m.primary_model
CROSS JOIN LATERAL (SELECT CASE
  WHEN p.pelican_config->>'question_kind' IN ('candy','custom') THEN p.pelican_config->>'question_kind'
  WHEN (p.pelican_config->>'prompt_mode') IS DISTINCT FROM 'random_creative'
    AND btrim(p.pelican_config->>'prompt')=ANY($3::text[]) THEN 'candy'
  ELSE 'pelican' END AS kind) q
LEFT JOIN LATERAL (
  SELECT max(x.finished_at) AS latest,
    (array_agg(x.status ORDER BY x.finished_at DESC, x.id DESC))[1] AS last_status,
    jsonb_agg(jsonb_build_object('checked_at', x.finished_at, 'status', x.status,
      'latency_ms', x.latency_ms, 'effort', x.effort, 'answer', x.answer,
      'expected_answer', x.expected_answer, 'answer_truncated', x.answer_truncated)
      ORDER BY x.finished_at, x.id) AS history
  FROM (
    SELECT r.id, r.finished_at, r.latency_ms, r.pelican_config->>'reasoning_effort' AS effort,
      CASE WHEN q.kind='candy' THEN left(COALESCE(r.response_text, ''), 2000) END AS answer,
      CASE WHEN q.kind='candy' THEN '21' END AS expected_answer,
      q.kind='candy' AND length(COALESCE(r.response_text, ''))>2000 AS answer_truncated,
      CASE WHEN r.status <> 'success' AND q.kind IN ('candy','custom') AND starts_with(r.error_message, 'answer_mismatch') THEN 'incorrect'
        WHEN r.status <> 'success' THEN 'failed'
        WHEN q.kind IN ('candy','custom') THEN 'normal'
        WHEN r.quality_verdict IN ('normal','degraded') THEN r.quality_verdict
        ELSE 'unknown' END AS status
    FROM pelican_group_test_results r
    WHERE r.plan_id=p.id AND r.pelican_config->>'model_id'=m.primary_model
      AND COALESCE(NULLIF(r.pelican_config->>'question_kind',''), 'pelican')=q.kind
      AND (q.kind<>'candy' OR r.pelican_config->>'prompt'=$2)
      AND (q.kind<>'custom' OR (r.pelican_config->>'prompt'=p.pelican_config->>'prompt'
        AND r.pelican_config->>'expected_answer'=p.pelican_config->>'expected_answer'))
    ORDER BY r.finished_at DESC, r.id DESC LIMIT 20
  ) x
) h ON true
WHERE m.id=ANY($1) AND m.enabled AND m.intelligence_group_id IS NOT NULL
ORDER BY m.id, p.id`

func (r *channelMonitorRepository) ListMonitorIntelligence(ctx context.Context, ids []int64) (map[int64]*service.MonitorIntelligence, error) {
	rows, err := r.db.QueryContext(ctx, channelMonitorIntelligenceSQL, pq.Array(ids), service.CandyPrompt, pq.Array(service.BuiltinCandyPrompts()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]*service.MonitorIntelligence, len(ids))
	for rows.Next() {
		var id int64
		var test service.MonitorIntelligenceTest
		var history []byte
		if err := rows.Scan(&id, &test.ID, &test.Kind, &test.Name, &test.State, &history); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(history, &test.History); err != nil {
			return nil, err
		}
		summary := out[id]
		if summary == nil {
			summary = &service.MonitorIntelligence{State: test.State, History: []service.MonitorIntelligencePoint{}}
			out[id] = summary
		}
		if test.ID > 0 {
			summary.Tests = append(summary.Tests, test)
			summary.History = append(summary.History, test.History...)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, summary := range out {
		// Keep the original flat timeline for older clients. The current cards display
		// each question separately, including when different questions disagree.
		if len(summary.Tests) > 1 {
			summary.State = "unknown"
			if allIntelligenceTestsPaused(summary.Tests) {
				summary.State = "paused"
			}
		}
		sort.SliceStable(summary.History, func(i, j int) bool {
			return summary.History[i].CheckedAt.Before(summary.History[j].CheckedAt)
		})
		if n := len(summary.History); n > 20 {
			summary.History = summary.History[n-20:]
		}
		if summary.State == "incorrect" {
			summary.State = "failed"
		}
		for i := range summary.History {
			if summary.History[i].Status == "incorrect" {
				summary.History[i].Status = "failed"
			}
		}
	}
	return out, nil
}

func allIntelligenceTestsPaused(tests []service.MonitorIntelligenceTest) bool {
	for _, test := range tests {
		if test.State != "paused" {
			return false
		}
	}
	return true
}
