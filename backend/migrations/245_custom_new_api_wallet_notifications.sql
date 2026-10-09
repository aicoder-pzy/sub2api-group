-- NewAPI management credentials are shared only within a site and upstream user.
CREATE TABLE IF NOT EXISTS new_api_site_authorizations (
    id BIGSERIAL PRIMARY KEY,
    site_url TEXT NOT NULL,
    upstream_user_id BIGINT NOT NULL CHECK (upstream_user_id > 0),
    access_token_ciphertext TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (site_url, upstream_user_id)
);
CREATE TABLE IF NOT EXISTS new_api_account_bindings (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    authorization_id BIGINT NOT NULL REFERENCES new_api_site_authorizations(id) ON DELETE CASCADE,
    upstream_token_id BIGINT NOT NULL CHECK (upstream_token_id > 0),
    account_fingerprint TEXT NOT NULL,
    token_group TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS new_api_account_bindings_authorization_id_idx ON new_api_account_bindings(authorization_id);

CREATE OR REPLACE FUNCTION invalidate_new_api_account_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.platform IS DISTINCT FROM NEW.platform
        OR OLD.type IS DISTINCT FROM NEW.type
        OR OLD.proxy_id IS DISTINCT FROM NEW.proxy_id
        OR OLD.credentials -> 'api_key' IS DISTINCT FROM NEW.credentials -> 'api_key'
        OR OLD.credentials -> 'base_url' IS DISTINCT FROM NEW.credentials -> 'base_url'
        OR OLD.credentials -> 'header_override_enabled' IS DISTINCT FROM NEW.credentials -> 'header_override_enabled'
        OR OLD.credentials -> 'header_overrides' IS DISTINCT FROM NEW.credentials -> 'header_overrides'
    THEN
        DELETE FROM new_api_account_bindings WHERE account_id = OLD.id;
        IF FOUND AND NEW.extra ? 'upstream_balance_probe' THEN
            NEW.extra := jsonb_set(NEW.extra, '{upstream_balance_probe}', (NEW.extra -> 'upstream_balance_probe') - 'snapshot');
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS new_api_account_binding_identity_guard ON accounts;
CREATE TRIGGER new_api_account_binding_identity_guard
BEFORE UPDATE ON accounts FOR EACH ROW EXECUTE FUNCTION invalidate_new_api_account_binding();

CREATE TABLE IF NOT EXISTS upstream_balance_notification_monitors (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    scope TEXT NOT NULL,
    currency TEXT NOT NULL,
    criteria TEXT NOT NULL,
    adverse BOOLEAN NOT NULL,
    episode_id TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (account_id, scope, currency)
);
CREATE TABLE IF NOT EXISTS upstream_balance_notification_events (
    id TEXT PRIMARY KEY,
    episode_id TEXT NOT NULL,
    phase TEXT NOT NULL CHECK (phase IN ('low', 'recovery')),
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    account_name TEXT NOT NULL,
    scope TEXT NOT NULL,
    currency TEXT NOT NULL,
    remaining DOUBLE PRECISION NOT NULL,
    threshold DOUBLE PRECISION NOT NULL,
    criteria TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    fresh_until TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'sent', 'failed', 'suppressed')),
    deliveries JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease TEXT NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (episode_id, phase)
);
CREATE INDEX IF NOT EXISTS upstream_balance_notification_events_due_idx
    ON upstream_balance_notification_events(next_attempt_at) WHERE status IN ('pending', 'sending', 'failed');
CREATE INDEX IF NOT EXISTS upstream_balance_notification_events_history_idx
    ON upstream_balance_notification_events(created_at DESC);

CREATE TABLE IF NOT EXISTS upstream_balance_notification_rate_limits (
    destination_hash TEXT PRIMARY KEY,
    next_send_at TIMESTAMPTZ NOT NULL
);
