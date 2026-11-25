package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPelicanTrafficAccountsReadExactPlanAndModel(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &pelicanGroupTestRepository{db: db}
	now := time.Date(2026, 11, 25, 4, 0, 0, 0, time.UTC)
	plan := &service.PelicanGroupTestPlan{ID: 22, GroupID: 45, ModelID: "gpt-6.2-sol"}
	mock.ExpectQuery(pelicanTrafficAccountsQuery).WithArgs(int64(45), "gpt-6.2-sol", now, int64(22)).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "name", "seen_at"}).AddRow(165, "actual", now))
	accounts, err := repo.ListTrafficAccounts(context.Background(), plan, now)
	require.NoError(t, err)
	require.Equal(t, []service.PelicanTrafficAccount{{AccountID: 165, AccountName: "actual", LastSeenAt: now}}, accounts)
	require.NoError(t, mock.ExpectationsWereMet())
}
