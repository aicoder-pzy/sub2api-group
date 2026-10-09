package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type upstreamBalanceNotificationRepository struct{ db *sql.DB }

func NewUpstreamBalanceNotificationRepository(db *sql.DB) service.UpstreamBalanceNotificationRepository {
	return &upstreamBalanceNotificationRepository{db: db}
}

func (r *upstreamBalanceNotificationRepository) ListAccounts(ctx context.Context) ([]service.UpstreamBalanceNotificationAccount, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name,extra FROM accounts WHERE deleted_at IS NULL AND status='active' AND type='apikey' AND extra @> '{"upstream_balance_probe":{"enabled":true}}'::jsonb`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.UpstreamBalanceNotificationAccount{}
	for rows.Next() {
		var account service.UpstreamBalanceNotificationAccount
		var raw []byte
		if err := rows.Scan(&account.ID, &account.Name, &raw); err != nil {
			return nil, err
		}
		var extra map[string]any
		if err := json.Unmarshal(raw, &extra); err != nil {
			return nil, err
		}
		account.State = service.DecodeUpstreamBalanceState(extra)
		out = append(out, account)
	}
	return out, rows.Err()
}

func (r *upstreamBalanceNotificationRepository) Observe(ctx context.Context, observation service.UpstreamBalanceNotificationObservation, notifyRecovery bool) error {
	// PostgreSQL rounds timestamps to microseconds, including the initial monitor row.
	observation.ObservedAt = observation.ObservedAt.Round(time.Microsecond)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO upstream_balance_notification_monitors(account_id,scope,currency,criteria,adverse,episode_id,observed_at) VALUES($1,$2,$3,$4,FALSE,'',$5) ON CONFLICT DO NOTHING`, observation.AccountID, observation.Scope, observation.Currency, observation.Criteria, observation.ObservedAt); err != nil {
		return err
	}
	var criteria, episode string
	var adverse bool
	var observed time.Time
	if err = tx.QueryRowContext(ctx, `SELECT criteria,adverse,episode_id,observed_at FROM upstream_balance_notification_monitors WHERE account_id=$1 AND scope=$2 AND currency=$3 FOR UPDATE`, observation.AccountID, observation.Scope, observation.Currency).Scan(&criteria, &adverse, &episode, &observed); err != nil {
		return err
	}
	if observation.ObservedAt.Before(observed) {
		return tx.Commit()
	}
	if criteria != observation.Criteria {
		adverse, episode = false, ""
	}
	nextAdverse := observation.Remaining <= observation.Threshold
	phase := ""
	if nextAdverse && !adverse {
		episode, phase = uuid.NewString(), "low"
	}
	if !nextAdverse && adverse && notifyRecovery {
		phase = "recovery"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE upstream_balance_notification_monitors SET criteria=$4,adverse=$5,episode_id=$6,observed_at=$7 WHERE account_id=$1 AND scope=$2 AND currency=$3`, observation.AccountID, observation.Scope, observation.Currency, observation.Criteria, nextAdverse, episode, observation.ObservedAt); err != nil {
		return err
	}
	if phase != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO upstream_balance_notification_events(id,episode_id,phase,account_id,account_name,scope,currency,remaining,threshold,criteria,observed_at,fresh_until) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(episode_id,phase) DO NOTHING`, uuid.NewString(), episode, phase, observation.AccountID, observation.AccountName, observation.Scope, observation.Currency, observation.Remaining, observation.Threshold, observation.Criteria, observation.ObservedAt, observation.FreshUntil)
		if err != nil {
			return err
		}
	}
	if episode != "" {
		phase := "recovery"
		if nextAdverse {
			phase = "low"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE upstream_balance_notification_events SET remaining=$3,observed_at=$4,fresh_until=$5 WHERE episode_id=$1 AND phase=$2 AND status IN ('pending','failed')`, episode, phase, observation.Remaining, observation.ObservedAt, observation.FreshUntil); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const balanceNotificationEventColumns = `e.id,e.episode_id,e.phase,e.account_id,e.account_name,e.scope,e.currency,e.remaining,e.threshold,e.criteria,e.observed_at,e.fresh_until,e.status,e.deliveries,e.attempts,e.lease,e.created_at`

func scanBalanceNotificationEvent(row interface{ Scan(...any) error }) (service.UpstreamBalanceNotificationEvent, error) {
	var event service.UpstreamBalanceNotificationEvent
	var deliveries []byte
	err := row.Scan(&event.ID, &event.EpisodeID, &event.Phase, &event.AccountID, &event.AccountName, &event.Scope, &event.Currency, &event.Remaining, &event.Threshold, &event.Criteria, &event.ObservedAt, &event.FreshUntil, &event.Status, &deliveries, &event.Attempts, &event.Lease, &event.CreatedAt)
	if err == nil {
		err = json.Unmarshal(deliveries, &event.Deliveries)
	}
	return event, err
}

func (r *upstreamBalanceNotificationRepository) Claim(ctx context.Context, now time.Time, limit int) ([]service.UpstreamBalanceNotificationEvent, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `UPDATE upstream_balance_notification_events e SET status='suppressed',lease='',lease_until=NULL WHERE (status IN ('pending','failed') OR status='sending' AND lease_until <= $1) AND NOT EXISTS (SELECT 1 FROM upstream_balance_notification_monitors m WHERE m.account_id=e.account_id AND m.scope=e.scope AND m.currency=e.currency AND m.criteria=e.criteria AND m.episode_id=e.episode_id AND m.adverse=(e.phase='low'))`, now)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `WITH due AS (SELECT id FROM upstream_balance_notification_events WHERE fresh_until>$1 AND attempts<3 AND next_attempt_at <= $1 AND (status IN ('pending','failed') OR status='sending' AND lease_until <= $1) ORDER BY next_attempt_at,created_at FOR UPDATE SKIP LOCKED LIMIT $2) UPDATE upstream_balance_notification_events e SET status='sending',attempts=attempts+1,lease=$3,lease_until=$1+INTERVAL '2 minutes' FROM due WHERE e.id=due.id RETURNING `+balanceNotificationEventColumns, now, limit, uuid.NewString())
	if err != nil {
		return nil, err
	}
	out := []service.UpstreamBalanceNotificationEvent{}
	for rows.Next() {
		event, err := scanBalanceNotificationEvent(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, event)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *upstreamBalanceNotificationRepository) SaveDelivery(ctx context.Context, event *service.UpstreamBalanceNotificationEvent, key string, delivery service.UpstreamBalanceNotificationDelivery) error {
	payload, err := json.Marshal(delivery)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE upstream_balance_notification_events SET deliveries=deliveries||jsonb_build_object($3::text,$4::jsonb) WHERE id=$1 AND lease=$2 AND status='sending' AND lease_until>NOW()`, event.ID, event.Lease, key, string(payload))
	return balanceNotificationLeaseResult(result, err)
}

func balanceNotificationLeaseResult(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("balance notification lease expired")
	}
	return nil
}

func (r *upstreamBalanceNotificationRepository) Complete(ctx context.Context, event *service.UpstreamBalanceNotificationEvent, status string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE upstream_balance_notification_events SET status=$3,attempts=CASE WHEN $3='pending' THEN GREATEST(attempts-1,0) ELSE attempts END,lease='',lease_until=NULL,next_attempt_at=NOW()+INTERVAL '5 minutes' WHERE id=$1 AND lease=$2 AND status='sending' AND lease_until>NOW()`, event.ID, event.Lease, status)
	return balanceNotificationLeaseResult(result, err)
}

func (r *upstreamBalanceNotificationRepository) History(ctx context.Context, limit int) ([]service.UpstreamBalanceNotificationEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+balanceNotificationEventColumns+` FROM upstream_balance_notification_events e ORDER BY created_at DESC,id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.UpstreamBalanceNotificationEvent{}
	for rows.Next() {
		event, err := scanBalanceNotificationEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (r *upstreamBalanceNotificationRepository) ReserveDestination(ctx context.Context, destination string) error {
	var at time.Time
	err := r.db.QueryRowContext(ctx, `INSERT INTO upstream_balance_notification_rate_limits(destination_hash,next_send_at) VALUES($1,NOW()+INTERVAL '3.1 seconds') ON CONFLICT(destination_hash) DO UPDATE SET next_send_at=GREATEST(NOW(),upstream_balance_notification_rate_limits.next_send_at)+INTERVAL '3.1 seconds' RETURNING next_send_at-INTERVAL '3.1 seconds'`, destination).Scan(&at)
	if err != nil {
		return err
	}
	if delay := time.Until(at); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
