package service

import (
	"context"
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

func (r *schedulingAgentRuntime) readSchedulingAgentMonitors(ctx context.Context, evidence []schedulingAgentEvidence) {
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
		if link.site == "" || read.Err() != nil {
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
		if snapshot.monitor == nil {
			continue
		}
		// Saved inventory also resolves manually linked keys. Do not fetch keys here.
		if inventory := snapshot.site.Inventory; inventory != nil && inventory.Revision == snapshot.site.Revision && link.key != "" {
			if index := slices.IndexFunc(inventory.Keys, func(key UpstreamSiteKey) bool { return key.ID == link.key }); index >= 0 {
				link.group = inventory.Keys[index].GroupID
			}
		}
		item.Monitors = schedulingMonitorRows(link, snapshot.site.Name, snapshot.monitor)
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
