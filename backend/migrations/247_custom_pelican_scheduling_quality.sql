ALTER TABLE pelican_group_test_results
    ADD COLUMN IF NOT EXISTS quality_verdict VARCHAR(16) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS quality_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS quality_model_id VARCHAR(100) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_pelican_group_quality_recent
    ON pelican_group_test_results (finished_at DESC, account_id)
    WHERE quality_verdict <> '';

CREATE INDEX IF NOT EXISTS idx_scheduled_pelican_quality_recent
    ON scheduled_test_results (finished_at DESC, plan_id)
    WHERE pelican_config IS NOT NULL AND quality_verdict <> '';

-- Only the service's assessment badge is carried over; arbitrary HTML attributes
-- outside this marker are not evidence. Future samples persist structured verdicts.
UPDATE scheduled_test_results
SET quality_verdict = substring(response_text from '<!--sub2api:pelican-assessment:start--><aside data-sub2api-quality="(normal|degraded|unknown)"')
WHERE pelican_config IS NOT NULL AND status = 'success' AND quality_verdict = ''
  AND response_text ~ '<!--sub2api:pelican-assessment:start--><aside data-sub2api-quality="(normal|degraded|unknown)"';

UPDATE pelican_group_test_results
SET quality_verdict = substring(response_text from '<!--sub2api:pelican-assessment:start--><aside data-sub2api-quality="(normal|degraded|unknown)"'),
    quality_model_id = COALESCE(pelican_config->>'model_id', '')
WHERE status = 'success' AND quality_verdict = ''
  AND response_text ~ '<!--sub2api:pelican-assessment:start--><aside data-sub2api-quality="(normal|degraded|unknown)"';

-- Manual account tests have no scheduled plan. Retain only their latest verdict
-- per account/model; the scheduling query applies the same 24-hour expiry.
CREATE TABLE IF NOT EXISTS pelican_manual_account_quality (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model_id VARCHAR(100) NOT NULL,
    quality_verdict VARCHAR(16) NOT NULL CHECK (quality_verdict IN ('normal', 'degraded', 'unknown')),
    quality_reason TEXT NOT NULL DEFAULT '',
    sampled_at TIMESTAMPTZ NOT NULL,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, model_id)
);
