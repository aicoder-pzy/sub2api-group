//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelStatisticsHistoricalCostsAndEvidence(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
CREATE TEMP TABLE accounts (id bigint,name text,deleted_at timestamptz);
CREATE TEMP TABLE usage_logs (id bigint,created_at timestamptz,account_id bigint,api_key_id bigint,group_id bigint,
 request_id text,model text,requested_model text,reasoning_effort text,duration_ms int,first_token_ms int,stream bool,
 total_cost numeric,actual_cost numeric,account_stats_cost numeric,account_rate_multiplier numeric,
 input_tokens bigint,output_tokens bigint,cache_creation_tokens bigint,cache_read_tokens bigint,image_count int,image_output_tokens bigint);
CREATE TEMP TABLE ops_error_logs (id bigint,created_at timestamptz,account_id bigint,api_key_id bigint,group_id bigint,
 request_id text,model text,requested_model text,error_phase text,is_count_tokens bool,error_owner text,
 upstream_errors jsonb,upstream_status_code int,status_code int);
CREATE TEMP TABLE upstream_quality_samples (account_id bigint,group_id bigint,model text,effort text,kind text,verdict text,sampled_at timestamptz);
INSERT INTO accounts VALUES (1,'A',NULL),(2,'B',NULL),(3,'C','2026-10-12');
INSERT INTO usage_logs VALUES
 (1,'2026-10-12 00:00:00+08',1,1,1,'r','mapped',' m ','xhigh',3000,1000,true,10,3,20,0.1,100,100,0,0,0,0),
 (2,'2026-10-12 00:00:01+08',2,1,1,'r','mapped','m','xhigh',4000,2000,true,10,4,NULL,NULL,100,100,0,0,0,0),
 (3,'2026-10-12 08:00:00+08',1,1,1,'free','m',NULL,'xhigh',100,NULL,false,0,0,NULL,NULL,10,0,0,0,0,0),
 (4,'2026-10-12 09:00:00+08',1,1,1,'placeholder','m',NULL,'xhigh',1,1,true,0,0,NULL,NULL,0,0,0,0,0,0);
INSERT INTO usage_logs SELECT 5,'2026-10-13 00:00:00+08',account_id,api_key_id,group_id,'end',model,requested_model,reasoning_effort,duration_ms,first_token_ms,stream,total_cost,actual_cost,account_stats_cost,account_rate_multiplier,input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,image_count,image_output_tokens FROM usage_logs WHERE id=1;
INSERT INTO usage_logs SELECT 6,created_at,account_id,api_key_id,2,'other-group',model,requested_model,reasoning_effort,duration_ms,first_token_ms,stream,total_cost,actual_cost,account_stats_cost,account_rate_multiplier,input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,image_count,image_output_tokens FROM usage_logs WHERE id=1;
INSERT INTO ops_error_logs VALUES
 (1,'2026-10-12 00:00:01+08',2,1,1,'r','m',NULL,'upstream',false,'provider','[{"account_id":1,"upstream_status_code":429},{"account_id":1,"upstream_status_code":429}]',NULL,200),
 (2,'2026-10-12 01:00:00+08',3,1,1,'failed','m',NULL,'upstream',false,'provider','[]',503,503),
 (3,'2026-10-12 02:00:00+08',1,1,1,'scope','m',NULL,'upstream',false,'provider','[{"account_id":1,"scope":"provider","upstream_status_code":429}]',429,429),
 (4,'2026-10-12 03:00:00+08',1,1,1,'client','m',NULL,'upstream',false,'client','[]',400,400);
INSERT INTO upstream_quality_samples SELECT 1,1,'m','xhigh','pelican',v,'2026-10-12 08:00:00+08' FROM unnest(ARRAY['normal','degraded','unknown','failed']) v;
INSERT INTO upstream_quality_samples VALUES
 (1,2,'m','xhigh','pelican','normal','2026-10-12 08:00:00+08'),
 (1,1,'other','xhigh','pelican','normal','2026-10-12 08:00:00+08'),
 (1,1,'m','medium','pelican','normal','2026-10-12 08:00:00+08'),
 (1,NULL,'m','xhigh','pelican','normal','2026-10-12 08:00:00+08'),
 (1,1,'m','xhigh','answer','normal','2026-10-12 08:00:00+08');`)
	require.NoError(t, err)
	start, err := time.Parse(time.RFC3339, "2026-10-12T00:00:00+08:00")
	require.NoError(t, err)
	repo := newUsageLogRepositoryWithSQL(nil, tx)
	query := service.ChannelStatisticsQuery{GroupID: 1, Model: "m", Effort: "xhigh"}
	got, err := repo.GetChannelStatistics(ctx, query, start, start.Add(24*time.Hour))
	require.NoError(t, err)
	s := got.Summary
	require.EqualValues(t, 3, s.Requests, "free calls count, zero-token placeholders and end boundary don't")
	require.EqualValues(t, 2, s.Successes, "failed first account cannot also count as a success for the same request")
	require.EqualValues(t, 1, s.Failures, "recovered duplicate failures count once; unknown-effort failure is separate")
	require.EqualValues(t, 1, s.UnclassifiedFailures)
	require.InDelta(t, 12, s.AccountCost, 1e-9, "historical custom price and multiplier, nil multiplier defaults to 1")
	require.InDelta(t, 7, s.UserCost, 1e-9)
	require.InDelta(t, 20, s.StandardCost, 1e-9)
	require.EqualValues(t, 2, s.LatencySamples)
	require.InDelta(t, 1500, *s.TTFTP50MS, 1e-9)
	require.InDelta(t, 1950, *s.TTFTP95MS, 1e-9)
	require.EqualValues(t, 1, s.Normal)
	require.EqualValues(t, 1, s.Degraded)
	require.EqualValues(t, 1, s.Unknown)
	require.EqualValues(t, 1, s.FailedTests)
	require.Len(t, got.Daily, 1)
	require.Equal(t, "2026-10-12", got.Daily[0].Day)
	require.Len(t, got.Accounts, 3, "failure-only account remains visible as unclassified")
	query.Effort = ""
	got, err = repo.GetChannelStatistics(ctx, query, start, start.Add(24*time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 2, got.Summary.Failures)
	require.EqualValues(t, 2, got.Summary.Normal, "medium included only when effort is unfiltered")
	require.True(t, got.Accounts[2].AccountDeleted)
	require.Nil(t, got.Accounts[2].TTFTP50MS, "missing first-token times must not become zero")
	query.Model = "no-data"
	got, err = repo.GetChannelStatistics(ctx, query, start, start.Add(24*time.Hour))
	require.NoError(t, err)
	require.Zero(t, got.Summary.Requests)
	require.Nil(t, got.Summary.TTFTP50MS)
	require.Empty(t, got.Accounts)
	require.Empty(t, got.Daily)
}
