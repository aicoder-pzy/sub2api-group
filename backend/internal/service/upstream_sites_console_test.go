package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestUpstreamSiteQuotaAndWalletUnits(t *testing.T) {
	now := time.Now()
	key := parseUpstreamSiteKey(gjson.Parse(`{"id":1,"status":1,"remain_quota":12000,"used_quota":8000,"unlimited_quota":false,"key":"sk-private-abcd"}`), "newapi", now)
	require.True(t, key.Available)
	require.Equal(t, "QUOTA", key.Quota.Currency)
	require.Equal(t, float64(12000), *key.Quota.Remaining)
	require.Equal(t, "abcd", key.Suffix)
	encoded, err := json.Marshal(key)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "sk-private")
	for _, tc := range []struct{ row, reason string }{
		{`{"status":2,"unlimited_quota":true}`, "disabled"},
		{`{"status":1,"expired_time":1,"unlimited_quota":true}`, "expired"},
		{`{"status":1,"remain_quota":0,"unlimited_quota":false}`, "quota_exhausted"},
	} {
		key = parseUpstreamSiteKey(gjson.Parse(tc.row), "newapi", now)
		require.False(t, key.Available)
		require.Equal(t, tc.reason, key.Status)
	}
	wallet, err := upstreamSiteWallet([]byte(`{"quota":2500000}`), "newapi")
	require.NoError(t, err)
	require.Equal(t, "QUOTA", wallet.Currency)
	require.Equal(t, float64(2500000), *wallet.Remaining)
	_, err = upstreamSiteWallet([]byte(`{"id":7}`), "sub2api")
	require.Error(t, err) // Missing balance must never look like a zero wallet.
}

func TestUpstreamSiteInferenceURLUsesOnlyRelayKey(t *testing.T) {
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "relay.example.com", req.URL.Host)
		require.Equal(t, "/tenant/v1/models", req.URL.Path)
		require.Equal(t, "Bearer relay-secret", req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("New-Api-User"))
		return siteImportResponse(`{"data":[{"id":"model-1"}]}`), nil
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	models, err := svc.upstreamSiteModels(context.Background(), &UpstreamSite{Kind: "newapi", BaseURL: "https://console.example.com", InferenceURL: "https://relay.example.com/tenant", AccessToken: "nap-management"}, &UpstreamSiteKey{secret: "relay-secret"})
	require.NoError(t, err)
	require.Equal(t, []string{"model-1"}, models)
}

func TestUpstreamSiteModelPlanPreservesAliasesAndNeverOpensRouting(t *testing.T) {
	account := &Account{ID: 12, Credentials: map[string]any{"model_mapping": map[string]any{"alias-*": "model-1", "gone": "model-old", "model-1": "model-1"}, "api_key": "secret"}}
	preview, err := upstreamSiteModelPlan(account, []string{"model-1", "model-2"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"alias-*": "model-1", "model-1": "model-1", "model-2": "model-2"}, preview.Mapping)
	require.Equal(t, []string{"model-2"}, preview.Added)
	require.Equal(t, []string{"gone"}, preview.Removed)
	require.NotContains(t, preview.Version, "secret")
	_, err = upstreamSiteModelPlan(account, nil)
	require.Error(t, err)
	account.Credentials["model_mapping"] = map[string]any{}
	_, err = upstreamSiteModelPlan(account, []string{"model-1"})
	require.Error(t, err)
}

func TestUpstreamSiteSyncRetainsSuccessfulDataOnPermissionFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) { return nil, errors.New("secret upstream body") }}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	svc.db = db
	oldTime := time.Now().Add(-time.Hour).UTC()
	remaining := float64(27)
	site := &UpstreamSite{ID: "site", Kind: "sub2api", BaseURL: "https://example.com", Revision: 3, AccessToken: "management", Inventory: &UpstreamSiteInventory{Wallet: &UpstreamBalanceAmount{Currency: "USD", Remaining: &remaining}, WalletAt: &oldTime, ReceivedAt: &oldTime, Keys: []UpstreamSiteKey{{ID: "8", Available: true}}}}
	mock.ExpectExec("UPDATE upstream_sites SET inventory").WithArgs("site", int64(3), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	snapshot, err := svc.SyncUpstreamSite(context.Background(), site)
	require.NoError(t, err)
	require.Equal(t, "attention", snapshot.Status)
	require.Len(t, snapshot.Errors, 3)
	require.Equal(t, oldTime, *snapshot.ReceivedAt)
	require.Equal(t, oldTime, *snapshot.WalletAt)
	require.Equal(t, remaining, *snapshot.Wallet.Remaining)
	require.Equal(t, "8", snapshot.Keys[0].ID)
	encoded, _ := json.Marshal(snapshot)
	require.NotContains(t, string(encoded), "secret upstream body")
	require.NoError(t, mock.ExpectationsWereMet())
}

type siteConsoleAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *siteConsoleAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func TestUpstreamSiteModelApplyRejectsConcurrentEditsAndCatalogChanges(t *testing.T) {
	for _, scenario := range []string{"changed version", "changed catalog", "concurrent credentials", "success"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			account := &Account{ID: 12, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://relay.example.com", "api_key": "relay-secret", "model_mapping": map[string]any{"alias-*": "model-1"}}, Extra: map[string]any{"upstream_import": map[string]any{"site_id": "site", "key_id": "3"}}}
			upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/v1/models" {
					return siteImportResponse(`{"data":[{"id":"model-1"},{"id":"model-2"}]}`), nil
				}
				return siteImportResponse(`{"data":{"items":[{"id":3,"status":"active","key":"relay-secret"}],"total":1}}`), nil
			}}
			svc := newUpstreamBillingProbeTestService(&siteConsoleAccountRepo{account: account}, upstream, nil)
			svc.db = db
			preview, err := upstreamSiteModelPlan(account, []string{"model-1", "model-2"})
			require.NoError(t, err)
			switch scenario {
			case "changed version":
				preview.Version = "stale"
			case "changed catalog":
				preview.Mapping["model-3"] = "model-3"
			default:
				mock.ExpectBegin()
				mock.ExpectQuery("SELECT revision FROM upstream_sites").WithArgs("site").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(3))
				mapping, _ := json.Marshal(preview.Mapping)
				credentials, _ := json.Marshal(account.Credentials)
				imported, _ := json.Marshal(account.Extra["upstream_import"])
				count := int64(1)
				if scenario == "concurrent credentials" {
					count = 0
				}
				mock.ExpectExec(regexp.QuoteMeta("UPDATE accounts SET credentials=jsonb_set")).WithArgs(int64(12), string(mapping), string(credentials), string(imported), "null").WillReturnResult(sqlmock.NewResult(0, count))
				if count == 0 {
					mock.ExpectRollback()
				} else {
					mock.ExpectExec("INSERT INTO scheduler_outbox").WithArgs(SchedulerOutboxEventAccountChanged, int64(12)).WillReturnResult(sqlmock.NewResult(1, 1))
					mock.ExpectCommit()
				}
			}
			_, err = svc.UpstreamSiteAccountModels(context.Background(), &UpstreamSite{ID: "site", Kind: "sub2api", BaseURL: "https://example.com", InferenceURL: "https://relay.example.com", AccessToken: "management", Revision: 3}, 12, preview)
			if scenario == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpstreamSiteManualLinkOnlyUpdatesProvenance(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		return siteImportResponse(`{"data":{"items":[{"id":3,"status":"active"}],"total":1}}`), nil
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	svc.db = db
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT revision FROM upstream_sites").WithArgs("site").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(3))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE accounts SET extra=jsonb_set")).WithArgs(int64(12), "site", `{"key_id":"3","site_id":"site"}`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").WithArgs(SchedulerOutboxEventAccountChanged, int64(12)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	require.NoError(t, svc.LinkUpstreamSiteAccount(context.Background(), &UpstreamSite{ID: "site", Kind: "sub2api", BaseURL: "https://example.com", AccessToken: "management", Revision: 3}, 12, "3", false))
	require.NoError(t, mock.ExpectationsWereMet())
}
