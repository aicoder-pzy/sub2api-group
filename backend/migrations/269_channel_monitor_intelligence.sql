ALTER TABLE channel_monitors ADD COLUMN IF NOT EXISTS intelligence_group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL;
