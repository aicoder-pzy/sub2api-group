package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type UpstreamSite struct {
	ID           string                         `json:"id"`
	Name         string                         `json:"name"`
	Kind         string                         `json:"kind"`
	BaseURL      string                         `json:"base_url"`
	InferenceURL string                         `json:"inference_url"`
	PurchaseNote string                         `json:"purchase_note"`
	Inventory    *UpstreamSiteInventory         `json:"inventory,omitempty"`
	Catalogs     map[string]UpstreamSiteCatalog `json:"catalogs,omitempty"`
	AccountCount int                            `json:"account_count"`
	UserID       int64                          `json:"user_id"`
	ProxyID      *int64                         `json:"proxy_id"`
	Revision     int64                          `json:"revision"`
	AccessToken  string                         `json:"access_token,omitempty"`
	Proxy        *Proxy                         `json:"-"`
	cipher       string
}

var ErrUpstreamKeyAlreadyImported = infraerrors.Conflict("UPSTREAM_KEY_ALREADY_IMPORTED", "This upstream key has already been imported for this platform")

type UpstreamSiteGroup struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Multiplier *float64 `json:"multiplier,omitempty"`
}

type UpstreamSiteKey struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	GroupID   string                 `json:"group_id"`
	GroupName string                 `json:"group_name"`
	Available bool                   `json:"available"`
	Status    string                 `json:"status"`
	Suffix    string                 `json:"suffix,omitempty"`
	Quota     *UpstreamBalanceAmount `json:"quota,omitempty"`
	secret    string
}

func upstreamSiteError(message string) error {
	return infraerrors.BadRequest("UPSTREAM_SITE_ERROR", message)
}

func (s *UpstreamBillingProbeService) ListUpstreamSites(ctx context.Context) ([]UpstreamSite, error) {
	if s == nil || s.db == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,kind,base_url,user_id,proxy_id,revision,inference_url,purchase_note,inventory,model_catalogs,
		(SELECT COUNT(*) FROM accounts a WHERE a.deleted_at IS NULL AND
		(a.extra->'upstream_import'->>'site_id'=upstream_sites.id::text OR a.extra->'upstream_site_link'->>'site_id'=upstream_sites.id::text))
		FROM upstream_sites ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []UpstreamSite{}
	for rows.Next() {
		var site UpstreamSite
		var inventory, catalogs []byte
		if err = rows.Scan(&site.ID, &site.Name, &site.Kind, &site.BaseURL, &site.UserID, &site.ProxyID, &site.Revision, &site.InferenceURL, &site.PurchaseNote, &inventory, &catalogs, &site.AccountCount); err != nil {
			return nil, err
		}
		if err = decodeUpstreamSiteCache(&site, inventory, catalogs); err != nil {
			return nil, err
		}
		out = append(out, site)
	}
	return out, rows.Err()
}

func (s *UpstreamBillingProbeService) GetUpstreamSite(ctx context.Context, id string) (*UpstreamSite, error) {
	if s == nil || s.db == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, upstreamSiteError("Invalid site ID")
	}
	var site UpstreamSite
	var inventory, catalogs []byte
	err := s.db.QueryRowContext(ctx, "SELECT id,name,kind,base_url,user_id,proxy_id,revision,token_cipher,inference_url,purchase_note,inventory,model_catalogs FROM upstream_sites WHERE id=$1", id).Scan(&site.ID, &site.Name, &site.Kind, &site.BaseURL, &site.UserID, &site.ProxyID, &site.Revision, &site.cipher, &site.InferenceURL, &site.PurchaseNote, &inventory, &catalogs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("UPSTREAM_SITE_NOT_FOUND", "Upstream site not found")
	}
	if err == nil {
		err = decodeUpstreamSiteCache(&site, inventory, catalogs)
	}
	return &site, err
}

// Management requests share the existing validated upstream transport, refuse
// redirects and never return raw upstream errors or credentials to the caller.
func (s *UpstreamBillingProbeService) upstreamSiteRequest(ctx context.Context, site *UpstreamSite, method, path string, payload any) ([]byte, error) {
	if s.accountTestService == nil || s.accountTestService.httpUpstream == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	if _, err := s.accountTestService.validateUpstreamBaseURL(site.BaseURL); err != nil {
		return nil, upstreamSiteError("Invalid or disallowed upstream URL")
	}
	secret := site.AccessToken
	if secret == "" {
		if s.newAPIEncryptor == nil {
			return nil, ErrNewAPIEncryptionKey
		}
		var err error
		secret, err = s.newAPIEncryptor.Decrypt(site.cipher)
		if err != nil {
			return nil, ErrNewAPIEncryptionKey
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, site.BaseURL+path, body)
	if err != nil {
		return nil, upstreamSiteError("Cannot build management request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+secret)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if site.Kind == "newapi" && !strings.HasPrefix(secret, "nap_") {
		if site.UserID <= 0 {
			return nil, upstreamSiteError("Legacy New API tokens require a user ID")
		}
		req.Header.Set("New-Api-User", strconv.FormatInt(site.UserID, 10))
	}
	proxyURL := ""
	if site.ProxyID != nil {
		if site.Proxy == nil || site.Proxy.ID != *site.ProxyID {
			return nil, upstreamSiteError("Site proxy is unavailable")
		}
		proxyURL = site.Proxy.URL()
	}
	resp, err := s.accountTestService.httpUpstream.Do(req, proxyURL, 0, 2)
	if err != nil {
		return nil, upstreamSiteError("Management request failed; check site, proxy and token")
	}
	if resp == nil || resp.Body == nil {
		return nil, upstreamSiteError("Empty management response")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamSiteError(fmt.Sprintf("Upstream management HTTP %d; check token permissions", resp.StatusCode))
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil || len(data) > 2*1024*1024 || !gjson.ValidBytes(data) {
		return nil, upstreamSiteError("Invalid or oversized management response")
	}
	result := gjson.ParseBytes(data)
	if success := result.Get("success"); success.Exists() && !success.Bool() {
		return nil, upstreamSiteError("Upstream rejected management request; check permissions and input")
	}
	if code := result.Get("code"); code.Exists() && code.Int() != 0 {
		return nil, upstreamSiteError("Upstream rejected management request")
	}
	if wrapped := result.Get("data"); wrapped.Exists() {
		return []byte(wrapped.Raw), nil
	}
	return data, nil
}

func (s *UpstreamBillingProbeService) SaveUpstreamSite(ctx context.Context, input UpstreamSite) (*UpstreamSite, error) {
	if s == nil || s.db == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	if !s.newAPIFixedKey || s.newAPIEncryptor == nil {
		return nil, ErrNewAPIEncryptionKey
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 100 || (input.Kind != "sub2api" && input.Kind != "newapi") || len(input.AccessToken) > 16384 || strings.ContainsAny(input.AccessToken, "\r\n") {
		return nil, upstreamSiteError("Invalid site configuration")
	}
	base, err := CanonicalNewAPISite(input.BaseURL)
	if err != nil {
		return nil, err
	}
	input.BaseURL = base
	if s.accountTestService == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	if input.InferenceURL == "" {
		input.InferenceURL = base
	}
	input.InferenceURL, err = CanonicalNewAPISite(input.InferenceURL)
	if err != nil {
		return nil, err
	}
	if _, err = s.accountTestService.validateUpstreamBaseURL(input.InferenceURL); err != nil {
		return nil, upstreamSiteError("Invalid or disallowed inference URL")
	}
	if len(input.PurchaseNote) > 2000 {
		return nil, upstreamSiteError("Purchase note is too long")
	}
	if input.ID != "" {
		old, loadErr := s.GetUpstreamSite(ctx, input.ID)
		if loadErr != nil {
			return nil, loadErr
		}
		if old.Revision != input.Revision {
			return nil, ErrUpstreamBillingProbeIdentityChanged
		}
		if input.AccessToken == "" {
			if old.BaseURL != input.BaseURL || old.Kind != input.Kind || old.UserID != input.UserID {
				return nil, upstreamSiteError("A new identity requires a new management token")
			}
			input.cipher = old.cipher
		}
	}
	if input.AccessToken == "" && input.cipher == "" {
		return nil, upstreamSiteError("Management access token required")
	}
	path := "/api/v1/auth/me"
	if input.Kind == "newapi" {
		path = "/api/user/self"
	}
	me, err := s.upstreamSiteRequest(ctx, &input, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	userID := gjson.GetBytes(me, "id").Int()
	if userID <= 0 {
		return nil, upstreamSiteError("Upstream did not return a valid user identity")
	}
	if input.Kind == "newapi" {
		if input.UserID > 0 && input.UserID != userID {
			return nil, upstreamSiteError("Management token belongs to another user")
		}
		input.UserID = userID
	}
	if input.AccessToken != "" {
		input.cipher, err = s.newAPIEncryptor.Encrypt(input.AccessToken)
		if err != nil {
			return nil, ErrNewAPIEncryptionKey
		}
	}
	if input.ID == "" {
		input.ID = uuid.NewString()
		input.Revision = 1
		err = s.db.QueryRowContext(ctx, "INSERT INTO upstream_sites(id,name,kind,base_url,user_id,token_cipher,proxy_id,inference_url,purchase_note) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING revision", input.ID, input.Name, input.Kind, input.BaseURL, input.UserID, input.cipher, input.ProxyID, input.InferenceURL, input.PurchaseNote).Scan(&input.Revision)
	} else {
		err = s.db.QueryRowContext(ctx, "UPDATE upstream_sites SET name=$2,kind=$3,base_url=$4,user_id=$5,token_cipher=$6,proxy_id=$7,inference_url=$9,purchase_note=$10,inventory='{}',inventory_at=NULL,model_catalogs='{}',revision=revision+1,updated_at=NOW() WHERE id=$1 AND revision=$8 RETURNING revision", input.ID, input.Name, input.Kind, input.BaseURL, input.UserID, input.cipher, input.ProxyID, input.Revision, input.InferenceURL, input.PurchaseNote).Scan(&input.Revision)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUpstreamBillingProbeIdentityChanged
		}
	}
	input.AccessToken = ""
	input.cipher = ""
	input.Proxy = nil
	input.Inventory = nil
	input.Catalogs = nil
	input.AccountCount = 0
	return &input, err
}

func (s *UpstreamBillingProbeService) DeleteUpstreamSite(ctx context.Context, id string) error {
	if _, err := s.GetUpstreamSite(ctx, id); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "DELETE FROM upstream_sites WHERE id=$1", id); err != nil {
		return err
	}
	// Remove manual relationships so retained accounts can be associated again.
	// The original import provenance and every account credential remain intact.
	if _, err = tx.ExecContext(ctx, `WITH unlinked AS (
		UPDATE accounts SET extra=extra-'upstream_site_link',updated_at=NOW()
		WHERE extra->'upstream_site_link'->>'site_id'=$1 AND deleted_at IS NULL RETURNING id
	) INSERT INTO scheduler_outbox(event_type,account_id) SELECT $2,id FROM unlinked`, id, SchedulerOutboxEventAccountChanged); err != nil {
		return err
	}
	return tx.Commit()
}

func upstreamRows(raw []byte) ([]gjson.Result, error) {
	r := gjson.ParseBytes(raw)
	if !r.IsArray() {
		r = r.Get("items")
	}
	if !r.IsArray() {
		return nil, upstreamSiteError("Unrecognized upstream list format")
	}
	return r.Array(), nil
}

func (s *UpstreamBillingProbeService) UpstreamSiteGroups(ctx context.Context, site *UpstreamSite) ([]UpstreamSiteGroup, error) {
	path := "/api/v1/groups/available"
	if site.Kind == "newapi" {
		path = "/api/user/self/groups"
	}
	data, err := s.upstreamSiteRequest(ctx, site, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	out := []UpstreamSiteGroup{}
	appendGroup := func(id, name string, ratio gjson.Result) {
		item := UpstreamSiteGroup{ID: id, Name: name}
		if ratio.IsObject() {
			ratio = ratio.Get("ratio")
		}
		if ratio.Type == gjson.Number {
			f := ratio.Float()
			if f >= 0 && !math.IsNaN(f) && !math.IsInf(f, 0) {
				item.Multiplier = &f
			}
		}
		out = append(out, item)
	}
	if site.Kind == "newapi" {
		r := gjson.ParseBytes(data)
		if !r.IsObject() {
			return nil, upstreamSiteError("Unrecognized upstream group format")
		}
		r.ForEach(func(k, v gjson.Result) bool { appendGroup(k.String(), k.String(), v); return true })
	} else {
		rows, e := upstreamRows(data)
		if e != nil {
			return nil, e
		}
		for _, row := range rows {
			appendGroup(row.Get("id").String(), row.Get("name").String(), row.Get("rate_multiplier"))
		}
	}
	return out, nil
}

func parseUpstreamSiteKey(row gjson.Result, kind string, now time.Time) UpstreamSiteKey {
	key := UpstreamSiteKey{ID: row.Get("id").String(), Name: row.Get("name").String(), secret: row.Get("key").String()}
	status := row.Get("status").String()
	key.Available = status == "active" || status == "enabled" || status == "1"
	key.Status = "disabled"
	if key.Available {
		key.Status = "active"
	}
	if len(key.secret) >= 8 {
		key.Suffix = key.secret[len(key.secret)-4:]
	}
	key.GroupID = row.Get("group_id").String()
	key.GroupName = row.Get("group.name").String()
	if kind == "newapi" {
		key.GroupID = row.Get("group").String()
		key.GroupName = key.GroupID
		if expires := row.Get("expired_time"); expires.Exists() && expires.Int() > 0 && expires.Int() <= now.Unix() {
			key.Available = false
			key.Status = "expired"
		}
		if unlimited := row.Get("unlimited_quota"); unlimited.Exists() && !unlimited.Bool() {
			remain := row.Get("remain_quota")
			if !remain.Exists() || remain.Float() <= 0 {
				key.Available = false
				key.Status = "quota_exhausted"
			}
		}
	} else {
		if expires := row.Get("expires_at"); expires.Exists() && expires.String() != "" {
			t, e := time.Parse(time.RFC3339, expires.String())
			if e != nil || !t.After(now) {
				key.Available = false
				key.Status = "expired"
			}
		}
		if limit := row.Get("quota").Float(); limit > 0 && row.Get("quota_used").Float() >= limit {
			key.Available = false
			key.Status = "quota_exhausted"
		}
	}
	key.Quota = upstreamSiteKeyQuota(row, kind)
	return key
}

func (s *UpstreamBillingProbeService) UpstreamSiteKeys(ctx context.Context, site *UpstreamSite) ([]UpstreamSiteKey, error) {
	out := []UpstreamSiteKey{}
	seen := map[string]bool{}
	page := 1
	if site.Kind == "newapi" {
		page = 0
	}
	expectedTotal := int64(-1)
	for iteration := 0; iteration < 100; iteration++ {
		path := fmt.Sprintf("/api/v1/keys?page=%d&page_size=100", page)
		if site.Kind == "newapi" {
			path = fmt.Sprintf("/api/token/?p=%d&page_size=100", page)
		}
		data, err := s.upstreamSiteRequest(ctx, site, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		rows, err := upstreamRows(data)
		if err != nil {
			return nil, err
		}
		if len(rows) > 1000 {
			return nil, upstreamSiteError("Too many keys in upstream page")
		}
		nextPage := page + 1
		returnedPage := gjson.GetBytes(data, "p")
		if !returnedPage.Exists() {
			returnedPage = gjson.GetBytes(data, "page")
		}
		if returnedPage.Exists() {
			firstPageClamped := iteration == 0 && page == 0 && returnedPage.Int() == 1
			if returnedPage.Type != gjson.Number || returnedPage.Float() != float64(returnedPage.Int()) || (returnedPage.Int() != int64(page) && !firstPageClamped) {
				return nil, upstreamSiteError("Invalid upstream pagination")
			}
			nextPage = int(returnedPage.Int()) + 1
		}
		total := gjson.GetBytes(data, "total")
		if total.Exists() {
			if total.Type != gjson.Number || total.Float() != float64(total.Int()) || total.Int() < 0 || total.Int() > 10000 || (expectedTotal >= 0 && total.Int() != expectedTotal) {
				return nil, upstreamSiteError("Upstream inventory changed or exceeded 10000 keys; refresh and retry")
			}
			expectedTotal = total.Int()
		}
		for _, row := range rows {
			key := parseUpstreamSiteKey(row, site.Kind, time.Now())
			numericID, e := strconv.ParseInt(key.ID, 10, 64)
			if e != nil || numericID <= 0 || seen[key.ID] || len(key.Name) > 1024 || len(key.secret) > 16384 || len(key.GroupID) > 256 {
				return nil, upstreamSiteError("Invalid or repeated key in upstream pagination")
			}
			seen[key.ID] = true
			out = append(out, key)
		}
		if expectedTotal >= 0 {
			if int64(len(out)) == expectedTotal {
				return out, nil
			}
			if int64(len(out)) > expectedTotal || len(rows) == 0 {
				return nil, upstreamSiteError("Incomplete upstream inventory; refresh and retry")
			}
		} else if len(rows) == 0 {
			return out, nil
		}
		if len(out) > 10000 {
			return nil, upstreamSiteError("Too many upstream keys (maximum 10000)")
		}
		page = nextPage
	}
	return nil, upstreamSiteError("Upstream pagination did not complete within 100 pages")
}

func (s *UpstreamBillingProbeService) ResolveUpstreamSiteKey(ctx context.Context, site *UpstreamSite, keyID string) (*UpstreamSiteKey, error) {
	keys, err := s.UpstreamSiteKeys(ctx, site)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if key.ID == keyID {
			if !key.Available {
				return nil, upstreamSiteError("Selected upstream key is not available")
			}
			if site.Kind == "newapi" && (key.secret == "" || strings.Contains(key.secret, "*")) {
				id, e := strconv.ParseInt(key.ID, 10, 64)
				if e != nil || id <= 0 {
					return nil, upstreamSiteError("Invalid key ID")
				}
				data, e := s.upstreamSiteRequest(ctx, site, http.MethodPost, fmt.Sprintf("/api/token/%d/key", id), map[string]any{})
				if e != nil {
					return nil, e
				}
				value := gjson.ParseBytes(data)
				key.secret = value.String()
				if value.IsObject() {
					key.secret = value.Get("key").String()
				}
			}
			if key.secret == "" || len(key.secret) > 16384 || strings.ContainsAny(key.secret, "*\r\n") {
				return nil, upstreamSiteError("Upstream did not reveal a usable key; check api_key:reveal permission")
			}
			return &key, nil
		}
	}
	return nil, upstreamSiteError("Selected upstream key no longer exists")
}

// Creation is sent once. Never automatically retry an ambiguous POST result.
func (s *UpstreamBillingProbeService) CreateUpstreamSiteKey(ctx context.Context, site *UpstreamSite, name, groupID string) error {
	if strings.TrimSpace(name) == "" || len(name) > 100 {
		return upstreamSiteError("Key name must contain 1-100 bytes")
	}
	groups, err := s.UpstreamSiteGroups(ctx, site)
	if err != nil {
		return err
	}
	found := false
	for _, group := range groups {
		found = found || group.ID == groupID
	}
	if !found {
		return upstreamSiteError("Select an available upstream group")
	}
	path := "/api/token/"
	body := map[string]any{"name": name, "group": groupID, "unlimited_quota": true, "remain_quota": 0, "expired_time": -1, "model_limits_enabled": false, "model_limits": "", "allow_ips": "", "cross_group_retry": false}
	if site.Kind == "sub2api" {
		id, e := strconv.ParseInt(groupID, 10, 64)
		if e != nil || id <= 0 {
			return upstreamSiteError("Invalid upstream group ID")
		}
		path = "/api/v1/keys"
		body = map[string]any{"name": name, "group_id": id}
	}
	_, err = s.upstreamSiteRequest(ctx, site, http.MethodPost, path, body)
	if err != nil {
		return upstreamSiteError("Key creation was not confirmed. Refresh the upstream key list before retrying; the upstream may have saved the key")
	}
	return nil
}

func (s *UpstreamBillingProbeService) upstreamSiteModels(ctx context.Context, site *UpstreamSite, key *UpstreamSiteKey) ([]string, error) {
	// Model discovery uses the selected relay key, never the management token.
	modelSite := *site
	modelSite.Kind = "sub2api"
	if site.InferenceURL != "" {
		modelSite.BaseURL = site.InferenceURL
	}
	modelSite.AccessToken = key.secret
	data, err := s.upstreamSiteRequest(ctx, &modelSite, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return nil, err
	}
	rows, err := upstreamRows(data)
	if err != nil {
		return nil, err
	}
	if len(rows) > 2000 {
		return nil, upstreamSiteError("Upstream model catalog is too large")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, row := range rows {
		model := row.Get("id").String()
		if err = ValidateAPIKeyModelMapping(map[string]string{model: model}); err != nil {
			return nil, upstreamSiteError("Invalid model in upstream catalog")
		}
		if !seen[model] {
			seen[model] = true
			out = append(out, model)
		}
	}
	return out, nil
}

func (s *UpstreamBillingProbeService) UpstreamSiteModels(ctx context.Context, site *UpstreamSite, keyID string) ([]string, error) {
	key, err := s.ResolveUpstreamSiteKey(ctx, site, keyID)
	if err != nil {
		return nil, err
	}
	started := time.Now().UTC()
	models, err := s.upstreamSiteModels(ctx, site, key)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, upstreamSiteError("Upstream returned an empty model catalog; existing model selection was kept")
	}
	if err = s.storeUpstreamSiteCatalog(ctx, site, keyID, models, started); err != nil {
		return nil, err
	}
	return models, nil
}

func (s *UpstreamBillingProbeService) UpstreamSiteImportCredentials(ctx context.Context, site *UpstreamSite, keyID string, selectedModels []string) (map[string]any, map[string]any, error) {
	key, err := s.ResolveUpstreamSiteKey(ctx, site, keyID)
	if err != nil {
		return nil, nil, err
	}
	models, err := s.upstreamSiteModels(ctx, site, key)
	if err != nil {
		return nil, nil, err
	}
	available := map[string]bool{}
	for _, model := range models {
		available[model] = true
	}
	mapping := map[string]any{}
	for _, model := range selectedModels {
		if !available[model] {
			return nil, nil, upstreamSiteError("Selected model is no longer in this upstream key's model catalog")
		}
		mapping[model] = model
	}
	current, err := s.GetUpstreamSite(ctx, site.ID)
	if err != nil {
		return nil, nil, err
	}
	if current.Revision != site.Revision {
		return nil, nil, ErrUpstreamBillingProbeIdentityChanged
	}
	baseURL := site.InferenceURL
	if baseURL == "" {
		baseURL = site.BaseURL
	}
	credentials := map[string]any{"base_url": baseURL, "api_key": key.secret, "model_mapping": mapping}
	extra := map[string]any{"upstream_import": map[string]any{"site_id": site.ID, "key_id": key.ID, "group_id": key.GroupID, "revision": site.Revision}}
	return credentials, extra, nil
}
