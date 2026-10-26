package repository

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestModelAutoRedisTrialsAreAtomicAndIsolated(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	counts := map[int64]int{}
	for i := 0; i < 40; i++ {
		id, fresh, err := cache.AssignModelAutoSession(ctx, 6, "m", strconv.Itoa(i), 1, []int64{1, 2, 3}, []int64{2, 3}, 0)
		require.NoError(t, err)
		require.True(t, fresh)
		counts[id]++
		for repeat := 0; repeat < 2; repeat++ {
			same, fresh, err := cache.AssignModelAutoSession(ctx, 6, "m", strconv.Itoa(i), 3, []int64{1, 2, 3}, []int64{1, 2}, 0)
			require.NoError(t, err)
			require.False(t, fresh)
			require.Equal(t, id, same)
		}
	}
	require.Equal(t, map[int64]int{1: 36, 2: 2, 3: 2}, counts)
	for _, scope := range []struct {
		group int64
		model string
	}{{7, "m"}, {6, "other"}} {
		id, fresh, err := cache.AssignModelAutoSession(ctx, scope.group, scope.model, "0", 3, []int64{1, 2, 3}, nil, 0)
		require.NoError(t, err)
		require.True(t, fresh)
		require.EqualValues(t, 3, id)
	}
	id, fresh, err := cache.AssignModelAutoSession(ctx, 6, "m", "0", 2, []int64{2, 3}, nil, 0)
	require.NoError(t, err)
	require.True(t, fresh)
	require.EqualValues(t, 2, id, "ineligible original account cannot be kept by sticky session")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := cache.AssignModelAutoSession(ctx, 8, "m", "same", 1, []int64{1, 2, 3}, []int64{2, 3}, 0)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	sequence, err := server.Get("model_auto:session:8:bQ:counter")
	require.NoError(t, err)
	require.Equal(t, "1", sequence)
}

func TestModelAutoRedisReviewNeedsThreeSpacedEvaluationsAndHold(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	key := service.GroupModelSchedulingKeyPrefix + "bQ"
	require.NoError(t, cache.SetSessionAccountID(ctx, 6, key, 1, 0))
	_, revision, err := cache.GetSessionAccountState(ctx, 6, key)
	require.NoError(t, err)
	input := service.ModelAutoReviewInput{PrimaryID: 1, Revision: revision, CandidateID: 2, ChangedAt: now, Now: now, Reason: "latency"}
	read := func() *service.ModelAutoReview {
		state, err := cache.ReviewModelAutoCandidate(ctx, 6, "m", "medium", input)
		require.NoError(t, err)
		require.NotNil(t, state)
		return state
	}
	require.Equal(t, 1, read().Evaluations)
	for i := 0; i < 20; i++ {
		require.Equal(t, 1, read().Evaluations)
	}
	input.Now = now.Add(5 * time.Minute)
	require.Equal(t, 2, read().Evaluations)
	input.Now = now.Add(10 * time.Minute)
	state := read()
	require.Equal(t, 3, state.Evaluations)
	require.False(t, state.Ready)
	for _, minute := range []int{15, 20, 25, 30} {
		input.Now = now.Add(time.Duration(minute) * time.Minute)
		state = read()
	}
	require.True(t, state.Ready)
	stored, err := cache.GetModelAutoReview(ctx, 6, "m", "medium")
	require.NoError(t, err)
	require.Equal(t, state, stored)
	missing, err := cache.GetModelAutoReview(ctx, 6, "m", "xhigh")
	require.NoError(t, err)
	require.Nil(t, missing)
	input.CandidateID = 0
	require.False(t, read().Ready)
	input.CandidateID = 2
	require.Equal(t, 1, read().Evaluations)
	input.Now = now.Add(60 * time.Minute)
	require.Equal(t, 1, read().Evaluations, "stale votes are not consecutive")
	input.CandidateID = 3
	require.Equal(t, 1, read().Evaluations)
	require.NoError(t, cache.SetSessionAccountID(ctx, 6, key, 2, 0))
	stale, err := cache.ReviewModelAutoCandidate(ctx, 6, "m", "medium", input)
	require.NoError(t, err)
	require.Nil(t, stale, "old primary revision must not authorize promotion")
}

func TestSmartChannelRedisTrialsRespectSharedCooldownAndSessionLimit(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	assign := func(group int64, model, session string, backups []int64) (int64, bool) {
		id, fresh, err := cache.AssignModelAutoSession(ctx, group, model, session, 1, []int64{1, 2, 3}, backups, 30*time.Minute)
		require.NoError(t, err)
		return id, fresh
	}
	id, fresh := assign(6, "m", "first", []int64{2, 3})
	require.True(t, fresh)
	require.EqualValues(t, 2, id, "low traffic can try the first qualified backup without a paid probe")
	server.FastForward(30 * time.Minute)
	for i := 0; i < 20; i++ {
		id, fresh = assign(6, "m", "first", []int64{2, 3})
		require.EqualValues(t, 2, id)
		require.False(t, fresh, "retries and old sessions cannot advance the trial budget")
	}
	for i := 0; i < 9; i++ {
		id, _ = assign(6, "m", "new-"+strconv.Itoa(i), []int64{2, 3})
		require.EqualValues(t, 1, id, "elapsed time alone cannot admit another trial")
	}
	id, _ = assign(6, "m", "next-trial", []int64{2, 3})
	require.EqualValues(t, 3, id, "rotate only admitted trials")
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, _, err := cache.AssignModelAutoSession(ctx, 6, "m", "parallel-"+strconv.Itoa(i), 1, []int64{1, 2, 3}, []int64{2, 3}, 30*time.Minute)
			if err != nil || id != 1 {
				t.Errorf("cooldown assignment: id=%d error=%v", id, err)
			}
		}(i)
	}
	wg.Wait()
	server.FastForward(30 * time.Minute)
	id, _ = assign(6, "m", "after-cooldown", []int64{2, 3})
	require.EqualValues(t, 2, id)
	for _, scope := range []struct {
		group int64
		model string
	}{{7, "m"}, {6, "other"}} {
		id, _ = assign(scope.group, scope.model, "isolated", []int64{3})
		require.EqualValues(t, 3, id, "different groups and models have independent limits")
	}
	server.FastForward(30 * time.Minute)
	// Expiry makes a session fresh, but cannot bypass the ten-session limit.
	server.FastForward(time.Hour)
	id, fresh = assign(6, "m", "after-cooldown", []int64{2, 3})
	require.True(t, fresh)
	require.EqualValues(t, 1, id)
	_, err := server.Get("model_auto:session:6:bQ:trial-count")
	require.NoError(t, err)
}
