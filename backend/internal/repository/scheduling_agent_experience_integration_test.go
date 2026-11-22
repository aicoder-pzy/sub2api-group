//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const experienceFixture = `CREATE TEMP TABLE usage_logs (
 id bigint GENERATED ALWAYS AS IDENTITY, group_id bigint DEFAULT 24, model text DEFAULT 'sol', requested_model text,
 account_id bigint, api_key_id bigint DEFAULT 7, session_id text, request_type int DEFAULT 2, stream bool DEFAULT true,
 inbound_endpoint text DEFAULT '/v1/responses', requested_reasoning_effort text DEFAULT 'xhigh', native_compaction_v2 bool DEFAULT false,
 created_at timestamptz, cache_read_tokens int, input_tokens int, cache_creation_tokens int DEFAULT 0,
 output_tokens int DEFAULT 20, first_token_ms int DEFAULT 2000, duration_ms int DEFAULT 5000
);
INSERT INTO usage_logs(account_id,session_id,created_at,cache_read_tokens,input_tokens)
SELECT 1,'session-'||s, date_bin('10 minutes',NOW()-INTERVAL '1 minute','2000-01-01'::timestamptz)
   - (age*10+turn)*INTERVAL '1 minute', hits, 4000-hits
FROM generate_series(1,10) AS s CROSS JOIN generate_series(0,2) AS age CROSS JOIN generate_series(1,3) AS turn
CROSS JOIN LATERAL (SELECT CASE WHEN s=1 AND age<2 THEN 2900 ELSE 3720 END AS hits) data;
INSERT INTO usage_logs(account_id,session_id,created_at,cache_read_tokens,input_tokens,request_type)
SELECT 1,'sync',created_at,2900,1100,1 FROM usage_logs WHERE session_id='session-1';
INSERT INTO usage_logs(account_id,session_id,created_at,cache_read_tokens,input_tokens,first_token_ms,duration_ms)
SELECT CASE WHEN s IN ('trial-a','trial-b','foreign') THEN 3 ELSE 1 END, s,
  NOW()-(5-i)*INTERVAL '1 minute',hits,4000-hits,latency,latency+2000
FROM unnest(ARRAY['trial-a','trial-b','control-a','control-b','foreign']) s CROSS JOIN generate_series(0,2) i
CROSS JOIN LATERAL (SELECT CASE WHEN i=0 OR s='foreign' THEN 0 WHEN s LIKE 'trial-%' THEN 3680 ELSE 2920 END AS hits,
 CASE WHEN i=0 OR s='foreign' THEN 100000 WHEN s LIKE 'trial-%' THEN 1800 ELSE 2000 END AS latency) payload;`

func TestSchedulingAgentSessionCacheAndAssignedWarmTrial(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.ExecContext(ctx, experienceFixture)
	require.NoError(t, err)
	repo := &usageLogRepository{sql: tx}
	incidents, err := repo.GetSchedulingAgentCacheIncidents(ctx, 24, []string{"sol"})
	require.NoError(t, err)
	require.Len(t, incidents, 1)
	require.Equal(t, 1, incidents[0].Affected)
	require.Equal(t, 10, incidents[0].Comparable)
	require.InDelta(t, .93, incidents[0].Low[0].Baseline, .001)
	require.InDelta(t, .725, incidents[0].Low[0].Latest, .001)
	require.NotContains(t, incidents[0].Low[0].Session, "session-1")
	assignments := make(map[string]int64)
	for session, account := range map[string]int64{"trial-a": 3, "trial-b": 3, "control-a": 1, "control-b": 1} {
		assignments[fmt.Sprintf("%x/0", sha256.Sum256([]byte("7:"+session)))] = account
	}
	now := time.Now()
	cohorts, err := repo.GetSchedulingAgentTrialCohorts(ctx, 24, "sol", service.SchedulingAgentRoute{TrialID: 3, TrialControlID: 1, TrialStartedAt: now.Add(-10 * time.Minute), TrialUntil: now.Add(time.Hour)}, assignments)
	require.NoError(t, err)
	require.Len(t, cohorts, 1)
	require.Equal(t, 4, cohorts[0].TrialRequests)
	require.Equal(t, 4, cohorts[0].ControlRequests)
	require.InDelta(t, .92, cohorts[0].TrialCache, .001)
	require.InDelta(t, .73, cohorts[0].ControlCache, .001)
	require.Equal(t, float64(1800), cohorts[0].TrialP95)
}
