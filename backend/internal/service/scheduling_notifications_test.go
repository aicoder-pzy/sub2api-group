package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type schedulingNoticeTestQueue struct {
	SchedulingNotificationQueue
	status string
	retry  bool
}

func (q *schedulingNoticeTestQueue) SaveSchedulingNotification(_ context.Context, event *SchedulingNotification, next *time.Time) error {
	q.status, q.retry = event.Status, next != nil
	return nil
}

func TestSchedulingNotificationTriggerAndRetries(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 24, Name: "Codex", AccountSchedulingMode: AccountSchedulingModeQualityPreferred})
	ctx = WithFastestFailoverRequestState(ctx)
	state, ok := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	require.True(t, ok)
	state.snapshots[groupModelSchedulingScope{24, "sol"}] = groupModelSchedulingSnapshot{accountID: 118}
	recordSchedulingSwitchFailure(ctx, 118, &UpstreamFailoverError{StatusCode: 500, ResponseBody: []byte("private upstream response")})
	event := NewSchedulingNotification(ctx, 24, "sol", GroupModelSchedulingTransition{PreviousAccountID: 118, AccountID: 113, Reason: "confirmed_failover", ChangedAt: time.Now()})
	require.Equal(t, "Codex", event.GroupName)
	require.Contains(t, event.Trigger, "500")
	settingsRepo := &upstreamBillingProbeSettingRepo{}
	s := newUpstreamBillingProbeTestService(nil, nil, settingsRepo)
	s.SetNewAPIAuthorization(nil, newAPIEncryptorFake{}, true)
	queue := &schedulingNoticeTestQueue{}
	s.schedulingNotifications = queue
	config, err := s.SetBalanceNotificationSettings(ctx, UpstreamBalanceNotificationSettings{NotifyAccountSwitch: true, Channels: []UpstreamBalanceNotificationChannel{{Provider: "webhook", Enabled: true, URL: "https://notify.example.com/first"}, {Provider: "webhook", Enabled: true, URL: "https://notify.example.com/second"}}})
	require.NoError(t, err)
	require.False(t, config.Enabled, "account switching does not depend on balance notifications")
	previous := balanceNotificationHTTPClient
	t.Cleanup(func() { balanceNotificationHTTPClient = previous })
	calls := map[string]int{}
	fail := true
	balanceNotificationHTTPClient = &http.Client{Transport: balanceNoticeTestTransport(func(request *http.Request) (*http.Response, error) {
		calls[request.URL.Path]++
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Equal(t, "account_switch", payload["event"])
		require.Equal(t, event.ID, payload["event_id"])
		require.Equal(t, float64(118), payload["previous_account_id"])
		require.Equal(t, float64(113), payload["account_id"])
		require.NotContains(t, string(body), "private upstream response")
		code := http.StatusOK
		if fail && request.URL.Path == "/second" {
			code = http.StatusServiceUnavailable
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	require.NoError(t, s.deliverSchedulingNotification(ctx, &event))
	require.True(t, queue.retry)
	require.Equal(t, "pending", queue.status)
	fail = false
	require.NoError(t, s.deliverSchedulingNotification(ctx, &event))
	require.Equal(t, "sent", queue.status)
	require.False(t, queue.retry)
	require.Equal(t, map[string]int{"/first": 1, "/second": 2}, calls)
	fail = true
	event.Deliveries = nil
	for i := 0; i < 3; i++ {
		require.NoError(t, s.deliverSchedulingNotification(ctx, &event))
	}
	require.Equal(t, "failed", queue.status)
	require.False(t, queue.retry, "exhausted destinations must leave the pending queue")
	require.Equal(t, map[string]int{"/first": 2, "/second": 5}, calls)
	event.Deliveries = nil
	event.ChangedAt = time.Now().Add(-25 * time.Hour)
	require.NoError(t, s.deliverSchedulingNotification(ctx, &event))
	require.Equal(t, "suppressed", queue.status)
	require.Equal(t, map[string]int{"/first": 2, "/second": 5}, calls)
	event.ChangedAt = time.Now()
	config.NotifyAccountSwitch = false
	_, err = s.SetBalanceNotificationSettings(ctx, *config)
	require.NoError(t, err)
	event.Deliveries = nil
	require.NoError(t, s.deliverSchedulingNotification(ctx, &event))
	require.Equal(t, "suppressed", queue.status)
	require.Equal(t, map[string]int{"/first": 2, "/second": 5}, calls)
}
