ALTER TABLE upstream_sites
    ADD COLUMN inference_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN purchase_note TEXT NOT NULL DEFAULT '',
    ADD COLUMN inventory JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN inventory_at TIMESTAMPTZ,
    ADD COLUMN model_catalogs JSONB NOT NULL DEFAULT '{}';

UPDATE upstream_sites SET inference_url = base_url;

CREATE INDEX accounts_upstream_site_link ON accounts
    ((extra->'upstream_site_link'->>'site_id'))
    WHERE deleted_at IS NULL AND extra->'upstream_site_link'->>'site_id' IS NOT NULL;
