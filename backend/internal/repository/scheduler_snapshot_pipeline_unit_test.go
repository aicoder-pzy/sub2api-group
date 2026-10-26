//go:build unit

package repository

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type snapshotReadHook struct {
	direct    int
	pipelines [][]redis.Cmder
}

func (h *snapshotReadHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h *snapshotReadHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { h.direct++; return next(ctx, cmd) }
}
func (h *snapshotReadHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipelines = append(h.pipelines, cmds)
		return next(ctx, cmds)
	}
}

func TestSchedulerSnapshotMemoKeepsFreshLastUsedAndDeletion(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	cache.mgetChunkSize = 2
	bucket := service.SchedulerBucket{GroupID: 91, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	accounts := []service.Account{{ID: 801}, {ID: 802}, {ID: 803}}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, accounts))
	latest := time.Unix(1700000000, 0).UTC()
	require.NoError(t, cache.UpdateLastUsed(ctx, map[int64]time.Time{803: latest}))
	hook := &snapshotReadHook{}
	cache.rdb.AddHook(hook)
	result, hit, err := cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Len(t, result, 3)
	for i, account := range result {
		require.Equal(t, accounts[i].ID, account.ID)
	}
	require.Equal(t, latest, *result[2].LastUsedAt)
	require.Equal(t, 6, hook.direct) // tags, membership, two metadata chunks, two LRU chunks
	require.Empty(t, hook.pipelines)
	// Side-key changes must be visible without publishing a new bucket version.
	newer := latest.Add(time.Minute)
	require.NoError(t, cache.UpdateLastUsed(ctx, map[int64]time.Time{803: newer}))
	hook.direct = 0
	result, hit, err = cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, newer, *result[2].LastUsedAt)
	require.Equal(t, 3, hook.direct) // memo hit reads tags and fresh LRU only
	require.NoError(t, cache.DeleteAccount(ctx, 802))
	_, hit, err = cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.False(t, hit) // missing metadata cannot return a partial candidate pool
}

func TestSchedulerSnapshotPipelineControlErrors(t *testing.T) {
	for _, kind := range []string{"missing_ready", "not_ready", "missing_active", "wrong_ready", "wrong_active", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			cache := newSchedulerCacheUnit(t)
			ctx := context.Background()
			bucket := service.SchedulerBucket{GroupID: 92, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
			ready := schedulerBucketKey(schedulerReadyPrefix, bucket)
			active := schedulerBucketKey(schedulerActivePrefix, bucket)
			switch kind {
			case "missing_ready":
				require.NoError(t, cache.rdb.LPush(ctx, active, "bad").Err())
			case "not_ready":
				require.NoError(t, cache.rdb.Set(ctx, ready, "0", 0).Err())
				require.NoError(t, cache.rdb.LPush(ctx, active, "bad").Err())
			case "missing_active", "wrong_active":
				require.NoError(t, cache.rdb.Set(ctx, ready, "1", 0).Err())
				if kind == "wrong_active" {
					require.NoError(t, cache.rdb.LPush(ctx, active, "bad").Err())
				}
			case "wrong_ready":
				require.NoError(t, cache.rdb.LPush(ctx, ready, "bad").Err())
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			_, hit, err := cache.GetSnapshot(ctx, bucket)
			require.False(t, hit)
			// MGET treats non-string control values as missing: fail closed.
			if kind == "cancelled" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
