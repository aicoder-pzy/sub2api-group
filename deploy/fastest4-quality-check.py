import json
import pathlib
import re
import subprocess
import sys

source = pathlib.Path(sys.argv[1]).read_text()
query = re.search(r"func .*GetGroupModelAccountQuality.*?QueryContext\(ctx, `(.*?)`", source, re.S).group(1)
schema = """
BEGIN;
CREATE TEMP TABLE usage_logs (
    account_id bigint, group_id bigint, requested_model text, model text, request_id text,
    created_at timestamptz DEFAULT now(), duration_ms int, first_token_ms int, stream boolean,
    actual_cost numeric DEFAULT 0, total_cost numeric DEFAULT 0, input_tokens int DEFAULT 0,
    output_tokens int DEFAULT 0, cache_creation_tokens int DEFAULT 0, cache_read_tokens int DEFAULT 0,
    image_count int DEFAULT 0, image_output_tokens int DEFAULT 0
);
CREATE TEMP TABLE ops_error_logs (
    id bigint, account_id bigint, group_id bigint, requested_model text, model text,
    request_id text, created_at timestamptz DEFAULT now(), error_phase text,
    error_owner text DEFAULT 'provider', is_count_tokens boolean DEFAULT false, upstream_errors jsonb
);
INSERT INTO usage_logs (account_id, group_id, requested_model, request_id, duration_ms, first_token_ms, stream, output_tokens) VALUES
    (1, 24, 'model-a', 'success', 2000, 100, true, 1),
    (1, 24, 'model-a', 'recovered', 2500, 200, true, 1),
    (1, 24, 'model-a', 'failure-placeholder', 99999, 0, true, 0),
    (1, 23, 'model-a', 'other-group', 99999, 0, false, 1),
    (1, 24, 'model-b', 'other-model', 99999, 0, false, 1),
    (2, 24, 'model-a', 'nonstream', 300, 2, false, 1);
INSERT INTO ops_error_logs (id, account_id, group_id, requested_model, request_id, error_phase, upstream_errors) VALUES
    (1, 1, 24, 'model-a', 'recovered', 'upstream', '[{"account_id":1},{"account_id":1},{"account_id":3}]'),
    (2, 1, 24, 'model-a', 'recovered', 'upstream', '[{"account_id":1}]'),
    (3, 2, 24, 'model-a', 'user-error', 'request', null),
    (4, 4, 24, 'model-a', 'auth-error', 'account_auth', '[]'),
    (5, 1, 24, 'model-b', 'wrong-model', 'upstream', null);
INSERT INTO ops_error_logs (id, account_id, group_id, requested_model, request_id, error_phase, is_count_tokens)
VALUES (6, 1, 24, 'model-a', 'count-tokens', 'upstream', true);
"""
query = query.replace("$1", "24").replace("$2", "'model-a'").replace("$3", "now() - interval '24 hours'")
sql = schema + "\nSELECT row_to_json(result) FROM (" + query + ") result(account_id, successes, failures, latency_ms);\nROLLBACK;\n"
completed = subprocess.run(
    ["docker", "exec", "-i", "sub2api-postgres", "psql", "-X", "-U", "sub2api", "-d", "sub2api", "-v", "ON_ERROR_STOP=1", "-Atq"],
    input=sql, text=True, capture_output=True, check=True
)
rows = {row["account_id"]: row for row in map(json.loads, completed.stdout.splitlines())}
assert rows.keys() == {1, 2, 3, 4}, rows
assert rows[1] == {"account_id": 1, "successes": 1, "failures": 1, "latency_ms": 195}, rows
assert rows[2] == {"account_id": 2, "successes": 1, "failures": 0, "latency_ms": 300}, rows
assert rows[3]["failures"] == rows[4]["failures"] == 1, rows
print("Quality SQL verified: free usage, P95, retry deduplication, model/group isolation, auth failures.")
subprocess.run(
    ["docker", "exec", "-i", "sub2api-postgres", "psql", "-X", "-U", "sub2api", "-d", "sub2api", "-v", "ON_ERROR_STOP=1", "-Atq"],
    input="BEGIN READ ONLY; SET LOCAL statement_timeout = '2s'; EXPLAIN " + query + "; ROLLBACK;",
    text=True, capture_output=True, check=True
)
print("Quality SQL also verified against the real production schema (read-only EXPLAIN).")
