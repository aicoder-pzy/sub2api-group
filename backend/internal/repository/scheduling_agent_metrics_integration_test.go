//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulingAgentActivityUsesOnlyUserVisibleStreamFailures(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.ExecContext(ctx, `
CREATE TEMP TABLE ops_error_logs (group_id bigint, api_key_id bigint, account_id bigint, created_at timestamptz, model text, requested_model text, request_id text, client_request_id text, request_type int, stream bool, status_code int, upstream_status_code int, error_phase text, is_count_tokens bool, error_owner text, is_business_limited bool);
CREATE TEMP TABLE usage_logs (group_id bigint, account_id bigint, created_at timestamptz, model text, requested_model text, request_type int, stream bool, inbound_endpoint text, reasoning_effort text, duration_ms int);
INSERT INTO usage_logs VALUES (24,1,NOW(),'success',null,2,true,'/v1/responses','xhigh',4000), (24,1,NOW(),'success',null,1,false,'/v1/responses','medium',20);
INSERT INTO ops_error_logs VALUES
 (24,1,1,NOW(),'sync',NULL,'sync',NULL,1,true,502,502,'upstream',false,'provider',false),
 (24,1,1,NOW(),'recovered',NULL,'recovered',NULL,2,true,200,502,'upstream',false,'provider',false),
 (24,1,1,NOW(),'request',NULL,'request',NULL,2,true,400,400,'request',false,'client',true),
 (24,1,NULL,NOW(),'user-limit',NULL,'limit',NULL,2,true,429,NULL,'concurrency',false,'platform',true),
 (24,1,1,NOW(),'sse',NULL,'sse',NULL,2,false,502,502,'upstream',false,'provider',false),
 (24,1,1,NOW(),'sse',NULL,'sse',NULL,2,false,502,502,'upstream',false,'provider',false),
 (24,1,1,NOW(),'ws',NULL,'ws',NULL,3,false,502,502,'upstream',false,'provider',false),
 (24,1,1,NOW(),'legacy',NULL,'legacy',NULL,NULL,true,502,502,'upstream',false,'provider',false),
 (24,1,1,NOW(),'queue',NULL,'queue',NULL,2,true,429,NULL,'concurrency',false,'platform',true),
 (24,1,1,NOW(),'gateway',NULL,'gw',NULL,2,true,504,NULL,'routing',false,'platform',false);`)
	require.NoError(t, err)
	repo := &usageLogRepository{sql: tx}
	models := []string{"sync", "recovered", "request", "user-limit", "success", "sse", "ws", "legacy", "queue", "gateway"}
	activity, err := repo.GetSchedulingAgentActivity(ctx, 24, models)
	require.NoError(t, err)
	byModel := make(map[string]service.SchedulingAgentActivity)
	var got []string
	for _, row := range activity {
		got = append(got, row.Model)
		byModel[row.Model] = row
	}
	require.ElementsMatch(t, []string{"sse", "ws", "legacy", "queue", "gateway"}, got)
	require.True(t, byModel["sse"].FailureSince.IsZero(), "duplicate rows from one request do not form a burst")
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_error_logs VALUES
        (24,1,1,NOW(),'sse',NULL,'another',NULL,2,false,502,502,'upstream',false,'provider',false)`)
	require.NoError(t, err)
	activity, err = repo.GetSchedulingAgentActivity(ctx, 24, []string{"sse"})
	require.NoError(t, err)
	require.Len(t, activity, 1)
	require.Equal(t, 2, activity[0].Failures)
	require.False(t, activity[0].FailureSince.IsZero())
	traffic, err := repo.GetSchedulingAgentTraffic(ctx, 24, "success")
	require.NoError(t, err)
	require.Len(t, traffic, 1)
	require.True(t, traffic[0].Stream)
	require.Equal(t, "xhigh", traffic[0].Effort)
}
