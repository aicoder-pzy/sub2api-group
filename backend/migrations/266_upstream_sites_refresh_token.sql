ALTER TABLE upstream_sites
    ADD COLUMN refresh_token_cipher TEXT NOT NULL DEFAULT '';
