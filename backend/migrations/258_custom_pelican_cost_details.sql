-- Preserve historical estimates; only new results receive pricing/usage snapshots.
ALTER TABLE pelican_group_test_results
    ADD COLUMN IF NOT EXISTS cost_details JSONB NOT NULL DEFAULT '[]'::jsonb;
