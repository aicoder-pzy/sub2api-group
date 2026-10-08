package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"golang.org/x/sync/errgroup"
)

const UpstreamBalanceProbeExtraKey = "upstream_balance_probe"
const upstreamBalanceSettingsKey = "upstream_balance_probe_settings"

type UpstreamBalanceSettings struct {
	Enabled             bool    `json:"enabled"`
	IntervalMinutes     int     `json:"interval_minutes"`
	LowBalanceThreshold float64 `json:"low_balance_threshold"`
}

type UpstreamBalanceConfig struct {
	Enabled           bool    `json:"enabled"`
	Provider          string  `json:"provider"`
	Currency          string  `json:"currency"`
	QuotaPerUnit      float64 `json:"quota_per_unit"`
	PauseOnExhaustion bool    `json:"pause_on_exhaustion"`
}

type UpstreamBalanceAmount struct {
	Scope     string     `json:"scope"`
	Currency  string     `json:"currency"`
	Remaining *float64   `json:"remaining"`
	Limit     *float64   `json:"limit,omitempty"`
	Used      *float64   `json:"used,omitempty"`
	Unlimited bool       `json:"unlimited"`
	ResetAt   *time.Time `json:"reset_at,omitempty"`
}

type UpstreamBalanceSnapshot struct {
	Status        string                  `json:"status"`
	Provider      string                  `json:"provider,omitempty"`
	Amounts       []UpstreamBalanceAmount `json:"amounts,omitempty"`
	ReceivedAt    *time.Time              `json:"received_at,omitempty"`
	FreshUntil    *time.Time              `json:"fresh_until,omitempty"`
	LastAttemptAt time.Time               `json:"last_attempt_at"`
	NextProbeAt   time.Time               `json:"next_probe_at"`
	FailureCount  int                     `json:"failure_count,omitempty"`
	HTTPStatus    int                     `json:"http_status,omitempty"`
	LastError     string                  `json:"last_error,omitempty"`
}

type UpstreamBalanceState struct {
	UpstreamBalanceConfig
	Snapshot *UpstreamBalanceSnapshot `json:"snapshot,omitempty"`
}

type UpstreamBalanceProbeResult struct {
	AccountID int64                 `json:"account_id"`
	State     *UpstreamBalanceState `json:"state,omitempty"`
	Error     string                `json:"error,omitempty"`
}

type upstreamBalanceStateWriter interface {
	UpdateUpstreamBalanceState(context.Context, *Account, *UpstreamBalanceState) error
}

type upstreamBalanceDueAccountLister interface {
	ListDueUpstreamBalanceAccounts(context.Context, time.Time, int) ([]Account, error)
}

var upstreamBalanceCurrencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

func DefaultUpstreamBalanceConfig() UpstreamBalanceConfig {
	return UpstreamBalanceConfig{Provider: "auto", Currency: "USD", QuotaPerUnit: 500000}
}

func (c UpstreamBalanceConfig) Validate() error {
	if c.Provider != "auto" && c.Provider != "sub2api" && c.Provider != "newapi" {
		return infraerrors.BadRequest("INVALID_BALANCE_PROVIDER", "provider must be auto, sub2api or newapi")
	}
	if !upstreamBalanceCurrencyPattern.MatchString(c.Currency) || math.IsNaN(c.QuotaPerUnit) || math.IsInf(c.QuotaPerUnit, 0) || c.QuotaPerUnit <= 0 || c.QuotaPerUnit > 1e12 {
		return infraerrors.BadRequest("INVALID_BALANCE_CONVERSION", "currency must be a three-letter code and quota_per_unit must be between 0 and 1e12")
	}
	if c.PauseOnExhaustion && !c.Enabled {
		return infraerrors.BadRequest("BALANCE_PROBE_REQUIRED", "automatic exhaustion pause requires balance probing")
	}
	return nil
}

func DecodeUpstreamBalanceState(extra map[string]any) UpstreamBalanceState {
	state := UpstreamBalanceState{UpstreamBalanceConfig: DefaultUpstreamBalanceConfig()}
	if raw, ok := extra[UpstreamBalanceProbeExtraKey]; ok && raw != nil {
		encoded, err := json.Marshal(raw)
		if err == nil {
			if err := json.Unmarshal(encoded, &state); err != nil {
				return UpstreamBalanceState{UpstreamBalanceConfig: DefaultUpstreamBalanceConfig()}
			}
		}
	}
	return state
}

func (a *Account) IsUpstreamBalancePaused(now time.Time) bool {
	if a == nil || a.Type != AccountTypeAPIKey {
		return false
	}
	if a.Extra[UpstreamBalanceProbeExtraKey] == nil {
		return false
	}
	state := DecodeUpstreamBalanceState(a.Extra)
	snapshot := state.Snapshot
	if !state.Enabled || !state.PauseOnExhaustion || state.Validate() != nil || snapshot == nil || snapshot.Status != UpstreamBillingProbeStatusOK || snapshot.ReceivedAt == nil || snapshot.FreshUntil == nil || snapshot.ReceivedAt.After(now.Add(5*time.Minute)) || !snapshot.FreshUntil.After(*snapshot.ReceivedAt) || !now.Before(*snapshot.FreshUntil) {
		return false
	}
	for _, amount := range snapshot.Amounts {
		if !amount.Unlimited && amount.Remaining != nil && !math.IsNaN(*amount.Remaining) && !math.IsInf(*amount.Remaining, 0) && *amount.Remaining <= 0 && (amount.ResetAt == nil || now.Before(*amount.ResetAt)) {
			return true
		}
	}
	return false
}

func (s *UpstreamBillingProbeService) GetBalanceSettings(ctx context.Context) (*UpstreamBalanceSettings, error) {
	settings := &UpstreamBalanceSettings{Enabled: true, IntervalMinutes: 30, LowBalanceThreshold: 5}
	if s == nil || s.settingService == nil || s.settingService.settingRepo == nil {
		return settings, nil
	}
	value, err := s.settingService.settingRepo.GetValue(ctx, upstreamBalanceSettingsKey)
	if errors.Is(err, ErrSettingNotFound) {
		return settings, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(value), settings); err != nil {
		return nil, err
	}
	return settings, settings.Validate()
}

func (settings UpstreamBalanceSettings) Validate() error {
	if settings.IntervalMinutes < 5 || settings.IntervalMinutes > 1440 || math.IsNaN(settings.LowBalanceThreshold) || math.IsInf(settings.LowBalanceThreshold, 0) || settings.LowBalanceThreshold < 0 || settings.LowBalanceThreshold > 1e9 {
		return infraerrors.BadRequest("INVALID_BALANCE_SETTINGS", "interval_minutes must be 5-1440 and low_balance_threshold must be 0-1e9")
	}
	return nil
}

func (s *UpstreamBillingProbeService) SetBalanceSettings(ctx context.Context, settings UpstreamBalanceSettings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	if s == nil || s.settingService == nil || s.settingService.settingRepo == nil {
		return ErrUpstreamBillingProbeUnavailable
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.settingService.settingRepo.Set(ctx, upstreamBalanceSettingsKey, string(data))
}

func (s *UpstreamBillingProbeService) SetBalanceConfig(ctx context.Context, id int64, config UpstreamBalanceConfig) (*UpstreamBalanceState, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if s == nil || s.accountRepo == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !isUpstreamBillingProbeAccount(account) {
		return nil, ErrUpstreamBillingProbeAccountInvalid
	}
	previous := DecodeUpstreamBalanceState(account.Extra)
	state := &UpstreamBalanceState{UpstreamBalanceConfig: config, Snapshot: previous.Snapshot}
	if config.Provider != previous.Provider || config.Currency != previous.Currency || config.QuotaPerUnit != previous.QuotaPerUnit {
		state.Snapshot = nil
	}
	if err := s.saveBalanceState(ctx, account, state); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *UpstreamBillingProbeService) saveBalanceState(ctx context.Context, account *Account, state *UpstreamBalanceState) error {
	writer, ok := s.accountRepo.(upstreamBalanceStateWriter)
	if !ok {
		return ErrUpstreamBillingProbeUnavailable
	}
	return writer.UpdateUpstreamBalanceState(ctx, account, state)
}

func (s *UpstreamBillingProbeService) RunBalanceDue(ctx context.Context) error {
	if s == nil || s.accountRepo == nil {
		return nil
	}
	settings, err := s.GetBalanceSettings(ctx)
	if err != nil || !settings.Enabled {
		return err
	}
	s.cycleMu.Lock()
	defer s.cycleMu.Unlock()
	release, acquired, err := s.tryAcquireLeaderLock(ctx, "upstream:balance:probe:leader")
	if err != nil || !acquired {
		return err
	}
	defer release()
	lister, ok := s.accountRepo.(upstreamBalanceDueAccountLister)
	if !ok {
		return nil
	}
	accounts, err := lister.ListDueUpstreamBalanceAccounts(ctx, s.currentTime(), UpstreamBillingProbeMaxBatchSize)
	if err != nil {
		return err
	}
	var group errgroup.Group
	for _, account := range accounts {
		group.Go(func() error {
			_, err := s.probeBalance(ctx, account.ID, true, settings.IntervalMinutes)
			return err
		})
	}
	return group.Wait()
}

func (s *UpstreamBillingProbeService) ProbeBalances(ctx context.Context, ids []int64) []UpstreamBalanceProbeResult {
	results := make([]UpstreamBalanceProbeResult, len(ids))
	var group errgroup.Group
	for i, id := range ids {
		group.Go(func() error {
			state, err := s.ProbeBalance(ctx, id)
			results[i] = UpstreamBalanceProbeResult{AccountID: id, State: state}
			if err != nil {
				results[i].Error = "balance_probe_failed"
			}
			return nil
		})
	}
	_ = group.Wait()
	return results
}

func (s *UpstreamBillingProbeService) ProbeBalance(ctx context.Context, id int64) (*UpstreamBalanceState, error) {
	settings, err := s.GetBalanceSettings(ctx)
	if err != nil {
		return nil, err
	}
	return s.probeBalance(ctx, id, false, settings.IntervalMinutes)
}

func (s *UpstreamBillingProbeService) probeBalance(ctx context.Context, id int64, scheduled bool, interval int) (*UpstreamBalanceState, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	value, err, _ := s.probeGroup.Do("balance:"+strconv.FormatInt(id, 10), func() (any, error) {
		select {
		case s.probeSlots <- struct{}{}:
			defer func() { <-s.probeSlots }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		account, err := s.accountRepo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if !isUpstreamBillingProbeAccount(account) {
			return nil, ErrUpstreamBillingProbeAccountInvalid
		}
		state := DecodeUpstreamBalanceState(account.Extra)
		if err := state.Validate(); err != nil {
			return nil, err
		}
		if scheduled && (!state.Enabled || !account.IsActive() || state.Snapshot != nil && s.currentTime().Before(state.Snapshot.NextProbeAt)) {
			return &state, nil
		}
		return s.probeLoadedBalance(ctx, account, state, interval)
	})
	if err != nil {
		return nil, err
	}
	state, ok := value.(*UpstreamBalanceState)
	if !ok {
		return nil, errors.New("invalid balance probe result")
	}
	return state, nil
}

func (s *UpstreamBillingProbeService) probeLoadedBalance(ctx context.Context, account *Account, state UpstreamBalanceState, interval int) (*UpstreamBalanceState, error) {
	now := s.currentTime().UTC()
	previous := state.Snapshot
	snapshot := &UpstreamBalanceSnapshot{Status: UpstreamBillingProbeStatusFailed, LastAttemptAt: now, NextProbeAt: now.Add(nextProbeDelay(interval, 0))}
	if previous != nil {
		snapshot.Amounts, snapshot.ReceivedAt, snapshot.FreshUntil, snapshot.Provider = previous.Amounts, previous.ReceivedAt, previous.FreshUntil, previous.Provider
		snapshot.FailureCount = previous.FailureCount + 1
	}
	state.Snapshot = snapshot
	fail := func(reason string, status int) (*UpstreamBalanceState, error) {
		snapshot.LastError, snapshot.HTTPStatus = reason, status
		if snapshot.FailureCount == 0 {
			snapshot.FailureCount = 1
		}
		if reason == "unsupported" {
			snapshot.Status = UpstreamBillingProbeStatusUnsupported
			snapshot.NextProbeAt = now.Add(unsupportedProbeDelay(interval, 0))
		}
		return &state, s.saveBalanceState(ctx, account, &state)
	}
	if s.accountTestService == nil || s.accountTestService.httpUpstream == nil {
		return fail("transport_unavailable", 0)
	}
	baseURL := account.GetCredential("base_url")
	if baseURL == "" || upstreamBillingProbeTargetIsOfficialAPI(baseURL) {
		return fail("unsupported", 0)
	}
	baseURL, err := s.accountTestService.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return fail("invalid_base_url", 0)
	}
	key := account.GetCredential("api_key")
	if key == "" {
		return fail("missing_api_key", 0)
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil || account.Proxy.ID != *account.ProxyID {
			return fail("proxy_unavailable", 0)
		}
		proxyURL = account.Proxy.URL()
	}
	providers := []string{state.Provider}
	if state.Provider == "auto" {
		providers = []string{"sub2api", "newapi"}
		if previous != nil && previous.Provider == "newapi" {
			providers = []string{"newapi", "sub2api"}
		}
	}
	for _, provider := range providers {
		path := "/v1/usage"
		if provider == "newapi" {
			path = "/api/usage/token/"
		}
		probeCtx, cancel := context.WithTimeout(ctx, upstreamBillingProbeRequestTimeout)
		endpoint := strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1") + path
		req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			cancel()
			return fail("request_build_failed", 0)
		}
		profile := HTTPUpstreamProfileDefault
		if account.Platform == PlatformOpenAI {
			profile = HTTPUpstreamProfileOpenAI
		}
		req = req.WithContext(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(req.Context(), profile)))
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		account.ApplyHeaderOverrides(req.Header)
		var tlsProfile *tlsfingerprint.Profile
		if s.accountTestService.tlsFPProfileService != nil {
			tlsProfile = s.accountTestService.tlsFPProfileService.ResolveTLSProfile(account)
		}
		resp, err := s.accountTestService.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
		if err != nil {
			cancel()
			return fail("request_failed", 0)
		}
		if resp == nil || resp.Body == nil {
			cancel()
			return fail("empty_response", 0)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, upstreamBillingProbeMaxBodyBytes+1))
		_ = resp.Body.Close()
		cancel()
		snapshot.HTTPStatus = resp.StatusCode
		if readErr != nil {
			return fail("response_read_failed", resp.StatusCode)
		}
		if len(body) > upstreamBillingProbeMaxBodyBytes {
			return fail("response_too_large", resp.StatusCode)
		}
		if resp.StatusCode == 404 || resp.StatusCode == 405 {
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			snapshot.NextProbeAt = now.Add(nextProbeDelay(interval, retryAfter(resp.Header, now)))
			return fail("http_error", resp.StatusCode)
		}
		amounts, err := parseUpstreamBalance(body, provider, state.UpstreamBalanceConfig)
		if err != nil {
			if state.Provider == "auto" {
				continue
			}
			return fail("invalid_response", resp.StatusCode)
		}
		snapshot.Status, snapshot.Provider, snapshot.Amounts = UpstreamBillingProbeStatusOK, provider, amounts
		snapshot.FailureCount = 0
		snapshot.ReceivedAt, snapshot.FreshUntil = probeTimePtr(now), probeTimePtr(now.Add(2*time.Duration(interval)*time.Minute))
		return &state, s.saveBalanceState(ctx, account, &state)
	}
	return fail("unsupported", snapshot.HTTPStatus)
}

func balanceNumber(value any) *float64 {
	var text string
	switch value := value.(type) {
	case json.Number:
		text = value.String()
	case string:
		text = value
	default:
		return nil
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Abs(number) > 1e15 {
		return nil
	}
	return &number
}

func balanceAmount(data map[string]any, scope, currency, remainingKey string, scale float64) (UpstreamBalanceAmount, error) {
	amount := UpstreamBalanceAmount{Scope: scope, Currency: currency}
	if raw, present := data["unlimited_quota"]; present {
		value, ok := raw.(bool)
		if !ok {
			return amount, errors.New("invalid unlimited quota flag")
		}
		amount.Unlimited = value
	}
	if amount.Unlimited {
		return amount, nil
	}
	amount.Remaining = balanceNumber(data[remainingKey])
	if amount.Remaining == nil {
		return amount, errors.New("missing balance")
	}
	*amount.Remaining /= scale
	return amount, nil
}

func parseUpstreamBalance(body []byte, provider string, config UpstreamBalanceConfig) ([]UpstreamBalanceAmount, error) {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var data map[string]any
	if err := decoder.Decode(&data); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing balance data")
	}
	if raw, present := data["success"]; present {
		if success, ok := raw.(bool); !ok || !success {
			return nil, errors.New("balance query rejected")
		}
	}
	if provider == "newapi" {
		if raw, present := data["code"]; present {
			switch code := raw.(type) {
			case bool:
				if !code {
					return nil, errors.New("balance query rejected")
				}
			case json.Number:
				if code.String() != "0" {
					return nil, errors.New("balance query rejected")
				}
			default:
				return nil, errors.New("invalid balance result code")
			}
		}
		nested, ok := data["data"].(map[string]any)
		if !ok || nested["object"] != "token_usage" {
			return nil, errors.New("invalid token usage schema")
		}
		currency, scale := config.Currency, config.QuotaPerUnit
		if unit, ok := nested["unit"].(string); ok {
			if !upstreamBalanceCurrencyPattern.MatchString(unit) {
				return nil, errors.New("invalid balance unit")
			}
			currency, scale = unit, 1
		}
		amount, err := balanceAmount(nested, "key", currency, "total_available", scale)
		if err != nil {
			return nil, err
		}
		if value := balanceNumber(nested["total_granted"]); value != nil {
			*value /= scale
			amount.Limit = value
		}
		if value := balanceNumber(nested["total_used"]); value != nil {
			*value /= scale
			amount.Used = value
		}
		return []UpstreamBalanceAmount{amount}, nil
	}
	if provider != "sub2api" || data["mode"] != "unrestricted" && data["mode"] != "quota_limited" {
		return nil, errors.New("invalid usage schema")
	}
	if raw, present := data["isValid"]; present {
		if valid, ok := raw.(bool); !ok || !valid {
			return nil, errors.New("invalid upstream key")
		}
	}
	currency := "USD"
	if unit, present := data["unit"]; present {
		value, ok := unit.(string)
		if !ok || !upstreamBalanceCurrencyPattern.MatchString(value) {
			return nil, errors.New("invalid balance unit")
		}
		currency = value
	}
	var amounts []UpstreamBalanceAmount
	if data["balance"] != nil {
		amount, err := balanceAmount(data, "wallet", currency, "balance", 1)
		if err != nil {
			return nil, err
		}
		amounts = append(amounts, amount)
	}
	if quota, ok := data["quota"].(map[string]any); ok {
		amount, err := balanceAmount(quota, "key", currency, "remaining", 1)
		if err != nil {
			return nil, err
		}
		amount.Limit, amount.Used = balanceNumber(quota["limit"]), balanceNumber(quota["used"])
		amounts = append(amounts, amount)
	}
	if windows, ok := data["rate_limits"].([]any); ok {
		for _, value := range windows {
			window, ok := value.(map[string]any)
			if !ok {
				return nil, errors.New("invalid quota window")
			}
			scope, _ := window["window"].(string)
			if scope != "5h" && scope != "1d" && scope != "7d" {
				return nil, errors.New("unknown quota window")
			}
			amount, err := balanceAmount(window, scope, currency, "remaining", 1)
			if err != nil {
				return nil, err
			}
			amount.Limit, amount.Used = balanceNumber(window["limit"]), balanceNumber(window["used"])
			if raw := window["reset_at"]; raw != nil {
				text, ok := raw.(string)
				if !ok {
					return nil, errors.New("invalid quota reset time")
				}
				reset, err := time.Parse(time.RFC3339Nano, text)
				if err != nil {
					return nil, err
				}
				amount.ResetAt = &reset
			}
			amounts = append(amounts, amount)
		}
	}
	if len(amounts) == 0 {
		if _, subscription := data["subscription"].(map[string]any); !subscription {
			return nil, errors.New("no balance or quota")
		}
		amount, err := balanceAmount(data, "subscription", currency, "remaining", 1)
		if err != nil {
			return nil, err
		}
		if amount.Remaining != nil && *amount.Remaining == -1 {
			amount.Unlimited, amount.Remaining = true, nil
		}
		amounts = append(amounts, amount)
	}
	return amounts, nil
}
