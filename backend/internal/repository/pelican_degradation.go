package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageLogRepository) GetPelicanDegradationStates(ctx context.Context, groupID int64, model string) (map[int64]*service.PelicanDegradationState, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT account_id,status,sampled_at,due_at
 FROM pelican_degradation_states WHERE group_id=$1 AND model=$2 AND sampled_at>=NOW()-INTERVAL '24 hours'`, groupID, model)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	states := make(map[int64]*service.PelicanDegradationState)
	for rows.Next() {
		var id int64
		state := &service.PelicanDegradationState{}
		if err := rows.Scan(&id, &state.Status, &state.SampledAt, &state.DueAt); err != nil {
			return nil, err
		}
		states[id] = state
	}
	return states, rows.Err()
}

func (r *pelicanGroupTestRepository) ListDegradationDue(ctx context.Context, now time.Time) ([]service.PelicanDegradationWork, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT p.id,d.group_id,d.account_id,d.sample_id,d.model,d.action,d.sampled_at,p.updated_at
 FROM pelican_degradation_states d JOIN groups g ON g.id=d.group_id
 JOIN LATERAL (SELECT id,updated_at FROM pelican_group_test_plans WHERE group_id=d.group_id AND enabled
 AND model_id IN (d.public_model,d.model) ORDER BY (model_id=d.public_model) DESC,id LIMIT 1) p ON true
 WHERE d.action<>'' AND d.due_at<=$1 AND (d.running_until IS NULL OR d.running_until<$1)
 AND d.sampled_at>=$1::timestamptz-INTERVAL '24 hours' AND g.deleted_at IS NULL AND g.status='active'
 AND g.account_scheduling_mode IN ('fastest_failover','quality_preferred','model_auto')
 AND EXISTS (SELECT 1 FROM account_groups ag WHERE ag.group_id=d.group_id AND ag.account_id=d.account_id)
 ORDER BY d.due_at,d.group_id,d.model,d.account_id`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	work := []service.PelicanDegradationWork{}
	for rows.Next() {
		var item service.PelicanDegradationWork
		if err := rows.Scan(&item.PlanID, &item.GroupID, &item.AccountID, &item.SampleID, &item.Model, &item.Action, &item.SampledAt, &item.PlanUpdatedAt); err != nil {
			return nil, err
		}
		work = append(work, item)
	}
	return work, rows.Err()
}

func (r *pelicanGroupTestRepository) ClaimDegradation(ctx context.Context, work service.PelicanDegradationWork, now, until time.Time, limit int) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	// The lock is scoped to the same group/model as the hourly limit, including
	// when several plans or account confirmations become due simultaneously.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text||':'||$2,0))`, work.GroupID, work.Model); err != nil {
		return false, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT account_id FROM pelican_degradation_states
 WHERE group_id=$1 AND model=$2 AND account_id=$3 AND sample_id=$4 AND action=$5 AND due_at<=$6
 AND (running_until IS NULL OR running_until<$6) FOR UPDATE`, work.GroupID, work.Model, work.AccountID, work.SampleID, work.Action, now).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var count int
	var oldest sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),MIN(started_at) FROM pelican_degradation_attempts
 WHERE group_id=$1 AND model=$2 AND started_at>$3::timestamptz-INTERVAL '1 hour'`, work.GroupID, work.Model, now).Scan(&count, &oldest); err != nil {
		return false, err
	}
	if limit < 1 || count >= limit {
		next := now.Add(time.Hour)
		if oldest.Valid {
			next = oldest.Time.Add(time.Hour)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pelican_degradation_states SET due_at=$4 WHERE group_id=$1 AND model=$2 AND account_id=$3`, work.GroupID, work.Model, work.AccountID, next); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	result, err := tx.ExecContext(ctx, `UPDATE pelican_group_test_plans SET running_until=$3
 WHERE id=$1 AND group_id=$4 AND enabled AND updated_at=$5 AND (running_until IS NULL OR running_until<$2)`, work.PlanID, now, until, work.GroupID, work.PlanUpdatedAt)
	if err != nil {
		return false, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pelican_degradation_states SET running_until=$4
 WHERE group_id=$1 AND model=$2 AND account_id=$3`, work.GroupID, work.Model, work.AccountID, until); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pelican_degradation_attempts(group_id,model,started_at) VALUES ($1,$2,$3)`, work.GroupID, work.Model, now); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r *pelicanGroupTestRepository) FinishDegradation(ctx context.Context, work service.PelicanDegradationWork, until, now time.Time, completed bool) error {
	// A newer sample may already have confirmed degradation or recovered it.
	// Clear only our lease; do not erase the follow-up queued by that sample.
	_, err := r.db.ExecContext(ctx, `UPDATE pelican_degradation_states SET running_until=NULL,
 action=CASE WHEN sample_id=$4 AND $5='replacement' AND $8 THEN '' ELSE action END,
 due_at=CASE WHEN sample_id=$4 AND ($5='retry' OR NOT $8) THEN $7::timestamptz+INTERVAL '5 minutes' ELSE due_at END
 WHERE group_id=$1 AND model=$2 AND account_id=$3 AND running_until=$6`, work.GroupID, work.Model, work.AccountID, work.SampleID, work.Action, until, now, completed)
	return err
}
