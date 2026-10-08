package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpstreamBalanceWriteRejectsStaleIdentityAndRollsBackOutboxFailure(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		t.Run(string(rune('0'+affected)), func(t *testing.T) {
			client, mock := newOllamaCloudUsageRepositoryTestClient(t)
			account := &service.Account{ID: 27, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}, Extra: map[string]any{}}
			state := &service.UpstreamBalanceState{UpstreamBalanceConfig: service.DefaultUpstreamBalanceConfig()}
			mock.ExpectBegin()
			mock.ExpectExec(`(?s)UPDATE accounts.*credentials = \$5::jsonb.*COALESCE\(extra -> 'upstream_balance_probe', 'null'::jsonb\) = \$7::jsonb`).
				WithArgs(sqlmock.AnyArg(), int64(27), service.PlatformOpenAI, service.AccountTypeAPIKey, `{"api_key":"test-key"}`, nil, "null").
				WillReturnResult(sqlmock.NewResult(0, affected))
			if affected == 1 {
				mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).WillReturnError(context.Canceled)
			}
			mock.ExpectRollback()
			repo := newAccountRepositoryWithSQL(client, nil, nil)
			err := repo.UpdateUpstreamBalanceState(context.Background(), account, state)
			if affected == 0 {
				require.ErrorIs(t, err, service.ErrUpstreamBillingProbeIdentityChanged)
			} else {
				require.ErrorIs(t, err, context.Canceled)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestBalanceNormalEditPreservesConfigAndInvalidatesChangedIdentity(t *testing.T) {
	for _, unchanged := range []bool{true, false} {
		client, mock := newOllamaCloudUsageRepositoryTestClient(t)
		saved := service.UpstreamBalanceState{UpstreamBalanceConfig: service.DefaultUpstreamBalanceConfig(), Snapshot: &service.UpstreamBalanceSnapshot{Status: "ok"}}
		saved.Enabled = true
		raw, err := json.Marshal(saved)
		require.NoError(t, err)
		mock.ExpectQuery(`(?s)SELECT.*FOR NO KEY UPDATE`).
			WithArgs(int64(27), service.PlatformOpenAI, service.AccountTypeAPIKey, `{"api_key":"test-key"}`, nil).
			WillReturnRows(sqlmock.NewRows(openCodeGoMergeMockColumns()).AddRow(unchanged, false, true, nil, nil, nil, nil, nil, nil, false, nil, nil, raw))
		account := &service.Account{ID: 27, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}, Extra: map[string]any{service.UpstreamBalanceProbeExtraKey: map[string]any{"enabled": false}}}
		extra, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, nil, nil)
		require.NoError(t, err)
		state := service.DecodeUpstreamBalanceState(extra)
		require.True(t, state.Enabled)
		require.Equal(t, unchanged, state.Snapshot != nil)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestSchedulerProjectionKeepsBalancePauseState(t *testing.T) {
	state := service.UpstreamBalanceState{UpstreamBalanceConfig: service.DefaultUpstreamBalanceConfig()}
	account := service.Account{ID: 27, Type: service.AccountTypeAPIKey, Extra: map[string]any{service.UpstreamBalanceProbeExtraKey: state, "unrelated": "drop"}}
	metadata := buildSchedulerMetadataAccount(account)
	require.Equal(t, state, metadata.Extra[service.UpstreamBalanceProbeExtraKey])
	require.NotContains(t, metadata.Extra, "unrelated")
}
