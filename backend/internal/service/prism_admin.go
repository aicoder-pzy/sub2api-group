package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const prismAdminSettingsKey = "custom_prism_admin_v1"

var prismAdminModels = []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-6-luna", "gpt-6.1-sol"}
var prismAdminEfforts = []string{"low", "medium", "high", "xhigh"}

// Only admin endpoints use these settings. They never change account routing.
type PrismAdminSettingsInput struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

type PrismAdminSettings struct {
	Enabled          bool   `json:"enabled"`
	BaseURL          string `json:"base_url"`
	APIKeyConfigured bool   `json:"api_key_configured"`
}

type PrismAdminAccount struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type PrismAdminDashboard struct {
	Settings PrismAdminSettings  `json:"settings"`
	Models   []string            `json:"models"`
	Efforts  []string            `json:"efforts"`
	Accounts []PrismAdminAccount `json:"accounts"`
}

type PrismAdminTestInput struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
	Prompt string `json:"prompt"`
}

type PrismAdminTestResult struct {
	Success        bool   `json:"success"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	RequestID      string `json:"request_id"`
	Text           string `json:"text"`
	DurationMS     int64  `json:"duration_ms"`
	UsageAvailable bool   `json:"usage_available"`
}

func prismAdminEligible(a *Account) bool {
	return a != nil && a.Platform == PlatformOpenAI && a.Type == AccountTypeOAuth &&
		!a.IsShadow() && !a.IsSyntheticUITest() && !a.IsOpenAIAgentIdentity() && !a.IsOpenAIPersonalAccessToken()
}

func prismAdminBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(raw), "/"))
	if err == nil && u.Scheme == "http" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Opaque == "" && u.RawPath == "" && u.Path == "/v1" {
		ip := net.ParseIP(u.Hostname())
		port, portErr := strconv.Atoi(u.Port())
		if (ip.Equal(net.ParseIP("127.0.0.1")) || ip.Equal(net.IPv6loopback)) && portErr == nil && port >= 1024 && port <= 65535 {
			return u.String(), nil
		}
	}
	return "", infraerrors.New(400, "PRISM_INVALID_ENDPOINT", "Prism requires http://127.0.0.1:<port>/v1 or http://[::1]:<port>/v1 (port 1024–65535)")
}

func (v PrismAdminSettingsInput) public() PrismAdminSettings {
	return PrismAdminSettings{Enabled: v.Enabled, BaseURL: v.BaseURL, APIKeyConfigured: v.APIKey != ""}
}

func (s *OpenAIGatewayService) prismAdminSettings(ctx context.Context) (PrismAdminSettingsInput, error) {
	v := PrismAdminSettingsInput{BaseURL: "http://127.0.0.1:8319/v1"}
	if s.settingService == nil || s.settingService.settingRepo == nil {
		return v, infraerrors.New(503, "PRISM_SETTINGS_UNAVAILABLE", "Prism settings are unavailable")
	}
	raw, err := s.settingService.settingRepo.GetValue(ctx, prismAdminSettingsKey)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && raw == "") {
		return v, nil
	}
	if err != nil || json.Unmarshal([]byte(raw), &v) != nil {
		return PrismAdminSettingsInput{}, infraerrors.New(503, "PRISM_SETTINGS_UNAVAILABLE", "Prism settings could not be read")
	}
	return v, nil
}

func (s *OpenAIGatewayService) GetPrismAdmin(ctx context.Context) (*PrismAdminDashboard, error) {
	v, err := s.prismAdminSettings(ctx)
	if err != nil {
		return nil, err
	}
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return nil, infraerrors.New(503, "PRISM_ACCOUNTS_UNAVAILABLE", "Accounts could not be loaded")
	}
	d := &PrismAdminDashboard{Settings: v.public(), Models: slices.Clone(prismAdminModels), Efforts: slices.Clone(prismAdminEfforts), Accounts: []PrismAdminAccount{}}
	for i := range accounts {
		if prismAdminEligible(&accounts[i]) {
			d.Accounts = append(d.Accounts, PrismAdminAccount{ID: accounts[i].ID, Name: accounts[i].Name, Status: accounts[i].Status})
		}
	}
	return d, nil
}

func (s *OpenAIGatewayService) SavePrismAdmin(ctx context.Context, v PrismAdminSettingsInput) (PrismAdminSettings, error) {
	var err error
	v.BaseURL, err = prismAdminBaseURL(v.BaseURL)
	if err != nil {
		return PrismAdminSettings{}, err
	}
	old, err := s.prismAdminSettings(ctx)
	if err != nil {
		return PrismAdminSettings{}, err
	}
	if v.APIKey == "" {
		v.APIKey = old.APIKey
	}
	if (v.Enabled || v.APIKey != "") && (len(v.APIKey) < 32 || len(v.APIKey) > 256 || strings.IndexFunc(v.APIKey, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0) {
		return PrismAdminSettings{}, infraerrors.New(400, "PRISM_INVALID_KEY", "Configure a bridge key of 32–256 visible ASCII characters, without spaces")
	}
	raw, _ := json.Marshal(v)
	if err := s.settingService.settingRepo.Set(ctx, prismAdminSettingsKey, string(raw)); err != nil {
		return PrismAdminSettings{}, infraerrors.New(503, "PRISM_SETTINGS_UNAVAILABLE", "Prism settings could not be saved")
	}
	return v.public(), nil
}

// No environment/account proxies, redirects, cookies, plugins or automatic retries.
// The adapter shares the application's network namespace and only binds loopback.
func prismAdminRequest(ctx context.Context, baseURL, path, key, token string, accountID int64, body []byte) ([]byte, int, error) {
	base, err := prismAdminBaseURL(baseURL)
	if err != nil {
		return nil, 0, err
	}
	method, endpoint, timeout := http.MethodGet, strings.TrimSuffix(base, "/v1")+"/health", 5*time.Second
	if path == "/responses" {
		method, endpoint, timeout = http.MethodPost, base+path, 5*time.Minute
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, infraerrors.New(502, "PRISM_UNAVAILABLE", "Prism request could not be prepared")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Prism-Account-ID", strconv.FormatInt(accountID, 10))
		req.Header.Set("X-Prism-OAuth-Token", token)
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		// Never include transport errors: they may contain addresses or credentials.
		return nil, 0, infraerrors.New(502, "PRISM_UNAVAILABLE", "Prism adapter unavailable or timed out; inspect pending state before retrying")
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return nil, 0, infraerrors.New(502, "PRISM_INVALID_RESPONSE", "Prism response is incomplete or too large; inspect pending state before retrying")
	}
	return data, resp.StatusCode, nil
}

func (s *OpenAIGatewayService) CheckPrismAdmin(ctx context.Context) (bool, error) {
	v, err := s.prismAdminSettings(ctx)
	if err != nil {
		return false, err
	}
	data, status, err := prismAdminRequest(ctx, v.BaseURL, "/health", v.APIKey, "", 0, nil)
	if err != nil {
		return false, err
	}
	var health struct {
		Status string `json:"status"`
	}
	return status == 200 && json.Unmarshal(data, &health) == nil && health.Status == "ok", nil
}

func prismAdminResponse(data []byte, input PrismAdminTestInput) (*PrismAdminTestResult, error) {
	var r struct {
		ID, Model, Status string
		Reasoning         struct{ Effort string }
		Output            []struct {
			Type, Role, Status string
			Content            []struct{ Type, Text string }
		}
	}
	invalid := infraerrors.New(502, "PRISM_INVALID_RESPONSE", "Prism returned no complete text response matching the selected model and effort")
	if json.Unmarshal(data, &r) != nil || r.Status != "completed" || r.ID == "" || len(r.ID) > 256 || r.Model != input.Model || r.Reasoning.Effort != input.Effort || len(r.Output) == 0 {
		return nil, invalid
	}
	var text []string
	for _, item := range r.Output {
		if item.Type != "message" || item.Role != "assistant" || item.Status != "completed" || len(item.Content) == 0 {
			return nil, invalid
		}
		for _, part := range item.Content {
			if part.Type != "output_text" || strings.TrimSpace(part.Text) == "" {
				return nil, invalid
			}
			text = append(text, part.Text)
		}
	}
	return &PrismAdminTestResult{Success: true, Model: r.Model, Effort: r.Reasoning.Effort, RequestID: r.ID, Text: strings.Join(text, "\n")}, nil
}

func prismAdminUpstreamError(data []byte, status int) error {
	var r struct{ Error struct{ Type string } }
	_ = json.Unmarshal(data, &r)
	code := r.Error.Type
	if !slices.Contains([]string{"model_unavailable", "reasoning_unavailable", "model_catalog_unavailable", "unsupported_model", "unsupported_reasoning", "pending_turn", "prism_busy", "resource_pressure", "credential_rotation", "start_not_sent", "prism_unavailable"}, code) {
		code = "adapter_rejected"
	}
	if !slices.Contains([]int{409, 422, 429, 503}, status) {
		status = 502 // Local bridge auth failures must not invalidate the admin login.
	}
	return infraerrors.New(status, "PRISM_"+strings.ToUpper(code), "Prism: "+code+"; no fallback or automatic retry was performed")
}

func (s *OpenAIGatewayService) TestPrismAdmin(ctx context.Context, accountID int64, input PrismAdminTestInput) (*PrismAdminTestResult, error) {
	if !slices.Contains(prismAdminModels, input.Model) || !slices.Contains(prismAdminEfforts, input.Effort) || strings.TrimSpace(input.Prompt) == "" || len(input.Prompt) > 4096 {
		return nil, infraerrors.New(400, "PRISM_INVALID_TEST", "Select a supported model and effort; provide 1–4096 bytes of text")
	}
	v, err := s.prismAdminSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !v.Enabled || v.APIKey == "" {
		return nil, infraerrors.New(400, "PRISM_DISABLED", "Save and enable Prism administrator testing first")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || !prismAdminEligible(account) || account.Status != StatusActive {
		return nil, infraerrors.New(400, "PRISM_INVALID_ACCOUNT", "Select an active OpenAI OAuth account (not a shadow or agent identity)")
	}
	// ponytail: one test per gateway process; the adapter also serializes across instances.
	if !s.prismAdminRunning.CompareAndSwap(false, true) {
		return nil, infraerrors.New(429, "PRISM_BUSY", "A Prism administrator test is already running")
	}
	defer s.prismAdminRunning.Store(false)
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil || token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, infraerrors.New(502, "PRISM_ACCOUNT_AUTH", "The account OAuth token could not be obtained")
	}
	body, _ := json.Marshal(map[string]any{"model": input.Model, "input": input.Prompt, "stream": false, "reasoning": map[string]string{"effort": input.Effort}})
	started := time.Now()
	data, status, err := prismAdminRequest(ctx, v.BaseURL, "/responses", v.APIKey, token, accountID, body)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, prismAdminUpstreamError(data, status)
	}
	result, err := prismAdminResponse(data, input)
	if err != nil {
		return nil, err
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}
