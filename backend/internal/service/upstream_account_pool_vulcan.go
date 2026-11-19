package service

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
)

type vulcanPoolAccount struct {
	ID      string   `json:"id"`
	Type    string   `json:"account_type"`
	Plan    string   `json:"plan_type_label"`
	Status  string   `json:"status"`
	Current *int     `json:"current_concurrency"`
	Limit   *int     `json:"concurrency_limit"`
	Five    *float64 `json:"used_percent_5h"`
	Seven   *float64 `json:"used_percent_7d"`
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
			if !ok {
				row = len(out.Rows)
				seenRows[acct.ID] = row
				status := acct.Status
				if status == "available" {
					status = "active"
				}
				item := accountPoolRow{Number: row + 1, Platform: "openai",
					Plan:    poolEnum(acct.Plan, "Free", "Go", "Plus", "Team", "Business", "Pro", "Pro 100", "Pro 200", "Pro 500", "Pro 5x", "Pro 20x"),
					Status:  poolEnum(status, "active", "error", "rate_limited", "overload", "temp_unschedulable", "paused", "disabled", "expired"),
					Current: poolNumber(acct.Current), Limit: poolNumber(acct.Limit)}
				for _, w := range []accountPoolWindow{{Name: "5h", Used: acct.Five}, {Name: "7d", Used: acct.Seven}} {
					if w.Used != nil && *w.Used >= 0 && *w.Used < 1e9 {
						item.Windows = append(item.Windows, w)
					}
				}
				out.Rows = append(out.Rows, item)
			}
			out.Rows[row].Groups = append(out.Rows[row].Groups, group.ID)
		}
	}
	return out, nil
}
