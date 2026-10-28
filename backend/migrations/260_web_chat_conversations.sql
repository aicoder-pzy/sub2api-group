CREATE TABLE IF NOT EXISTS web_chat_conversations (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    id VARCHAR(80) NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    title VARCHAR(80) NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    byte_size INTEGER NOT NULL DEFAULT 0 CHECK (byte_size >= 0 AND byte_size <= 25165824),
    deleted BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, id)
);
