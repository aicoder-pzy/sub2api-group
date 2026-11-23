-- Candy answers are shown in test history and must not become Pelican routing samples.
DROP TRIGGER IF EXISTS capture_group_quality ON pelican_group_test_results;
CREATE TRIGGER capture_group_quality AFTER INSERT ON pelican_group_test_results
    FOR EACH ROW WHEN ((NEW.pelican_config->>'question_kind') IS DISTINCT FROM 'candy')
    EXECUTE FUNCTION capture_upstream_quality_sample();

DROP TRIGGER IF EXISTS capture_scheduled_quality ON scheduled_test_results;
CREATE TRIGGER capture_scheduled_quality AFTER INSERT ON scheduled_test_results
    FOR EACH ROW WHEN ((NEW.pelican_config->>'question_kind') IS DISTINCT FROM 'candy')
    EXECUTE FUNCTION capture_upstream_quality_sample();
