package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGetGroupModelAccountQuality(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("WITH failure_requests AS").
		WithArgs(int64(4), "gpt-5", since).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "successes", "failures", "latency_ms"}).
			AddRow(int64(12), int64(100), int64(2), 180.0).
			AddRow(int64(13), int64(0), int64(5), 0.0))

	repo := newUsageLogRepositoryWithSQL(nil, db)
	got, err := repo.GetGroupModelAccountQuality(context.Background(), 4, "gpt-5", since)
	if err != nil {
		t.Fatal(err)
	}
	if got[12].LatencyMS != 180 || got[12].Successes != 100 || got[12].Failures != 2 || got[13].Failures != 5 {
		t.Fatalf("unexpected quality samples: %+v", got)
	}
	cached, err := repo.GetGroupModelAccountQuality(context.Background(), 4, "gpt-5", since)
	if err != nil || cached[12] != got[12] {
		t.Fatalf("cache read = %+v, err = %v", cached, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
