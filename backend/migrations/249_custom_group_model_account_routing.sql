-- Split legacy global allowlists by the groups of their selected accounts.
-- Keep the legacy setting intact for rollback; the new application ignores it.
-- An explicit empty allowlist preserves denial in all existing groups.
WITH rules AS (
    SELECT rule, position
    FROM settings s,
         jsonb_array_elements(s.value::jsonb->'rules') WITH ORDINALITY AS r(rule, position)
    WHERE s.key = 'model_account_routing'
), scoped_rules AS (
    SELECT g.id AS group_id, r.position,
           jsonb_build_object(
               'model', r.rule->'model',
               'account_ids', COALESCE(
                   (SELECT jsonb_agg(ids.account_id ORDER BY ids.position)
                    FROM jsonb_array_elements(r.rule->'account_ids') WITH ORDINALITY AS ids(account_id, position)
                    JOIN account_groups ag ON ag.account_id = (ids.account_id::text)::bigint
                    WHERE ag.group_id = g.id),
                   '[]'::jsonb)
           ) AS rule
    FROM rules r CROSS JOIN groups g
    WHERE g.deleted_at IS NULL
      AND (r.rule->'account_ids' = '[]'::jsonb
           OR EXISTS (
               SELECT 1 FROM account_groups ag
               WHERE ag.group_id = g.id
                 AND r.rule->'account_ids' @> jsonb_build_array(ag.account_id)
           ))
)
INSERT INTO settings (key, value, updated_at)
SELECT 'model_account_routing_group_' || group_id,
       jsonb_build_object('rules', jsonb_agg(rule ORDER BY position))::text,
       NOW()
FROM scoped_rules
GROUP BY group_id
ON CONFLICT (key) DO NOTHING;
