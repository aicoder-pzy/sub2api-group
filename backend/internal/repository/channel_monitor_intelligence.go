package repository

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Only finished results for the configured group and exact public model count.
// Text results also have to match the current question and grading rule. Only
// question, bounded output and grading value are public; account/error fields
// never leave the repository. Raw drawing output is rendered as escaped text.
// Traffic targets scope the current verdict, never the historical timeline.
var channelMonitorIntelligenceSQL = `
SELECT m.id, probe.model, COALESCE(p.id, 0), q.kind,
  COALESCE(p.pelican_config->>'question_name', ''), CASE
    WHEN g.id IS NULL THEN 'unknown'
    WHEN p.id IS NULL THEN 'no_plan'
    WHEN NOT p.enabled OR g.status <> 'active' THEN 'paused'
    ELSE h.last_status
  END, COALESCE(h.history, '[]'::jsonb)
FROM channel_monitors m
CROSS JOIN LATERAL (
  SELECT DISTINCT model
  FROM jsonb_array_elements_text(jsonb_build_array(m.primary_model) || COALESCE(m.extra_models, '[]'::jsonb)) AS models(model)
  WHERE btrim(model) <> ''
) probe
LEFT JOIN groups g ON g.id=m.intelligence_group_id AND g.deleted_at IS NULL
LEFT JOIN pelican_group_test_plans p ON p.group_id=g.id AND p.model_id=probe.model
CROSS JOIN LATERAL (SELECT CASE
  WHEN p.pelican_config->>'question_kind' IN ('candy','custom') THEN p.pelican_config->>'question_kind'
  WHEN (p.pelican_config->>'prompt_mode') IS DISTINCT FROM 'random_creative'
    AND btrim(p.pelican_config->>'prompt')=ANY($2::text[]) THEN 'candy'
  ELSE 'pelican' END AS kind) q
LEFT JOIN LATERAL (
  WITH targets AS (
    SELECT t.account_id FROM (` + pelicanTrafficAccountsSQL("g.id", "probe.model", "now()") + `) t
    JOIN accounts a ON a.id=t.account_id AND a.deleted_at IS NULL
    JOIN account_groups ag ON ag.group_id=g.id AND ag.account_id=t.account_id
  ), samples AS (
    SELECT r.id, r.account_id, r.finished_at, r.latency_ms,
      row_number() OVER (PARTITION BY r.account_id ORDER BY r.finished_at DESC, r.id DESC) AS position,
      r.pelican_config->>'reasoning_effort' AS effort,
      CASE WHEN q.kind IN ('candy','custom') THEN left(COALESCE(r.pelican_config->>'prompt',''),2000) ELSE '' END AS prompt,
      q.kind IN ('candy','custom') AND length(COALESCE(r.pelican_config->>'prompt',''))>2000 AS prompt_truncated,
      CASE WHEN q.kind IN ('candy','custom') THEN left(COALESCE(r.response_text, ''), 2000) ELSE '' END AS answer,
      CASE WHEN q.kind='candy' THEN '21' WHEN q.kind='custom' THEN r.pelican_config->>'expected_answer' ELSE '' END AS expected_answer,
      q.kind IN ('candy','custom') AND length(COALESCE(r.response_text, ''))>2000 AS answer_truncated,
      r.intelligence_pending AS confirmation_pending,
      CASE WHEN r.intelligence_status<>'' AND q.kind IN ('candy','custom') THEN r.intelligence_status
        WHEN r.status <> 'success' AND q.kind IN ('candy','custom') AND starts_with(r.error_message, 'answer_mismatch') THEN 'unknown'
        WHEN r.status <> 'success' THEN 'failed'
        WHEN q.kind IN ('candy','custom') THEN 'normal'
        WHEN r.quality_verdict IN ('normal','degraded') THEN r.quality_verdict
        ELSE 'unknown' END AS status
    FROM pelican_group_test_results r
    WHERE r.plan_id=p.id AND r.pelican_config->>'model_id'=probe.model
      AND COALESCE(NULLIF(r.pelican_config->>'question_kind',''), 'pelican')=q.kind
      AND (q.kind<>'candy' OR r.pelican_config->>'prompt'=p.pelican_config->>'prompt')
      AND (q.kind<>'custom' OR (r.pelican_config->>'prompt'=p.pelican_config->>'prompt'
        AND r.pelican_config->>'expected_answer'=p.pelican_config->>'expected_answer'))
  )
  SELECT (SELECT CASE
      WHEN bool_or(x.status='failed' AND x.finished_at>=now()-interval '24 hours') THEN 'failed'
      WHEN bool_or(x.status='degraded' AND x.finished_at>=now()-interval '24 hours') THEN 'degraded'
      WHEN count(x.id)<count(t.account_id) OR count(t.account_id)=0 THEN 'pending'
      WHEN bool_or(x.finished_at<now()-interval '24 hours') THEN 'stale'
      WHEN bool_or(x.status<>'normal') THEN 'unknown'
      ELSE 'normal' END
    FROM targets t LEFT JOIN samples x ON x.account_id=t.account_id AND x.position=1) AS last_status,
    (SELECT jsonb_agg(jsonb_build_object('checked_at', x.finished_at, 'status', x.status,
      'latency_ms', x.latency_ms, 'effort', x.effort, 'answer', x.answer,
      'expected_answer', x.expected_answer, 'answer_truncated', x.answer_truncated,
      'prompt', x.prompt, 'prompt_truncated', x.prompt_truncated,
      'confirmation_pending', x.confirmation_pending)
      ORDER BY x.finished_at, x.id)
  FROM (
    SELECT * FROM samples ORDER BY finished_at DESC, id DESC LIMIT 20
  ) x) AS history
) h ON true
WHERE m.id=ANY($1) AND m.enabled AND m.intelligence_group_id IS NOT NULL
ORDER BY m.id, probe.model, p.id`

func (r *channelMonitorRepository) ListMonitorIntelligence(ctx context.Context, ids []int64) (map[int64]map[string]*service.MonitorIntelligence, error) {
	rows, err := r.db.QueryContext(ctx, channelMonitorIntelligenceSQL, pq.Array(ids), pq.Array(service.BuiltinCandyPrompts()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]map[string]*service.MonitorIntelligence, len(ids))
	for rows.Next() {
		var id int64
		var model string
		var test service.MonitorIntelligenceTest
		var history []byte
		if err := rows.Scan(&id, &model, &test.ID, &test.Kind, &test.Name, &test.State, &history); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(history, &test.History); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = make(map[string]*service.MonitorIntelligence)
		}
		summary := out[id][model]
		if summary == nil {
			summary = &service.MonitorIntelligence{State: test.State, History: []service.MonitorIntelligencePoint{}}
			out[id][model] = summary
		}
		if test.ID > 0 {
			summary.Tests = append(summary.Tests, test)
			summary.History = append(summary.History, test.History...)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, models := range out {
		for _, summary := range models {
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
