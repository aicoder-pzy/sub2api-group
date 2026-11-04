//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSiteImportIdentityAndSiteDeletion(t *testing.T) {
	ctx := context.Background()
	siteID := uuid.NewString()
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO upstream_sites(id,name,kind,base_url,token_cipher) VALUES($1,'fixture','newapi','https://example.invalid','encrypted-fixture')`, siteID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE extra->'upstream_import'->>'site_id'=$1`, siteID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM upstream_sites WHERE id=$1`, siteID)
		require.NoError(t, err)
	})
	makeAccount := func(platform string) *service.Account {
		return &service.Account{Name: "import fixture", Platform: platform, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Concurrency: 1, Credentials: map[string]any{"api_key": "relay-fixture"}, Extra: map[string]any{"upstream_import": map[string]any{"site_id": siteID, "key_id": "7"}}}
	}
	first := makeAccount(service.PlatformOpenAI)
	require.NoError(t, createAccountRecord(ctx, integrationEntClient, first))
	require.ErrorIs(t, createAccountRecord(ctx, integrationEntClient, makeAccount(service.PlatformOpenAI)), service.ErrUpstreamKeyAlreadyImported)
	require.NoError(t, createAccountRecord(ctx, integrationEntClient, makeAccount(service.PlatformAnthropic)))
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM upstream_sites WHERE id=$1`, siteID)
	require.NoError(t, err)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE extra->'upstream_import'->>'site_id'=$1`, siteID).Scan(&count))
	require.Equal(t, 2, count, "deleting the connection must retain imported accounts")
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET deleted_at=NOW() WHERE id=$1`, first.ID)
	require.NoError(t, err)
	require.NoError(t, createAccountRecord(ctx, integrationEntClient, makeAccount(service.PlatformOpenAI)), "a removed import can be replaced")
}
