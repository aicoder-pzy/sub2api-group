//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSitesConsoleLoadsCacheAndUnlinksOnlyMetadata(t *testing.T) {
	ctx := context.Background()
	siteID := uuid.NewString()
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO upstream_sites(id,name,kind,base_url,inference_url,token_cipher,inventory,model_catalogs)
		VALUES($1,'console fixture','newapi','https://console.example.invalid','https://relay.example.invalid','encrypted-fixture',
		'{"revision":1,"status":"ok","attempt_at":"2026-11-07T00:00:00Z","groups":[],"keys":[],"errors":{}}',
		'{"7":{"models":["model-1"],"attempt_at":"2026-11-07T00:00:00Z"}}')`, siteID)
	require.NoError(t, err)
	account := &service.Account{Name: "console account", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Concurrency: 3,
		Credentials: map[string]any{"api_key": "relay-private-fixture", "base_url": "https://relay.example.invalid", "model_mapping": map[string]any{"alias-*": "model-1"}},
		Extra:       map[string]any{"keep-me": true, "upstream_site_link": map[string]any{"site_id": siteID, "key_id": "7"}}}
	require.NoError(t, createAccountRecord(ctx, integrationEntClient, account))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id=$1", account.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM upstream_sites WHERE id=$1", siteID)
		require.NoError(t, err)
	})
	svc := service.NewUpstreamBillingProbeService(nil, nil, nil)
	svc.SetLeaderLock(nil, integrationDB)
	t.Cleanup(svc.Stop)
	sites, err := svc.ListUpstreamSites(ctx)
	require.NoError(t, err)
	var loaded *service.UpstreamSite
	for i := range sites {
		if sites[i].ID == siteID {
			loaded = &sites[i]
			break
		}
	}
	require.NotNil(t, loaded)
	require.Equal(t, 1, loaded.AccountCount)
	require.Equal(t, "https://relay.example.invalid", loaded.InferenceURL)
	require.Equal(t, "ok", loaded.Inventory.Status)
	require.Equal(t, []string{"model-1"}, loaded.Catalogs["7"].Models)
	site, err := svc.GetUpstreamSite(ctx, siteID)
	require.NoError(t, err)
	accounts, err := svc.UpstreamSiteAccounts(ctx, siteID)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "linked", accounts[0].Source)
	require.Equal(t, "model-1", accounts[0].Models["alias-*"])
	encoded, _ := json.Marshal([]any{sites, site, accounts})
	require.NotContains(t, string(encoded), "encrypted-fixture")
	require.NotContains(t, string(encoded), "relay-private-fixture")
	var before, after string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT credentials::text FROM accounts WHERE id=$1", account.ID).Scan(&before))
	require.NoError(t, svc.LinkUpstreamSiteAccount(ctx, site, account.ID, "7", true))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT credentials::text FROM accounts WHERE id=$1", account.ID).Scan(&after))
	require.Equal(t, before, after)
	var retained bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT (extra->>'keep-me')::boolean FROM accounts WHERE id=$1", account.ID).Scan(&retained))
	require.True(t, retained)
	accounts, err = svc.UpstreamSiteAccounts(ctx, siteID)
	require.NoError(t, err)
	require.Empty(t, accounts)
	// Deleting a connection must not strand a manual association on the account.
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(extra,'{upstream_site_link}',jsonb_build_object('site_id',$2::text,'key_id','7')) WHERE id=$1`, account.ID, siteID)
	require.NoError(t, err)
	require.NoError(t, svc.DeleteUpstreamSite(ctx, siteID))
	var linked bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT credentials::text,extra ? 'upstream_site_link' FROM accounts WHERE id=$1", account.ID).Scan(&after, &linked))
	require.Equal(t, before, after)
	require.False(t, linked)
}
