package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
)

type UpstreamSiteInventory struct {
	Revision   int64                  `json:"revision"`
	Status     string                 `json:"status"`
	AttemptAt  time.Time              `json:"attempt_at"`
	ReceivedAt *time.Time             `json:"received_at,omitempty"`
	Groups     []UpstreamSiteGroup    `json:"groups"`
	Keys       []UpstreamSiteKey      `json:"keys"`
	Wallet     *UpstreamBalanceAmount `json:"wallet,omitempty"`
	WalletAt   *time.Time             `json:"wallet_at,omitempty"`
	Errors     map[string]string      `json:"errors"`
}

type UpstreamSiteCatalog struct {
	Models    []string  `json:"models"`
	AttemptAt time.Time `json:"attempt_at"`
}

type UpstreamSiteAccount struct {
	ID             int64             `json:"id"`
	Name           string            `json:"name"`
	Platform       string            `json:"platform"`
	Status         string            `json:"status"`
	KeyID          string            `json:"key_id"`
	Source         string            `json:"source"`
	Models         map[string]string `json:"models"`
	Concurrency    int               `json:"concurrency"`
	RateMultiplier float64           `json:"rate_multiplier"`
}

func decodeUpstreamSiteCache(site *UpstreamSite, inventory, catalogs []byte) error {
	if len(inventory) > 2 {
		if err := json.Unmarshal(inventory, &site.Inventory); err != nil {
			return err
		}
	}
	return json.Unmarshal(catalogs, &site.Catalogs)
}

func upstreamSiteKeyQuota(row gjson.Result, kind string) *UpstreamBalanceAmount {
	var data map[string]any
	decoder := json.NewDecoder(strings.NewReader(row.Raw))
	decoder.UseNumber()
	if decoder.Decode(&data) != nil {
		return nil
	}
	if kind == "newapi" {
		amount, err := balanceAmount(data, "key", "QUOTA", "remain_quota", 1)
		if err != nil {
			return nil
		}
		amount.Used = balanceNumber(data["used_quota"])
		return &amount
	}
	limit, used := balanceNumber(data["quota"]), balanceNumber(data["quota_used"])
	if limit == nil {
		return nil
	}
	amount := &UpstreamBalanceAmount{Scope: "key", Currency: "USD", Limit: limit, Used: used, Unlimited: *limit == 0}
	if *limit > 0 && used != nil {
		remaining := *limit - *used
		amount.Remaining = &remaining
	}
	return amount
}

func upstreamSiteWallet(data []byte, kind string) (*UpstreamBalanceAmount, error) {
	var values map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&values); err != nil {
		return nil, upstreamSiteError("Unrecognized wallet response")
	}
	field, currency := "balance", "USD"
	if kind == "newapi" {
		field, currency = "quota", "QUOTA"
	}
	amount, err := balanceAmount(values, "wallet", currency, field, 1)
	if err != nil {
		return nil, upstreamSiteError("Upstream did not provide a readable wallet balance")
	}
	amount.Used = balanceNumber(values["used_quota"])
	return &amount, nil
}

// A failed component retains its last successful value and its original timestamp.
// Revisions and attempt ordering prevent an older request from overwriting a newer sync.
func (s *UpstreamBillingProbeService) SyncUpstreamSite(ctx context.Context, site *UpstreamSite) (*UpstreamSiteInventory, error) {
	started := time.Now().UTC()
	snapshot := UpstreamSiteInventory{Groups: []UpstreamSiteGroup{}, Keys: []UpstreamSiteKey{}}
	if site.Inventory != nil {
		snapshot = *site.Inventory
	}
	snapshot.Revision, snapshot.AttemptAt = site.Revision, started
	snapshot.Errors = map[string]string{}
	path := "/api/v1/auth/me"
	if site.Kind == "newapi" {
		path = "/api/user/self"
	}
	me, err := s.upstreamSiteRequest(ctx, site, http.MethodGet, path, nil)
	if err == nil {
		snapshot.Wallet, err = upstreamSiteWallet(me, site.Kind)
		if err == nil {
			now := time.Now().UTC()
			snapshot.WalletAt = &now
		}
		if err != nil && site.Inventory != nil {
			snapshot.Wallet = site.Inventory.Wallet
		}
	}
	if err != nil {
		snapshot.Errors["wallet"] = err.Error()
	}
	groups, err := s.UpstreamSiteGroups(ctx, site)
	if err != nil {
		snapshot.Errors["groups"] = err.Error()
	} else {
		snapshot.Groups = groups
	}
	keys, err := s.UpstreamSiteKeys(ctx, site)
	if err != nil {
		snapshot.Errors["keys"] = err.Error()
	} else {
		for i := range keys {
			if keys[i].GroupName == "" {
				for _, group := range snapshot.Groups {
					if group.ID == keys[i].GroupID {
						keys[i].GroupName = group.Name
						break
					}
				}
			}
		}
		snapshot.Keys = keys
		now := time.Now().UTC()
		snapshot.ReceivedAt = &now
	}
	snapshot.Status = "ok"
	if len(snapshot.Errors) > 0 {
		snapshot.Status = "attention"
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE upstream_sites SET inventory=$3::jsonb,inventory_at=$4
		WHERE id=$1 AND revision=$2 AND (inventory_at IS NULL OR inventory_at <= $4)`, site.ID, site.Revision, string(encoded), started)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrUpstreamBillingProbeIdentityChanged
	}
	return &snapshot, nil
}

func (s *UpstreamBillingProbeService) storeUpstreamSiteCatalog(ctx context.Context, site *UpstreamSite, keyID string, models []string, started time.Time) error {
	encoded, err := json.Marshal(UpstreamSiteCatalog{Models: models, AttemptAt: started})
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE upstream_sites SET model_catalogs=jsonb_set(model_catalogs,ARRAY[$3::text],$4::jsonb)
		WHERE id=$1 AND revision=$2 AND (model_catalogs->$3->>'attempt_at' IS NULL OR (model_catalogs->$3->>'attempt_at')::timestamptz <= $5)`, site.ID, site.Revision, keyID, string(encoded), started)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrUpstreamBillingProbeIdentityChanged
	}
	return err
}

func (s *UpstreamBillingProbeService) UpstreamSiteAccounts(ctx context.Context, siteID string) ([]UpstreamSiteAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,platform,status,
		CASE WHEN extra->'upstream_import'->>'site_id'=$1 THEN extra->'upstream_import'->>'key_id' ELSE extra->'upstream_site_link'->>'key_id' END,
		CASE WHEN extra->'upstream_import'->>'site_id'=$1 THEN 'imported' ELSE 'linked' END,
		COALESCE(credentials->'model_mapping','{}'),concurrency,COALESCE(rate_multiplier,1)
		FROM accounts WHERE deleted_at IS NULL AND (extra->'upstream_import'->>'site_id'=$1 OR extra->'upstream_site_link'->>'site_id'=$1) ORDER BY id`, siteID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	accounts := []UpstreamSiteAccount{}
	for rows.Next() {
		var account UpstreamSiteAccount
		var mapping []byte
		var keyID sql.NullString
		if err = rows.Scan(&account.ID, &account.Name, &account.Platform, &account.Status, &keyID, &account.Source, &mapping, &account.Concurrency, &account.RateMultiplier); err != nil {
			return nil, err
		}
		account.KeyID = keyID.String
		if err = json.Unmarshal(mapping, &account.Models); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

// Only relationship metadata is changed. Imported provenance and account settings are immutable here.
func (s *UpstreamBillingProbeService) LinkUpstreamSiteAccount(ctx context.Context, site *UpstreamSite, accountID int64, keyID string, unlink bool) error {
	if accountID <= 0 {
		return upstreamSiteError("Invalid local account ID")
	}
	if !unlink {
		keys, err := s.UpstreamSiteKeys(ctx, site)
		if err != nil {
			return err
		}
		found := false
		for _, key := range keys {
			found = found || key.ID == keyID
		}
		if !found {
			return upstreamSiteError("Select an existing upstream key")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var revision int64
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM upstream_sites WHERE id=$1 FOR SHARE", site.ID).Scan(&revision); err != nil {
		return err
	}
	if revision != site.Revision {
		return ErrUpstreamBillingProbeIdentityChanged
	}
	link, _ := json.Marshal(map[string]string{"site_id": site.ID, "key_id": keyID})
	query := `UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'),'{upstream_site_link}',$3::jsonb),updated_at=NOW()
		WHERE id=$1 AND deleted_at IS NULL AND type='apikey' AND parent_account_id IS NULL
		AND extra->'upstream_import'->>'site_id' IS NULL
		AND (extra->'upstream_site_link'->>'site_id' IS NULL OR extra->'upstream_site_link'->>'site_id'=$2)`
	if unlink {
		query = `UPDATE accounts SET extra=extra-'upstream_site_link',updated_at=NOW()
			WHERE id=$1 AND deleted_at IS NULL AND extra->'upstream_site_link'->>'site_id'=$2 AND $3::jsonb IS NOT NULL`
	}
	result, err := tx.ExecContext(ctx, query, accountID, site.ID, string(link))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return infraerrors.Conflict("UPSTREAM_ACCOUNT_LINK_CONFLICT", "Account is missing, imported, or already linked to another site")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO scheduler_outbox(event_type,account_id) VALUES($1,$2)", SchedulerOutboxEventAccountChanged, accountID); err != nil {
		return err
	}
	return tx.Commit()
}

type UpstreamSiteModelPreview struct {
	AccountID int64             `json:"account_id"`
	Version   string            `json:"version"`
	Added     []string          `json:"added"`
	Removed   []string          `json:"removed"`
	Mapping   map[string]string `json:"mapping"`
}

func upstreamSiteAccountIdentity(account *Account, siteID string) (string, error) {
	if account.Type != AccountTypeAPIKey || account.ParentAccountID != nil {
		return "", upstreamSiteError("Only independent API key accounts support model sync")
	}
	for _, field := range []string{"upstream_import", "upstream_site_link"} {
		link, ok := account.Extra[field].(map[string]any)
		if ok && link["site_id"] == siteID {
			keyID, _ := link["key_id"].(string)
			if keyID != "" {
				return keyID, nil
			}
		}
	}
	return "", upstreamSiteError("Account is not associated with this site and key")
}

func upstreamSiteModelVersion(account *Account) string {
	data, _ := json.Marshal([]any{account.Credentials, account.Extra["upstream_import"], account.Extra["upstream_site_link"]})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func upstreamSiteModelPlan(account *Account, models []string) (*UpstreamSiteModelPreview, error) {
	data, err := json.Marshal(account.Credentials["model_mapping"])
	if err != nil {
		return nil, err
	}
	old := map[string]string{}
	if err = json.Unmarshal(data, &old); err != nil {
		return nil, upstreamSiteError("Account model mapping cannot be synchronized")
	}
	// An empty mapping means unrestricted routing. Never replace it implicitly.
	if len(old) == 0 {
		return nil, upstreamSiteError("Account has no explicit model scope; configure its model mapping before synchronization")
	}
	available := map[string]bool{}
	for _, model := range models {
		available[model] = true
	}
	preview := &UpstreamSiteModelPreview{AccountID: account.ID, Version: upstreamSiteModelVersion(account), Added: []string{}, Removed: []string{}, Mapping: map[string]string{}}
	for source, target := range old {
		if available[target] {
			preview.Mapping[source] = target
		} else {
			preview.Removed = append(preview.Removed, source)
		}
	}
	for _, model := range models {
		if _, exists := old[model]; !exists {
			preview.Mapping[model] = model
			preview.Added = append(preview.Added, model)
		}
	}
	if len(preview.Mapping) == 0 {
		return nil, upstreamSiteError("Synchronization would remove all model restrictions; account was kept unchanged")
	}
	if err = ValidateAPIKeyModelMapping(preview.Mapping); err != nil {
		return nil, err
	}
	sort.Strings(preview.Added)
	sort.Strings(preview.Removed)
	return preview, nil
}

// Apply repeats discovery and checks the complete preview and credentials before a narrow JSON update.
func (s *UpstreamBillingProbeService) UpstreamSiteAccountModels(ctx context.Context, site *UpstreamSite, accountID int64, expected *UpstreamSiteModelPreview) (*UpstreamSiteModelPreview, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	keyID, err := upstreamSiteAccountIdentity(account, site.ID)
	if err != nil {
		return nil, err
	}
	key, err := s.ResolveUpstreamSiteKey(ctx, site, keyID)
	if err != nil {
		return nil, err
	}
	base, _ := CanonicalNewAPISite(account.GetCredential("base_url"))
	if account.GetCredential("api_key") != key.secret || base != site.InferenceURL {
		return nil, upstreamSiteError("Local account credentials differ from this site/key; update the account connection before synchronizing models")
	}
	models, err := s.upstreamSiteModels(ctx, site, key)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, upstreamSiteError("Upstream model catalog is empty; account was kept unchanged")
	}
	preview, err := upstreamSiteModelPlan(account, models)
	if err != nil {
		return nil, err
	}
	if expected == nil {
		if err = s.storeUpstreamSiteCatalog(ctx, site, keyID, models, time.Now().UTC()); err != nil {
			return nil, err
		}
		return preview, nil
	}
	want, _ := json.Marshal(expected.Mapping)
	actual, _ := json.Marshal(preview.Mapping)
	if expected.Version != preview.Version || string(want) != string(actual) {
		return nil, infraerrors.Conflict("UPSTREAM_MODELS_CHANGED", "Account or upstream models changed; preview again before applying")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var revision int64
	err = tx.QueryRowContext(ctx, "SELECT revision FROM upstream_sites WHERE id=$1 FOR SHARE", site.ID).Scan(&revision)
	if err != nil {
		return nil, err
	}
	if revision != site.Revision {
		return nil, ErrUpstreamBillingProbeIdentityChanged
	}
	credentials, _ := json.Marshal(account.Credentials)
	imported, _ := json.Marshal(account.Extra["upstream_import"])
	linked, _ := json.Marshal(account.Extra["upstream_site_link"])
	result, err := tx.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{model_mapping}',$2::jsonb),updated_at=NOW()
		WHERE id=$1 AND deleted_at IS NULL AND type='apikey' AND parent_account_id IS NULL AND credentials=$3::jsonb
		AND COALESCE(extra->'upstream_import','null'::jsonb)=$4::jsonb AND COALESCE(extra->'upstream_site_link','null'::jsonb)=$5::jsonb`, accountID, string(actual), string(credentials), string(imported), string(linked))
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, infraerrors.Conflict("UPSTREAM_ACCOUNT_CHANGED", "Account changed; preview again before applying")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO scheduler_outbox(event_type,account_id) VALUES($1,$2)", SchedulerOutboxEventAccountChanged, accountID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return preview, nil
}
