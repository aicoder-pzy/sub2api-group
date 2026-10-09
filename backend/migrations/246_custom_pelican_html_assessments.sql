-- Quality metadata only; no HTML, API keys or account credentials are stored here.
CREATE TABLE IF NOT EXISTS pelican_html_assessments (
    html_hash       CHAR(64) PRIMARY KEY,
    status          VARCHAR(20) NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    remote_task_id  VARCHAR(128) NOT NULL,
    deadline_at     TIMESTAMPTZ NOT NULL,
    assessment      JSONB,
    error_message   TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
