-- Group intelligence statistics outlive artwork/admin-history retention. Keep
-- only the latest 100 completed, successfully classified tests per group.
CREATE TABLE IF NOT EXISTS pelican_group_intelligence_samples (
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    source_result_id BIGINT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('normal', 'degraded')),
    tested_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (group_id, source_result_id)
);
CREATE INDEX IF NOT EXISTS idx_pelican_intelligence_recent
    ON pelican_group_intelligence_samples(group_id, tested_at DESC, source_result_id DESC);

CREATE OR REPLACE FUNCTION capture_pelican_group_intelligence() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    target_group BIGINT;
BEGIN
    SELECT group_id INTO target_group FROM pelican_group_test_plans WHERE id = NEW.plan_id;
    IF target_group IS NULL THEN RETURN NEW; END IF;
    -- Concurrent completions must commit one bounded window, without lost samples.
    PERFORM pg_advisory_xact_lock(hashtextextended('pelican-intelligence:' || target_group::text, 0));
    IF NEW.status = 'success' AND NEW.quality_verdict IN ('normal', 'degraded') THEN
        INSERT INTO pelican_group_intelligence_samples(group_id, source_result_id, verdict, tested_at)
        VALUES (target_group, NEW.id, NEW.quality_verdict, NEW.finished_at)
        ON CONFLICT (group_id, source_result_id) DO UPDATE
            SET verdict = EXCLUDED.verdict, tested_at = EXCLUDED.tested_at;
    ELSE
        DELETE FROM pelican_group_intelligence_samples WHERE group_id = target_group AND source_result_id = NEW.id;
    END IF;
    DELETE FROM pelican_group_intelligence_samples
    WHERE group_id = target_group AND source_result_id NOT IN (
        SELECT source_result_id FROM pelican_group_intelligence_samples WHERE group_id = target_group
        ORDER BY tested_at DESC, source_result_id DESC LIMIT 100
    );
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS capture_group_intelligence ON pelican_group_test_results;
CREATE TRIGGER capture_group_intelligence AFTER INSERT OR UPDATE OF status, quality_verdict, finished_at
    ON pelican_group_test_results FOR EACH ROW EXECUTE FUNCTION capture_pelican_group_intelligence();

-- Reuse saved conclusions only. Source results take precedence over compact
-- scheduling evidence and surviving published snapshots; deduplicate by result.
WITH candidates AS (
    SELECT p.group_id, r.id AS source_result_id, r.quality_verdict AS verdict,
        r.finished_at AS tested_at, 1 AS preference
    FROM pelican_group_test_results r JOIN pelican_group_test_plans p ON p.id = r.plan_id
    WHERE r.status = 'success' AND r.quality_verdict IN ('normal', 'degraded')
    UNION ALL
    SELECT s.group_id, s.source_id::bigint, s.verdict, s.sampled_at, 2
    FROM upstream_quality_samples s
    WHERE s.source = 'pelican_group_test_results' AND s.kind = 'pelican'
        AND s.group_id IS NOT NULL AND s.verdict IN ('normal', 'degraded')
        AND s.source_id ~ '^[0-9]{1,18}$'
        AND NOT EXISTS (SELECT 1 FROM pelican_group_test_results r WHERE r.id::text = s.source_id)
    UNION ALL
    SELECT i.group_id, i.source_result_id, i.quality_verdict, i.generated_at, 3
    FROM pelican_showcase_items i
    WHERE i.quality_verdict IN ('normal', 'degraded')
        AND NOT EXISTS (SELECT 1 FROM pelican_group_test_results r WHERE r.id = i.source_result_id)
), deduplicated AS (
    SELECT DISTINCT ON (group_id, source_result_id) group_id, source_result_id, verdict, tested_at
    FROM candidates ORDER BY group_id, source_result_id, preference
), ranked AS (
    SELECT *, ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY tested_at DESC, source_result_id DESC) AS rn
    FROM deduplicated
)
INSERT INTO pelican_group_intelligence_samples(group_id, source_result_id, verdict, tested_at)
SELECT group_id, source_result_id, verdict, tested_at FROM ranked WHERE rn <= 100
ON CONFLICT (group_id, source_result_id) DO NOTHING;

DELETE FROM pelican_group_intelligence_samples WHERE (group_id, source_result_id) IN (
    SELECT group_id, source_result_id FROM (
        SELECT group_id, source_result_id,
            ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY tested_at DESC, source_result_id DESC) AS rn
        FROM pelican_group_intelligence_samples
    ) ranked WHERE rn > 100
);
