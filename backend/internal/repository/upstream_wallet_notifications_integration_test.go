//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func walletNotificationTestAccount(t *testing.T) *service.Account {
	t.Helper()
	account := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "wallet-notification-test", Platform: "openai", Type: "apikey", Status: service.StatusActive, Credentials: map[string]any{"api_key": "test-key", "base_url": fmt.Sprintf("https://wallet-%d.example/v1", time.Now().UnixNano())}, Extra: map[string]any{}})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM scheduler_outbox WHERE account_id=$1`, account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id=$1`, account.ID)
	})
	return account
}

func TestNewAPIWalletSharedRotationCASAndCredentialInvalidation(t *testing.T) {
	ctx := context.Background()
	account := walletNotificationTestAccount(t)
	site, err := service.CanonicalNewAPISite(account.GetCredential("base_url"))
	require.NoError(t, err)
	repo := NewNewAPIAuthorizationRepository(integrationDB)
	profile := &service.NewAPISiteAuthorization{SiteURL: site, UserID: 7, Ciphertext: "cipher-one"}
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM new_api_site_authorizations WHERE site_url=$1`, site)
	})
	require.NoError(t, repo.Save(ctx, profile, []service.NewAPIBindingSave{{Account: account, TokenID: 3}}))
	binding, err := repo.GetBinding(ctx, account.ID)
	require.NoError(t, err)
	accounts := newAccountRepositoryWithSQL(testEntClient(t), integrationDB, nil)
	loaded, err := accounts.GetByID(ctx, account.ID)
	require.NoError(t, err)
	state := service.DecodeUpstreamBalanceState(loaded.Extra)
	require.True(t, state.Enabled)
	require.Equal(t, "newapi", state.Provider)
	value := 12.0
	state.Snapshot = &service.UpstreamBalanceSnapshot{Status: "ok", Amounts: []service.UpstreamBalanceAmount{{Scope: "wallet", Currency: "USD", Remaining: &value}}}
	require.NoError(t, repo.WriteSnapshot(ctx, loaded, binding, &state))
	updated, err := accounts.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, 12.0, *service.DecodeUpstreamBalanceState(updated.Extra).Snapshot.Amounts[0].Remaining)
	profile, err = repo.GetProfile(ctx, site, 7)
	require.NoError(t, err)
	profile.Ciphertext = "cipher-two"
	require.NoError(t, repo.Save(ctx, profile, []service.NewAPIBindingSave{{Account: updated, TokenID: 3}}))
	rotated, err := accounts.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Nil(t, service.DecodeUpstreamBalanceState(rotated.Extra).Snapshot)
	require.ErrorIs(t, repo.WriteSnapshot(ctx, rotated, binding, &state), service.ErrUpstreamBillingProbeIdentityChanged)
	require.ErrorIs(t, accounts.UpdateUpstreamBalanceProbeState(ctx, rotated, &state), service.ErrUpstreamBillingProbeIdentityChanged)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{api_key}','"changed"'::jsonb) WHERE id=$1`, account.ID)
	require.NoError(t, err)
	removed, err := repo.GetBinding(ctx, account.ID)
	require.NoError(t, err)
	require.Nil(t, removed)
}

func TestBalanceNotificationDurableEpisodesReceiptsAndExclusiveClaims(t *testing.T) {
	ctx := context.Background()
	account := walletNotificationTestAccount(t)
	repo := NewUpstreamBalanceNotificationRepository(integrationDB)
	now := time.Now().UTC()
	observation := service.UpstreamBalanceNotificationObservation{AccountID: account.ID, AccountName: account.Name, Scope: "wallet", Currency: "USD", Remaining: 1, Threshold: 5, Criteria: "test-criteria", ObservedAt: now, FreshUntil: now.Add(time.Hour)}
	require.NoError(t, repo.Observe(ctx, observation, true))
	require.NoError(t, repo.Observe(ctx, observation, true))
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM upstream_balance_notification_events WHERE account_id=$1`, account.ID).Scan(&count))
	require.Equal(t, 1, count)
	events, err := repo.Claim(ctx, now.Add(time.Second), 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	other, err := repo.Claim(ctx, now.Add(time.Second), 1)
	require.NoError(t, err)
	require.Empty(t, other)
	event := &events[0]
	delivery := service.UpstreamBalanceNotificationDelivery{Name: "ops", Provider: "webhook", Status: "sent", Attempts: 1, SentAt: &now}
	require.NoError(t, repo.SaveDelivery(ctx, event, "channel-one", delivery))
	require.NoError(t, repo.Complete(ctx, event, "failed"))
	retry, err := repo.Claim(ctx, now.Add(6*time.Minute), 1)
	require.NoError(t, err)
	require.Len(t, retry, 1)
	require.Equal(t, "sent", retry[0].Deliveries["channel-one"].Status)
	require.Error(t, repo.SaveDelivery(ctx, event, "old-worker", delivery))
	require.NoError(t, repo.Complete(ctx, &retry[0], "sent"))
	observation.Remaining = 20
	observation.ObservedAt = now.Add(time.Minute)
	require.NoError(t, repo.Observe(ctx, observation, true))
	require.NoError(t, repo.Observe(ctx, observation, true))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM upstream_balance_notification_events WHERE account_id=$1`, account.ID).Scan(&count))
	require.Equal(t, 2, count)
	recovery, err := repo.Claim(ctx, now.Add(6*time.Minute), 1)
	require.NoError(t, err)
	require.Len(t, recovery, 1)
	require.Equal(t, "recovery", recovery[0].Phase)
	require.NoError(t, repo.Complete(ctx, &recovery[0], "sent"))
	observation.Remaining = 0
	observation.ObservedAt = now.Add(2 * time.Minute)
	require.NoError(t, repo.Observe(ctx, observation, true))
	next, err := repo.Claim(ctx, now.Add(6*time.Minute), 1)
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.NotEqual(t, event.EpisodeID, next[0].EpisodeID)
}
