package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageLogRepository) GetQualityPreferredAccountQuality(ctx context.Context, groupID int64, model, effort string) (map[int64]service.GroupModelAccountQuality, error) {
	// A saved test must affect the next selection, including on another server
	// replica, rather than waiting for the statistics cache to expire.
	var revision int64
	if err := scanSingleRow(ctx, r.sql, `SELECT COALESCE(MAX(id),0) FROM upstream_quality_samples
 WHERE model=$2 AND (group_id=$1 OR group_id IS NULL) AND sampled_at>=NOW()-INTERVAL '7 days'`, []any{groupID, model}, &revision); err != nil {
		return nil, err
	}
	key := fmt.Sprintf("quality:%s:%d", observationKey(ctx, groupID, model, effort), revision)
	if value, ok := r.schedulingQuality.Get(key); ok {
		if samples, ok := value.(map[int64]service.GroupModelAccountQuality); ok {
			return samples, nil
		}
	}
	now := time.Now()
	day, err := r.GetGroupModelAccountQuality(ctx, groupID, model, now.Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	week, err := r.GetGroupModelAccountQuality(ctx, groupID, model, now.Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	hour, err := r.GetGroupModelAccountQuality(ctx, groupID, model, now.Add(-time.Hour))
	if err != nil {
		return nil, err
	}
	result := make(map[int64]service.GroupModelAccountQuality, len(week))
	for id, w := range week {
		d, h := day[id], hour[id]
		d.Quality = &service.AccountQualityEvidence{Successes7D: w.Successes, Failures7D: w.Failures, Successes1H: h.Successes, Failures1H: h.Failures,
			SuccessLowerBound: service.QualitySuccessLowerBound(w.Successes, w.Failures), Tier: "unknown", Effort: effort}
		d.IntelligenceVerdict, d.IntelligenceReason, d.IntelligenceAt = "", "", nil
		result[id] = d
	}
	query, args := scopedSchedulingQuery(ctx, `WITH chosen AS (
 SELECT COALESCE(NULLIF($3,''),(SELECT COALESCE(reasoning_effort,'') FROM usage_logs
 WHERE group_id=$1 AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=/*business_model*/$2 /*ingress:*/
 AND created_at>=NOW()-INTERVAL '24 hours' GROUP BY reasoning_effort ORDER BY COUNT(*) DESC,reasoning_effort LIMIT 1),'') AS effort
), evidence AS (
 SELECT s.*,ROW_NUMBER() OVER (PARTITION BY account_id,kind ORDER BY sampled_at DESC,id DESC) AS position
 FROM upstream_quality_samples s WHERE model=$2 AND sampled_at>=NOW()-INTERVAL '7 days'
 AND (group_id=$1 OR group_id IS NULL)
), history AS (
 SELECT account_id,COUNT(*) FILTER(WHERE kind='pelican' AND verdict='normal') AS normal,
 COUNT(*) FILTER(WHERE kind='pelican' AND verdict='degraded') AS degraded,
 COUNT(*) FILTER(WHERE kind='pelican' AND verdict='unknown') AS unknown,
 COUNT(*) FILTER(WHERE verdict='failed') AS failed,
 COUNT(*) FILTER(WHERE kind='answer' AND verdict='normal') AS answer_normal,
 COUNT(*) FILTER(WHERE kind='answer' AND verdict='degraded') AS answer_degraded,
 COALESCE(MIN(position) FILTER(WHERE kind='pelican' AND verdict<>'normal')-1,COUNT(*) FILTER(WHERE kind='pelican')) AS streak,
 MAX(verdict) FILTER(WHERE kind='pelican' AND position=1) AS latest_verdict,
 MAX(reason) FILTER(WHERE kind='pelican' AND position=1) AS latest_reason,
 MAX(latency_ms) FILTER(WHERE kind='pelican' AND position=1 AND (e.effort=c.effort OR c.effort='')) AS test_latency,
 MAX(e.effort) FILTER(WHERE kind='pelican' AND position=1) AS test_effort,
 MAX(sampled_at) FILTER(WHERE kind='pelican' AND position=1) AS latest_at,
 MAX(sampled_at) AS last_test_at FROM evidence e CROSS JOIN chosen c GROUP BY account_id
), speed AS (
 SELECT u.account_id,COUNT(*) AS samples,
 percentile_cont(0.5) WITHIN GROUP(ORDER BY u.first_token_ms) AS p50,
 percentile_cont(0.95) WITHIN GROUP(ORDER BY u.first_token_ms) AS p95
 FROM usage_logs u,chosen c WHERE u.group_id=$1 AND COALESCE(NULLIF(BTRIM(u.requested_model),''),u.model)=/*business_model*/$2 /*ingress:u*/
 AND u.created_at>=NOW()-INTERVAL '24 hours' AND u.stream AND u.first_token_ms>0
 AND u.duration_ms>=u.first_token_ms AND u.output_tokens>0
 AND (COALESCE(u.reasoning_effort,'')=c.effort OR c.effort='')
 AND NOT EXISTS(SELECT 1 FROM ops_error_logs e
 LEFT JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(e.upstream_errors)='array' THEN e.upstream_errors ELSE '[]'::jsonb END) event ON true
 WHERE e.group_id=u.group_id AND e.api_key_id=u.api_key_id
 AND ((NULLIF(e.request_id,'') IS NOT NULL AND e.request_id=u.request_id)
 OR (NULLIF(e.client_request_id,'') IS NOT NULL AND u.request_id='client:'||e.client_request_id))
 AND COALESCE(NULLIF(BTRIM(e.requested_model),''),e.model)=/*business_model*/$2 /*ingress:e*/ AND e.created_at>=NOW()-INTERVAL '24 hours'
 AND COALESCE(NULLIF((event->>'account_id')::bigint,0),e.account_id)=u.account_id AND e.error_phase IN ('upstream','account_auth')
 AND COALESCE(e.is_count_tokens,false)=false AND COALESCE(e.error_owner,'') NOT IN ('client','user')
 AND COALESCE(event->>'scope','') NOT IN ('request','provider','proxy'))
 GROUP BY u.account_id
)
 SELECT COALESCE(h.account_id,s.account_id),COALESCE(h.normal,0),COALESCE(h.degraded,0),COALESCE(h.unknown,0),COALESCE(h.failed,0),
 COALESCE(h.answer_normal,0),COALESCE(h.answer_degraded,0),COALESCE(h.streak,0),COALESCE(h.latest_verdict,''),COALESCE(h.latest_reason,''),COALESCE(h.test_latency,0),h.latest_at,h.last_test_at,
 COALESCE(s.samples,0),COALESCE(s.p50,0),COALESCE(s.p95,0),c.effort,COALESCE(h.test_effort,'')
 FROM history h FULL OUTER JOIN speed s USING(account_id) CROSS JOIN chosen c`, []any{groupID, model, effort}, model)
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var e service.AccountQualityEvidence
		if err := rows.Scan(&id, &e.Normal, &e.Degraded, &e.Unknown, &e.FailedTests, &e.AnswerNormal, &e.AnswerDegraded, &e.NormalStreak,
			&e.LatestVerdict, &e.LatestReason, &e.TestLatencyMS, &e.LatestAt, &e.LastTestAt, &e.LatencySamples, &e.LatencyP50MS, &e.LatencyP95MS, &e.Effort, &e.TestEffort); err != nil {
			return nil, err
		}
		s := result[id]
		if s.Quality != nil {
			e.Successes7D = s.Quality.Successes7D
			e.Failures7D = s.Quality.Failures7D
			e.Successes1H = s.Quality.Successes1H
			e.Failures1H = s.Quality.Failures1H
			e.SuccessLowerBound = s.Quality.SuccessLowerBound
		}
		e.QualityLowerBound = service.QualitySuccessLowerBound(e.Normal, e.Degraded)
		e.Tier = service.QualityEvidenceTier(&e)
		s.Quality = &e
		s.IntelligenceVerdict, s.IntelligenceReason, s.IntelligenceAt = e.LatestVerdict, e.LatestReason, e.LatestAt
		if s.IntelligenceVerdict == "failed" {
			s.IntelligenceVerdict = "unknown"
		}
		result[id] = s
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	r.schedulingQuality.SetDefault(key, result)
	return result, nil
}
