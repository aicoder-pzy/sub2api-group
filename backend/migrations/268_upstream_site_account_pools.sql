CREATE TABLE IF NOT EXISTS upstream_site_account_pools (
    site_id UUID PRIMARY KEY REFERENCES upstream_sites(id) ON DELETE CASCADE,
    snapshot JSONB NOT NULL,
    aliases JSONB NOT NULL DEFAULT '{}'
);
