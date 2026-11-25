package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Expressions are internal SQL, never request input. Share the same evidence
// between test targeting and public status; synchronous monitors cannot qualify.
func pelicanTrafficAccountsSQL(group, model, now string) string {
	return `SELECT account_id, max(seen_at) AS seen_at FROM (
 SELECT account_id, created_at AS seen_at FROM (
  SELECT DISTINCT ON (api_key_id, COALESCE(session_id,''), CASE WHEN COALESCE(session_id,'')='' THEN id END)
    account_id, created_at
  FROM usage_logs WHERE group_id=` + group + `
    AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=` + model + `
    AND created_at>=` + now + `-INTERVAL '1 hour' AND created_at<=` + now + `
    AND account_id>0 AND api_key_id IS NOT NULL AND ` + schedulingAgentStreamSQL("") + `
  ORDER BY api_key_id, COALESCE(session_id,''), CASE WHEN COALESCE(session_id,'')='' THEN id END, created_at DESC, id DESC
 ) sessions
 UNION ALL
 SELECT account_id, created_at FROM ops_error_logs WHERE group_id=` + group + `
   AND COALESCE(NULLIF(BTRIM(requested_model),''),model)=` + model + `
   AND created_at>=` + now + `-INTERVAL '1 hour' AND created_at<=` + now + `
   AND account_id>0 AND ` + schedulingAgentStreamSQL("") + ` AND ` + schedulingAgentFinalFailure + `
) traffic GROUP BY account_id`
}

var pelicanTrafficAccountsQuery = `SELECT t.account_id, a.name, t.seen_at FROM (` +
	pelicanTrafficAccountsSQL("$1", "$2", "$3::timestamptz") + `) t
 JOIN accounts a ON a.id=t.account_id AND a.deleted_at IS NULL
 JOIN account_groups ag ON ag.account_id=t.account_id AND ag.group_id=$1
 LEFT JOIN LATERAL (SELECT max(r.started_at) AS checked_at FROM pelican_group_test_results r
   JOIN pelican_group_test_plans p ON p.id=r.plan_id
   WHERE p.group_id=$1 AND p.id=$4 AND r.account_id=t.account_id) checked ON true
 ORDER BY checked.checked_at ASC NULLS FIRST, t.seen_at DESC, t.account_id`

func (r *pelicanGroupTestRepository) ListTrafficAccounts(ctx context.Context, plan *service.PelicanGroupTestPlan, now time.Time) ([]service.PelicanTrafficAccount, error) {
	rows, err := r.db.QueryContext(ctx, pelicanTrafficAccountsQuery, plan.GroupID, plan.ModelID, now, plan.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []service.PelicanTrafficAccount
	for rows.Next() {
		var account service.PelicanTrafficAccount
		if err := rows.Scan(&account.AccountID, &account.AccountName, &account.LastSeenAt); err != nil {
			return nil, err
		}
		result = append(result, account)
	}
	return result, rows.Err()
}
