//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
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
 request_id text,model text,requested_model text,error_phase text,is_count_tokens bool,error_owner text,upstream_errors jsonb,client_request_id text);
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
 (10,NOW(),2,1,6,'recover','mapped','m','medium',3000,1000,true,10,10,0.05,10000,100,0,0),
 (11,NOW(),1,1,6,'long','mapped','m','medium',3000,1000,true,10,20,0.1,204800,100,0,0),
 (12,NOW(),1,1,6,'longest','mapped','m','medium',3000,1000,true,10,20,0.1,262144,100,0,0),
 (13,NOW(),1,1,6,'client:retry','mapped','m','medium',3000,50,true,10,20,0.1,10000,100,0,0),
 (14,NOW(),2,1,6,'client:fallback','mapped','m','medium',3000,1000,true,10,10,0.05,10000,100,0,0),
 (15,NOW(),1,1,6,'cross-key','mapped','m','medium',3000,1000,true,10,20,0.1,10000,100,0,0),
 (16,NOW(),1,1,6,'','mapped','m','medium',3000,1000,true,10,20,0.1,10000,100,0,0),
 (17,NOW(),1,1,6,'client:scope','mapped','m','medium',3000,1000,true,10,20,0.1,10000,100,0,0);
INSERT INTO ops_error_logs VALUES
 (NOW(),1,1,6,'partial','m','m','upstream',false,'provider','[]',NULL),
 (NOW(),1,1,6,'recover','m','m','upstream',false,'provider','[{"account_id":1,"upstream_status_code":504}]',NULL),
 (NOW(),1,1,6,'server-retry','m','m','upstream',false,'provider','[]','retry'),
 (NOW(),1,1,6,'server-fallback','m','m','upstream',false,'provider','[{"account_id":1,"upstream_status_code":503}]','fallback'),
 (NOW(),1,2,6,'cross-key','m','m','upstream',false,'provider','[]',NULL),
 (NOW(),1,1,6,'','m','m','upstream',false,'provider','[]',NULL),
 (NOW(),1,1,6,'server-scope','m','m','upstream',false,'provider','[{"account_id":1,"scope":"provider","upstream_status_code":429}]','scope');
ALTER TABLE usage_logs ADD COLUMN actual_cost numeric DEFAULT 0;
ALTER TABLE usage_logs ADD COLUMN image_count int DEFAULT 0;
ALTER TABLE usage_logs ADD COLUMN image_output_tokens bigint DEFAULT 0;
ALTER TABLE ops_error_logs ADD COLUMN id bigserial;
CREATE TEMP TABLE upstream_quality_samples (id bigint,account_id bigint,group_id bigint,model text,effort text,kind text,
 verdict text,reason text,latency_ms float8,sampled_at timestamptz);`)
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
	require.Len(t, got, 5)
	require.EqualValues(t, 1, got[0].account)
	require.Equal(t, "medium", got[0].effort)
	require.Equal(t, 1, got[0].bucket.ContextBucket)
	require.EqualValues(t, 4, got[0].bucket.Samples, "exclude same-channel retry, keep other keys, empty IDs and provider-scoped errors")
	require.InDelta(t, 1000, got[0].bucket.P95MS, 0.01)
	require.InDelta(t, 50, got[0].bucket.OutputTPS, 0.01)
	require.InDelta(t, 0.2, got[0].bucket.CostRatio, 0.001, "use the historical account price and multiplier")
	require.Equal(t, 3, got[1].bucket.ContextBucket, "cache tokens are part of the input context")
	require.Equal(t, 4, got[2].bucket.ContextBucket, "200K contexts must not be compared with 128K contexts")
	require.Equal(t, 5, got[3].bucket.ContextBucket, "256K has its own long-context bucket")
	require.EqualValues(t, 2, got[4].account, "successful fallback is not excluded for the first account's failure")
	require.EqualValues(t, 2, got[4].bucket.Samples, "fallback association supports both server and client request IDs")
	require.NoError(t, rows.Close())
	// The quality ranking's latency query must apply the same error association.
	repo := newUsageLogRepositoryWithSQL(nil, tx)
	quality, err := repo.GetQualityPreferredAccountQuality(ctx, 6, "m", "medium")
	require.NoError(t, err)
	require.EqualValues(t, 7, quality[1].Quality.LatencySamples, "quality latency also excludes client-ID retries, empty output and isolates API keys")
	require.EqualValues(t, 2, quality[2].Quality.LatencySamples)
	require.EqualValues(t, 10, quality[1].Successes, "reliability also associates client IDs without excluding another API key")
	_, err = tx.ExecContext(ctx, `ALTER TABLE usage_logs ADD COLUMN inbound_endpoint text DEFAULT '/v1/chat/completions';
ALTER TABLE ops_error_logs ADD COLUMN inbound_endpoint text DEFAULT '/v1/chat/completions';
UPDATE usage_logs SET inbound_endpoint='/v1/responses' WHERE id=1;
INSERT INTO upstream_quality_samples VALUES(1,1,6,'mapped','medium','pelican','normal','fixture',100,NOW());`)
	require.NoError(t, err)
	ctx = context.WithValue(ctx, ctxkey.Group, &service.Group{ID: 6, AccountSchedulingMode: service.AccountSchedulingModeSmartChannel})
	ctx = context.WithValue(ctx, ctxkey.RequestedPublicModel, "m")
	ctx = context.WithValue(ctx, ctxkey.InboundEndpoint, "/v1/responses")
	observed, err := repo.GetModelAutoAccountQuality(ctx, 6, "mapped", "medium")
	require.NoError(t, err)
	require.EqualValues(t, 1, observed[1].Successes, "business evidence uses the public model and exact ingress")
	require.EqualValues(t, 1, observed[1].Quality.LatencySamples)
	require.EqualValues(t, 1, observed[1].Quality.Normal, "quality still uses the routing/test model")
	require.Len(t, observed[1].Auto.Buckets, 1)
	require.EqualValues(t, 1, observed[1].Auto.Buckets[0].Samples)
	require.InDelta(t, 1000, observed[1].Auto.Buckets[0].P95MS, .01)
	chatCtx := context.WithValue(ctx, ctxkey.InboundEndpoint, "/v1/chat/completions")
	chat, err := repo.GetModelAutoAccountQuality(chatCtx, 6, "mapped", "medium")
	require.NoError(t, err)
	require.Greater(t, chat[1].Successes, observed[1].Successes, "different ingress cannot reuse the responses cache")
	require.EqualValues(t, 1, chat[1].Quality.Normal)
}
