package service

import (
	"context"
	"math"
	"slices"
	"strings"
	"time"
)

type schedulingAgentMonitorLink struct {
	site, key, group, model string
}

type schedulingAgentMonitor struct {
	Source     string `json:"source"`
	Scope      string `json:"scope"`
	GroupMatch bool   `json:"group_match"`
	Stale      bool   `json:"stale,omitempty"`
	UpstreamSiteMonitorRow
}

func schedulingAgentAccountMonitorLink(account *Account, model string) schedulingAgentMonitorLink {
	if account == nil {
		return schedulingAgentMonitorLink{}
	}
	for _, field := range []string{"upstream_import", "upstream_site_link"} {
		extra, _ := account.Extra[field].(map[string]any)
		site, _ := extra["site_id"].(string)
		if site != "" {
			key, _ := extra["key_id"].(string)
			group, _ := extra["group_id"].(string)
			upstream := account.GetMappedModel(model)
			if account.IsOpenAICompatible() {
				upstream = resolveOpenAIAccountUpstreamModelForRequest(account, model, false)
			}
			return schedulingAgentMonitorLink{site: site, key: key, group: group, model: upstream}
		}
	}
	return schedulingAgentMonitorLink{}
}

type schedulingAgentPool struct {
	Group        string         `json:"upstream_group"`
	UpdatedAt    time.Time      `json:"updated_at"`
	Stale        bool           `json:"stale"`
	StatusCounts map[string]int `json:"reported_account_status_counts"`
}

func schedulingPoolEvidence(snapshot accountPoolSnapshot, siteRevision int64, group string, now time.Time) *schedulingAgentPool {
	if group == "" || snapshot.Updated == nil || snapshot.Updated.IsZero() || (snapshot.SiteRevision != siteRevision && (snapshot.SiteRevision != 0 || siteRevision != 1)) {
		return nil
	}
	index := slices.IndexFunc(snapshot.Groups, func(pool accountPoolSourceGroup) bool { return pool.SourceID == group && pool.Count > 0 })
	if index < 0 {
		return nil
	}
	pool := snapshot.Groups[index].ID
	out := &schedulingAgentPool{Group: group, UpdatedAt: *snapshot.Updated,
		Stale: snapshot.Updated.Before(now.Add(-15*time.Minute)) || snapshot.Updated.After(now.Add(time.Minute)), StatusCounts: map[string]int{}}
	for _, row := range snapshot.Rows {
		if slices.Contains(row.Groups, pool) {
			out.StatusCounts[row.Status]++
		}
	}
	return out
}

type schedulingAgentQuota struct {
	Source     string     `json:"source"`
	Scope      string     `json:"scope"`
	Group      string     `json:"upstream_group,omitempty"`
	State      string     `json:"state"`
	ReceivedAt time.Time  `json:"received_at"`
	ValidUntil time.Time  `json:"valid_until"`
	ResetAt    *time.Time `json:"reset_at,omitempty"`
}

func schedulingQuotaReading(source, group string, amount UpstreamBalanceAmount, at, expires, now time.Time) schedulingAgentQuota {
	reading := schedulingAgentQuota{Source: source, Scope: amount.Scope, Group: group,
		State: "unknown", ReceivedAt: at, ValidUntil: expires, ResetAt: amount.ResetAt}
	if amount.ResetAt != nil {
		if amount.ResetAt.Before(expires) {
			reading.ValidUntil = *amount.ResetAt
		}
		if !amount.ResetAt.After(at) || !now.Before(*amount.ResetAt) {
			reading.State = "past_reset"
			return reading
		}
	}
	if at.After(now.Add(time.Minute)) || !expires.After(at) || !now.Before(expires) {
		reading.State = "stale"
		return reading
	}
	if amount.Unlimited {
		reading.State = "unlimited"
	} else if amount.Remaining != nil && !math.IsNaN(*amount.Remaining) && !math.IsInf(*amount.Remaining, 0) {
		reading.State = "available"
		if *amount.Remaining <= 0 {
			reading.State = "exhausted"
		}
	}
	return reading
}

func schedulingAccountQuota(account *Account, now time.Time) []schedulingAgentQuota {
	if account.Type != AccountTypeAPIKey || account.Extra[UpstreamBalanceProbeExtraKey] == nil {
		return nil
	}
	state := DecodeUpstreamBalanceState(account.Extra)
	snapshot := state.Snapshot
	if !state.Enabled || snapshot == nil || snapshot.Status != UpstreamBillingProbeStatusOK || snapshot.ReceivedAt == nil || snapshot.FreshUntil == nil {
		return nil
	}
	fingerprint := NewAPIAccountFingerprint(account)
	if snapshot.SourceIdentity != fingerprint && !(snapshot.Provider == "newapi" && strings.HasPrefix(snapshot.SourceIdentity, fingerprint+":")) {
		return nil
	}
	result := make([]schedulingAgentQuota, 0, len(snapshot.Amounts))
	for _, amount := range snapshot.Amounts {
		result = append(result, schedulingQuotaReading("account_balance", "", amount, *snapshot.ReceivedAt, *snapshot.FreshUntil, now))
	}
	return result
}

func (e schedulingAgentEvidence) quotaExhausted(now time.Time) bool {
	return slices.ContainsFunc(e.Quota, func(q schedulingAgentQuota) bool {
		return q.State == "exhausted" && now.Before(q.ValidUntil)
	})
}

// accountID is zero only when the model has a single call left and cannot request an inspection.
func (r *schedulingAgentRuntime) readSchedulingAgentMonitors(ctx context.Context, evidence []schedulingAgentEvidence, accountID int64) {
	reader := r.monitor.Load()
	if reader == nil {
		return
	}
	read, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	type siteEvidence struct {
		site    *UpstreamSite
		monitor *UpstreamSiteMonitor
	}
	snapshots := make(map[string]siteEvidence)
	for i := range evidence {
		item := &evidence[i]
		link := item.monitorLink
		if (accountID != 0 && item.AccountID != accountID) || link.site == "" || read.Err() != nil {
			continue
		}
		snapshot, seen := snapshots[link.site]
		if !seen {
			// ponytail: bound reads per investigation. Revisit parallel reads if a group routinely uses more sites.
			if len(snapshots) >= 6 {
				continue
			}
			siteCtx, stop := context.WithTimeout(read, 1500*time.Millisecond)
			site, err := reader.GetUpstreamSite(siteCtx, link.site)
			if err == nil {
				monitor, _ := reader.UpstreamSiteMonitoring(siteCtx, site)
				snapshot = siteEvidence{site, monitor}
			}
			stop()
			snapshots[link.site] = snapshot
		}
		if snapshot.site == nil {
			continue
		}
		// Read the saved inventory for the linked key, never the whole site's balance.
		if inventory := snapshot.site.Inventory; inventory != nil && inventory.Revision == snapshot.site.Revision && inventory.Errors["keys"] == "" && link.key != "" {
			if index := slices.IndexFunc(inventory.Keys, func(key UpstreamSiteKey) bool { return key.ID == link.key }); index >= 0 {
				key := inventory.Keys[index]
				if link.group == "" || link.group == key.GroupID {
					link.group = key.GroupID
					if key.Quota != nil && inventory.ReceivedAt != nil {
						at := *inventory.ReceivedAt
						item.Quota = append(item.Quota, schedulingQuotaReading("linked_key", link.group, *key.Quota, at, at.Add(15*time.Minute), time.Now()))
					}
				}
			}
		}
		if accountID != 0 && link.group != "" {
			if pool, _, err := reader.readAccountPool(read, snapshot.site.ID, true); err == nil {
				item.ReportedPool = schedulingPoolEvidence(pool, snapshot.site.Revision, link.group, time.Now())
			}
		}
		if snapshot.monitor != nil {
			item.Monitors = schedulingMonitorRows(link, snapshot.site.Name, snapshot.monitor)
		}
	}
}

func schedulingMonitorRows(link schedulingAgentMonitorLink, source string, monitor *UpstreamSiteMonitor) []schedulingAgentMonitor {
	var rows []schedulingAgentMonitor
	for _, row := range monitor.Rows {
		if link.group != "" && row.GroupID != "" && row.GroupID != link.group {
			continue
		}
		scope := "other_model"
		if row.Model == "" {
			scope = "group"
		} else if strings.EqualFold(row.Model, link.model) {
			scope = "same_model"
		}
		row.Points = nil
		rows = append(rows, schedulingAgentMonitor{Source: source, Scope: scope,
			GroupMatch: link.group != "" && row.GroupID == link.group, Stale: monitor.Stale || monitor.Status != "ok", UpstreamSiteMonitorRow: row})
	}
	slices.SortStableFunc(rows, func(a, b schedulingAgentMonitor) int {
		rank := func(row schedulingAgentMonitor) int {
			score := 0
			if row.GroupMatch {
				score += 4
			}
			switch row.Scope {
			case "same_model":
				score += 2
			case "group":
				score++
			}
			return score
		}
		if difference := rank(b) - rank(a); difference != 0 {
			return difference
		}
		if a.CheckedAt == nil {
			if b.CheckedAt == nil {
				return 0
			}
			return 1
		}
		if b.CheckedAt == nil {
			return -1
		}
		return b.CheckedAt.Compare(*a.CheckedAt)
	})
	return rows[:min(4, len(rows))]
}

func hasHealthySchedulingMonitor(rows []schedulingAgentMonitor, now time.Time) bool {
	healthy := false
	for _, row := range rows {
		if row.Stale || row.CheckedAt == nil || row.CheckedAt.Before(now.Add(-15*time.Minute)) || row.CheckedAt.After(now.Add(time.Minute)) ||
			(!row.GroupMatch && row.GroupID != "") {
			continue
		}
		if row.Scope != "other_model" && (row.State == "critical" || row.State == "warning") {
			return false
		}
		healthy = healthy || row.State == "healthy"
	}
	return healthy
}
