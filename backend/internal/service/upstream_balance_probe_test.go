package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type balanceTestRepo struct {
	*upstreamBillingProbeAccountRepo
}

func (r *balanceTestRepo) UpdateUpstreamBalanceState(_ context.Context, expected *Account, state *UpstreamBalanceState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.accounts[expected.ID]
	if !reflect.DeepEqual(current.Credentials, expected.Credentials) || !reflect.DeepEqual(current.Extra[UpstreamBalanceProbeExtraKey], expected.Extra[UpstreamBalanceProbeExtraKey]) {
		return ErrUpstreamBillingProbeIdentityChanged
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return err
	}
	current.Extra[UpstreamBalanceProbeExtraKey] = value
	return nil
}

func TestUpstreamBalanceParsing(t *testing.T) {
	for _, tt := range []struct {
		name, provider, body string
		scope                string
		remaining            float64
		unlimited, invalid   bool
	}{
		{name: "wallet", provider: "sub2api", body: `{"mode":"unrestricted","isValid":true,"unit":"USD","balance":25.59613293}`, scope: "wallet", remaining: 25.59613293},
		{name: "key", provider: "sub2api", body: `{"mode":"quota_limited","quota":{"limit":10,"used":10,"remaining":0}}`, scope: "key"},
		{name: "window", provider: "sub2api", body: `{"mode":"quota_limited","rate_limits":[{"window":"5h","remaining":0,"reset_at":"2026-10-09T12:00:00Z"}]}`, scope: "5h"},
		{name: "subscription unlimited", provider: "sub2api", body: `{"mode":"unrestricted","subscription":{},"remaining":-1}`, scope: "subscription", unlimited: true},
		{name: "subscription", provider: "sub2api", body: `{"mode":"unrestricted","subscription":{},"remaining":3.5}`, scope: "subscription", remaining: 3.5},
		{name: "raw token units", provider: "newapi", body: `{"code":true,"data":{"object":"token_usage","total_available":1500000,"total_granted":2000000,"total_used":500000}}`, scope: "key", remaining: 3},
		{name: "explicit currency", provider: "newapi", body: `{"code":0,"data":{"object":"token_usage","unit":"USD","total_available":3}}`, scope: "key", remaining: 3},
		{name: "token unlimited", provider: "newapi", body: `{"data":{"object":"token_usage","unlimited_quota":true,"total_available":0}}`, scope: "key", unlimited: true},
		{name: "malformed unlimited flag", provider: "newapi", body: `{"data":{"object":"token_usage","unlimited_quota":"true","total_available":0}}`, invalid: true},
		{name: "malformed validity flag", provider: "sub2api", body: `{"mode":"unrestricted","isValid":"false","balance":0}`, invalid: true},
		{name: "malformed reset time", provider: "sub2api", body: `{"mode":"quota_limited","rate_limits":[{"window":"5h","remaining":0,"reset_at":123}]}`, invalid: true},
		{name: "missing wallet", provider: "sub2api", body: `{"mode":"unrestricted","remaining":0}`, invalid: true},
		{name: "missing token quota", provider: "newapi", body: `{"data":{"object":"token_usage","total_used":50}}`, invalid: true},
		{name: "rejected", provider: "newapi", body: `{"code":false,"data":{"object":"token_usage","total_available":0}}`, invalid: true},
		{name: "invalid key", provider: "sub2api", body: `{"mode":"unrestricted","isValid":false,"balance":0}`, invalid: true},
		{name: "nonfinite", provider: "sub2api", body: `{"mode":"unrestricted","balance":"NaN"}`, invalid: true},
		{name: "wrong schema", provider: "sub2api", body: `{"balance":0}`, invalid: true},
		{name: "invalid JSON", provider: "sub2api", body: `{"mode":`, invalid: true},
		{name: "trailing JSON", provider: "sub2api", body: `{"mode":"unrestricted","balance":0} {}`, invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			amounts, err := parseUpstreamBalance([]byte(tt.body), tt.provider, DefaultUpstreamBalanceConfig())
			if tt.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, amounts, 1)
			require.Equal(t, tt.scope, amounts[0].Scope)
			require.Equal(t, tt.unlimited, amounts[0].Unlimited)
			if tt.unlimited {
				require.Nil(t, amounts[0].Remaining)
			} else {
				require.InDelta(t, tt.remaining, *amounts[0].Remaining, 1e-9)
			}
		})
	}
}

func TestUpstreamBalanceSchedulingSafety(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	zero, positive := 0.0, 10.0
	for _, tt := range []struct {
		name   string
		mutate func(*UpstreamBalanceState)
		paused bool
	}{
		{name: "fresh zero", paused: true},
		{name: "recharged", mutate: func(s *UpstreamBalanceState) { s.Snapshot.Amounts[0].Remaining = &positive }},
		{name: "failed after zero", mutate: func(s *UpstreamBalanceState) { s.Snapshot.Status = "failed" }},
		{name: "unsupported", mutate: func(s *UpstreamBalanceState) { s.Snapshot.Status = "unsupported" }},
		{name: "stale", mutate: func(s *UpstreamBalanceState) { s.Snapshot.FreshUntil = probeTimePtr(now.Add(-time.Second)) }},
		{name: "expired window", mutate: func(s *UpstreamBalanceState) { s.Snapshot.Amounts[0].ResetAt = probeTimePtr(now.Add(-time.Second)) }},
		{name: "unlimited", mutate: func(s *UpstreamBalanceState) { s.Snapshot.Amounts[0].Unlimited = true }},
		{name: "missing balance", mutate: func(s *UpstreamBalanceState) { s.Snapshot.Amounts[0].Remaining = nil }},
		{name: "disabled", mutate: func(s *UpstreamBalanceState) { s.Enabled = false }},
		{name: "policy off", mutate: func(s *UpstreamBalanceState) { s.PauseOnExhaustion = false }},
		{name: "missing timestamp", mutate: func(s *UpstreamBalanceState) { s.Snapshot.ReceivedAt = nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := UpstreamBalanceState{UpstreamBalanceConfig: DefaultUpstreamBalanceConfig(), Snapshot: &UpstreamBalanceSnapshot{Status: "ok", ReceivedAt: probeTimePtr(now.Add(-time.Minute)), FreshUntil: probeTimePtr(now.Add(time.Hour)), Amounts: []UpstreamBalanceAmount{{Scope: "wallet", Currency: "USD", Remaining: &zero}}}}
			state.Enabled, state.PauseOnExhaustion = true, true
			if tt.mutate != nil {
				tt.mutate(&state)
			}
			account := &Account{Type: AccountTypeAPIKey, Extra: map[string]any{UpstreamBalanceProbeExtraKey: state}}
			require.Equal(t, tt.paused, account.IsUpstreamBalancePaused(now))
		})
	}
}

func TestUpstreamBalanceProbeReadOnlyAndFailurePreservesData(t *testing.T) {
	for _, tt := range []struct {
		name           string
		code           int
		body           string
		transportError bool
		expected       string
	}{
		{name: "success", code: 200, body: `{"mode":"unrestricted","balance":12}`, expected: "ok"},
		{name: "403", code: 403, body: `{"secret":"must-not-save"}`, expected: "failed"},
		{name: "timeout", transportError: true, expected: "failed"},
		{name: "unsupported", code: 404, expected: "unsupported"},
		{name: "invalid response", code: 200, body: `{"mode":"unrestricted"}`, expected: "unsupported"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			zero := 0.0
			state := UpstreamBalanceState{UpstreamBalanceConfig: DefaultUpstreamBalanceConfig(), Snapshot: &UpstreamBalanceSnapshot{Status: "ok", Amounts: []UpstreamBalanceAmount{{Scope: "wallet", Currency: "USD", Remaining: &zero}}}}
			state.Enabled, state.PauseOnExhaustion = true, true
			account := &Account{ID: 17, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"base_url": "https://upstream.example/v1", "api_key": "secret-key"}, Extra: map[string]any{UpstreamBalanceProbeExtraKey: state}}
			repo := &balanceTestRepo{&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{17: account}}}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				{StatusCode: tt.code, Body: io.NopCloser(strings.NewReader(tt.body))},
				{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))},
			}}
			if tt.transportError {
				upstream.err = errors.New("secret transport detail")
			}
			svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
			result, err := svc.ProbeBalance(context.Background(), 17)
			require.NoError(t, err)
			require.Equal(t, tt.expected, result.Snapshot.Status)
			for _, req := range upstream.requests {
				require.Equal(t, http.MethodGet, req.Method)
				require.Contains(t, []string{"/v1/usage", "/api/usage/token/"}, req.URL.Path)
				require.Nil(t, req.Body)
			}
			if tt.code == 403 {
				require.Len(t, upstream.requests, 1)
			}
			if tt.expected != "ok" {
				require.Equal(t, 0.0, *result.Snapshot.Amounts[0].Remaining)
				require.False(t, account.IsUpstreamBalancePaused(time.Now()))
			}
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "secret")
			require.True(t, account.Schedulable)
		})
	}
}

func TestUpstreamBalanceConfigInvalidatesConvertedData(t *testing.T) {
	state := UpstreamBalanceState{UpstreamBalanceConfig: DefaultUpstreamBalanceConfig(), Snapshot: &UpstreamBalanceSnapshot{Status: "ok"}}
	account := &Account{ID: 17, Type: AccountTypeAPIKey, Platform: PlatformGrok, Credentials: map[string]any{}, Extra: map[string]any{UpstreamBalanceProbeExtraKey: state}}
	repo := &balanceTestRepo{&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{17: account}}}
	svc := newUpstreamBillingProbeTestService(repo, nil, &upstreamBillingProbeSettingRepo{})
	config := state.UpstreamBalanceConfig
	config.QuotaPerUnit = 1000000
	result, err := svc.SetBalanceConfig(context.Background(), 17, config)
	require.NoError(t, err)
	require.Nil(t, result.Snapshot)
	config.PauseOnExhaustion = true
	require.Error(t, config.Validate())
}
