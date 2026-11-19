package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const accountPoolBaseURL = "https://aishenji.top"

var (
	errPoolSite    = infraerrors.NotFound("ACCOUNT_POOL_SITE", "Account pool is not enabled for this site")
	errPoolChanged = infraerrors.Conflict("ACCOUNT_POOL_CHANGED", "The upstream account list changed during refresh; previous snapshot kept")
	errPoolBusy    = infraerrors.TooManyRequests("ACCOUNT_POOL_BUSY", "Refresh in progress or cooling down; try again in a minute")
)

type accountPoolWindow struct {
	Name string   `json:"name"`
	Used *float64 `json:"used,omitempty"`
}

type accountPoolRow struct {
	Number   int                 `json:"number"`
	Platform string              `json:"platform"`
	Plan     string              `json:"plan"`
	Status   string              `json:"status"`
	Current  *int                `json:"current,omitempty"`
	Limit    *int                `json:"limit,omitempty"`
	Windows  []accountPoolWindow `json:"windows,omitempty"`
	Groups   []string            `json:"pools,omitempty"`
	LastUsed *time.Time          `json:"last_used,omitempty"`
}

type accountPoolSourceGroup struct {
	SourceID string `json:"source_id"`
	Original string `json:"original"`
	ID       string `json:"id"`
	Count    int    `json:"count"`
}

type accountPoolSnapshot struct {
	Updated *time.Time               `json:"updated"`
	Groups  []accountPoolSourceGroup `json:"groups"`
	Rows    []accountPoolRow         `json:"rows"`
}

type accountPoolGroup struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Count    int    `json:"count"`
	Original string `json:"original,omitempty"`
	Alias    string `json:"alias,omitempty"`
}

type AccountPoolDisplay struct {
	Updated      *time.Time         `json:"updated"`
	Pools        []accountPoolGroup `json:"pools"`
	Rows         []accountPoolRow   `json:"rows"`
	Refreshing   bool               `json:"refreshing,omitempty"`
	RefreshError string             `json:"refresh_error,omitempty"`
}

func accountPoolEnabled(site *UpstreamSite) bool {
	return site != nil && site.Kind == "sub2api" && site.Purpose == "public" && site.BaseURL == accountPoolBaseURL
}

func (s *UpstreamBillingProbeService) readAccountPool(ctx context.Context, siteID string) (accountPoolSnapshot, map[string]string, error) {
	var snapshot accountPoolSnapshot
	var raw, names []byte
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(p.snapshot,'{}'), COALESCE(p.aliases,'{}')
		FROM upstream_sites s LEFT JOIN upstream_site_account_pools p ON s.id=p.site_id
		WHERE s.id=$1 AND s.base_url=$2 AND s.kind='sub2api' AND s.purpose='public'`, siteID, accountPoolBaseURL).Scan(&raw, &names)
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
	out := AccountPoolDisplay{Updated: snapshot.Updated, Pools: []accountPoolGroup{}, Rows: snapshot.Rows}
	if out.Rows == nil {
		out.Rows = []accountPoolRow{}
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
		if s.accountPoolSite == siteID {
			running, refreshError = s.accountPoolRunning, s.accountPoolError
		}
		s.accountPoolMu.Unlock()
	}
	snapshot, aliases, err := s.readAccountPool(ctx, siteID)
	if err != nil {
		return AccountPoolDisplay{}, err
	}
	out := snapshot.display(aliases, admin)
	out.Refreshing, out.RefreshError = running, refreshError
	return out, nil
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
		AND p.snapshot->'groups' @> jsonb_build_array(jsonb_build_object('id',$2::text))`, site.ID, id, name, accountPoolBaseURL)
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
	if !accountPoolEnabled(site) {
		return errPoolSite
	}
	// ponytail: one refresh per process; add distributed locking for a multi-writer deployment.
	s.accountPoolMu.Lock()
	defer s.accountPoolMu.Unlock()
	if s.accountPoolRunning || time.Now().Before(s.accountPoolRetryAt) {
		return errPoolBusy
	}
	s.accountPoolSite, s.accountPoolRunning, s.accountPoolError = site.ID, true, ""
	go func() {
		ctx, cancel := context.WithTimeout(s.parentCtx, 7*time.Minute)
		defer cancel()
		fetch := &accountPoolFetcher{ctx: ctx, request: func(path string) ([]byte, error) {
			return s.upstreamSiteRequestWithStatus(ctx, site, http.MethodGet, path, nil)
		}}
		err := s.refreshAccountPool(ctx, site, fetch.get)
		s.accountPoolMu.Lock()
		defer s.accountPoolMu.Unlock()
		if err != nil {
			_, status := infraerrors.ToHTTP(err)
			s.accountPoolError = status.Message
		}
		s.accountPoolRetryAt = time.Now().Add(time.Minute)
		if fetch.retryAt.After(s.accountPoolRetryAt) {
			s.accountPoolRetryAt = fetch.retryAt
		}
		s.accountPoolRunning = false
	}()
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
		f.retryAt = now.Add(accountPoolRetryDelay(status.retryAfter, now))
		deadline, _ := f.ctx.Deadline()
		if attempt == 1 || f.retryAt.After(deadline) {
			return nil, infraerrors.TooManyRequests("ACCOUNT_POOL_UPSTREAM_RATE_LIMIT", "神鸡上游限流，已保留上一份快照，请稍后重试")
		}
		f.next = f.retryAt
	}
}

func (s *UpstreamBillingProbeService) refreshAccountPool(ctx context.Context, site *UpstreamSite, get func(string) ([]byte, error)) error {
	previous, _, err := s.readAccountPool(ctx, site.ID)
	if err != nil {
		return err
	}
	data, err := get("/api/v1/account-pool/access")
	if err != nil {
		return err
	}
	var access struct {
		Verified bool `json:"verified"`
	}
	if json.Unmarshal(data, &access) != nil || !access.Verified {
		return upstreamSiteError("The saved upstream login does not have account pool access")
	}
	snapshot, err := collectAccountPool(get, previous.Groups)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO upstream_site_account_pools(site_id,snapshot)
		SELECT id,$2::jsonb FROM upstream_sites WHERE id=$1 AND base_url=$3 AND kind='sub2api' AND purpose='public'
		ON CONFLICT (site_id) DO UPDATE SET snapshot=EXCLUDED.snapshot`, site.ID, string(raw), accountPoolBaseURL)
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

type upstreamPoolPage struct {
	Page  int  `json:"page"`
	Size  int  `json:"page_size"`
	Total *int `json:"total"`
	Pages int  `json:"pages"`
	Items []struct {
		ID       json.Number `json:"id"`
		Platform string      `json:"platform"`
		Plan     string      `json:"plan_label"`
		Status   string      `json:"status"`
		Current  *int        `json:"current_concurrency"`
		Limit    *int        `json:"concurrency"`
		LastUsed string      `json:"last_used_at"`
		Windows  []struct {
			Name string   `json:"label"`
			Used *float64 `json:"utilization"`
		} `json:"usage_windows"`
		Groups []struct {
			ID   json.Number `json:"id"`
			Name string      `json:"name"`
		} `json:"groups"`
	} `json:"items"`
}

func collectAccountPool(get func(string) ([]byte, error), previous []accountPoolSourceGroup) (accountPoolSnapshot, error) {
	const size, maxPages = 100, 100
	out := accountPoolSnapshot{Groups: append([]accountPoolSourceGroup{}, previous...)}
	groupIndex := make(map[string]int)
	for i := range out.Groups {
		groupIndex[out.Groups[i].SourceID] = i
		out.Groups[i].Count = 0
	}
	seen := map[json.Number]bool{}
	total, pages := -1, 1
	read := func(page int) error {
		data, err := get(fmt.Sprintf("/api/v1/account-pool?page=%d&page_size=%d", page, size))
		if err != nil {
			return err
		}
		var result upstreamPoolPage
		if json.Unmarshal(data, &result) != nil || result.Total == nil || result.Items == nil || *result.Total < 0 || *result.Total > size*maxPages {
			return errPoolChanged
		}
		if total < 0 {
			total = *result.Total
			pages = max(1, (total+size-1)/size)
		}
		if result.Page != page || result.Size != size || *result.Total != total || result.Pages != pages || len(result.Items) != min(size, total-(page-1)*size) {
			return errPoolChanged
		}
		inPage := make(map[json.Number]bool, len(result.Items))
		for _, item := range result.Items {
			if item.ID == "" || inPage[item.ID] {
				return errPoolChanged
			}
			inPage[item.ID] = true
			if seen[item.ID] {
				continue
			}
			if len(seen) == total {
				return errPoolChanged
			}
			seen[item.ID] = true
			row := accountPoolRow{Number: len(out.Rows) + 1, Platform: poolEnum(item.Platform, "openai", "grok", "anthropic", "gemini"),
				Plan:   poolEnum(item.Plan, "promax", "Pro", "Plus", "Team", "Free", "go", "SuperGrok Heavy", "pro20", "SuperGrok"),
				Status: poolEnum(item.Status, "active", "error", "rate_limited", "paused", "disabled", "inactive"), Current: poolNumber(item.Current), Limit: poolNumber(item.Limit)}
			if t, err := time.Parse(time.RFC3339, item.LastUsed); err == nil {
				row.LastUsed = &t
			}
			for _, w := range item.Windows[:min(6, len(item.Windows))] {
				name := poolEnum(w.Name, "5h", "7d", "requests", "tokens")
				if name != "unknown" && w.Used != nil && *w.Used >= 0 && *w.Used < 1e9 {
					row.Windows = append(row.Windows, accountPoolWindow{Name: name, Used: w.Used})
				}
			}
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
				if strings.Contains(group.Name, "@") {
					entry.Original = ""
				} else {
					name := []rune(strings.TrimSpace(group.Name))
					entry.Original = string(name[:min(100, len(name))])
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
					entry.Count++
				}
			}
			out.Rows = append(out.Rows, row)
		}
		return nil
	}
	for page := 1; page <= pages; page++ {
		if err := read(page); err != nil {
			return accountPoolSnapshot{}, err
		}
	}
	// ponytail: upstream has no snapshot cursor; check a second sweep for coverage, then keep the old snapshot if still incomplete.
	for page := 1; page <= pages && len(seen) < total; page++ {
		if err := read(page); err != nil {
			return accountPoolSnapshot{}, err
		}
	}
	if len(seen) != total {
		return accountPoolSnapshot{}, errPoolChanged
	}
	now := time.Now().UTC()
	out.Updated = &now
	return out, nil
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
