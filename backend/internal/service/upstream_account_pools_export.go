package service

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// Read membership, current Key mappings and snapshots in one database view.
const groupAccountPoolsQuery = `WITH linked AS (
  SELECT COALESCE(a.extra->'upstream_import'->>'site_id',a.extra->'upstream_site_link'->>'site_id') AS site_id,
         CASE WHEN a.extra->'upstream_import'->>'site_id' IS NOT NULL THEN a.extra->'upstream_import'->>'key_id'
              ELSE a.extra->'upstream_site_link'->>'key_id' END AS key_id
  FROM account_groups ag JOIN accounts a ON a.id=ag.account_id
  WHERE ag.group_id=$1 AND a.deleted_at IS NULL AND a.type='apikey' AND a.status='active' AND a.schedulable
    AND (NOT a.auto_pause_on_expired OR a.expires_at IS NULL OR a.expires_at > NOW())
), by_site AS (
  SELECT site_id,jsonb_agg(DISTINCT key_id) AS keys FROM linked WHERE site_id IS NOT NULL GROUP BY site_id
), sources AS (
  SELECT b.site_id,b.keys,s.base_url,s.revision,s.inventory,p.snapshot
  FROM by_site b LEFT JOIN upstream_sites s ON s.id::text=b.site_id
  LEFT JOIN upstream_site_account_pools p ON p.site_id=s.id
  WHERE s.id IS NULL OR (s.purpose='public' AND s.kind='sub2api' AND s.base_url IN ($2,$3))
)
SELECT g.name,COALESCE(s.base_url,''),COALESCE(s.revision,0),COALESCE(s.keys,'[]'),COALESCE(s.inventory,'{}'),COALESCE(s.snapshot,'{}')
FROM groups g LEFT JOIN sources s ON true
WHERE g.id=$1 AND g.deleted_at IS NULL AND g.status='active'
ORDER BY s.site_id`

type groupAccountPoolRow struct {
	accountPoolRow
	AccountType string `json:"account_type"`
	PlanCode    string `json:"plan_code"`
}

type groupAccountPoolPlan struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type GroupAccountPools struct {
	GroupID    int64                  `json:"group_id"`
	Name       string                 `json:"name"`
	Complete   bool                   `json:"complete"`
	Updated    *time.Time             `json:"updated"`
	VerifiedAt *time.Time             `json:"verified_at"`
	Total      int                    `json:"total"`
	Plans      []groupAccountPoolPlan `json:"plans"`
	Rows       []groupAccountPoolRow  `json:"rows"`
	Page       int                    `json:"page"`
	PageSize   int                    `json:"page_size"`
	Pages      int                    `json:"pages"`
}

func (s *UpstreamBillingProbeService) ExportGroupAccountPools(ctx context.Context, groupID int64, page, pageSize int) (GroupAccountPools, error) {
	if s == nil || s.db == nil {
		return GroupAccountPools{}, ErrUpstreamBillingProbeUnavailable
	}
	rows, err := s.db.QueryContext(ctx, groupAccountPoolsQuery, groupID, accountPoolBaseURL, vulcanPoolBaseURL)
	if err != nil {
		return GroupAccountPools{}, err
	}
	defer func() { _ = rows.Close() }()
	out := GroupAccountPools{GroupID: groupID, Complete: true, Rows: []groupAccountPoolRow{}, Plans: []groupAccountPoolPlan{}}
	found := false
	for rows.Next() {
		found = true
		var baseURL string
		var revision int64
		var keysJSON, inventoryJSON, snapshotJSON []byte
		if err := rows.Scan(&out.Name, &baseURL, &revision, &keysJSON, &inventoryJSON, &snapshotJSON); err != nil {
			return GroupAccountPools{}, err
		}
		var keys []string
		var inventory UpstreamSiteInventory
		var snapshot accountPoolSnapshot
		if json.Unmarshal(keysJSON, &keys) != nil || json.Unmarshal(inventoryJSON, &inventory) != nil || json.Unmarshal(snapshotJSON, &snapshot) != nil {
			out.Complete = false
			continue
		}
		out.addSite(baseURL, revision, keys, inventory, snapshot)
	}
	if err := rows.Err(); err != nil {
		return GroupAccountPools{}, err
	}
	if !found {
		return GroupAccountPools{}, ErrGroupNotFound
	}
	out.paginate(page, pageSize)
	return out, nil
}

func (out *GroupAccountPools) addSite(baseURL string, revision int64, keyIDs []string, inventory UpstreamSiteInventory, snapshot accountPoolSnapshot) {
	if len(keyIDs) == 0 {
		return
	}
	// Legacy snapshots are only trusted for unedited site connections.
	_, keysFailed := inventory.Errors["keys"]
	if revision == 0 || keysFailed || inventory.Revision != revision || inventory.ReceivedAt == nil || inventory.ReceivedAt.IsZero() ||
		snapshot.Updated == nil || snapshot.Updated.IsZero() || (snapshot.SiteRevision != revision && !(snapshot.SiteRevision == 0 && revision == 1)) {
		out.Complete = false
		return
	}
	keys := make(map[string]UpstreamSiteKey, len(inventory.Keys))
	for _, key := range inventory.Keys {
		keys[key.ID] = key
	}
	groups := make(map[string]string, len(snapshot.Groups))
	for _, group := range snapshot.Groups {
		// The collectors also retain aliases of historically seen groups with zero members.
		if group.Count > 0 {
			groups[group.SourceID] = group.ID
		}
	}
	selected := map[string]bool{}
	for _, id := range keyIDs {
		key := keys[id]
		poolID := groups[key.GroupID]
		// Vulcan's 6.1 exclusive group uses its published Pro mixed pool.
		if poolID == "" && baseURL == vulcanPoolBaseURL && key.GroupID == "141" {
			poolID = groups["97"]
		}
		if id == "" || !key.Available || key.GroupID == "" || poolID == "" {
			out.Complete = false
			continue
		}
		selected[poolID] = true
	}
	if len(selected) == 0 {
		return
	}
	if out.Updated == nil || snapshot.Updated.Before(*out.Updated) {
		out.Updated = snapshot.Updated
	}
	if out.VerifiedAt == nil || inventory.ReceivedAt.Before(*out.VerifiedAt) {
		out.VerifiedAt = inventory.ReceivedAt
	}
	// Rows are already unique within a site. Select their union, not one copy per Key or group.
	for _, row := range snapshot.Rows {
		if !slices.ContainsFunc(row.Groups, func(id string) bool { return selected[id] }) {
			continue
		}
		row.Groups = nil
		row.Number = len(out.Rows) + 1
		code := strings.Join(strings.Fields(strings.ToLower(row.Plan)), "_")
		switch code {
		case "promax", "pro_max", "pro_500":
			code, row.Plan = "pro_500", "Pro 500"
		case "go":
			row.Plan = "Go"
		}
		out.Rows = append(out.Rows, groupAccountPoolRow{accountPoolRow: row, AccountType: "oauth", PlanCode: code})
	}
}

func (out *GroupAccountPools) paginate(page, pageSize int) {
	counts := map[string]groupAccountPoolPlan{}
	for _, row := range out.Rows {
		plan := counts[row.PlanCode]
		plan.Code, plan.Name = row.PlanCode, row.Plan
		plan.Count++
		counts[row.PlanCode] = plan
	}
	for _, plan := range counts {
		out.Plans = append(out.Plans, plan)
	}
	slices.SortFunc(out.Plans, func(a, b groupAccountPoolPlan) int { return cmp.Compare(a.Code, b.Code) })
	out.Total = len(out.Rows)
	out.Page, out.PageSize = page, pageSize
	out.Pages = max(1, (out.Total+pageSize-1)/pageSize)
	start := out.Total
	if page <= out.Pages {
		start = (page - 1) * pageSize
	}
	out.Rows = out.Rows[start:min(start+pageSize, out.Total)]
}
