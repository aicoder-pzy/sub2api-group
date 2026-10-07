ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS account_scheduling_mode VARCHAR(32) NOT NULL DEFAULT 'priority';
