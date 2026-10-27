package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestPelicanSwitchClaimDeduplicatesAcrossReplicasAndBoundsSpend(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	key := service.GroupModelSchedulingKeyPrefix + "bW9kZWwtYQ"
	now := time.Now().UTC()
	require.NoError(t, cache.SetSessionAccountID(ctx, 24, key, 1, 0))
	initial, err := cache.GetGroupModelSchedulingTransition(ctx, 24, "model-a")
	require.NoError(t, err)
	claim := func(c *gatewayCache, group int64, public, effort string, change *service.GroupModelSchedulingTransition, at time.Time, limit int) bool {
		t.Helper()
		ok, err := c.ClaimPelicanSwitchTest(ctx, group, public, "model-a", effort, change, at, limit)
		require.NoError(t, err)
		return ok
	}
	require.False(t, claim(cache, 24, "model-a", "medium", initial, now.Add(time.Second), 10), "initial selection is not a switch")
	require.NoError(t, cache.SetSessionAccountID(ctx, 24, key, 2, 0))
	change, err := cache.GetGroupModelSchedulingTransition(ctx, 24, "model-a")
	require.NoError(t, err)
	var wins atomic.Int64
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			ok, err := (&gatewayCache{rdb: client}).ClaimPelicanSwitchTest(ctx, 24, "model-a", "model-a", "medium", change, now.Add(time.Second), 2)
			require.NoError(t, err)
			if ok {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, wins.Load())
	require.True(t, claim(cache, 24, "model-a", "high", change, now.Add(time.Second), 2), "effort scopes are independent")
	require.False(t, claim(cache, 24, "model-a", "xhigh", change, now.Add(time.Second), 2), "hourly budget is shared across efforts")
	require.False(t, claim(cache, 24, "model-a", "medium", change, now.Add(30*time.Minute), 10), "failed tests keep the one-hour reservation")
	require.False(t, claim(cache, 25, "model-a", "medium", change, now.Add(time.Second), 10), "a different group's missing binding must not be tested")
	require.True(t, claim(cache, 24, "other-public-model", "medium", change, now.Add(time.Second), 2), "public model budgets are isolated")
	require.NoError(t, cache.SetSessionAccountID(ctx, 24, key, 3, 0))
	require.False(t, claim(cache, 24, "new-scope", "medium", change, now.Add(time.Second), 10), "stale switch must not authorize a test")
	require.NoError(t, cache.SetSessionAccountID(ctx, 24, key, 2, 0))
	back, err := cache.GetGroupModelSchedulingTransition(ctx, 24, "model-a")
	require.NoError(t, err)
	require.False(t, claim(cache, 24, "model-a", "medium", back, now.Add(30*time.Minute), 10), "switching away and back must reuse the cooldown")
	mr.FastForward(time.Hour)
	require.True(t, claim(cache, 24, "model-a", "medium", back, back.ChangedAt.Add(59*time.Minute), 2), "expired cooldown permits a newer switch")
	require.False(t, claim(cache, 24, "old-switch", "medium", back, back.ChangedAt.Add(time.Hour), 10), "old transitions must never become periodic paid probes")
}
