package repository

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

var claimPelicanSwitchTestScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
local raw = redis.call('GET', KEYS[2])
if not raw or cjson.decode(raw).changed_at ~= ARGV[2] then return 0 end
if redis.call('EXISTS', KEYS[3]) == 1 then return 0 end
redis.call('ZREMRANGEBYSCORE', KEYS[4], '-inf', tonumber(ARGV[3])-3600000)
if tonumber(ARGV[4]) < 1 or redis.call('ZCARD', KEYS[4]) >= tonumber(ARGV[4]) then return 0 end
redis.call('SET', KEYS[3], '1', 'EX', 3600)
redis.call('ZADD', KEYS[4], ARGV[3], KEYS[3] .. ':' .. ARGV[3])
redis.call('EXPIRE', KEYS[4], 3600)
return 1
`)

func (c *gatewayCache) ClaimPelicanSwitchTest(ctx context.Context, groupID int64, publicModel, bindingModel, effort string, change *service.GroupModelSchedulingTransition, now time.Time, limit int) (bool, error) {
	if change == nil || change.AccountID <= 0 || change.PreviousAccountID <= 0 || change.AccountID == change.PreviousAccountID || change.ChangedAt.After(now) || now.Sub(change.ChangedAt) >= time.Hour {
		return false, nil
	}
	modelKey := service.GroupModelSchedulingKeyPrefix + base64.RawURLEncoding.EncodeToString([]byte(bindingModel))
	binding := buildSessionKey(groupID, modelKey)
	scope, err := json.Marshal([]any{groupID, publicModel, change.AccountID, effort})
	if err != nil {
		return false, err
	}
	budget, err := json.Marshal([]any{groupID, publicModel})
	if err != nil {
		return false, err
	}
	keys := []string{binding, binding + ":transition", fmt.Sprintf("pelican_switch_test:cooldown:%x", sha256.Sum256(scope)), fmt.Sprintf("pelican_switch_test:budget:%x", sha256.Sum256(budget))}
	result, err := claimPelicanSwitchTestScript.Run(ctx, c.rdb, keys, change.AccountID, change.ChangedAt.Format(time.RFC3339Nano), now.UnixMilli(), limit).Int()
	return result == 1, err
}

// Lease the existing plan only after checking retained evidence. No cron or
// next_run_at write is needed. Sibling plans cannot duplicate switch samples.
func (r *pelicanGroupTestRepository) ClaimSwitchTest(ctx context.Context, plan *service.PelicanGroupTestPlan, accountID int64, model string, now, until time.Time) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('pelican-switch:'||$1::text||':'||$2,0))`, plan.GroupID, plan.ModelID); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE pelican_group_test_plans p SET running_until=$3
 WHERE p.id=$1 AND p.enabled AND p.updated_at=$4 AND p.group_id=$5 AND p.model_id=$6
 AND (p.running_until IS NULL OR p.running_until<$2)
 AND EXISTS (SELECT 1 FROM groups g WHERE g.id=p.group_id AND g.deleted_at IS NULL
   AND g.status='active' AND g.account_scheduling_mode='smart_channel')
 AND NOT EXISTS (SELECT 1 FROM pelican_group_test_plans other WHERE other.group_id=p.group_id
   AND other.model_id=p.model_id AND COALESCE(other.pelican_config->>'reasoning_effort','')=$8 AND other.running_until>=$2)
 AND NOT EXISTS (SELECT 1 FROM scheduled_test_plans other WHERE other.account_id=$7
   AND COALESCE(NULLIF(other.pelican_config->>'model_id',''),other.model_id)=$9
   AND other.pelican_config IS NOT NULL AND other.running_until>=$2)
 AND NOT EXISTS (SELECT 1 FROM upstream_quality_samples s WHERE s.account_id=$7 AND s.model=$9
   AND (s.group_id=p.group_id OR s.group_id IS NULL) AND s.effort=$8 AND s.kind='pelican'
   AND s.sampled_at>=$2::timestamptz-INTERVAL '1 hour' AND s.sampled_at<=$2
   AND (s.verdict IN ('normal','degraded') OR s.sampled_at>=$2::timestamptz-INTERVAL '5 minutes'))`,
		plan.ID, now, until, plan.UpdatedAt, plan.GroupID, plan.ModelID, accountID, plan.PelicanConfig.ReasoningEffort, model)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, tx.Commit()
}

func (r *pelicanGroupTestRepository) ReleaseSwitchTest(ctx context.Context, planID int64, until time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE pelican_group_test_plans SET running_until=NULL WHERE id=$1 AND running_until=$2`, planID, until)
	return err
}

func (r *pelicanGroupTestRepository) HasRecentSwitchTest(ctx context.Context, groupID, accountID int64, model, effort string, now time.Time) (bool, error) {
	var recent bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM upstream_quality_samples s
 WHERE s.account_id=$2 AND s.model=$3 AND (s.group_id=$1 OR s.group_id IS NULL)
 AND s.effort=$4 AND s.kind='pelican' AND s.sampled_at>=$5::timestamptz-INTERVAL '1 hour'
 AND s.sampled_at<=$5 AND (s.verdict IN ('normal','degraded') OR s.sampled_at>=$5::timestamptz-INTERVAL '5 minutes'))`,
		groupID, accountID, model, effort, now).Scan(&recent)
	return recent, err
}
