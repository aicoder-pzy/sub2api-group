-- Reset only the public statistics, preserving test history and artwork. The
-- watermark prevents old assessments or in-flight tests from refilling them.
CREATE TABLE IF NOT EXISTS pelican_group_intelligence_resets (
    group_id BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    reset_at TIMESTAMPTZ NOT NULL
);

CREATE OR REPLACE FUNCTION capture_pelican_group_intelligence() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    target_group BIGINT;
    cutoff TIMESTAMPTZ;
BEGIN
    SELECT group_id INTO target_group FROM pelican_group_test_plans WHERE id = NEW.plan_id;
    IF target_group IS NULL THEN RETURN NEW; END IF;
    -- Resets and completions serialize on the same per-group lock.
    PERFORM pg_advisory_xact_lock(hashtextextended('pelican-intelligence:' || target_group::text, 0));
    SELECT reset_at INTO cutoff FROM pelican_group_intelligence_resets WHERE group_id = target_group;
    IF NEW.status = 'success' AND NEW.quality_verdict IN ('normal', 'degraded')
        AND (cutoff IS NULL OR NEW.started_at >= cutoff) THEN
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
