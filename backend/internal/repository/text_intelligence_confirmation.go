package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *pelicanGroupTestRepository) ListTextIntelligenceDue(ctx context.Context, now time.Time) ([]service.TextIntelligenceRetry, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT s.plan_id,s.account_id,s.result_id,p.updated_at
 FROM text_intelligence_states s JOIN pelican_group_test_plans p ON p.id=s.plan_id
 JOIN groups g ON g.id=p.group_id
 WHERE s.due_at<=$1 AND (s.running_until IS NULL OR s.running_until<$1)
 AND s.sampled_at>=$1::timestamptz-INTERVAL '24 hours' AND p.enabled
 AND g.deleted_at IS NULL AND g.status='active'
 AND s.config=text_intelligence_config(p.pelican_config || jsonb_build_object('model_id',p.model_id))
 AND EXISTS (SELECT 1 FROM account_groups ag WHERE ag.group_id=p.group_id AND ag.account_id=s.account_id)
 ORDER BY s.due_at,s.plan_id,s.account_id LIMIT 100`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var work []service.TextIntelligenceRetry
	for rows.Next() {
		var item service.TextIntelligenceRetry
		if err := rows.Scan(&item.PlanID, &item.AccountID, &item.ResultID, &item.PlanUpdatedAt); err != nil {
			return nil, err
		}
		work = append(work, item)
	}
	return work, rows.Err()
}

func (r *pelicanGroupTestRepository) ClaimTextIntelligenceRetry(ctx context.Context, work service.TextIntelligenceRetry, now, until time.Time) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Only one Candy/custom confirmation call may consume upstream capacity at once,
	// even when several groups and application replicas report mismatches together.
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('text-intelligence-retry-global',0))"); err != nil {
		return false, err
	}
	var active bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM text_intelligence_states WHERE running_until>$1)", now).Scan(&active); err != nil {
		return false, err
	}
	if active {
		return false, nil
	}
	// Reuse the plan lease: a retry cannot overlap its scheduled/manual samples.
	claimed, err := tx.ExecContext(ctx, `UPDATE pelican_group_test_plans SET running_until=$3
 WHERE id=$1 AND enabled AND updated_at=$4 AND (running_until IS NULL OR running_until<$2)`, work.PlanID, now, until, work.PlanUpdatedAt)
	if err != nil {
		return false, err
	}
	if n, err := claimed.RowsAffected(); err != nil || n != 1 {
		return false, err
	}
	claimed, err = tx.ExecContext(ctx, `UPDATE text_intelligence_states SET running_until=$5
 WHERE plan_id=$1 AND account_id=$2 AND result_id=$3 AND due_at<=$4
 AND (running_until IS NULL OR running_until<$4)`, work.PlanID, work.AccountID, work.ResultID, now, until)
	if err != nil {
		return false, err
	}
	if n, err := claimed.RowsAffected(); err != nil || n != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func (r *pelicanGroupTestRepository) FinishTextIntelligenceRetry(ctx context.Context, work service.TextIntelligenceRetry, until, _ time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE text_intelligence_states SET running_until=NULL,
	 due_at=NULL
	 WHERE plan_id=$1 AND account_id=$2 AND running_until=$3`, work.PlanID, work.AccountID, until)
	return err
}
