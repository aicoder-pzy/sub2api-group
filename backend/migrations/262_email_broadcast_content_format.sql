ALTER TABLE email_broadcasts
    ADD COLUMN IF NOT EXISTS content_format TEXT NOT NULL DEFAULT 'text'
    CHECK (content_format IN ('text', 'html'));
