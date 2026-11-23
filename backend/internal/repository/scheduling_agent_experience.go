package repository

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const schedulingExperienceContextBucket = `CASE WHEN tokens<8192 THEN 0 WHEN tokens<32768 THEN 1
 WHEN tokens<131072 THEN 2 WHEN tokens<204800 THEN 3 WHEN tokens<262144 THEN 4 ELSE 5 END`

const schedulingExperienceSession = `encode(sha256(convert_to(api_key_id::text||':'||session_id,'UTF8')),'hex')`
const schedulingExperienceTokens = `(input_tokens::bigint+cache_creation_tokens+cache_read_tokens)`

// Use closed windows with a short ingestion allowance. If the bounded read is
// exceeded, do not diagnose an incident using a partial slice of the group.
var schedulingCacheIncidentsSQL = `WITH bounds AS (
 SELECT date_bin('10 minutes',NOW()-INTERVAL '1 minute','2000-01-01'::timestamptz) AS finish
), bounded AS MATERIALIZED (
 SELECT COALESCE(NULLIF(BTRIM(requested_model),''),model) AS model, account_id, api_key_id, session_id,
        COALESCE(NULLIF(request_type,0),2) AS protocol, COALESCE(inbound_endpoint,'') AS endpoint,
        COALESCE(NULLIF(LOWER(BTRIM(requested_reasoning_effort)),''),'') AS effort, native_compaction_v2 AS compacted,
        created_at, finish, cache_read_tokens, ` + schedulingExperienceTokens + ` AS tokens
 FROM usage_logs CROSS JOIN bounds
 WHERE group_id=$1 AND created_at>=finish-INTERVAL '80 minutes' AND created_at<finish
 AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=ANY($2)
 AND ` + schedulingAgentStreamSQL("") + ` AND NULLIF(session_id,'') IS NOT NULL
 AND output_tokens>0 AND first_token_ms>0 AND duration_ms>=first_token_ms AND ` + schedulingExperienceTokens + `>=2048
 LIMIT 12001
), turns AS MATERIALIZED (
 SELECT *, ` + schedulingExperienceSession + ` AS session, ` + schedulingExperienceContextBucket + ` AS bucket,
 CASE WHEN created_at>=finish-INTERVAL '10 minutes' THEN 0 WHEN created_at>=finish-INTERVAL '20 minutes' THEN 1 ELSE 2 END AS age
 FROM bounded WHERE (SELECT count(*) FROM bounded)<=12000
), flags AS (
 SELECT model,session,bool_or(compacted) FILTER(WHERE age<2) AS compacted,
        count(DISTINCT account_id)>1 AS changed_account
 FROM turns GROUP BY model,session
), cohorts AS (
 SELECT model,account_id,session,protocol,endpoint,effort,bucket,finish,
        count(*) FILTER(WHERE age=0) AS latest_requests,count(*) FILTER(WHERE age=1) AS previous_requests,
        sum(cache_read_tokens) FILTER(WHERE age=0)::float8 / sum(tokens) FILTER(WHERE age=0) AS latest,
        sum(cache_read_tokens) FILTER(WHERE age=1)::float8 / sum(tokens) FILTER(WHERE age=1) AS previous,
        sum(cache_read_tokens) FILTER(WHERE age=2)::float8 / sum(tokens) FILTER(WHERE age=2) AS baseline
 FROM turns GROUP BY model,account_id,session,protocol,endpoint,effort,bucket,finish
 HAVING count(*) FILTER(WHERE age=0)>=3 AND count(*) FILTER(WHERE age=1)>=3 AND count(*) FILTER(WHERE age=2)>=3
), ranked AS (
 SELECT *, baseline>=0.85 AND latest<=baseline-0.15 AND previous<=baseline-0.15 AS low,
   COALESCE(f.compacted,false) AS compacted, f.changed_account
 FROM cohorts JOIN flags f USING(model,session)
), counts AS (
 SELECT model,account_id,finish,count(DISTINCT session) AS comparable,
        count(DISTINCT session) FILTER(WHERE low) AS affected
 FROM ranked GROUP BY model,account_id,finish HAVING bool_or(low)
), examples AS (
 SELECT DISTINCT ON (model,account_id,session) * FROM ranked WHERE low
 ORDER BY model,account_id,session,baseline-latest DESC,effort,endpoint,bucket
)
SELECT c.model,c.account_id,c.finish,c.affected,c.comparable,coalesce((
 SELECT jsonb_agg(to_jsonb(e)-'model'-'account_id'-'finish'-'low') FROM (
  SELECT e.* FROM examples e WHERE e.model=c.model AND e.account_id=c.account_id ORDER BY baseline-latest DESC,session LIMIT 6
 ) e
),'[]'::jsonb)
 FROM counts c ORDER BY affected DESC,model,account_id LIMIT 16`

func (r *usageLogRepository) GetSchedulingAgentCacheIncidents(ctx context.Context, group int64, models []string) ([]service.SchedulingAgentCacheIncident, error) {
	rows, err := r.sql.QueryContext(ctx, schedulingCacheIncidentsSQL, group, pq.Array(models))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []service.SchedulingAgentCacheIncident
	for rows.Next() {
		var incident service.SchedulingAgentCacheIncident
		var evidence []byte
		if err := rows.Scan(&incident.Model, &incident.AccountID, &incident.At, &incident.Affected, &incident.Comparable, &evidence); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(evidence, &incident.Low); err != nil {
			return nil, err
		}
		result = append(result, incident)
	}
	return result, rows.Err()
}

// Compare only sessions assigned by this trial. A first completed turn is
// reported separately, and never contributes to the warm comparison.
var schedulingTrialCohortsSQL = `WITH assignments AS MATERIALIZED (
 SELECT split_part(a.key,'/',1) AS session,a.value::bigint AS account_id,
        to_timestamp(split_part(a.key,'/',2)::double precision) AS assigned_at FROM jsonb_each_text($7::jsonb) a
), logged AS MATERIALIZED (
 SELECT *, ` + schedulingExperienceTokens + ` AS tokens, ` + schedulingExperienceSession + ` AS session
 FROM usage_logs WHERE group_id=$1 AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=$2
 AND created_at >= $3 AND created_at < LEAST(NOW(),$4::timestamptz+INTERVAL '10 minutes')
 AND account_id IN ($5,$6) AND NULLIF(session_id,'') IS NOT NULL
 AND ` + schedulingAgentStreamSQL("") + ` AND output_tokens>0 AND first_token_ms>0 AND duration_ms>=first_token_ms
), assigned AS (
 SELECT logged.*, ROW_NUMBER() OVER (PARTITION BY logged.session ORDER BY logged.created_at,logged.id) AS turn,
 COALESCE(NULLIF(request_type,0),2) AS protocol, COALESCE(inbound_endpoint,'') AS endpoint,
 COALESCE(NULLIF(LOWER(BTRIM(requested_reasoning_effort)),''),'') AS effort, ` + schedulingExperienceContextBucket + ` AS bucket
 FROM logged JOIN assignments a ON a.session=logged.session AND a.account_id=logged.account_id AND logged.created_at>=a.assigned_at
), scores AS (
 SELECT account_id,effort,endpoint,protocol,bucket,stage,count(DISTINCT session) AS sessions,count(*) AS requests,
 sum(cache_read_tokens)::float8/sum(tokens) AS cache,percentile_cont(0.95) WITHIN GROUP(ORDER BY first_token_ms) AS p95
 FROM assigned CROSS JOIN LATERAL unnest(CASE WHEN turn=1 THEN ARRAY['first']
  WHEN turn=2 THEN ARRAY['warm','second'] ELSE ARRAY['warm','third_or_later'] END) AS stages(stage)
 WHERE tokens>0 AND (turn=1 OR tokens>=2048) AND NOT native_compaction_v2
 GROUP BY account_id,effort,endpoint,protocol,bucket,stage
), matched AS (
 SELECT t.effort,t.endpoint,t.protocol,t.bucket,t.stage,t.sessions AS trial_sessions,c.sessions AS control_sessions,
 t.requests AS trial_requests,c.requests AS control_requests,t.cache AS trial_cache,c.cache AS control_cache,
 t.p95 AS trial_first_event_p95_ms,c.p95 AS control_first_event_p95_ms
 FROM scores t JOIN scores c USING(effort,endpoint,protocol,bucket,stage) WHERE t.account_id=$5 AND c.account_id=$6
), cohorts AS (
 SELECT effort,endpoint,protocol,bucket,
 COALESCE(max(trial_sessions) FILTER(WHERE stage='warm'),0) AS trial_sessions,
 COALESCE(max(control_sessions) FILTER(WHERE stage='warm'),0) AS control_sessions,
 COALESCE(max(trial_requests) FILTER(WHERE stage='warm'),0) AS trial_requests,
 COALESCE(max(control_requests) FILTER(WHERE stage='warm'),0) AS control_requests,
 COALESCE(max(trial_cache) FILTER(WHERE stage='warm'),0) AS trial_cache,
 COALESCE(max(control_cache) FILTER(WHERE stage='warm'),0) AS control_cache,
 COALESCE(max(trial_first_event_p95_ms) FILTER(WHERE stage='warm'),0) AS trial_p95,
 COALESCE(max(control_first_event_p95_ms) FILTER(WHERE stage='warm'),0) AS control_p95,
 COALESCE(jsonb_agg(to_jsonb(m)-'effort'-'endpoint'-'protocol'-'bucket'-'trial_requests'-'control_requests' ORDER BY stage) FILTER(WHERE stage<>'warm'),'[]'::jsonb) AS stages
 FROM matched m GROUP BY effort,endpoint,protocol,bucket
)
SELECT count(*) FILTER(WHERE a.account_id=$5),count(*) FILTER(WHERE a.account_id=$6),
 count(c.session) FILTER(WHERE a.account_id=$5),count(c.session) FILTER(WHERE a.account_id=$6),
 COALESCE((SELECT jsonb_agg(to_jsonb(x)) FROM (
  SELECT effort,endpoint,protocol,bucket AS context_bucket,trial_sessions,control_sessions,
   trial_requests AS trial_warm_requests,control_requests AS control_warm_requests,trial_cache,control_cache,
   trial_p95 AS trial_first_event_p95_ms,control_p95 AS control_first_event_p95_ms,stages AS matched_turn_stages
  FROM cohorts ORDER BY LEAST(trial_requests,control_requests) DESC,effort,endpoint,protocol,bucket LIMIT 24
 ) x),'[]'::jsonb)
FROM assignments a LEFT JOIN assigned c ON c.session=a.session AND c.account_id=a.account_id AND c.turn=1
WHERE a.assigned_at<=NOW()-INTERVAL '2 minutes'`

func (r *usageLogRepository) GetSchedulingAgentTrialCohorts(ctx context.Context, group int64, model string, route service.SchedulingAgentRoute, assignments map[string]int64) (service.SchedulingAgentTrialResult, error) {
	result := service.SchedulingAgentTrialResult{AccountID: route.TrialID, ControlID: route.TrialControlID, StartedAt: route.TrialStartedAt}
	input, err := json.Marshal(assignments)
	if err != nil {
		return result, err
	}
	var cohorts []byte
	err = scanSingleRow(ctx, r.sql, schedulingTrialCohortsSQL, []any{group, model, route.TrialStartedAt, route.TrialUntil, route.TrialID, route.TrialControlID, string(input)},
		&result.TrialAssigned, &result.ControlAssigned, &result.TrialCompleted, &result.ControlCompleted, &cohorts)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(cohorts, &result.Cohorts)
	return result, err
}
