ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS prompt_policy JSONB;

CREATE TABLE IF NOT EXISTS upstream_sites (
    id UUID PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    kind VARCHAR(16) NOT NULL CHECK (kind IN ('sub2api', 'newapi')),
    base_url TEXT NOT NULL,
    user_id BIGINT NOT NULL DEFAULT 0,
    token_cipher TEXT NOT NULL,
    proxy_id BIGINT REFERENCES proxies(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS accounts_upstream_import_identity
ON accounts ((extra->'upstream_import'->>'site_id'), (extra->'upstream_import'->>'key_id'), platform)
WHERE deleted_at IS NULL AND extra->'upstream_import'->>'site_id' IS NOT NULL;
