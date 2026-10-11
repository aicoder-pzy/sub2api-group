-- Keep verdicts with the artwork after admin history is pruned.
ALTER TABLE pelican_showcase_items
    ADD COLUMN IF NOT EXISTS quality_verdict VARCHAR(16) NOT NULL DEFAULT 'unknown'
        CHECK (quality_verdict IN ('normal', 'degraded', 'unknown')),
    ADD COLUMN IF NOT EXISTS quality_reason TEXT NOT NULL DEFAULT '';

UPDATE pelican_showcase_items i
SET quality_verdict = r.quality_verdict, quality_reason = r.quality_reason
FROM pelican_group_test_results r
WHERE i.source_result_id = r.id AND i.response_text = r.response_text
    AND r.quality_verdict IN ('normal', 'degraded', 'unknown');

UPDATE pelican_showcase_items i
SET quality_verdict = r.quality_verdict, quality_reason = r.quality_reason
FROM scheduled_test_results r
WHERE i.source_result_id = r.id AND i.response_text = r.response_text
    AND r.pelican_config IS NOT NULL
    AND r.quality_verdict IN ('normal', 'degraded', 'unknown');

-- Legacy copies may outlive their source record. Reuse only the same service
-- marker accepted by migration 247, never arbitrary model HTML attributes.
UPDATE pelican_showcase_items i
SET quality_verdict = substring(response_text from '<!--sub2api:pelican-assessment:start--><aside data-sub2api-quality="(normal|degraded|unknown)"')
WHERE NOT EXISTS (SELECT 1 FROM pelican_group_test_results r WHERE r.id = i.source_result_id)
    AND NOT EXISTS (SELECT 1 FROM scheduled_test_results r WHERE r.id = i.source_result_id)
    AND response_text ~ '<!--sub2api:pelican-assessment:start--><aside data-sub2api-quality="(normal|degraded|unknown)"';
