package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type pelicanAssessmentRepository struct{ db *sql.DB }

func NewPelicanAssessmentRepository(db *sql.DB) service.PelicanAssessmentRepository {
	return &pelicanAssessmentRepository{db: db}
}

func (r *pelicanAssessmentRepository) Get(ctx context.Context, hash string) (*service.PelicanAssessmentRecord, error) {
	record := &service.PelicanAssessmentRecord{Hash: hash}
	var assessment []byte
	err := r.db.QueryRowContext(ctx, `SELECT status, remote_task_id, deadline_at, assessment, error_message
 FROM pelican_html_assessments WHERE html_hash = $1`, hash).Scan(&record.Status, &record.TaskID, &record.Deadline, &assessment, &record.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(assessment) > 0 {
		if err := json.Unmarshal(assessment, &record.Assessment); err != nil {
			return nil, err
		}
	}
	return record, nil
}

func (r *pelicanAssessmentRepository) Save(ctx context.Context, record *service.PelicanAssessmentRecord) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO pelican_html_assessments
 (html_hash, status, remote_task_id, deadline_at) VALUES ($1, 'running', $2, $3)
 ON CONFLICT (html_hash) DO UPDATE SET status = 'running', remote_task_id = EXCLUDED.remote_task_id,
 deadline_at = EXCLUDED.deadline_at, assessment = NULL, error_message = '', updated_at = NOW()
 WHERE pelican_html_assessments.status = 'failed'`, record.Hash, record.TaskID, record.Deadline)
	return err
}

func (r *pelicanAssessmentRepository) Finish(ctx context.Context, record *service.PelicanAssessmentRecord) error {
	var assessment any
	if record.Assessment != nil {
		data, err := json.Marshal(record.Assessment)
		if err != nil {
			return err
		}
		assessment = string(data)
	}
	_, err := r.db.ExecContext(ctx, `UPDATE pelican_html_assessments SET status = $3, assessment = $4,
 error_message = $5, updated_at = NOW() WHERE html_hash = $1 AND remote_task_id = $2 AND status = 'running'`,
		record.Hash, record.TaskID, record.Status, assessment, record.Error)
	return err
}
