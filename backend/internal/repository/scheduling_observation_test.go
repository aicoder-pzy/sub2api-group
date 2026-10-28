package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSmartChannelObservationBudgetAndUnknownExpiry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	r := newUsageLogRepositoryWithSQL(nil, db)
	ctx := context.WithValue(context.Background(), ctxkey.Group, &service.Group{ID: 7, AccountSchedulingMode: service.AccountSchedulingModeSmartChannel})
	ctx = context.WithValue(ctx, ctxkey.RequestedPublicModel, "public")
	ctx = context.WithValue(ctx, ctxkey.InboundEndpoint, "/v1/responses")
	mock.ExpectQuery("SELECT COALESCE").WillDelayFor(100 * time.Millisecond).WillReturnError(errors.New("offline"))
	for range 10 {
		got, err := r.GetSmartChannelAccountQuality(ctx, 7, "routing", "medium")
		require.NoError(t, err)
		require.Nil(t, got, "a cold scope stays unknown and never invents zero latency")
	}
	r.observation.mu.Lock()
	require.Equal(t, 1, r.observation.active, "identical reads share one refresh")
	entry := r.observation.entries[observationKey(ctx, 7, "routing", "medium")]
	r.observation.mu.Unlock()
	_, err = r.GetSmartChannelAccountQuality(ctx, 8, "routing", "medium")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		r.observation.mu.Lock()
		defer r.observation.mu.Unlock()
		return r.observation.active == 0
	}, time.Second, time.Millisecond)
	require.NoError(t, mock.ExpectationsWereMet(), "global start budget prevents a second immediate query")
	r.observation.mu.Lock()
	entry.samples, entry.at = map[int64]service.GroupModelAccountQuality{1: {Successes: 100, LatencyMS: 1000}}, time.Now()
	r.observation.next = time.Now().Add(time.Hour)
	r.observation.mu.Unlock()
	got, err := r.GetSmartChannelAccountQuality(ctx, 7, "routing", "medium")
	require.NoError(t, err)
	require.EqualValues(t, 100, got[1].Successes)
	r.observation.mu.Lock()
	entry.at = time.Now().Add(-61 * time.Second)
	r.observation.mu.Unlock()
	got, err = r.GetSmartChannelAccountQuality(ctx, 7, "routing", "medium")
	require.NoError(t, err)
	require.Nil(t, got, "expired observations cannot be renewed by reads")
}
