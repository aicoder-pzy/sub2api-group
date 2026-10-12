package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSchedulingNotificationsOnlyCommittedSwitches(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	key := service.GroupModelSchedulingKeyPrefix + "bW9kZWwtYQ"
	require.NoError(t, cache.SetSessionAccountID(ctx, 24, key, 118, 0))
	require.NoError(t, cache.SetSessionAccountID(ctx, 24, key, 118, 0))
	events, err := cache.SchedulingNotificationHistory(ctx, 50)
	require.NoError(t, err)
	require.Empty(t, events, "initial and unchanged bindings must not notify")
	_, revision, err := cache.GetSessionAccountState(ctx, 24, key)
	require.NoError(t, err)
	var wg sync.WaitGroup
	errors := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cache.CompareAndSwapSessionAccountID(ctx, 24, key, revision, 113)
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	events, err = cache.SchedulingNotificationHistory(ctx, 50)
	require.NoError(t, err)
	require.Len(t, events, 1, "concurrent confirmations must produce only one notification")
	event, err := cache.NextSchedulingNotification(ctx, time.Now().Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, int64(118), event.PreviousAccountID)
	require.Equal(t, int64(113), event.AccountID)
	require.Equal(t, int64(24), event.GroupID)
	require.Equal(t, "model-a", event.Model)
	next := time.Now().Add(time.Minute)
	event.Deliveries = map[string]service.UpstreamBalanceNotificationDelivery{"mail": {Status: "sent", Attempts: 1}}
	require.NoError(t, cache.SaveSchedulingNotification(ctx, event, &next))
	notDue, err := cache.NextSchedulingNotification(ctx, time.Now())
	require.NoError(t, err)
	require.Nil(t, notDue)
	due, err := cache.NextSchedulingNotification(ctx, next.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, "sent", due.Deliveries["mail"].Status)
	event.Status = "sent"
	require.NoError(t, cache.SaveSchedulingNotification(ctx, event, nil))
	notDue, err = cache.NextSchedulingNotification(ctx, next.Add(time.Hour))
	require.NoError(t, err)
	require.Nil(t, notDue)
	require.NoError(t, cache.SetSessionAccountID(ctx, 24, key, 118, 0))
	events, err = cache.SchedulingNotificationHistory(ctx, 50)
	require.NoError(t, err)
	require.Len(t, events, 2, "manual switches also notify")
}
