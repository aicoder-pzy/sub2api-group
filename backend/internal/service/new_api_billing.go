package service

import (
	"context"
	"time"
)

// User-scoped groups expose the ratio after upstream user-group overrides.
// Read the live owned token instead of trusting its saved binding group.
func (s *UpstreamBillingProbeService) probeNewAPIBilling(ctx context.Context, account *Account, binding *NewAPIAccountBinding, now time.Time) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, token, secret, err := s.fetchBoundNewAPI(ctx, account, binding)
	if err != nil {
		return nil, err
	}
	// A token that can route across groups has no single provable billing rate.
	if token.Group == "" || token.Group == "auto" || token.CrossGroupRetry || len(token.RouteGroups) > 0 {
		return nil, newAPIError("dynamic_token_group")
	}
	var groups struct {
		Success bool `json:"success"`
		Data    map[string]struct {
			Ratio *float64 `json:"ratio"`
		} `json:"data"`
	}
	if err := s.newAPIRequest(ctx, account, binding.Profile.SiteURL, binding.Profile.UserID, secret, "GET", "/api/user/self/groups", &groups); err != nil {
		return nil, err
	}
	ratio := groups.Data[token.Group].Ratio
	if !groups.Success || ratio == nil || !newAPIFinite(*ratio) || *ratio < 0 {
		return nil, newAPIError("invalid_group_ratio")
	}
	return map[string]any{
		"object": "newapi.key_billing", "schema_version": 1, "billing_scope": "token",
		"group_name":            sanitizeUpstreamBalanceText(token.Group, 128),
		"group_rate_multiplier": *ratio, "resolved_rate_multiplier": *ratio,
		"effective_rate_multiplier": *ratio, "peak_rate_enabled": false,
		"observed_at": now.Format(time.RFC3339),
	}, nil
}
