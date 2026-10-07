package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const bpsTicketSettingsKey = "custom_bps_ticket_v1"
const bpsTicketAccountKey = "openai_bps_ticket"
const bpsTicketStateKey = "openai_bps_ticket_state"

// Settings are global; membership of a billing/scheduling group is irrelevant.
type BPSTicketSettings struct {
	HarvestEnabled         bool    `json:"harvest_enabled"`
	TargetLength           int     `json:"target_length"`
	TargetGateway          string  `json:"target_gateway"`
	TTLSeconds             int     `json:"ttl_seconds"`
	RefreshBeforeSeconds   int     `json:"refresh_before_seconds"`
	HarvestIntervalSeconds int     `json:"harvest_interval_seconds"`
	AttemptTimeoutSeconds  int     `json:"attempt_timeout_seconds"`
	MaxAttempts            int     `json:"max_attempts"`
	FailClosed             bool    `json:"fail_closed"`
	HarvestProxySource     string  `json:"harvest_proxy_source"`
	ProxyIDs               []int64 `json:"proxy_ids"`
	AutoProbeEnabled       bool    `json:"auto_probe_enabled"`
	ProbeIntervalSeconds   int     `json:"probe_interval_seconds"`
	DegradedThreshold      int     `json:"degraded_threshold"`
	HealthyThreshold       int     `json:"healthy_threshold"`
}

func DefaultBPSTicketSettings() BPSTicketSettings {
	return BPSTicketSettings{TargetLength: 780, TargetGateway: "any", TTLSeconds: 240,
		RefreshBeforeSeconds: 60, HarvestIntervalSeconds: 180, AttemptTimeoutSeconds: 45,
		MaxAttempts: 3, HarvestProxySource: "account", ProbeIntervalSeconds: 900,
		DegradedThreshold: 2, HealthyThreshold: 2, ProxyIDs: []int64{}}
}

func (v BPSTicketSettings) Validate() error {
	if v.TargetLength != 780 && v.TargetLength != 292 {
		return errors.New("target_length must be 780 or 292 (team: 332)")
	}
	if v.TTLSeconds < 60 || v.TTLSeconds > 240 || v.RefreshBeforeSeconds < 10 || v.RefreshBeforeSeconds >= v.TTLSeconds {
		return errors.New("ticket TTL must be 60–240 seconds; refresh must be earlier than expiry")
	}
	if v.HarvestIntervalSeconds < 30 || v.HarvestIntervalSeconds > 3600 || v.AttemptTimeoutSeconds < 5 || v.AttemptTimeoutSeconds > 90 || v.MaxAttempts < 1 || v.MaxAttempts > 6 {
		return errors.New("invalid harvest interval, timeout or attempt budget")
	}
	if v.ProbeIntervalSeconds < 120 || v.ProbeIntervalSeconds > 86400 || v.DegradedThreshold < 1 || v.DegradedThreshold > 10 || v.HealthyThreshold < 1 || v.HealthyThreshold > 10 {
		return errors.New("invalid probe interval or consecutive-result thresholds")
	}
	if !slices.Contains([]string{"account", "static", "mihomo"}, v.HarvestProxySource) {
		return errors.New("invalid harvest proxy source")
	}
	if len(v.ProxyIDs) > 64 || (v.HarvestProxySource == "static" && len(v.ProxyIDs) == 0) {
		return errors.New("select 1–64 static proxies")
	}
	for _, id := range v.ProxyIDs {
		if id <= 0 {
			return errors.New("invalid proxy ID")
		}
	}
	g := normalizeCodex780Gateway(v.TargetGateway)
	if g != "any" && !codex780TargetRE.MatchString(g) {
		return errors.New("target gateway must be any or unified-N")
	}
	return nil
}

type BPSTicketAccountConfig struct {
	BPS         bool     `json:"bps"`
	Models      []string `json:"models"`
	ProxySource string   `json:"proxy_source"`
	Tickets     bool     `json:"tickets"`
	AutoProbe   bool     `json:"auto_probe"`
	AutoSwitch  bool     `json:"auto_switch"`
}

func (a *Account) BPSTicketConfig() BPSTicketAccountConfig {
	v := BPSTicketAccountConfig{ProxySource: "account", Models: []string{}}
	if a == nil {
		return v
	}
	raw, _ := json.Marshal(a.Extra[bpsTicketAccountKey])
	_ = json.Unmarshal(raw, &v)
	return v
}

func bpsTicketEligible(a *Account) bool {
	return a != nil && a.IsOpenAIOAuthLike() && !a.IsSyntheticUITest() && !a.IsShadow() && !a.IsOpenAIAgentIdentity() && !a.IsOpenAIPersonalAccessToken()
}

func bpsEligible(a *Account) bool {
	return bpsTicketEligible(a) && !strings.EqualFold(strings.TrimSpace(a.GetCredential("plan_type")), "free")
}

func (v BPSTicketAccountConfig) Validate(a *Account) error {
	if !bpsTicketEligible(a) {
		return errors.New("requires a non-shadow OpenAI OAuth subscription account")
	}
	if (v.BPS || v.AutoSwitch) && !bpsEligible(a) {
		return errors.New("BPS is unavailable for free accounts")
	}
	if v.AutoSwitch && !v.AutoProbe {
		return errors.New("automatic BPS switching requires scheduled state probes")
	}
	if len(v.Models) > 16 || ((v.BPS || v.Tickets || v.AutoProbe) && len(v.Models) == 0) {
		return errors.New("select 1–16 models")
	}
	seen := map[string]bool{}
	for _, model := range v.Models {
		if strings.TrimSpace(model) != model || model == "" || len(model) > 128 || strings.ContainsAny(model, "\r\n\t") || seen[model] {
			return errors.New("invalid or duplicate model")
		}
		seen[model] = true
	}
	if !slices.Contains([]string{"account", "static", "mihomo"}, v.ProxySource) {
		return errors.New("invalid BPS proxy source")
	}
	return nil
}

func (a *Account) bpsTicketModelSelected(model string) bool {
	_, ok := a.selectedBPSTicketModel(model)
	return ok
}

func (a *Account) selectedBPSTicketModel(model string) (string, bool) {
	// Outbound model names have already been mapped; do not map them twice.
	for _, selected := range a.BPSTicketConfig().Models {
		mapped := normalizeOpenAIModelForUpstream(a, a.GetMappedModel(selected))
		if model == mapped {
			return mapped, true
		}
	}
	for _, selected := range a.BPSTicketConfig().Models {
		if model == selected {
			return normalizeOpenAIModelForUpstream(a, a.GetMappedModel(selected)), true
		}
	}
	return "", false
}

func bpsTicketAccountRevision(a *Account) string {
	// Credential, model mapping and manual edits invalidate stale automatic actions.
	raw, _ := json.Marshal([]any{a.BPSTicketConfig(), a.Credentials, a.ProxyID})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

type BPSTicketModelState struct {
	Probe         *OpenAICodexStateProbeResult `json:"probe,omitempty"`
	DegradedCount int                          `json:"degraded_count"`
	HealthyCount  int                          `json:"healthy_count"`
	AutoBPS       bool                         `json:"auto_bps"`
}

type BPSTicketAccountState struct {
	Revision string                         `json:"revision"`
	Models   map[string]BPSTicketModelState `json:"models"`
}

func (a *Account) BPSTicketState() BPSTicketAccountState {
	v := BPSTicketAccountState{}
	raw, _ := json.Marshal(a.Extra[bpsTicketStateKey])
	_ = json.Unmarshal(raw, &v)
	if v.Revision != bpsTicketAccountRevision(a) || v.Models == nil {
		v = BPSTicketAccountState{Revision: bpsTicketAccountRevision(a), Models: map[string]BPSTicketModelState{}}
	}
	return v
}

func (a *Account) IsExcelBPSEnabledForModel(model string) bool {
	if !bpsEligible(a) {
		return false
	}
	mapped, selected := a.selectedBPSTicketModel(model)
	if !selected {
		return false
	}
	cfg := a.BPSTicketConfig()
	if cfg.BPS {
		return true
	}
	return cfg.AutoProbe && cfg.AutoSwitch && a.BPSTicketState().Models[mapped].AutoBPS
}

func (a *Account) bpsTicketNeedsHTTP() bool {
	if !bpsTicketEligible(a) {
		return false
	}
	cfg := a.BPSTicketConfig()
	return cfg.BPS || cfg.Tickets || cfg.AutoSwitch
}

func (s *OpenAIGatewayService) GetBPSTicketSettings(ctx context.Context) (BPSTicketSettings, error) {
	v := DefaultBPSTicketSettings()
	if s.settingService == nil || s.settingService.settingRepo == nil {
		return v, nil
	}
	raw, err := s.settingService.settingRepo.GetValue(ctx, bpsTicketSettingsKey)
	if errors.Is(err, ErrSettingNotFound) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return v, err
		}
	}
	return v, v.Validate()
}

func (s *OpenAIGatewayService) SaveBPSTicketSettings(ctx context.Context, v BPSTicketSettings) error {
	v.TargetGateway = normalizeCodex780Gateway(v.TargetGateway)
	if err := v.Validate(); err != nil {
		return err
	}
	if s.settingService == nil {
		return errors.New("settings service unavailable")
	}
	if len(v.ProxyIDs) > 0 {
		proxies, err := s.settingService.proxyRepo.ListByIDs(ctx, v.ProxyIDs)
		if err != nil {
			return err
		}
		if len(proxies) != len(v.ProxyIDs) {
			return errors.New("one or more proxies do not exist")
		}
		for _, p := range proxies {
			if !p.IsActive() || p.IsExpired(time.Now()) {
				return fmt.Errorf("proxy %d is disabled or expired", p.ID)
			}
		}
	}
	raw, _ := json.Marshal(v)
	if err := s.settingService.settingRepo.Set(ctx, bpsTicketSettingsKey, string(raw)); err != nil {
		return err
	}
	s.bpsTickets.mu.Lock()
	s.bpsTickets.settings = v
	s.bpsTickets.settingsAt = time.Now()
	s.bpsTickets.tickets = nil
	s.bpsTickets.mu.Unlock()
	return nil
}

func (s *OpenAIGatewayService) SaveBPSTicketAccount(ctx context.Context, id int64, v BPSTicketAccountConfig) error {
	a, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err = v.Validate(a); err != nil {
		return err
	}
	return s.accountRepo.UpdateExtra(ctx, id, map[string]any{bpsTicketAccountKey: v, bpsTicketStateKey: nil})
}
