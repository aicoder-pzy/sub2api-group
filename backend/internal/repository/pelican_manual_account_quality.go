package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *accountRepository) SavePelicanAccountQuality(ctx context.Context, accountID int64, model string, assessment *service.PelicanAssessment, sampledAt time.Time, latencyMS int64) error {
	effort := ""
	if value := service.RequestedReasoningEffortFromContext(ctx); value != nil {
		effort = *value
	}
	_, err := r.sql.ExecContext(ctx, `INSERT INTO pelican_manual_account_quality
 (account_id, model_id, quality_verdict, quality_reason, sampled_at, latency_ms, effort)
 VALUES ($1, $2, $3, $4, $5, $6, $7)
 ON CONFLICT (account_id, model_id) DO UPDATE SET
 quality_verdict = EXCLUDED.quality_verdict, quality_reason = EXCLUDED.quality_reason,
 sampled_at = EXCLUDED.sampled_at, latency_ms = EXCLUDED.latency_ms, effort = EXCLUDED.effort
 WHERE pelican_manual_account_quality.sampled_at < EXCLUDED.sampled_at`,
		accountID, model, assessment.Quality, assessment.Reason, sampledAt, latencyMS, effort)
	return err
}
