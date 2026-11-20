package service

import (
	"context"
	"net/url"
	"sort"
	"time"
)

type SchedulingAgentAccount struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Site          string     `json:"site"`
	UpstreamGroup string     `json:"upstream_group"`
	Rate          *float64   `json:"rate,omitempty"`
	VerifiedAt    *time.Time `json:"verified_at,omitempty"`
	Available     bool       `json:"available"`
	Models        []string   `json:"models"`
}

type SchedulingAgentStatus struct {
	Policy   SchedulingPolicy         `json:"policy"`
	Run      SchedulingAgentRun       `json:"run"`
	Accounts []SchedulingAgentAccount `json:"accounts"`
	History  []SchedulingAgentRun     `json:"history,omitempty"`
}

func (s *SettingService) GetSchedulingAgentStatus(ctx context.Context, id int64) (SchedulingAgentStatus, error) {
	var result SchedulingAgentStatus
	r := s.schedulingAgent
	group, err := r.groups.GetByID(ctx, id)
	if err != nil {
		return result, err
	}
	result.Policy, err = s.GetSchedulingPolicy(ctx, id)
	if err != nil {
		return result, err
	}
	result.Run, err = s.GetSchedulingAgentRun(ctx, id)
	if err != nil {
		return result, err
	}
	result.History = result.Run.History
	result.Run.History = nil
	if result.Run.State == "applied" || len(result.Run.Routes) > 0 {
		c, err := s.GetSchedulingAgentConnection(ctx)
		if err != nil {
			return result, err
		}
		valid := result.Run.Version == result.Policy.Version && result.Run.ConnectionVersion == c.Version && c.KeyConfigured &&
			result.Policy.AgentMode == "auto" && group.AccountSchedulingMode == AccountSchedulingModeSmartChannel
		if !valid {
			result.Run.Routes = nil
		}
		if result.Run.State == "applied" || result.Run.State == "route_trial" || result.Run.State == "route_selected" {
			if !valid {
				result.Run.State = "revoked"
			} else if result.Run.State != "route_selected" && time.Now().After(result.Run.Expires) {
				result.Run.State = "expired"
			} else if result.Run.State == "applied" {
				r.readActionCount(id, &result.Run)
			}
		}
		for model, route := range result.Run.Routes {
			if !time.Now().Before(route.TrialUntil) {
				route.TrialID = 0
			}
			if route.TrialID == 0 && route.AccountID == 0 {
				delete(result.Run.Routes, model)
			} else {
				result.Run.Routes[model] = route
			}
		}
	}
	ids, err := r.groups.GetAccountIDsByGroupIDs(ctx, []int64{id})
	if err != nil {
		return result, err
	}
	accounts, err := r.accounts.GetByIDs(ctx, ids)
	if err != nil {
		return result, err
	}
	result.Accounts = make([]SchedulingAgentAccount, 0, len(accounts))
	for _, account := range accounts {
		entry := SchedulingAgentAccount{ID: account.ID, Name: account.Name, Available: account.IsSchedulable(), Models: make([]string, 0)}
		for model := range account.GetModelMapping() {
			entry.Models = append(entry.Models, model)
		}
		sort.Strings(entry.Models)
		if parsed, err := url.Parse(account.GetTypeSafeBaseURL()); err == nil {
			entry.Site = parsed.Hostname()
		}
		snapshot := decodeUpstreamBillingProbeSnapshot(account.Extra)
		if imported, ok := account.Extra["upstream_import"].(map[string]any); ok {
			if id, ok := imported["group_id"].(string); ok && id != "" {
				entry.UpstreamGroup = "上游组 #" + id
			}
		}
		if snapshot != nil && snapshot.Status == "ok" && snapshot.FreshUntil != nil && time.Now().Before(*snapshot.FreshUntil) {
			if rate, ok := snapshot.Data["resolved_rate_multiplier"].(float64); ok {
				entry.Rate, entry.VerifiedAt = &rate, snapshot.ReceivedAt
			}
		}
		if snapshot != nil && snapshot.Status == "ok" && snapshot.Source == "newapi" && snapshot.FreshUntil != nil && time.Now().Before(*snapshot.FreshUntil) {
			entry.UpstreamGroup, _ = snapshot.Data["group_name"].(string)
			if entry.UpstreamGroup != "" {
				entry.VerifiedAt = snapshot.ReceivedAt
				if rate, ok := snapshot.Data["effective_rate_multiplier"].(float64); ok {
					entry.Rate = &rate
				}
			}
		}
		result.Accounts = append(result.Accounts, entry)
	}
	sort.Slice(result.Accounts, func(i, j int) bool { return result.Accounts[i].ID < result.Accounts[j].ID })
	return result, nil
}
