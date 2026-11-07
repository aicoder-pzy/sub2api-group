-- Independent gallery snapshots keep creative questions after admin history expires.
ALTER TABLE pelican_showcase_items ADD COLUMN IF NOT EXISTS creative_round JSONB;
