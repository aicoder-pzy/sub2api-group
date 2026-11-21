package service

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

const (
	accountPoolBaseURL         = "https://aishenji.top"
	vulcanPoolBaseURL          = "https://vulcanapi.com"
	accountPoolRefreshInterval = 10 * time.Minute
	accountPoolHeadInterval    = 90 * time.Second
	accountPoolFirstPage       = "/api/v1/account-pool?page=1&page_size=100"
)

var (
	errPoolSite    = infraerrors.NotFound("ACCOUNT_POOL_SITE", "Account pool is not enabled for this site")
	errPoolPaused  = infraerrors.Conflict("ACCOUNT_POOL_PAUSED", "\u79cb API \u53f7\u6c60\u91c7\u96c6\u5df2\u6682\u505c")
	errPoolChanged = infraerrors.Conflict("ACCOUNT_POOL_CHANGED", "The upstream account list changed during refresh; previous snapshot kept")
	errPoolBusy    = infraerrors.TooManyRequests("ACCOUNT_POOL_BUSY", "Refresh in progress or cooling down; try again in a minute")
)

type accountPoolWindow struct {
	Name string   `json:"name"`
	Used *float64 `json:"used,omitempty"`
}

type accountPoolRow struct {
	Number   int                  `json:"number"`
	Platform string               `json:"platform"`
	Plan     string               `json:"plan"`
	Status   string               `json:"status"`
	Current  *int                 `json:"current,omitempty"`
	Limit    *int                 `json:"limit,omitempty"`
	Windows  []accountPoolWindow  `json:"windows,omitempty"`
	Groups   []string             `json:"pools,omitempty"`
	LastUsed *time.Time           `json:"last_used,omitempty"`
	Admin    *accountPoolAdminRow `json:"admin,omitempty"`
}

type accountPoolAdminRow struct {
	SourceID     string             `json:"source_id,omitempty"`
	Name         string             `json:"name,omitempty"`
	Type         string             `json:"type,omitempty"`
	Plan         string             `json:"plan,omitempty"`
	Status       string             `json:"status,omitempty"`
	Rates        map[string]float64 `json:"rates,omitempty"`
	AccountRate  *float64           `json:"account_rate,omitempty"`
	ObservedAt   *time.Time         `json:"observed_at,omitempty"`
	UsageUpdated *time.Time         `json:"usage_updated,omitempty"`
	Reset5h      *time.Time         `json:"reset_5h,omitempty"`
	Reset7d      *time.Time         `json:"reset_7d,omitempty"`
	RecoverAt    *time.Time         `json:"recover_at,omitempty"`
	Priority     *int               `json:"priority,omitempty"`
	Intelligence string             `json:"intelligence,omitempty"`
}

func (r accountPoolRow) publicRow() accountPoolRow {
	r.Admin = nil
	return r
}

type accountPoolSourceGroup struct {
	SourceID string `json:"source_id"`
	Original string `json:"original"`
	ID       string `json:"id"`
	Count    int    `json:"count"`
}

type accountPoolSnapshot struct {
	SiteRevision int64                    `json:"site_revision,omitempty"`
	Updated      *time.Time               `json:"updated"`
	StartedAt    *time.Time               `json:"started_at,omitempty"`
	CheckedAt    *time.Time               `json:"checked_at,omitempty"`
	Groups       []accountPoolSourceGroup `json:"groups"`
	Rows         []accountPoolRow         `json:"rows"`
}

type accountPoolGroup struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Count    int    `json:"count"`
	Original string `json:"original,omitempty"`
	Alias    string `json:"alias,omitempty"`
}

type AccountPoolDisplay struct {
	Updated         *time.Time         `json:"updated"`
	CheckedAt       *time.Time         `json:"checked_at,omitempty"`
	RecentCheckedAt *time.Time         `json:"recent_checked_at,omitempty"`
	Pools           []accountPoolGroup `json:"pools"`
	Rows            []accountPoolRow   `json:"rows"`
	Refreshing      bool               `json:"refreshing,omitempty"`
	RefreshError    string             `json:"refresh_error,omitempty"`
	Page            *accountPoolPage   `json:"page_info,omitempty"`
}

type accountPoolPage struct {
	Total       int                 `json:"total"`
	Pages       int                 `json:"pages"`
	Platforms   []string            `json:"platforms"`
	PlansByPool map[string][]string `json:"plans_by_pool"`
}

type accountPoolRefreshState struct {
	Error   string
	RetryAt time.Time
}

type accountPoolHead struct {
	revision int64
	rows     map[string]accountPoolRow
}

func (s *UpstreamBillingProbeService) recordAccountPoolHead(site *UpstreamSite, raw []byte) bool {
	var page upstreamPoolPage
	if json.Unmarshal(raw, &page) != nil || page.Page != 1 || page.Size != 100 || page.Total == nil ||
		*page.Total < 0 || len(page.Items) != min(*page.Total, page.Size) {
		return false
	}
	observedAt := time.Now().UTC()
	head := accountPoolHead{revision: site.Revision, rows: make(map[string]accountPoolRow, len(page.Items))}
	for _, item := range page.Items {
		id := item.ID.String()
		if _, duplicate := head.rows[id]; id == "" || duplicate {
			return false
		}
		row := item.liveStatus()
		row.Admin.ObservedAt = &observedAt
		head.rows[id] = row
	}
	s.accountPoolMu.Lock()
	defer s.accountPoolMu.Unlock()
	if s.accountPoolHeads == nil {
		s.accountPoolHeads = make(map[string]accountPoolHead)
	}
	if previous := s.accountPoolHeads[site.ID]; previous.revision == head.revision {
		for id, row := range previous.rows {
			if _, exists := head.rows[id]; !exists && observedAt.Sub(*row.Admin.ObservedAt) < accountPoolRefreshInterval {
				head.rows[id] = row
			}
		}
	}
	s.accountPoolHeads[site.ID] = head
	return true
}

// Apply fresh observations only to members of the last verified inventory.
func (s *UpstreamBillingProbeService) applyAccountPoolHead(siteID string, snapshot *accountPoolSnapshot) *time.Time {
	s.accountPoolMu.Lock()
	head, ok := s.accountPoolHeads[siteID]
	s.accountPoolMu.Unlock()
	if !ok || snapshot.Updated == nil || head.revision != snapshot.SiteRevision {
		return nil
	}
	var recent *time.Time
	for i := range snapshot.Rows {
		row := &snapshot.Rows[i]
		if row.Admin == nil {
			continue
		}
		live, ok := head.rows[row.Admin.SourceID]
		if !ok || !live.Admin.ObservedAt.After(poolTime(row.Admin.ObservedAt)) {
			continue
		}
		row.Status, row.Current, row.Limit, row.Windows = live.Status, live.Current, live.Limit, live.Windows
		row.LastUsed = live.LastUsed
		row.Admin.Status = live.Admin.Status
		row.Admin.ObservedAt = live.Admin.ObservedAt
		if live.Admin.ObservedAt.After(poolTime(recent)) {
			recent = live.Admin.ObservedAt
		}
	}
	return recent
}

func accountPoolEnabled(site *UpstreamSite) bool {
	return site != nil && site.Kind == "sub2api" && site.Purpose == "public" &&
		(site.BaseURL == accountPoolBaseURL || site.BaseURL == vulcanPoolBaseURL)
}

func (s *UpstreamBillingProbeService) readAccountPool(ctx context.Context, siteID string, allowVulcan bool) (accountPoolSnapshot, map[string]string, error) {
	var snapshot accountPoolSnapshot
	var raw, names []byte
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(p.snapshot,'{}'), COALESCE(p.aliases,'{}')
		FROM upstream_sites s LEFT JOIN upstream_site_account_pools p ON s.id=p.site_id
		WHERE s.id=$1 AND (s.base_url=$2 OR ($3 AND s.base_url=$4)) AND s.kind='sub2api' AND s.purpose='public'`,
		siteID, accountPoolBaseURL, allowVulcan, vulcanPoolBaseURL).Scan(&raw, &names)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, nil, errPoolSite
	}
	if err != nil {
		return snapshot, nil, err
	}
	aliases := map[string]string{}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return snapshot, nil, err
	}
	return snapshot, aliases, json.Unmarshal(names, &aliases)
}

func (snapshot accountPoolSnapshot) display(aliases map[string]string, admin bool) AccountPoolDisplay {
	out := AccountPoolDisplay{Updated: snapshot.Updated, Pools: []accountPoolGroup{}, Rows: []accountPoolRow{}}
	if admin {
		out.CheckedAt = snapshot.CheckedAt
		if snapshot.Rows != nil {
			out.Rows = snapshot.Rows
		}
	} else {
		for _, row := range snapshot.Rows {
			out.Rows = append(out.Rows, row.publicRow())
		}
	}
	for i, group := range snapshot.Groups {
		if group.Count == 0 {
			continue
		}
		alias := aliases[group.ID]
		name := alias
		if name == "" {
			name = fmt.Sprintf("\u53f7\u6c60 %d", i+1)
		}
		item := accountPoolGroup{ID: group.ID, Name: name, Count: group.Count}
		if admin {
			item.Original, item.Alias = group.Original, alias
		}
		out.Pools = append(out.Pools, item)
	}
	return out
}

func (s *UpstreamBillingProbeService) AccountPoolDisplay(ctx context.Context, siteID string, admin bool) (AccountPoolDisplay, error) {
	if s == nil || s.db == nil {
		return AccountPoolDisplay{}, ErrUpstreamBillingProbeUnavailable
	}
	if _, err := uuid.Parse(siteID); err != nil {
		return AccountPoolDisplay{}, errPoolSite
	}
	var running bool
	var refreshError string
	if admin {
		s.accountPoolMu.Lock()
		running = s.accountPoolSite == siteID
		refreshError = s.accountPoolRefreshStates[siteID].Error
		s.accountPoolMu.Unlock()
	}
	snapshot, aliases, err := s.readAccountPool(ctx, siteID, admin)
	if err != nil {
		return AccountPoolDisplay{}, err
	}
	var recent *time.Time
	if admin {
		recent = s.applyAccountPoolHead(siteID, &snapshot)
	}
	out := snapshot.display(aliases, admin)
	out.RecentCheckedAt = recent
	out.Refreshing, out.RefreshError = running, refreshError
	return out, nil
}

// Paginate the admin view without hiding filter and publication options from later pages.
func (out *AccountPoolDisplay) Paginate(page, size int, poolID, platform string) {
	page, size = max(1, page), max(1, size)
	info := &accountPoolPage{Total: len(out.Rows), Platforms: []string{}, PlansByPool: map[string][]string{}}
	platforms := map[string]bool{}
	plans := map[string]map[string]bool{}
	matching := make([]accountPoolRow, 0, len(out.Rows))
	for _, row := range out.Rows {
		if !platforms[row.Platform] {
			platforms[row.Platform] = true
			info.Platforms = append(info.Platforms, row.Platform)
		}
		for _, id := range row.Groups {
			if plans[id] == nil {
				plans[id] = map[string]bool{}
			}
			if !plans[id][row.Plan] {
				plans[id][row.Plan] = true
				info.PlansByPool[id] = append(info.PlansByPool[id], row.Plan)
			}
		}
		if (platform == "" || row.Platform == platform) && (poolID == "" || slices.Contains(row.Groups, poolID)) {
			matching = append(matching, row)
		}
	}
	slices.Sort(info.Platforms)
	for _, names := range info.PlansByPool {
		slices.Sort(names)
	}
	slices.SortFunc(matching, compareAccountPoolRows)
	info.Pages = max(1, (len(matching)+size-1)/size)
	start := (min(page, info.Pages) - 1) * size
	out.Page, out.Rows = info, matching[start:min(start+size, len(matching))]
}

func poolTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func compareAccountPoolRows(a, b accountPoolRow) int {
	at, _ := accountPoolActivity(a)
	bt, _ := accountPoolActivity(b)
	if order := poolTime(bt).Compare(poolTime(at)); order != 0 {
		return order
	}
	if order := poolTime(b.LastUsed).Compare(poolTime(a.LastUsed)); order != 0 {
		return order
	}
	return cmp.Compare(a.Number, b.Number)
}

func accountPoolActivity(row accountPoolRow) (*time.Time, string) {
	if row.Admin != nil {
		if row.Admin.UsageUpdated != nil {
			return row.Admin.UsageUpdated, "usage_updated"
		}
		if row.Current != nil && *row.Current > 0 && poolTime(row.Admin.ObservedAt).After(poolTime(row.LastUsed)) {
			return row.Admin.ObservedAt, "active_concurrency"
		}
	}
	if row.LastUsed != nil {
		return row.LastUsed, "last_used"
	}
	return nil, ""
}

func (s *UpstreamBillingProbeService) RenameAccountPool(ctx context.Context, site *UpstreamSite, id, name string) error {
	if !accountPoolEnabled(site) {
		return errPoolSite
	}
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 60 || strings.Contains(name, "@") || strings.IndexFunc(name, unicode.IsControl) != -1 {
		return upstreamSiteError("Invalid display name (maximum 60 characters, no email addresses)")
	}
	if _, err := uuid.Parse(id); err != nil {
		return errPoolSite
	}
	result, err := s.db.ExecContext(ctx, `UPDATE upstream_site_account_pools p SET aliases=aliases || jsonb_build_object($2::text,$3::text)
		FROM upstream_sites s WHERE p.site_id=$1 AND p.site_id=s.id AND s.base_url=$4 AND s.kind='sub2api' AND s.purpose='public'
		AND p.snapshot->'groups' @> jsonb_build_array(jsonb_build_object('id',$2::text))`, site.ID, id, name, site.BaseURL)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count == 0 {
		return errPoolSite
	}
	return nil
}

func (s *UpstreamBillingProbeService) StartAccountPoolRefresh(site *UpstreamSite) error {
	return s.startAccountPoolRefresh(site, false, true)
}

func (s *UpstreamBillingProbeService) startAccountPoolRefresh(site *UpstreamSite, automatic, full bool) error {
	if !accountPoolEnabled(site) {
		return errPoolSite
	}
	if site.BaseURL != accountPoolBaseURL {
		return errPoolPaused
	}
	// ponytail: one refresh per process; use distributed locking for a multi-writer deployment.
	s.accountPoolMu.Lock()
	defer s.accountPoolMu.Unlock()
	state := s.accountPoolRefreshStates[site.ID]
	retryAt := state.RetryAt
	if automatic && full && state.Error != "" {
		retryAt = retryAt.Add(accountPoolRefreshInterval - time.Minute)
	}
	started := time.Now().UTC()
	if s.accountPoolSite != "" || started.Before(retryAt) {
		return errPoolBusy
	}
	s.accountPoolSite = site.ID
	if full {
		delete(s.accountPoolRefreshStates, site.ID)
	}
	go func() {
		timeout := 90 * time.Second
		if full {
			timeout = 25 * time.Minute
		}
		ctx, cancel := context.WithTimeout(s.parentCtx, timeout)
		defer cancel()
		requests := 0
		fetch := &accountPoolFetcher{ctx: ctx, request: func(path string) ([]byte, error) {
			requests++
			return s.upstreamSiteRequestWithStatus(ctx, site, http.MethodGet, path, nil)
		}}
		var err error
		if full {
			var headAt time.Time
			err = s.refreshAccountPool(ctx, site, started, func(path string) ([]byte, error) {
				if path != accountPoolFirstPage && !headAt.IsZero() && time.Since(headAt) >= accountPoolHeadInterval {
					raw, err := fetch.get(accountPoolFirstPage)
					if code, _ := infraerrors.ToHTTP(err); code == http.StatusTooManyRequests {
						return nil, err
					}
					if err == nil {
						s.recordAccountPoolHead(site, raw)
					}
					headAt = time.Now()
				}
				raw, err := fetch.get(path)
				if err == nil && path == accountPoolFirstPage {
					s.recordAccountPoolHead(site, raw)
					headAt = time.Now()
				}
				return raw, err
			})
		} else {
			raw, fetchErr := fetch.get(accountPoolFirstPage)
			if fetchErr == nil {
				s.recordAccountPoolHead(site, raw)
			}
		}
		s.accountPoolMu.Lock()
		defer s.accountPoolMu.Unlock()
		state := s.accountPoolRefreshStates[site.ID]
		if full {
			state = accountPoolRefreshState{RetryAt: time.Now().Add(time.Minute)}
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				state.Error = "Account pool collection timed out; previous snapshot kept"
			} else {
				_, status := infraerrors.ToHTTP(err)
				state.Error = status.Message
			}
		}
		if fetch.retryAt.After(state.RetryAt) {
			state.RetryAt = fetch.retryAt
		}
		if full && s.parentCtx.Err() == nil {
			code, status := infraerrors.ToHTTP(err)
			logger.LegacyPrintf("service.upstream_account_pool", "full_refresh_finished: status=%d reason=%s requests=%d elapsed_ms=%d", code, status.Reason, requests, time.Since(started).Milliseconds())
		}
		if s.accountPoolRefreshStates == nil {
			s.accountPoolRefreshStates = map[string]accountPoolRefreshState{}
		}
		s.accountPoolRefreshStates[site.ID] = state
		s.accountPoolSite = ""
	}()
	return nil
}

func (s *UpstreamBillingProbeService) refreshDueAccountPool(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	s.accountPoolMu.Lock()
	running := s.accountPoolSite != ""
	s.accountPoolMu.Unlock()
	if running {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,
		(COALESCE(p.snapshot->>'started_at',p.snapshot->>'checked_at')::timestamptz IS NULL
		 OR COALESCE(p.snapshot->>'started_at',p.snapshot->>'checked_at')::timestamptz <= $2
		 OR (p.snapshot->>'site_revision')::bigint IS DISTINCT FROM s.revision) AS due
		FROM upstream_sites s
		LEFT JOIN upstream_site_account_pools p ON p.site_id=s.id
		WHERE s.purpose='public' AND s.kind='sub2api' AND s.base_url=$1
		ORDER BY due DESC, COALESCE(p.snapshot->>'started_at',p.snapshot->>'checked_at')::timestamptz NULLS FIRST,s.id`,
		accountPoolBaseURL, time.Now().Add(-accountPoolRefreshInterval))
	if err != nil {
		return err
	}
	defer rows.Close()
	type candidate struct {
		id  string
		due bool
	}
	var sites []candidate
	for rows.Next() {
		var next candidate
		if err := rows.Scan(&next.id, &next.due); err != nil {
			return err
		}
		sites = append(sites, next)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, next := range sites {
		site, err := s.GetUpstreamSite(ctx, next.id)
		if err != nil {
			return err
		}
		if err := s.startAccountPoolRefresh(site, true, next.due); err == nil {
			return nil
		} else if !errors.Is(err, errPoolBusy) && !errors.Is(err, errPoolSite) && !errors.Is(err, errPoolPaused) {
			return err
		}
	}
	return nil
}

type accountPoolFetcher struct {
	ctx           context.Context
	request       func(string) ([]byte, error)
	next, retryAt time.Time
}

func accountPoolRetryDelay(value string, now time.Time) time.Duration {
	delay := time.Minute
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds >= 0 {
		delay = time.Duration(seconds) * time.Second
	} else if when, err := http.ParseTime(value); err == nil {
		delay = when.Sub(now)
	}
	if delay < 6*time.Second {
		return 6 * time.Second
	}
	return delay
}

func (f *accountPoolFetcher) get(path string) ([]byte, error) {
	const maxRetries = 3
	for attempt := 0; ; attempt++ {
		if wait := time.Until(f.next); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-f.ctx.Done():
				timer.Stop()
				return nil, f.ctx.Err()
			case <-timer.C:
			}
		}
		f.next = time.Now().Add(6 * time.Second)
		data, err := f.request(path)
		var status *upstreamSiteHTTPError
		if !errors.As(err, &status) {
			return data, err
		}
		if status.code != http.StatusTooManyRequests {
			return nil, upstreamSiteError(status.Error())
		}
		now := time.Now()
		delay := accountPoolRetryDelay(status.retryAfter, now)
		if minimum := (15 * time.Second) << attempt; delay < minimum {
			delay = minimum
		}
		f.retryAt = now.Add(delay)
		deadline, _ := f.ctx.Deadline()
		if attempt == maxRetries || f.retryAt.After(deadline) {
			return nil, infraerrors.TooManyRequests("ACCOUNT_POOL_UPSTREAM_RATE_LIMIT", "上游限流，已保留上一份快照，请稍后重试")
		}
		f.next = f.retryAt
	}
}

func (s *UpstreamBillingProbeService) refreshAccountPool(ctx context.Context, site *UpstreamSite, started time.Time, get func(string) ([]byte, error)) error {
	previous, _, err := s.readAccountPool(ctx, site.ID, true)
	if err != nil {
		return err
	}
	var snapshot accountPoolSnapshot
	if site.BaseURL == vulcanPoolBaseURL {
		var raw []byte
		raw, err = get("/api/v1/public/account-pool")
		if err == nil {
			snapshot, err = collectVulcanPool(raw, previous.Groups)
		}
	} else {
		if previous.SiteRevision != site.Revision {
			previous.Rows = nil
		}
		snapshot, err = collectShenjiPool(get, previous)
	}
	if err != nil {
		return err
	}
	snapshot.SiteRevision = site.Revision
	snapshot.StartedAt = &started
	checkedAt := time.Now().UTC()
	snapshot.CheckedAt = &checkedAt
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO upstream_site_account_pools(site_id,snapshot)
		SELECT id,$2::jsonb FROM upstream_sites WHERE id=$1 AND base_url=$3 AND kind='sub2api' AND purpose='public' AND revision=$4
		ON CONFLICT (site_id) DO UPDATE SET snapshot=EXCLUDED.snapshot`, site.ID, string(raw), site.BaseURL, site.Revision)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count == 0 {
		return errPoolSite
	}
	return nil
}

func collectShenjiPool(get func(string) ([]byte, error), previous accountPoolSnapshot) (accountPoolSnapshot, error) {
	data, err := get("/api/v1/account-pool/access")
	if err != nil {
		return accountPoolSnapshot{}, err
	}
	var access struct {
		Verified bool `json:"verified"`
	}
	if json.Unmarshal(data, &access) != nil || !access.Verified {
		return accountPoolSnapshot{}, upstreamSiteError("The saved upstream login does not have account pool access")
	}
	return collectAccountPool(get, previous.Groups, previous.Rows)
}

type upstreamPoolPage struct {
	Page  int                `json:"page"`
	Size  int                `json:"page_size"`
	Total *int               `json:"total"`
	Pages int                `json:"pages"`
	Items []upstreamPoolItem `json:"items"`
}

type upstreamPoolItem struct {
	ID          json.Number `json:"id"`
	Name        string      `json:"display_name"`
	Type        string      `json:"auth_type"`
	Platform    string      `json:"platform"`
	Plan        string      `json:"plan_label"`
	Status      string      `json:"status"`
	Current     *int        `json:"current_concurrency"`
	Limit       *int        `json:"concurrency"`
	LastUsed    string      `json:"last_used_at"`
	RateEnabled *bool       `json:"account_rate_enabled"`
	Rate        *float64    `json:"rate_multiplier"`
	Windows     []struct {
		Name string   `json:"label"`
		Used *float64 `json:"utilization"`
	} `json:"usage_windows"`
	Groups []struct {
		ID        json.Number `json:"id"`
		Name      string      `json:"name"`
		Effective *float64    `json:"effective_multiplier"`
		Rate      *float64    `json:"rate_multiplier"`
	} `json:"groups"`
}

func (item upstreamPoolItem) liveStatus() accountPoolRow {
	row := accountPoolRow{Status: poolEnum(item.Status, "active", "error", "rate_limited", "paused", "disabled", "inactive"),
		Current: poolNumber(item.Current), Limit: poolNumber(item.Limit), LastUsed: poolTimestamp(item.LastUsed),
		Admin: &accountPoolAdminRow{Status: item.Status}}
	for _, w := range item.Windows[:min(6, len(item.Windows))] {
		name := poolEnum(w.Name, "5h", "7d", "requests", "tokens")
		if name != "unknown" && w.Used != nil && *w.Used >= 0 && *w.Used < 1e9 {
			row.Windows = append(row.Windows, accountPoolWindow{Name: name, Used: w.Used})
		}
	}
	return row
}

func collectAccountPool(get func(string) ([]byte, error), previous []accountPoolSourceGroup, known []accountPoolRow) (accountPoolSnapshot, error) {
	snapshot, err := collectAccountPoolPass(get, previous, known)
	// Reset coverage if the upstream inventory changed or could not be covered.
	if errors.Is(err, errPoolChanged) {
		return collectAccountPoolPass(get, previous, nil)
	}
	return snapshot, err
}

func collectAccountPoolPass(get func(string) ([]byte, error), previous []accountPoolSourceGroup, known []accountPoolRow) (accountPoolSnapshot, error) {
	const size, maxPages = 100, 100
	out := accountPoolSnapshot{Groups: append([]accountPoolSourceGroup{}, previous...)}
	groupIndex := make(map[string]int)
	for i := range out.Groups {
		groupIndex[out.Groups[i].SourceID] = i
		out.Groups[i].Count = 0
	}
	positions := map[json.Number]int{}
	total := -1
	pageCount := func(width int) int { return max(1, (total+width-1)/width) }
	read := func(page, width int, exactID string) error {
		path := fmt.Sprintf("/api/v1/account-pool?page=%d&page_size=%d", page, width)
		if exactID != "" {
			path += "&search=" + url.QueryEscape(exactID)
		}
		data, err := get(path)
		if err != nil {
			return err
		}
		var result upstreamPoolPage
		if json.Unmarshal(data, &result) != nil || result.Total == nil || result.Items == nil || *result.Total < 0 || *result.Total > size*maxPages {
			return errPoolChanged
		}
		if exactID != "" {
			if result.Page != 1 || result.Size != width || result.Pages != 1 || *result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID.String() != exactID {
				return nil
			}
		} else {
			if total < 0 {
				total = *result.Total
			}
			if result.Page != page || result.Size != width || *result.Total != total || result.Pages != pageCount(width) || len(result.Items) != min(width, total-(page-1)*width) {
				return errPoolChanged
			}
		}
		observedAt := time.Now().UTC()
		inPage := make(map[json.Number]bool, len(result.Items))
		for _, item := range result.Items {
			if item.ID == "" || inPage[item.ID] {
				return errPoolChanged
			}
			inPage[item.ID] = true
			index, exists := positions[item.ID]
			if !exists && len(out.Rows) == total {
				return errPoolChanged
			}
			if !exists {
				index = len(out.Rows)
				positions[item.ID] = index
			}
			plan := item.Plan
			if strings.EqualFold(plan, "Pro Max") || strings.EqualFold(plan, "promax") {
				plan = "promax"
			}
			row := item.liveStatus()
			row.Number, row.Platform = index+1, poolEnum(item.Platform, "openai", "grok", "anthropic", "gemini")
			row.Plan = poolEnum(plan, "promax", "Pro", "Plus", "Team", "Free", "go", "SuperGrok Heavy", "pro20", "SuperGrok")
			row.Admin.SourceID, row.Admin.Name, row.Admin.Type, row.Admin.Plan, row.Admin.ObservedAt = item.ID.String(), item.Name, item.Type, item.Plan, &observedAt
			for _, group := range item.Groups {
				if group.ID == "" {
					return errPoolChanged
				}
				id := group.ID.String()
				i, ok := groupIndex[id]
				if !ok {
					i = len(out.Groups)
					if i >= 1000 {
						return errPoolChanged
					}
					groupIndex[id] = i
					out.Groups = append(out.Groups, accountPoolSourceGroup{SourceID: id, ID: uuid.NewString()})
				}
				entry := &out.Groups[i]
				entry.Original = accountPoolGroupName(group.Name)
				rate := group.Effective
				if rate == nil {
					rate = group.Rate
				}
				if rate != nil {
					if row.Admin.Rates == nil {
						row.Admin.Rates = make(map[string]float64)
					}
					row.Admin.Rates[entry.ID] = *rate
				}
				duplicate := false
				for _, assigned := range row.Groups {
					if assigned == entry.ID {
						duplicate = true
						break
					}
				}
				if !duplicate {
					row.Groups = append(row.Groups, entry.ID)
				}
			}
			if len(row.Admin.Rates) == 0 || (item.RateEnabled != nil && *item.RateEnabled) {
				row.Admin.AccountRate = item.Rate
			}
			if exists {
				out.Rows[index] = row
			} else {
				out.Rows = append(out.Rows, row)
			}
		}
		return nil
	}
	if err := read(1, size, ""); err != nil {
		return accountPoolSnapshot{}, err
	}
	pages := pageCount(size)
	firstDrift, lastDrift := 1, 1
	for page := pages; page > 1; page-- {
		before := len(out.Rows)
		if err := read(page, size, ""); err != nil {
			return accountPoolSnapshot{}, err
		}
		if len(out.Rows)-before < min(size, total-(page-1)*size) {
			if lastDrift == 1 {
				lastDrift = page
			}
			firstDrift = page
		}
	}
	// A small number of missing members can be cheaper to check by verified ID.
	triedKnown := false
	fillKnown := func() error {
		missing := total - len(out.Rows)
		if triedKnown || missing == 0 || missing > 6 {
			return nil
		}
		triedKnown = true
		attempts := 0
		for _, row := range known {
			if row.Admin == nil || row.Admin.SourceID == "" {
				continue
			}
			if _, seen := positions[json.Number(row.Admin.SourceID)]; seen {
				continue
			}
			if err := read(1, size, row.Admin.SourceID); err != nil {
				if errors.Is(err, errPoolChanged) {
					break
				}
				return err
			}
			attempts++
			if len(out.Rows) == total || attempts == missing {
				break
			}
		}
		return nil
	}
	if err := fillKnown(); err != nil {
		return accountPoolSnapshot{}, err
	}
	// ponytail: Shenji has no stable cursor. Shift the boundaries around pages
	// that overlapped before falling back to a full sweep at a different width.
	lo, hi := max(0, firstDrift-2)*size, min(total, (lastDrift+1)*size)
	for _, width := range []int{80, 90, 95} {
		start, end := lo/width+1, (hi+width-1)/width
		if width == 95 {
			start, end = 1, pageCount(width)
		}
		for page := start; page <= end && len(out.Rows) < total; page++ {
			if err := read(page, width, ""); err != nil {
				return accountPoolSnapshot{}, err
			}
			if err := fillKnown(); err != nil {
				return accountPoolSnapshot{}, err
			}
		}
	}
	if len(out.Rows) != total {
		return accountPoolSnapshot{}, errPoolChanged
	}
	if pages > 1 {
		if err := read(1, size, ""); err != nil {
			return accountPoolSnapshot{}, err
		}
	}
	groupsByID := make(map[string]*accountPoolSourceGroup, len(out.Groups))
	for i := range out.Groups {
		groupsByID[out.Groups[i].ID] = &out.Groups[i]
	}
	for _, row := range out.Rows {
		for _, id := range row.Groups {
			groupsByID[id].Count++
		}
	}
	now := time.Now().UTC()
	out.Updated = &now
	return out, nil
}

func accountPoolGroupName(value string) string {
	if strings.Contains(value, "@") {
		return ""
	}
	name := []rune(strings.TrimSpace(value))
	return string(name[:min(100, len(name))])
}

func poolEnum(value string, allowed ...string) string {
	for _, entry := range allowed {
		if value == entry {
			return entry
		}
	}
	return "unknown"
}

func poolNumber(value *int) *int {
	if value == nil || *value < 0 || *value >= 1e9 {
		return nil
	}
	return value
}

func poolTimestamp(raw string) *time.Time {
	stamp, err := time.Parse(time.RFC3339, raw)
	if err != nil || stamp.IsZero() {
		return nil
	}
	return &stamp
}
