package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	poolStatusSettingPrefix = "published_account_pool_"
	poolStatusCredentialKey = "account_pool_status_credential"
)

func (s *UpstreamBillingProbeService) AccountPoolStatusCredential(ctx context.Context) (string, error) {
	if s == nil || s.settingService == nil || s.settingService.settingRepo == nil {
		return "", nil
	}
	value, err := s.settingService.settingRepo.GetValue(ctx, poolStatusCredentialKey)
	if errors.Is(err, ErrSettingNotFound) {
		return "", nil
	}
	return value, err
}

func (s *UpstreamBillingProbeService) SetAccountPoolStatusCredential(ctx context.Context, value string) error {
	if s == nil || s.settingService == nil || s.settingService.settingRepo == nil {
		return ErrUpstreamBillingProbeUnavailable
	}
	return s.settingService.settingRepo.Set(ctx, poolStatusCredentialKey, value)
}

type AccountPoolStatusConfig struct {
	PoolID     string            `json:"pool_id"`
	SourceName string            `json:"source_name"`
	Name       string            `json:"name"`
	PlanMap    map[string]string `json:"plan_map"`
	Plans      []string          `json:"plans"`
}

type storedAccountPoolStatusConfig struct {
	AccountPoolStatusConfig
	BaseURL string `json:"base_url"`
}

type accountPoolStatusRow struct {
	Number       int                        `json:"number"`
	Platform     string                     `json:"platform"`
	Plan         string                     `json:"plan"`
	Status       string                     `json:"status"`
	AccountType  string                     `json:"account_type,omitempty"`
	Current      *int                       `json:"current,omitempty"`
	Limit        *int                       `json:"limit,omitempty"`
	Windows      []accountPoolWindow        `json:"windows,omitempty"`
	LastUsed     *time.Time                 `json:"last_used,omitempty"`
	ObservedAt   *time.Time                 `json:"observed_at,omitempty"`
	UsageUpdated *time.Time                 `json:"usage_updated,omitempty"`
	Reset5h      *time.Time                 `json:"reset_5h,omitempty"`
	Reset7d      *time.Time                 `json:"reset_7d,omitempty"`
	RecoverAt    *time.Time                 `json:"recover_at,omitempty"`
	ActivityAt   *time.Time                 `json:"activity_at,omitempty"`
	ActivityKind string                     `json:"activity_kind,omitempty"`
	Admin        *accountPoolStatusIdentity `json:"admin,omitempty"`
}

type accountPoolStatusIdentity struct {
	Name     string `json:"name,omitempty"`
	SourceID string `json:"source_id,omitempty"`
}

type PublishedAccountPoolStatus struct {
	ID        string                 `json:"id"`
	Source    string                 `json:"source"`
	Name      string                 `json:"name"`
	Updated   *time.Time             `json:"updated"`
	CheckedAt *time.Time             `json:"checked_at,omitempty"`
	Available bool                   `json:"snapshot_available"`
	Total     int                    `json:"total"`
	Plans     map[string]int         `json:"plans"`
	Statuses  map[string]int         `json:"statuses"`
	Rows      []accountPoolStatusRow `json:"rows"`
	Pages     int                    `json:"pages"`
}

func (s *UpstreamBillingProbeService) AccountPoolStatusConfig(ctx context.Context, site *UpstreamSite) (AccountPoolStatusConfig, error) {
	if !accountPoolEnabled(site) {
		return AccountPoolStatusConfig{}, errPoolSite
	}
	raw, err := s.settingService.settingRepo.GetValue(ctx, poolStatusSettingPrefix+site.ID)
	if errors.Is(err, ErrSettingNotFound) {
		return AccountPoolStatusConfig{}, nil
	}
	if err != nil {
		return AccountPoolStatusConfig{}, err
	}
	var stored storedAccountPoolStatusConfig
	err = json.Unmarshal([]byte(raw), &stored)
	if stored.BaseURL != site.BaseURL {
		return AccountPoolStatusConfig{}, err
	}
	return stored.AccountPoolStatusConfig, err
}

func (s *UpstreamBillingProbeService) SaveAccountPoolStatusConfig(ctx context.Context, site *UpstreamSite, cfg AccountPoolStatusConfig) (AccountPoolStatusConfig, error) {
	if !accountPoolEnabled(site) {
		return AccountPoolStatusConfig{}, errPoolSite
	}
	key := poolStatusSettingPrefix + site.ID
	if cfg.PoolID == "" {
		return AccountPoolStatusConfig{}, s.settingService.settingRepo.Delete(ctx, key)
	}
	snapshot, _, err := s.readAccountPool(ctx, site.ID, true)
	if err != nil {
		return AccountPoolStatusConfig{}, err
	}
	if !snapshot.hasCurrentPool(site.Revision, cfg.PoolID) {
		return AccountPoolStatusConfig{}, upstreamSiteError("Refresh the upstream account pool and select an existing group")
	}
	if err := cfg.normalize(); err != nil {
		return AccountPoolStatusConfig{}, err
	}
	raw, err := json.Marshal(storedAccountPoolStatusConfig{AccountPoolStatusConfig: cfg, BaseURL: site.BaseURL})
	if err != nil {
		return AccountPoolStatusConfig{}, err
	}
	return cfg, s.settingService.settingRepo.Set(ctx, key, string(raw))
}

func validPoolStatusLabel(label string) bool {
	return label != "" && utf8.RuneCountInString(label) <= 60 && !strings.Contains(label, "@") && strings.IndexFunc(label, unicode.IsControl) == -1
}

func (c *AccountPoolStatusConfig) normalize() error {
	c.SourceName = strings.TrimSpace(c.SourceName)
	c.Name = strings.TrimSpace(c.Name)
	if !validPoolStatusLabel(c.SourceName) || !validPoolStatusLabel(c.Name) || len(c.Plans) == 0 || len(c.Plans) > 32 || len(c.PlanMap) > 32 {
		return upstreamSiteError("Provide display names and select 1 to 32 plans")
	}
	plans := make(map[string]string, len(c.Plans))
	for i, plan := range c.Plans {
		plan = strings.TrimSpace(plan)
		key := strings.ToLower(plan)
		if !validPoolStatusLabel(plan) || plans[key] != "" {
			return upstreamSiteError("Displayed plan names must be valid and unique")
		}
		c.Plans[i], plans[key] = plan, plan
	}
	planMap := make(map[string]string, len(c.PlanMap))
	for source, target := range c.PlanMap {
		source, target = strings.ToLower(strings.TrimSpace(source)), strings.TrimSpace(target)
		if !validPoolStatusLabel(source) || !validPoolStatusLabel(target) || planMap[source] != "" {
			return upstreamSiteError("Invalid or duplicate plan mapping")
		}
		planMap[source] = target
	}
	c.PlanMap = planMap
	return nil
}

func (p accountPoolSnapshot) hasCurrentPool(revision int64, poolID string) bool {
	return p.Updated != nil && !p.Updated.IsZero() && (p.SiteRevision == revision || (p.SiteRevision == 0 && revision == 1)) &&
		slices.ContainsFunc(p.Groups, func(group accountPoolSourceGroup) bool { return group.ID == poolID })
}

func publishedAccountPoolRow(row accountPoolRow, number int) accountPoolStatusRow {
	out := accountPoolStatusRow{Number: number, Platform: row.Platform, Plan: row.Plan, Status: row.Status,
		Current: row.Current, Limit: row.Limit, Windows: row.Windows, LastUsed: row.LastUsed}
	out.ActivityAt, out.ActivityKind = accountPoolActivity(row)
	if row.Admin != nil {
		switch row.Admin.Type {
		case "oauth", "apikey", "setup-token", "upstream", "bedrock", "service_account":
			out.AccountType = row.Admin.Type
		}
		out.ObservedAt, out.UsageUpdated = row.Admin.ObservedAt, row.Admin.UsageUpdated
		out.Reset5h, out.Reset7d, out.RecoverAt = row.Admin.Reset5h, row.Admin.Reset7d, row.Admin.RecoverAt
	}
	return out
}

func (c AccountPoolStatusConfig) status(snapshot accountPoolSnapshot, revision int64, page, size int, includeIdentity bool) PublishedAccountPoolStatus {
	out := PublishedAccountPoolStatus{ID: c.PoolID, Source: c.SourceName, Name: c.Name, Plans: map[string]int{}, Statuses: map[string]int{}, Rows: []accountPoolStatusRow{}, Pages: 1}
	if !snapshot.hasCurrentPool(revision, c.PoolID) {
		return out
	}
	out.Available, out.Updated, out.CheckedAt = true, snapshot.Updated, snapshot.CheckedAt
	allowed := make(map[string]string, len(c.Plans))
	for _, plan := range c.Plans {
		allowed[strings.ToLower(plan)] = plan
	}
	var matching []accountPoolRow
	for _, row := range snapshot.Rows {
		if !slices.Contains(row.Groups, c.PoolID) {
			continue
		}
		plan := row.Plan
		if alias := c.PlanMap[strings.ToLower(plan)]; alias != "" {
			plan = alias
		} else if strings.EqualFold(plan, "promax") {
			plan = "Pro Max"
		}
		plan = allowed[strings.ToLower(plan)]
		if plan == "" {
			continue
		}
		row.Plan = plan
		matching = append(matching, row)
		out.Plans[plan]++
		out.Statuses[row.Status]++
	}
	out.Total = len(matching)
	out.Pages = max(1, (out.Total+size-1)/size)
	if page <= out.Pages {
		slices.SortFunc(matching, compareAccountPoolRows)
		start := (page - 1) * size
		for i := start; i < min(start+size, out.Total); i++ {
			row := publishedAccountPoolRow(matching[i], i+1)
			if admin := matching[i].Admin; includeIdentity && admin != nil && (admin.Name != "" || admin.SourceID != "") {
				row.Admin = &accountPoolStatusIdentity{Name: admin.Name, SourceID: admin.SourceID}
			}
			out.Rows = append(out.Rows, row)
		}
	}
	return out
}

func (s *UpstreamBillingProbeService) AccountPoolStatus(ctx context.Context, page, size int, includeIdentity bool) ([]PublishedAccountPoolStatus, error) {
	if s == nil || s.db == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.base_url, s.revision, conf.value, COALESCE(p.snapshot,'{}')
		FROM upstream_sites s JOIN settings conf ON conf.key=$1 || s.id::text
		LEFT JOIN upstream_site_account_pools p ON s.id=p.site_id
		WHERE s.purpose='public' AND s.kind='sub2api' AND s.base_url IN ($2,$3)
		ORDER BY s.id`, poolStatusSettingPrefix, accountPoolBaseURL, vulcanPoolBaseURL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PublishedAccountPoolStatus{}
	for rows.Next() {
		var baseURL string
		var revision int64
		var rawConfig, rawSnapshot []byte
		if err := rows.Scan(&baseURL, &revision, &rawConfig, &rawSnapshot); err != nil {
			return nil, err
		}
		var cfg storedAccountPoolStatusConfig
		if err := json.Unmarshal(rawConfig, &cfg); err != nil {
			return nil, err
		}
		if cfg.BaseURL != baseURL || cfg.PoolID == "" {
			continue
		}
		var snapshot accountPoolSnapshot
		if err := json.Unmarshal(rawSnapshot, &snapshot); err != nil {
			return nil, err
		}
		out = append(out, cfg.status(snapshot, revision, page, size, includeIdentity))
	}
	return out, rows.Err()
}
