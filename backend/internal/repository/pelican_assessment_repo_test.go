package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPelicanAssessmentRepositoryMetadataOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewPelicanAssessmentRepository(db)
	ctx := context.Background()
	record := &service.PelicanAssessmentRecord{Hash: "artwork-hash", TaskID: "private-task", Deadline: time.Now().UTC(), Status: "running"}
	mock.ExpectQuery("SELECT status, remote_task_id").WithArgs(record.Hash).WillReturnError(sql.ErrNoRows)
	loaded, err := repo.Get(ctx, record.Hash)
	require.NoError(t, err)
	require.Nil(t, loaded)
	mock.ExpectExec(`(?s)INSERT INTO pelican_html_assessments.*WHERE pelican_html_assessments.status = 'failed'`).WithArgs(record.Hash, record.TaskID, record.Deadline).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Save(ctx, record))
	mock.ExpectQuery("SELECT status, remote_task_id").WithArgs(record.Hash).WillReturnRows(sqlmock.NewRows([]string{"status", "remote_task_id", "deadline_at", "assessment", "error_message"}).AddRow("succeeded", record.TaskID, record.Deadline, `{"quality":"unknown","reason":"cannot classify","source":"local"}`, ""))
	loaded, err = repo.Get(ctx, record.Hash)
	require.NoError(t, err)
	require.Equal(t, "unknown", loaded.Assessment.Quality)
	loaded.Status = "failed"
	loaded.Assessment = nil
	loaded.Error = "unknown"
	mock.ExpectExec(`(?s)UPDATE pelican_html_assessments.*remote_task_id = \$2 AND status = 'running'`).WithArgs(record.Hash, record.TaskID, "failed", nil, "unknown").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Finish(ctx, loaded))
	require.NoError(t, mock.ExpectationsWereMet())
}
