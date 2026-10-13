//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelAutoSpeedSeparatesEffortContextCostAndIncompleteResponses(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
CREATE TEMP TABLE usage_logs (id bigint,created_at timestamptz,account_id bigint,api_key_id bigint,group_id bigint,
 request_id text,model text,requested_model text,reasoning_effort text,duration_ms int,first_token_ms int,stream bool,
 total_cost numeric,account_stats_cost numeric,account_rate_multiplier numeric,
 input_tokens bigint,output_tokens bigint,cache_creation_tokens bigint,cache_read_tokens bigint);
CREATE TEMP TABLE ops_error_logs (created_at timestamptz,account_id bigint,api_key_id bigint,group_id bigint,
 request_id text,model text,requested_model text,error_phase text,is_count_tokens bool,error_owner text,upstream_errors jsonb);
INSERT INTO usage_logs VALUES
 (1,NOW(),1,1,6,'ok','mapped',' m ','medium',3000,1000,true,10,20,0.1,10000,100,0,0),
 (2,NOW(),1,1,6,'xhigh','mapped','m','xhigh',3000,50,true,10,20,0.1,10000,100,0,0),
 (3,NOW(),1,1,7,'group','mapped','m','medium',3000,50,true,10,20,0.1,10000,100,0,0),
 (4,NOW(),1,1,6,'model','mapped','other','medium',3000,50,true,10,20,0.1,10000,100,0,0),
 (5,NOW()-INTERVAL '25 hours',1,1,6,'old','mapped','m','medium',3000,50,true,10,20,0.1,10000,100,0,0),
 (6,NOW(),1,1,6,'partial','mapped','m','medium',3000,50,true,10,20,0.1,10000,100,0,0),
 (7,NOW(),1,1,6,'nonstream','mapped','m','medium',3000,50,false,10,20,0.1,10000,100,0,0),
 (8,NOW(),1,1,6,'empty','mapped','m','medium',3000,50,true,10,20,0.1,10000,0,0,0),
 (9,NOW(),1,1,6,'cached','mapped','m','medium',6000,2000,true,10,10,0.1,1000,200,0,140000),
 (10,NOW(),2,1,6,'recover','mapped','m','medium',3000,1000,true,10,10,0.05,10000,100,0,0);
INSERT INTO ops_error_logs VALUES
 (NOW(),1,1,6,'partial','m','m','upstream',false,'provider','[]'),
 (NOW(),1,1,6,'recover','m','m','upstream',false,'provider','[{"account_id":1,"upstream_status_code":504}]');`)
	require.NoError(t, err)
	rows, err := tx.QueryContext(ctx, modelAutoSpeedSQL, 6, "m", "medium")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var got []struct {
		account int64
		effort  string
		bucket  service.ModelAutoSpeedBucket
	}
	for rows.Next() {
		var row struct {
			account int64
			effort  string
			bucket  service.ModelAutoSpeedBucket
		}
		require.NoError(t, rows.Scan(&row.account, &row.effort, &row.bucket.ContextBucket, &row.bucket.Samples, &row.bucket.P50MS, &row.bucket.P95MS, &row.bucket.OutputTPS, &row.bucket.CostRatio, &row.bucket.OutputSamples))
		got = append(got, row)
	}
	require.NoError(t, rows.Err())
	require.Len(t, got, 3)
	require.EqualValues(t, 1, got[0].account)
	require.Equal(t, "medium", got[0].effort)
	require.Equal(t, 1, got[0].bucket.ContextBucket)
	require.EqualValues(t, 1, got[0].bucket.Samples)
	require.InDelta(t, 1000, got[0].bucket.P95MS, 0.01)
	require.InDelta(t, 50, got[0].bucket.OutputTPS, 0.01)
	require.InDelta(t, 0.2, got[0].bucket.CostRatio, 0.001, "use the historical account price and multiplier")
	require.Equal(t, 3, got[1].bucket.ContextBucket, "cache tokens are part of the input context")
	require.EqualValues(t, 2, got[2].account, "successful fallback is not excluded for the first account's failure")
}
