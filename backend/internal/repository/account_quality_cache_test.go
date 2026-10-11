package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestQualityChallengerRequiresSustainedAdvantageAndStableBinding(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache, ok := NewGatewayCache(client).(*gatewayCache)
	require.True(t, ok)
	ctx := context.Background()
	model := "model-a"
	binding := service.GroupModelSchedulingKeyPrefix + "bW9kZWwtYQ"
	require.NoError(t, cache.SetSessionAccountID(ctx, 6, binding, 1, 0))
	ready, err := cache.QualityChallengerReady(ctx, 6, model, 1, 2, true)
	require.NoError(t, err)
	require.False(t, ready, "minimum 30 minute hold")
	key := buildSessionKey(6, binding)
	transition, _ := json.Marshal(service.GroupModelSchedulingTransition{AccountID: 1, ChangedAt: time.Now().Add(-time.Hour)})
	require.NoError(t, client.Set(ctx, key+":transition", transition, 0).Err())
	keys := []string{key, key + ":transition", key + ":quality_challenger"}
	observe := func(active, candidate, now int64, better string) int {
		value, err := qualityChallengerScript.Run(ctx, client, keys, active, candidate, now, better).Int()
		require.NoError(t, err)
		return value
	}
	require.Zero(t, observe(1, 2, 1000, "1"))
	require.Zero(t, observe(1, 2, 1299, "1"))
	require.Equal(t, 1, observe(1, 2, 1300, "1"))
	require.Zero(t, observe(1, 3, 1301, "1"), "changing the challenger resets observations")
	require.Zero(t, observe(1, 3, 1400, "0"), "loss of advantage resets observations")
	require.Zero(t, observe(1, 3, 1700, "1"))
	require.Zero(t, observe(1, 3, 3000, "1"), "stale observations are discarded")
	require.Zero(t, observe(2, 3, 3300, "1"), "a superseded binding cannot promote")
}
