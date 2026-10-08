package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func marshalPelicanConfig(config *service.PelicanTestConfig) any {
	if config == nil {
		return nil
	}
	data, _ := json.Marshal(config)
	return string(data)
}

// Account advisory lock and the persisted lease prevent two plans for the same
// account starting together, including on different application replicas.
func (r *scheduledTestPlanRepository) ClaimPelican(ctx context.Context, plan *service.ScheduledTestPlan, now, until, next time.Time) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('pelican-account:' || $1::text, 0))`, plan.AccountID).Scan(&locked); err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE scheduled_test_plans SET running_until=$3,next_run_at=$4
 WHERE id=$1 AND enabled=true AND next_run_at<=$2
 AND (running_until IS NULL OR running_until<$2) AND updated_at=$5 AND next_run_at=$6
 AND EXISTS(SELECT 1 FROM accounts WHERE accounts.id=account_id AND deleted_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM scheduled_test_plans other WHERE other.account_id=scheduled_test_plans.account_id
 AND other.id<>scheduled_test_plans.id AND other.pelican_config IS NOT NULL AND other.running_until>$2)`, plan.ID, now, until, next, plan.UpdatedAt, plan.NextRunAt)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n == 1, nil
}
func (r *scheduledTestPlanRepository) FinishPelican(ctx context.Context, id int64, until, finished time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_test_plans SET running_until=NULL,last_run_at=$3 WHERE id=$1 AND running_until=$2`, id, until, finished)
	return err
}
func (r *scheduledTestResultRepository) PruneExpiredPelican(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM scheduled_test_results WHERE id IN(
 SELECT results.id FROM scheduled_test_results results JOIN scheduled_test_plans plans ON plans.id=results.plan_id
 WHERE plans.pelican_config IS NOT NULL AND results.created_at<$1 ORDER BY results.created_at LIMIT 1000)`, before)
	return err
}
func (r *scheduledTestResultRepository) GetResult(ctx context.Context, planID, resultID int64) (*service.ScheduledTestResult, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,plan_id,status,response_text,error_message,quality_verdict,quality_reason,judge_account_id,latency_ms,started_at,finished_at,created_at,pelican_config
 FROM scheduled_test_results WHERE plan_id=$1 AND id=$2`, planID, resultID)
	result := &service.ScheduledTestResult{}
	var config []byte
	if err := row.Scan(&result.ID, &result.PlanID, &result.Status, &result.ResponseText, &result.ErrorMessage, &result.QualityVerdict, &result.QualityReason, &result.JudgeAccountID, &result.LatencyMs, &result.StartedAt, &result.FinishedAt, &result.CreatedAt, &config); err != nil {
		return nil, err
	}
	if len(config) > 0 {
		if err := json.Unmarshal(config, &result.PelicanConfig); err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (r *scheduledTestResultRepository) ListPelicanHistory(ctx context.Context, beforeID int64, limit int) ([]*service.PelicanHistoryResult, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT results.id,results.plan_id,results.status,results.error_message,results.latency_ms,results.started_at,results.finished_at,results.created_at,results.pelican_config,plans.account_id,COALESCE(accounts.name,'')
 FROM scheduled_test_results results JOIN scheduled_test_plans plans ON plans.id=results.plan_id
 LEFT JOIN accounts ON accounts.id=plans.account_id
 WHERE results.pelican_config IS NOT NULL AND ($1::bigint=0 OR results.id<$1) ORDER BY results.id DESC LIMIT $2`, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]*service.PelicanHistoryResult, 0)
	for rows.Next() {
		item := &service.PelicanHistoryResult{}
		var config []byte
		if err := rows.Scan(&item.ID, &item.PlanID, &item.Status, &item.ErrorMessage, &item.LatencyMs, &item.StartedAt, &item.FinishedAt, &item.CreatedAt, &config, &item.AccountID, &item.AccountName); err != nil {
			return nil, err
		}
		if len(config) > 0 {
			if err := json.Unmarshal(config, &item.PelicanConfig); err != nil {
				return nil, err
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
