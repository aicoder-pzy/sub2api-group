-- Administrator-defined usage labels, unrelated to access control or routing.
ALTER TABLE upstream_sites
    ADD COLUMN purpose TEXT NOT NULL DEFAULT ''
    CHECK (purpose IN ('', 'public', 'private'));
