//go:build integration

package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

type broadcastTestSettings struct{ SettingRepository }

func (*broadcastTestSettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{SettingKeySMTPHost: "smtp.invalid", SettingKeySMTPFrom: "sender@example.com"}, nil
}

func TestEmailBroadcastPersistentQueue(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("broadcast_test"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	migration, err := os.ReadFile("../../migrations/261_email_broadcasts.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = db.Exec(string(migration))
		require.NoError(t, err)
	}
	svc := NewEmailBroadcastService(db, NewEmailService(&broadcastTestSettings{}, nil))
	input := EmailBroadcastInput{ID: uuid.NewString(), Recipients: "12345,67890,11111,12345@qq.com", Subject: "通知", Content: "<script>plain</script>\n正文", IntervalSeconds: 2}
	// Retried and concurrent submissions create exactly one immutable job.
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := svc.Create(ctx, 1, input); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	d, err := svc.Detail(ctx, input.ID, "", 1)
	require.NoError(t, err)
	require.Equal(t, 3, d.Broadcast.Total)
	changed := input
	changed.Subject = "different"
	_, err = svc.Create(ctx, 1, changed)
	require.Error(t, err)
	_, err = svc.Create(ctx, 2, input)
	require.Error(t, err)
	bad := input
	bad.ID, bad.Recipients = uuid.NewString(), "invalid"
	_, err = svc.Create(ctx, 1, bad)
	require.Error(t, err)
	_, err = svc.Detail(ctx, input.ID, "pending|sending", 1)
	require.Error(t, err)

	var calls atomic.Int32
	started, finish := make(chan struct{}), make(chan struct{})
	svc.send = func(_ context.Context, _ *SMTPConfig, to, subject, body string) error {
		require.Equal(t, "12345@qq.com", to)
		require.Equal(t, input.Subject, subject)
		require.NotContains(t, body, "<script>")
		calls.Add(1)
		close(started)
		<-finish
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- svc.tick(ctx) }()
	<-started
	// A second worker sees the persisted claim and cannot send another message.
	require.NoError(t, svc.tick(ctx))
	require.EqualValues(t, 1, calls.Load())
	require.NoError(t, svc.Cancel(ctx, input.ID))
	close(finish)
	require.NoError(t, <-done)
	require.NoError(t, svc.tick(ctx))
	d, err = svc.Detail(ctx, input.ID, "", 1)
	require.NoError(t, err)
	require.Equal(t, "cancelled", d.Broadcast.Status)
	require.Equal(t, 1, d.Broadcast.Sent)
	require.Equal(t, 2, d.Broadcast.Cancelled)
	require.EqualValues(t, 1, calls.Load())

	// Recreate the service to verify durable state, throttling and interrupted-delivery recovery.
	restarted := NewEmailBroadcastService(db, svc.email)
	restarted.send = func(_ context.Context, _ *SMTPConfig, to, _, _ string) error {
		calls.Add(1)
		if to == "67890@qq.com" {
			return errors.New("provider rejected secret data")
		}
		return nil
	}
	input.ID = uuid.NewString()
	_, err = restarted.Create(ctx, 1, input)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE email_broadcast_recipients SET status='sending',updated_at=NOW()-INTERVAL '3 minutes' WHERE broadcast_id=$1 AND email='12345@qq.com'`, input.ID)
	require.NoError(t, err)
	require.NoError(t, restarted.tick(ctx)) // unknown first address, failure on second
	require.NoError(t, restarted.tick(ctx)) // interval prevents third delivery
	require.EqualValues(t, 2, calls.Load())
	_, err = db.Exec(`UPDATE email_broadcasts SET next_send_at=NOW()-INTERVAL '1 second' WHERE id=$1`, input.ID)
	require.NoError(t, err)
	require.NoError(t, restarted.tick(ctx))
	require.NoError(t, restarted.tick(ctx))
	d, err = restarted.Detail(ctx, input.ID, "", 1)
	require.NoError(t, err)
	require.Equal(t, "completed", d.Broadcast.Status)
	require.Equal(t, 1, d.Broadcast.Unknown)
	require.Equal(t, 1, d.Broadcast.Failed)
	require.Equal(t, 1, d.Broadcast.Sent)
	require.EqualValues(t, 3, calls.Load())
	require.NotContains(t, d.Recipients[1].Error, "secret")
	for range 3 {
		require.NoError(t, restarted.tick(ctx))
	}
	require.EqualValues(t, 3, calls.Load()) // no implicit retries, including unknown outcomes
	filtered, err := restarted.Detail(ctx, input.ID, "failed", 1)
	require.NoError(t, err)
	require.Equal(t, 1, filtered.Total)
	items, err := restarted.List(ctx, 1)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Empty(t, items[0].Content)
	require.NoError(t, restarted.Test(ctx, "11111", "测试", "test"))
	require.EqualValues(t, 4, calls.Load())
	require.Error(t, restarted.Test(ctx, "11111,22222", "test", "test"))
}
