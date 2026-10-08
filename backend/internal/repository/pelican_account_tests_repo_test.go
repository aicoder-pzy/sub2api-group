package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPelicanAccountLeaseChecksVersionAndOtherAccountRuns(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewScheduledTestPlanRepository(db)
	now := time.Now().Truncate(time.Microsecond)
	next := now.Add(time.Hour)
	until := now.Add(15 * time.Minute)
	due := now.Add(-time.Minute)
	plan := &service.ScheduledTestPlan{ID: 1, AccountID: 11, UpdatedAt: now.Add(-time.Hour), NextRunAt: &due}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WithArgs(plan.AccountID).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectExec(`(?s)UPDATE scheduled_test_plans.*enabled=true.*updated_at=\$5.*other.running_until>\$2`).WithArgs(plan.ID, now, until, next, plan.UpdatedAt, plan.NextRunAt).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	ok, err := repo.ClaimPelican(context.Background(), plan, now, until, next)
	require.NoError(t, err)
	require.True(t, ok)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WithArgs(plan.AccountID).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(false))
	mock.ExpectRollback()
	ok, err = repo.ClaimPelican(context.Background(), plan, now, until, next)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}
