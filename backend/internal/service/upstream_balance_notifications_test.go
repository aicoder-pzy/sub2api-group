package service

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type balanceNoticeTestTransport func(*http.Request) (*http.Response, error)

func (f balanceNoticeTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBalanceNotificationEmailRespectsDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = io.Copy(io.Discard, conn)
	}()
	addr, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = (&EmailService{}).sendEmailWithConfig(ctx, &SMTPConfig{Host: "127.0.0.1", Port: addr.Port, From: "sender@example.com"}, "ops@example.com", "test", "body")
	require.Error(t, err)
	require.Less(t, time.Since(started), 2*time.Second)
	<-done
}

func TestBalanceNotificationsEncryptAndRetainCredentials(t *testing.T) {
	settingsRepo := &upstreamBillingProbeSettingRepo{}
	s := newUpstreamBillingProbeTestService(nil, nil, settingsRepo)
	s.SetNewAPIAuthorization(nil, newAPIEncryptorFake{}, true)
	config := UpstreamBalanceNotificationSettings{Enabled: true, NotifyRecovery: true, Channels: []UpstreamBalanceNotificationChannel{{ID: uuid.NewString(), Provider: "dingtalk", Enabled: true, Name: "ops", URL: "https://oapi.dingtalk.com/robot/send?access_token=private-destination", Secret: "private-signing-secret"}}}
	saved, err := s.SetBalanceNotificationSettings(context.Background(), config)
	require.NoError(t, err)
	require.True(t, saved.Channels[0].URLConfigured)
	require.True(t, saved.Channels[0].SecretConfigured)
	payload, err := json.Marshal(saved)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "private-destination")
	require.NotContains(t, string(payload), "private-signing-secret")
	revision := saved.Channels[0].revision
	saved, err = s.SetBalanceNotificationSettings(context.Background(), *saved)
	require.NoError(t, err)
	require.Equal(t, revision, saved.Channels[0].revision)
	saved.Channels[0].ClearSecret = true
	saved, err = s.SetBalanceNotificationSettings(context.Background(), *saved)
	require.NoError(t, err)
	require.False(t, saved.Channels[0].SecretConfigured)
	require.True(t, saved.Channels[0].URLConfigured)
	require.NotEqual(t, revision, saved.Channels[0].revision)
	saved.Channels[0].Provider = "feishu"
	_, err = s.SetBalanceNotificationSettings(context.Background(), *saved)
	require.Error(t, err)
	s.newAPIFixedKey = false
	_, err = s.SetBalanceNotificationSettings(context.Background(), config)
	require.ErrorIs(t, err, ErrNewAPIEncryptionKey)
	config.Channels = []UpstreamBalanceNotificationChannel{{Provider: "email", Email: "ops@example.com", Enabled: true}}
	_, err = s.SetBalanceNotificationSettings(context.Background(), config)
	require.NoError(t, err)
}

func TestBalanceNotificationsIgnoreMissingFailedExpiredAndUnlimitedAmounts(t *testing.T) {
	now := time.Now().UTC()
	remaining := 0.0
	makeAccount := func() UpstreamBalanceNotificationAccount {
		state := UpstreamBalanceState{UpstreamBalanceConfig: DefaultUpstreamBalanceConfig(), Snapshot: &UpstreamBalanceSnapshot{Status: "ok", ReceivedAt: probeTimePtr(now.Add(-time.Minute)), FreshUntil: probeTimePtr(now.Add(time.Hour)), Amounts: []UpstreamBalanceAmount{{Scope: "wallet", Currency: "USD", Remaining: &remaining}}}}
		state.Enabled = true
		return UpstreamBalanceNotificationAccount{ID: 1, Name: "channel", State: state}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*UpstreamBalanceNotificationAccount)
	}{
		{"disabled", func(a *UpstreamBalanceNotificationAccount) { a.State.Enabled = false }},
		{"failed", func(a *UpstreamBalanceNotificationAccount) { a.State.Snapshot.Status = "failed" }},
		{"stale", func(a *UpstreamBalanceNotificationAccount) {
			a.State.Snapshot.FreshUntil = probeTimePtr(now.Add(-time.Second))
		}},
		{"unknown", func(a *UpstreamBalanceNotificationAccount) { a.State.Snapshot.Amounts[0].Remaining = nil }},
		{"unlimited", func(a *UpstreamBalanceNotificationAccount) { a.State.Snapshot.Amounts[0].Unlimited = true }},
		{"reset", func(a *UpstreamBalanceNotificationAccount) { a.State.Snapshot.Amounts[0].ResetAt = probeTimePtr(now) }},
		{"future", func(a *UpstreamBalanceNotificationAccount) {
			a.State.Snapshot.ReceivedAt = probeTimePtr(now.Add(time.Second))
		}},
		{"opt out", func(a *UpstreamBalanceNotificationAccount) { v := false; a.State.NotificationEnabled = &v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := makeAccount()
			tc.mutate(&a)
			require.Empty(t, balanceNotificationObservations(a, 5, now))
		})
	}
	account := makeAccount()
	first := balanceNotificationObservations(account, 5, now)
	require.Len(t, first, 1)
	require.Zero(t, first[0].Remaining)
	custom := 2.0
	account.State.NotificationThreshold = &custom
	next := balanceNotificationObservations(account, 5, now)
	require.Equal(t, 2.0, next[0].Threshold)
	require.NotEqual(t, first[0].Criteria, next[0].Criteria)
}

func TestBalanceNotificationRobotsValidateSignaturesAndResponseCodes(t *testing.T) {
	previous := balanceNotificationHTTPClient
	t.Cleanup(func() { balanceNotificationHTTPClient = previous })
	s := newUpstreamBillingProbeTestService(nil, nil, nil)
	s.SetNewAPIAuthorization(nil, newAPIEncryptorFake{}, true)
	event := &UpstreamBalanceNotificationEvent{Phase: "low", UpstreamBalanceNotificationObservation: UpstreamBalanceNotificationObservation{AccountID: 1, AccountName: "channel", Scope: "wallet", Currency: "USD", Remaining: 1, Threshold: 5, ObservedAt: time.Now()}}
	for _, tc := range []struct{ provider, url, success, failure string }{
		{"wecom", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test", `{"errcode":0}`, `{"errcode":1}`},
		{"dingtalk", "https://oapi.dingtalk.com/robot/send?access_token=test", `{"errcode":0}`, `{}`},
		{"feishu", "https://open.feishu.cn/open-apis/bot/v2/hook/test", `{"code":0}`, `{"code":1}`},
		{"webhook", "https://notify.example.com/hooks", ``, ``},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			channel := UpstreamBalanceNotificationChannel{Provider: tc.provider, urlCipher: "encrypted-" + tc.url, secretCipher: "encrypted-signing-secret"}
			response := tc.success
			balanceNotificationHTTPClient = &http.Client{Transport: balanceNoticeTestTransport(func(request *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				var payload map[string]any
				require.NoError(t, json.Unmarshal(body, &payload))
				require.NotContains(t, string(body), "signing-secret")
				if tc.provider == "dingtalk" {
					ts := request.URL.Query().Get("timestamp")
					require.NotEmpty(t, ts)
					require.Equal(t, signBalanceNotification("signing-secret", ts+"\n"+"signing-secret"), request.URL.Query().Get("sign"))
				}
				if tc.provider == "feishu" {
					ts, ok := payload["timestamp"].(string)
					require.True(t, ok)
					require.Equal(t, signBalanceNotification(ts+"\n"+"signing-secret", ""), payload["sign"])
				}
				if tc.provider == "webhook" {
					require.NotEmpty(t, request.Header.Get("X-Sub2API-Signature"))
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: http.Header{}}, nil
			})}
			require.NoError(t, s.sendBalanceNotification(context.Background(), channel, event))
			if tc.provider != "webhook" {
				response = tc.failure
				require.Error(t, s.sendBalanceNotification(context.Background(), channel, event))
			}
		})
	}
	for _, raw := range []string{"http://notify.example.com", "https://127.0.0.1/hook", "https://192.168.1.1/hook", "https://localhost/hook", "https://user:pass@example.com/hook", "https://example.com/#secret"} {
		require.Error(t, validateBalanceNotificationURL("webhook", raw))
	}
}

type balanceNoticeTestRepo struct {
	UpstreamBalanceNotificationRepository
	deliveries map[string]UpstreamBalanceNotificationDelivery
	status     string
	reserve    func()
}

func (r *balanceNoticeTestRepo) ReserveDestination(context.Context, string) error {
	if r.reserve != nil {
		r.reserve()
	}
	return nil
}

func (r *balanceNoticeTestRepo) SaveDelivery(_ context.Context, _ *UpstreamBalanceNotificationEvent, key string, delivery UpstreamBalanceNotificationDelivery) error {
	if r.deliveries == nil {
		r.deliveries = map[string]UpstreamBalanceNotificationDelivery{}
	}
	r.deliveries[key] = delivery
	return nil
}
func (r *balanceNoticeTestRepo) Complete(_ context.Context, _ *UpstreamBalanceNotificationEvent, status string) error {
	r.status = status
	return nil
}

func TestBalanceNotificationRetriesOnlyFailedChannelsAndDefersUnknown(t *testing.T) {
	now := time.Now().UTC()
	value := 1.0
	state := UpstreamBalanceState{UpstreamBalanceConfig: DefaultUpstreamBalanceConfig(), Snapshot: &UpstreamBalanceSnapshot{Status: "ok", ReceivedAt: probeTimePtr(now.Add(-time.Minute)), FreshUntil: probeTimePtr(now.Add(time.Hour)), Amounts: []UpstreamBalanceAmount{{Scope: "wallet", Currency: "USD", Remaining: &value}}}}
	state.Enabled = true
	account := &Account{ID: 1, Name: "channel", Type: AccountTypeAPIKey, Status: StatusActive, Extra: map[string]any{UpstreamBalanceProbeExtraKey: state}}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: account}}
	s := newUpstreamBillingProbeTestService(repo, nil, &upstreamBillingProbeSettingRepo{})
	s.SetNewAPIAuthorization(nil, newAPIEncryptorFake{}, true)
	notices := &balanceNoticeTestRepo{}
	s.balanceNotificationRepo = notices
	config, err := s.SetBalanceNotificationSettings(context.Background(), UpstreamBalanceNotificationSettings{Enabled: true, NotifyRecovery: true, Channels: []UpstreamBalanceNotificationChannel{{Provider: "webhook", Enabled: true, URL: "https://notify.example.com/first"}, {Provider: "webhook", Enabled: true, URL: "https://notify.example.com/second"}}})
	require.NoError(t, err)
	previous := balanceNotificationHTTPClient
	t.Cleanup(func() { balanceNotificationHTTPClient = previous })
	calls := map[string]int{}
	fail := true
	balanceNotificationHTTPClient = &http.Client{Transport: balanceNoticeTestTransport(func(request *http.Request) (*http.Response, error) {
		calls[request.URL.Path]++
		code := 200
		if request.URL.Path == "/second" && fail {
			code = 503
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	observation := balanceNotificationObservations(UpstreamBalanceNotificationAccount{ID: 1, Name: "channel", State: state}, 5, now)[0]
	event := &UpstreamBalanceNotificationEvent{UpstreamBalanceNotificationObservation: observation, Phase: "low"}
	require.NoError(t, s.deliverBalanceNotification(context.Background(), event))
	require.Equal(t, "failed", notices.status)
	fail = false
	require.NoError(t, s.deliverBalanceNotification(context.Background(), event))
	require.Equal(t, "sent", notices.status)
	require.Equal(t, 1, calls["/first"])
	require.Equal(t, 2, calls["/second"])
	require.Len(t, event.Deliveries, 2)
	require.True(t, config.Enabled)
	state.Snapshot.Status = "failed"
	account.Extra[UpstreamBalanceProbeExtraKey] = state
	require.NoError(t, s.deliverBalanceNotification(context.Background(), event))
	require.Equal(t, "pending", notices.status)
	require.Equal(t, 2, calls["/second"])
	state.Snapshot.Status = "ok"
	account.Extra[UpstreamBalanceProbeExtraKey] = state
	event.Deliveries = nil
	notices.deliveries = nil
	notices.reserve = func() {
		state.Snapshot.Status = "failed"
		account.Extra[UpstreamBalanceProbeExtraKey] = state
	}
	require.NoError(t, s.deliverBalanceNotification(context.Background(), event))
	require.Equal(t, "pending", notices.status)
	require.Empty(t, notices.deliveries)
	require.Equal(t, 1, calls["/first"])
}
