CREATE TABLE IF NOT EXISTS email_broadcasts (
    id UUID PRIMARY KEY,
    actor_id BIGINT NOT NULL,
    payload_hash TEXT NOT NULL,
    subject VARCHAR(200) NOT NULL,
    content TEXT NOT NULL,
    interval_seconds INTEGER NOT NULL CHECK (interval_seconds BETWEEN 1 AND 60),
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','completed','cancelled')),
    next_send_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS email_broadcast_recipients (
    id BIGSERIAL PRIMARY KEY,
    broadcast_id UUID NOT NULL REFERENCES email_broadcasts(id) ON DELETE CASCADE,
    email VARCHAR(254) NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sending','sent','failed','unknown','cancelled')),
    error TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (broadcast_id,email)
);
CREATE INDEX IF NOT EXISTS email_broadcast_pending_idx ON email_broadcast_recipients (broadcast_id,id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS email_broadcast_active_idx ON email_broadcast_recipients (updated_at) WHERE status = 'sending';
