package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Usage attribution follows the historical channel_id. Supplier labels describe
// the current manual classification, never an inferred historical supplier.
const channelOperationsUsageBase = `WITH base AS (
 SELECT u.*,COALESCE(m.upstream_id,0) AS operation_upstream_id,
 COALESCE(NULLIF(BTRIM(u.requested_model),''),u.model) AS public_model,
 (u.actual_cost>0 OR u.total_cost>0 OR u.account_stats_cost>0 OR u.input_tokens>0 OR u.output_tokens>0
 OR u.cache_read_tokens>0 OR u.cache_creation_tokens>0 OR u.image_count>0 OR u.image_output_tokens>0) AS measured
 FROM usage_logs u LEFT JOIN channel_operation_members m ON m.channel_id=u.channel_id AND m.account_id=u.account_id
 WHERE u.created_at >= $1 AND u.created_at < $2
 AND ($3::bigint=0 OR u.channel_id=$3) AND ($4::bigint=0 OR m.upstream_id=$4 OR ($4=-1 AND m.upstream_id IS NULL))
 AND ($5::bigint=0 OR u.account_id=$5) AND ($6::bigint=0 OR u.group_id=$6)
 AND ($7='' OR CASE $8 WHEN 'public' THEN COALESCE(NULLIF(BTRIM(u.requested_model),''),u.model)
 WHEN 'recorded' THEN u.model WHEN 'upstream' THEN u.upstream_model END=$7)
), measured AS (
 SELECT *,CASE WHEN stream AND measured AND first_token_ms>0 THEN first_token_ms END AS ttft,
 CASE WHEN measured AND duration_ms>0 THEN duration_ms END AS duration FROM base
)`

func (r *channelRepository) operationsSection(ctx context.Context, query string, args ...any) service.ChannelOperationsSection {
	out := service.ChannelOperationsSection{Status: "query_failed", Data: json.RawMessage("null"), QueriedAt: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var raw []byte
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&raw); err != nil {
		out.Error = "Statistics unavailable; retry with a smaller time range."
		return out
	}
	out.Status, out.Data = "ready", raw
	return out
}

// Quality and error records lack historical channel attribution. These sections
// explicitly use current channel-group and supplier membership; no success rate
// is calculated from error logs without a matching request denominator.
const channelOperationsCurrentScope = `
 AND ($3::bigint=0 OR EXISTS(SELECT 1 FROM channel_groups cg WHERE cg.group_id=s.group_id AND cg.channel_id=$3)
 OR (s.group_id IS NULL AND EXISTS(SELECT 1 FROM channel_operation_members m WHERE m.channel_id=$3 AND m.account_id=s.account_id)))
 AND ($4::bigint=0 OR EXISTS(SELECT 1 FROM channel_operation_members m WHERE m.channel_id=$3 AND m.account_id=s.account_id AND m.upstream_id=$4)
 OR ($4=-1 AND NOT EXISTS(SELECT 1 FROM channel_operation_members m WHERE m.channel_id=$3 AND m.account_id=s.account_id)))
 AND ($5::bigint=0 OR s.account_id=$5) AND ($6::bigint=0 OR s.group_id=$6)`

func (r *channelRepository) ReadChannelOperations(ctx context.Context, f service.ChannelOperationsFilter) *service.ChannelOperationsSnapshot {
	out := &service.ChannelOperationsSnapshot{From: f.From, Until: f.Until, ModelScope: f.ModelScope,
		Scope: service.ChannelOperationsScope{ChannelID: f.ChannelID, UpstreamID: f.UpstreamID, AccountID: f.AccountID,
			GroupID: f.GroupID, Model: f.Model, ModelScope: f.ModelScope, Dimension: f.Dimension}}
	dimension, names := `COALESCE(channel_id,0)`, `COALESCE((SELECT name FROM channels WHERE id=b.dimension_id),'Unattributed channel')`
	switch f.Dimension {
	case "upstream":
		dimension, names = `operation_upstream_id`, `COALESCE((SELECT name FROM channel_operation_upstreams WHERE id=b.dimension_id),'Unassigned supplier')`
	case "account":
		dimension, names = `account_id`, `COALESCE((SELECT name FROM accounts WHERE id=b.dimension_id),'Deleted account')`
	}
	aggregate := `count(*) AS requests,count(*) FILTER(WHERE ttft IS NOT NULL) AS samples,
 count(*) FILTER(WHERE stream AND measured AND ttft IS NULL) AS unknown_samples,count(duration) AS duration_samples,
 percentile_cont(.5) WITHIN GROUP(ORDER BY ttft) AS p50_ms,percentile_cont(.9) WITHIN GROUP(ORDER BY ttft) AS p90_ms,
 percentile_cont(.95) WITHIN GROUP(ORDER BY ttft) AS p95_ms,percentile_cont(.9) WITHIN GROUP(ORDER BY duration) AS duration_p90_ms,
 COALESCE(sum(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1)),0) AS account_cost,
 COALESCE(sum(actual_cost),0) AS user_cost,max(created_at) AS latest_sample_at`
	args := []any{f.From, f.Until, f.ChannelID, f.UpstreamID, f.AccountID, f.GroupID, f.Model, f.ModelScope, f.PageSize, (f.Page - 1) * f.PageSize}
	out.Usage = r.operationsSection(ctx, channelOperationsUsageBase+`, buckets AS (SELECT `+dimension+` AS dimension_id,`+aggregate+` FROM measured GROUP BY `+dimension+`),
 summary AS (SELECT `+aggregate+` FROM measured), lineage AS (
 SELECT public_model,model AS recorded_model,upstream_model,model_mapping_chain,count(*) AS samples FROM measured
 GROUP BY public_model,model,upstream_model,model_mapping_chain)
 SELECT jsonb_build_object('summary',(SELECT to_jsonb(s) FROM summary s),'total',(SELECT count(*) FROM buckets),
 'rows',COALESCE((SELECT jsonb_agg(to_jsonb(x)) FROM (SELECT b.*,`+names+` AS name FROM buckets b ORDER BY requests DESC,dimension_id LIMIT $9 OFFSET $10)x),'[]'::jsonb),
 'lineage',COALESCE((SELECT jsonb_agg(to_jsonb(l)) FROM (SELECT * FROM lineage ORDER BY samples DESC,public_model,recorded_model,upstream_model,model_mapping_chain LIMIT 50)l),'[]'::jsonb),
 'lineage_total',(SELECT count(*) FROM lineage))`, args...)
	if f.Model != "" && f.ModelScope != "upstream" {
		out.Quality = service.ChannelOperationsSection{Status: "unsupported_scope", Data: json.RawMessage("null"), QueriedAt: time.Now().UTC()}
	} else {
		qualityArgs := []any{f.From, f.Until, f.ChannelID, f.UpstreamID, f.AccountID, f.GroupID, f.Model, f.QualityBefore, f.PageSize}
		out.Quality = r.operationsSection(ctx, `WITH scoped AS (SELECT s.* FROM upstream_quality_samples s WHERE s.sampled_at >= $1 AND s.sampled_at < $2 AND s.kind='pelican'`+channelOperationsCurrentScope+`
 AND ($7='' OR s.model=$7)) SELECT jsonb_build_object('summary',(SELECT jsonb_build_object('normal',count(*) FILTER(WHERE verdict='normal'),
 'degraded',count(*) FILTER(WHERE verdict='degraded'),'unknown',count(*) FILTER(WHERE verdict='unknown'),'failed',count(*) FILTER(WHERE verdict='failed')) FROM scoped),
 'rows',COALESCE((SELECT jsonb_agg(to_jsonb(x)) FROM (SELECT id,account_id,model,effort,verdict,reason,sampled_at FROM scoped WHERE ($8::bigint=0 OR id<$8) ORDER BY id DESC LIMIT $9)x),'[]'::jsonb),
 'has_more',(SELECT count(*)>$9 FROM scoped WHERE ($8::bigint=0 OR id<$8)))`, qualityArgs...)
	}
	out.Errors = r.operationsSection(ctx, `WITH scoped AS (SELECT s.* FROM ops_error_logs s WHERE s.created_at >= $1 AND s.created_at < $2`+channelOperationsCurrentScope+`
 AND ($7='' OR CASE $8 WHEN 'public' THEN COALESCE(NULLIF(BTRIM(s.requested_model),''),s.model) WHEN 'recorded' THEN s.model WHEN 'upstream' THEN s.upstream_model END=$7)),
 buckets AS (SELECT error_phase,error_type,status_code,count(*) AS records FROM scoped GROUP BY error_phase,error_type,status_code)
 SELECT jsonb_build_object('records',(SELECT count(*) FROM scoped),'bucket_total',(SELECT count(*) FROM buckets),
 'rows',COALESCE((SELECT jsonb_agg(to_jsonb(x)) FROM (SELECT * FROM buckets ORDER BY records DESC,error_phase,error_type,status_code LIMIT 50)x),'[]'::jsonb))`, args[:8]...)
	out.Capacity = r.operationsSection(ctx, `SELECT jsonb_build_object('accounts',count(*),'schedulable',count(*) FILTER(WHERE a.schedulable AND a.status='active'),
 'configured_concurrency',COALESCE(sum(a.concurrency),0)) FROM accounts a WHERE a.deleted_at IS NULL
 AND ($1::bigint=0 OR EXISTS(SELECT 1 FROM account_groups ag JOIN channel_groups cg ON cg.group_id=ag.group_id WHERE ag.account_id=a.id AND cg.channel_id=$1))
 AND ($2::bigint=0 OR EXISTS(SELECT 1 FROM channel_operation_members m WHERE m.channel_id=$1 AND m.account_id=a.id AND m.upstream_id=$2)
 OR ($2=-1 AND NOT EXISTS(SELECT 1 FROM channel_operation_members m WHERE m.channel_id=$1 AND m.account_id=a.id)))
 AND ($3::bigint=0 OR a.id=$3) AND ($4::bigint=0 OR EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=$4))`, f.ChannelID, f.UpstreamID, f.AccountID, f.GroupID)
	return out
}

var _ service.ChannelOperationsRepository = (*channelRepository)(nil)
