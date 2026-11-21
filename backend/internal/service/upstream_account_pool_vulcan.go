package service

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
)

type vulcanPoolAccount struct {
	ID           string   `json:"id"`
	Type         string   `json:"account_type"`
	Plan         string   `json:"plan_type_label"`
	Status       string   `json:"status"`
	Current      *int     `json:"current_concurrency"`
	Limit        *int     `json:"concurrency_limit"`
	Five         *float64 `json:"used_percent_5h"`
	Seven        *float64 `json:"used_percent_7d"`
	UsageUpdated string   `json:"usage_updated_at"`
	Reset5h      string   `json:"reset_at_5h"`
	Reset7d      string   `json:"reset_at_7d"`
	RecoverAt    string   `json:"recover_at"`
	Priority     *int     `json:"priority"`
	Intelligence string   `json:"intelligence_status_label"`
}

func collectVulcanPool(raw []byte, previous []accountPoolSourceGroup) (accountPoolSnapshot, error) {
	var data struct {
		Updated time.Time `json:"updated_at"`
		Groups  []struct {
			ID       int                 `json:"id"`
			Name     string              `json:"name"`
			Count    *int                `json:"account_count"`
			Accounts []vulcanPoolAccount `json:"accounts"`
		} `json:"groups"`
	}
	if json.Unmarshal(raw, &data) != nil || data.Updated.IsZero() || data.Groups == nil || len(data.Groups) > 1000 {
		return accountPoolSnapshot{}, errPoolChanged
	}
	out := accountPoolSnapshot{Updated: &data.Updated, Groups: append([]accountPoolSourceGroup{}, previous...)}
	known := make(map[string]int, len(out.Groups))
	for i := range out.Groups {
		known[out.Groups[i].SourceID] = i
		out.Groups[i].Count = 0
	}
	seenGroups := map[int]bool{}
	seenRows := map[string]int{}
	for _, src := range data.Groups {
		if src.ID <= 0 || seenGroups[src.ID] || src.Count == nil || *src.Count != len(src.Accounts) {
			return accountPoolSnapshot{}, errPoolChanged
		}
		seenGroups[src.ID] = true
		id := strconv.Itoa(src.ID)
		i, ok := known[id]
		if !ok {
			i = len(out.Groups)
			if i >= 1000 {
				return accountPoolSnapshot{}, errPoolChanged
			}
			out.Groups = append(out.Groups, accountPoolSourceGroup{SourceID: id, ID: uuid.NewString()})
		}
		group := &out.Groups[i]
		group.Original, group.Count = accountPoolGroupName(src.Name), *src.Count
		members := make(map[string]bool, *src.Count)
		for _, acct := range src.Accounts {
			if acct.ID == "" || members[acct.ID] || acct.Type != "oauth" {
				return accountPoolSnapshot{}, errPoolChanged
			}
			members[acct.ID] = true
			row, ok := seenRows[acct.ID]
			stamp := poolTimestamp(acct.UsageUpdated)
			if !ok {
				row = len(out.Rows)
				seenRows[acct.ID] = row
				out.Rows = append(out.Rows, accountPoolRow{})
			}
			item := &out.Rows[row]
			if !ok || (stamp != nil && (item.Admin.UsageUpdated == nil || stamp.After(*item.Admin.UsageUpdated))) {
				status := acct.Status
				if status == "available" {
					status = "active"
				}
				*item = accountPoolRow{Number: row + 1, Platform: "openai", Groups: item.Groups,
					Plan:    poolEnum(acct.Plan, "Free", "Go", "Plus", "Team", "Business", "Pro", "Pro 100", "Pro 200", "Pro 500", "Pro 5x", "Pro 20x"),
					Status:  poolEnum(status, "active", "error", "rate_limited", "overload", "temp_unschedulable", "paused", "disabled", "expired"),
					Current: poolNumber(acct.Current), Limit: poolNumber(acct.Limit),
					Admin: &accountPoolAdminRow{Type: acct.Type, Plan: acct.Plan, Status: acct.Status, UsageUpdated: stamp,
						Reset5h: poolTimestamp(acct.Reset5h), Reset7d: poolTimestamp(acct.Reset7d), RecoverAt: poolTimestamp(acct.RecoverAt),
						Priority: acct.Priority, Intelligence: acct.Intelligence}}
				for _, w := range []accountPoolWindow{{Name: "5h", Used: acct.Five}, {Name: "7d", Used: acct.Seven}} {
					if w.Used != nil && *w.Used >= 0 && *w.Used < 1e9 {
						item.Windows = append(item.Windows, w)
					}
				}
			}
			item.Groups = append(item.Groups, group.ID)
		}
	}
	return out, nil
}
