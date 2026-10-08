package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *accountRepository) UpdateUpstreamBalanceState(ctx context.Context, account *service.Account, state *service.UpstreamBalanceState) error {
	if account == nil || state == nil {
		return service.ErrAccountNilInput
	}
	if err := state.Validate(); err != nil {
		return err
	}
	if dbent.TxFromContext(ctx) != nil {
		return r.updateUpstreamBalanceStateInTx(ctx, account, state)
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.updateUpstreamBalanceStateInTx(dbent.NewTxContext(ctx, tx), account, state); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.syncSchedulerAccountSnapshot(ctx, account.ID)
	return nil
}

func (r *accountRepository) updateUpstreamBalanceStateInTx(ctx context.Context, account *service.Account, state *service.UpstreamBalanceState) error {
	client := clientFromContext(ctx, r.client)
	proxyMatches, err := lockAndMatchProbeProxyIdentity(ctx, client, account)
	if err != nil {
		return err
	}
	if !proxyMatches {
		return service.ErrUpstreamBillingProbeIdentityChanged
	}
	payload, err := json.Marshal(map[string]any{service.UpstreamBalanceProbeExtraKey: state})
	if err != nil {
		return err
	}
	credentials, err := json.Marshal(account.Credentials)
	if err != nil {
		return err
	}
	expected, err := json.Marshal(account.Extra[service.UpstreamBalanceProbeExtraKey])
	if err != nil {
		return err
	}
	var proxyID any
	if account.ProxyID != nil {
		proxyID = *account.ProxyID
	}
	result, err := client.ExecContext(ctx, `
		UPDATE accounts SET extra = COALESCE(extra, '{}'::jsonb) || $1::jsonb, updated_at = NOW()
		WHERE id = $2 AND platform = $3 AND type = $4 AND credentials = $5::jsonb
		  AND proxy_id IS NOT DISTINCT FROM $6
		  AND COALESCE(extra -> 'upstream_balance_probe', 'null'::jsonb) = $7::jsonb
		  AND deleted_at IS NULL
	`, string(payload), account.ID, account.Platform, account.Type, string(credentials), proxyID, string(expected))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return service.ErrUpstreamBillingProbeIdentityChanged
	}
	return enqueueSchedulerOutbox(ctx, client, service.SchedulerOutboxEventAccountChanged, &account.ID, nil, nil)
}

func (r *accountRepository) ListDueUpstreamBalanceAccounts(ctx context.Context, now time.Time, limit int) ([]service.Account, error) {
	if limit <= 0 {
		return []service.Account{}, nil
	}
	if r.sql == nil {
		return nil, errors.New("account repository SQL executor not configured")
	}
	rows, err := r.sql.QueryContext(ctx, `
		WITH candidates AS (
			SELECT id, extra #>> '{upstream_balance_probe,snapshot,next_probe_at}' AS next_probe_at
			FROM accounts
			WHERE deleted_at IS NULL AND status = 'active' AND type = 'apikey'
			  AND extra @> '{"upstream_balance_probe":{"enabled":true}}'::jsonb
		), parsed AS MATERIALIZED (
			SELECT id, next_probe_at,
				jsonb_path_query_first_tz(
					jsonb_build_object('value', replace(regexp_replace(regexp_replace(next_probe_at,
						'(\.[0-9]{6})[0-9]+(Z|[+-][0-9]{2}:[0-9]{2})$', '\1\2'), 'Z$', '+00:00'), 'T', ' ')),
					'$.value.datetime()', '{}'::jsonb, true) #>> '{}' AS parsed_at
			FROM candidates
		), normalized AS (
			SELECT id, parsed_at,
				next_probe_at ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$'
				AND parsed_at IS NOT NULL AS valid FROM parsed
		)
		SELECT id FROM normalized
		WHERE CASE WHEN valid THEN parsed_at::timestamptz <= $1 ELSE TRUE END
		ORDER BY CASE WHEN valid THEN parsed_at::timestamptz END ASC NULLS FIRST, id
		LIMIT $2
	`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []service.Account{}, nil
	}
	accounts, err := r.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make([]service.Account, 0, len(accounts))
	for _, account := range accounts {
		if account != nil {
			result = append(result, *account)
		}
	}
	return result, nil
}
