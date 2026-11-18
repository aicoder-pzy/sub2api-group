package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGetGroupModelAccountQuality(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("(?s)WITH failure_requests AS.*NOT \\$4 OR successes.last_success_at IS NULL OR failures.last_failure_at > successes.last_success_at").
		WithArgs(int64(4), "gpt-5", since, false).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "successes", "failures", "latency_ms", "recent_failures", "last_success_at", "last_failure_at", "intelligence_verdict", "intelligence_reason", "intelligence_at"}).
			AddRow(int64(12), int64(100), int64(2), 180.0, int64(1), since, since, "degraded", "drawing", since).
			AddRow(int64(13), int64(0), int64(5), 0.0, int64(5), nil, since, "", "", nil))

	repo := newUsageLogRepositoryWithSQL(nil, db)
	got, err := repo.GetGroupModelAccountQuality(context.Background(), 4, "gpt-5", since)
	require.NoError(t, err)
	if got[12].LatencyMS != 180 || got[12].Successes != 100 || got[12].Failures != 2 || got[13].RecentFailures != 5 || got[12].LastSuccessAt == nil {
		t.Fatalf("unexpected quality samples: %+v", got)
	}
	if got[12].IntelligenceVerdict != "degraded" || got[12].IntelligenceAt == nil || got[13].IntelligenceVerdict != "" {
		t.Fatalf("unexpected intelligence evidence: %+v", got)
	}
	cached, err := repo.GetGroupModelAccountQuality(context.Background(), 4, "gpt-5", since)
	if err != nil || cached[12] != got[12] {
		t.Fatalf("cache read = %+v, err = %v", cached, err)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSmartChannelRecentFailuresResetAfterSuccessfulRequest(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	since := time.Now().Add(-24 * time.Hour)
	query := "(?s)WITH failure_requests AS.*NOT \\$4 OR successes.last_success_at IS NULL OR failures.last_failure_at > successes.last_success_at"
	mock.ExpectQuery(query).
		WithArgs(int64(4), "routing-model", since, false).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "successes", "failures", "latency_ms", "recent_failures", "last_success_at", "last_failure_at", "intelligence_verdict", "intelligence_reason", "intelligence_at"}))
	mock.ExpectQuery(query).
		WithArgs(int64(4), "routing-model", since, true, "routing-model", "").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "successes", "failures", "latency_ms", "recent_failures", "last_success_at", "last_failure_at", "intelligence_verdict", "intelligence_reason", "intelligence_at"}))

	repo := newUsageLogRepositoryWithSQL(nil, db)
	_, err = repo.GetGroupModelAccountQuality(context.Background(), 4, "routing-model", since)
	require.NoError(t, err)

	ctx := context.Background()
	ctx = context.WithValue(ctx, ctxkey.Group, &service.Group{ID: 4, AccountSchedulingMode: service.AccountSchedulingModeSmartChannel})
	_, err = repo.GetGroupModelAccountQuality(ctx, 4, "routing-model", since)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
