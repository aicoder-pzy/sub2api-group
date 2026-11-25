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
	_, err = tx.ExecContext(ctx, `ALTER TABLE ops_error_logs ADD COLUMN upstream_latency_ms bigint, ADD COLUMN response_latency_ms bigint`)
	require.NoError(t, err)
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
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_error_logs (group_id,api_key_id,account_id,created_at,model,request_id,request_type,stream,status_code,upstream_status_code,error_phase,is_count_tokens,error_owner,is_business_limited,upstream_latency_ms) VALUES
 (24,1,1,NOW(),'rescued','r1',2,true,200,504,'upstream',false,'provider',false,25000),
 (24,1,1,NOW(),'rescued','r1',2,true,200,504,'upstream',false,'provider',false,25000),
 (24,1,1,NOW(),'sync-rescued','s1',1,false,200,504,'upstream',false,'provider',false,25000),
 (24,1,1,NOW(),'sync-rescued','s2',1,false,200,504,'upstream',false,'provider',false,25000),
 (24,1,1,NOW(),'quick-rescued','q1',2,true,200,502,'upstream',false,'provider',false,1000),
 (24,1,1,NOW(),'quick-rescued','q2',2,true,200,502,'upstream',false,'provider',false,1000)`)
	require.NoError(t, err)
	activity, err = repo.GetSchedulingAgentActivity(ctx, 24, []string{"rescued", "sync-rescued", "quick-rescued"})
	require.NoError(t, err)
	require.Empty(t, activity, "one slow recovery or duplicate rows must not wake AI")
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_error_logs (group_id,api_key_id,account_id,created_at,model,request_id,request_type,stream,status_code,upstream_status_code,error_phase,is_count_tokens,error_owner,is_business_limited,upstream_latency_ms) VALUES
 (24,1,1,NOW(),'rescued','r2',3,false,200,504,'upstream',false,'provider',false,25000)`)
	require.NoError(t, err)
	activity, err = repo.GetSchedulingAgentActivity(ctx, 24, []string{"rescued", "sync-rescued", "quick-rescued"})
	require.NoError(t, err)
	require.Len(t, activity, 1)
	require.Equal(t, "rescued", activity[0].Model)
}

func TestSchedulingAgentRequestTracesJoinRecoveryWithoutCrossingScope(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.ExecContext(ctx, `
CREATE TEMP TABLE ops_error_logs (id bigserial, group_id bigint, api_key_id bigint, account_id bigint, created_at timestamptz, model text, requested_model text, request_id text, client_request_id text, request_type int, stream bool, status_code int, upstream_status_code int, error_phase text, is_count_tokens bool, error_owner text, is_business_limited bool, upstream_errors jsonb);
CREATE TEMP TABLE usage_logs (group_id bigint, api_key_id bigint, account_id bigint, created_at timestamptz, model text, requested_model text, request_id text, request_type int, stream bool, first_token_ms bigint);
INSERT INTO ops_error_logs (group_id,api_key_id,account_id,created_at,model,requested_model,request_id,client_request_id,request_type,stream,status_code,upstream_status_code,error_phase,is_count_tokens,error_owner,is_business_limited,upstream_errors) VALUES
 (24,1,4,NOW(),'mapped','gpt-6.2','server-recovered','from-client',2,true,200,502,'upstream',false,'provider',false,'[{"account_id":3,"upstream_status_code":429,"scope":"account","dropped_earlier_attempts":1},{"account_id":4,"proxy_id":7,"upstream_status_code":502,"kind":"failover","message":"hidden fixture"}]'),
 (24,1,4,NOW()-INTERVAL '1 second','gpt-6.2',NULL,'server-local',NULL,3,false,200,502,'upstream',false,'provider',false,'null'),
 (24,1,4,NOW()-INTERVAL '2 seconds','gpt-6.2',NULL,'server-failed',NULL,2,true,502,502,'upstream',false,'provider',false,'[]'),
 (24,1,4,NOW()-INTERVAL '3 seconds','gpt-6.2',NULL,'server-unmatched',NULL,2,true,200,502,'upstream',false,'provider',false,NULL),
 (24,1,4,NOW(),'gpt-6.2',NULL,'sync',NULL,1,true,502,502,'upstream',false,'provider',false,NULL),
 (24,1,4,NOW(),'gpt-6.2',NULL,'probe',NULL,2,true,502,502,'upstream',true,'provider',false,NULL),
 (25,1,4,NOW(),'gpt-6.2',NULL,'another-group',NULL,2,true,502,502,'upstream',false,'provider',false,NULL),
 (24,1,4,NOW(),'gpt-6.1',NULL,'another-model',NULL,2,true,502,502,'upstream',false,'provider',false,NULL);
INSERT INTO usage_logs VALUES
 (24,1,5,NOW(),'mapped','gpt-6.2','client:from-client',2,true,1200),
 (24,1,6,NOW(),'gpt-6.2',NULL,'local:server-local',3,false,2100),
 (24,2,88,NOW(),'gpt-6.2',NULL,'local:server-unmatched',2,true,10),
 (25,1,88,NOW(),'gpt-6.2',NULL,'local:server-unmatched',2,true,10),
 (24,1,88,NOW(),'gpt-6.1',NULL,'local:server-unmatched',2,true,10),
 (24,1,88,NOW(),'gpt-6.2',NULL,'local:server-failed',2,true,10);`)
	require.NoError(t, err)
	traces, err := (&usageLogRepository{sql: tx}).GetSchedulingAgentRequestTraces(ctx, 24, "gpt-6.2")
	require.NoError(t, err)
	require.Len(t, traces, 4)
	require.Equal(t, 200, traces[0].ClientStatus)
	require.Equal(t, int64(4), traces[0].LastFailedAccount)
	require.NotNil(t, traces[0].ServedAccount)
	require.Equal(t, int64(5), *traces[0].ServedAccount)
	require.Equal(t, int64(1200), *traces[0].ServingFirstMs)
	require.Len(t, traces[0].Attempts, 2)
	require.Equal(t, int64(3), traces[0].Attempts[0].AccountID)
	require.Equal(t, int64(7), *traces[0].Attempts[1].ProxyID)
	require.True(t, traces[0].Truncated)
	require.Equal(t, int64(6), *traces[1].ServedAccount)
	require.Empty(t, traces[1].Attempts)
	require.Equal(t, 502, traces[2].ClientStatus)
	require.Nil(t, traces[2].ServedAccount)
	require.Nil(t, traces[3].ServedAccount)
}
