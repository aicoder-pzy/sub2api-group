package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSchedulingPoolRecoveryCoordinatesTrialsAndRejectsLateOutcomes(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	now := time.Now()
	mr.SetTime(now)
	call := func(mode, owner string) int64 {
		n, err := cache.SchedulingPoolRecovery(ctx, "recover:pool-a:astra", mode, owner)
		require.NoError(t, err)
		return n
	}
	require.Equal(t, int64(2), call("claim", "first"))
	require.Zero(t, call("claim", "competing-replica"))
	call("success", "first")
	call("success", "first") // A duplicate completion cannot count twice.
	require.Zero(t, call("claim", "second"))
	mr.SetTime(now.Add(time.Minute))
	require.Equal(t, int64(2), call("claim", "second"))
	call("failure", "second") // Failed trial resets the successful streak.
	for i := 2; i <= 4; i++ {
		mr.SetTime(now.Add(time.Duration(i) * time.Minute))
		require.Equal(t, int64(2), call("claim", "trial"))
		call("success", "stale-owner")
		call("success", "trial")
	}
	require.Equal(t, int64(1), call("claim", "normal-traffic"))
	n, err := cache.SchedulingPoolRecovery(ctx, "recover:pool-a:sol", "claim", "different-model")
	require.NoError(t, err)
	require.Equal(t, int64(2), n)
}
