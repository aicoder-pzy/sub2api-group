-- Keep compact evidence independently of artwork/history pruning. Existing modes
-- keep their original sample rules; only quality_preferred reads this history.
ALTER TABLE pelican_manual_account_quality ADD COLUMN effort TEXT NOT NULL DEFAULT '';
CREATE TABLE upstream_quality_samples (
    id BIGSERIAL PRIMARY KEY,
    source TEXT NOT NULL,
    source_id TEXT NOT NULL,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    group_id BIGINT REFERENCES groups(id) ON DELETE CASCADE,
    model TEXT NOT NULL,
    effort TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'pelican',
    verdict TEXT NOT NULL CHECK (verdict IN ('normal','degraded','unknown','failed')),
    reason TEXT NOT NULL DEFAULT '',
    latency_ms BIGINT NOT NULL DEFAULT 0,
    sampled_at TIMESTAMPTZ NOT NULL,
    UNIQUE(source, source_id, account_id)
);
CREATE INDEX idx_upstream_quality_model_recent ON upstream_quality_samples(model, sampled_at DESC, account_id);
CREATE INDEX idx_upstream_quality_expiry ON upstream_quality_samples(sampled_at);

CREATE FUNCTION capture_upstream_quality_sample() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    target_account BIGINT;
    target_group BIGINT;
    target_model TEXT;
    target_kind TEXT := 'pelican';
    target_effort TEXT := '';
    target_time TIMESTAMPTZ;
    sample_key TEXT;
    sample_verdict TEXT;
    attempt JSONB;
BEGIN
    IF TG_TABLE_NAME = 'pelican_manual_account_quality' THEN
        target_account := NEW.account_id;
        target_model := NEW.model_id;
        target_effort := NEW.effort;
        target_time := NEW.sampled_at;
        sample_key := NEW.account_id::text || ':' || NEW.model_id || ':' || NEW.sampled_at::text;
        sample_verdict := NEW.quality_verdict;
    ELSIF TG_TABLE_NAME = 'scheduled_test_results' THEN
        SELECT account_id, model_id INTO target_account, target_model FROM scheduled_test_plans WHERE id = NEW.plan_id;
        IF NEW.pelican_config IS NULL THEN
            IF NEW.quality_verdict NOT IN ('correct','incorrect','unknown') THEN RETURN NEW; END IF;
            target_kind := 'answer';
            SELECT reasoning_effort INTO target_effort FROM scheduled_test_plans WHERE id = NEW.plan_id;
        ELSE
            target_model := COALESCE(NULLIF(NEW.pelican_config->>'model_id',''), target_model);
            target_effort := COALESCE(NEW.pelican_config->>'reasoning_effort','');
        END IF;
        target_time := NEW.finished_at;
        sample_key := NEW.id::text;
        sample_verdict := CASE WHEN NEW.status <> 'success' THEN 'failed' ELSE NEW.quality_verdict END;
    ELSE
        target_account := NEW.account_id;
        SELECT group_id INTO target_group FROM pelican_group_test_plans WHERE id = NEW.plan_id;
        target_model := COALESCE(NULLIF(NEW.quality_model_id,''),NEW.pelican_config->>'model_id','');
        target_effort := COALESCE(NEW.pelican_config->>'reasoning_effort','');
        target_time := NEW.finished_at;
        sample_key := NEW.id::text;
        sample_verdict := CASE WHEN NEW.status <> 'success' THEN 'failed' ELSE NEW.quality_verdict END;
        -- Record failed channels as well as the final successful fallback.
        FOR attempt IN SELECT value FROM jsonb_array_elements(COALESCE(NEW.attempts,'[]'::jsonb)) LOOP
            IF EXISTS (SELECT 1 FROM accounts WHERE id = (attempt->>'account_id')::bigint) THEN
                INSERT INTO upstream_quality_samples(source,source_id,account_id,group_id,model,effort,verdict,sampled_at)
                VALUES ('group_attempt',sample_key,(attempt->>'account_id')::bigint,target_group,target_model,target_effort,'failed',target_time)
                ON CONFLICT DO NOTHING;
            END IF;
        END LOOP;
    END IF;
    IF target_account IS NULL OR target_account <= 0 OR target_model = '' THEN RETURN NEW; END IF;
    sample_verdict := CASE sample_verdict WHEN 'correct' THEN 'normal' WHEN 'incorrect' THEN 'degraded'
        WHEN 'normal' THEN 'normal' WHEN 'degraded' THEN 'degraded' WHEN 'failed' THEN 'failed' ELSE 'unknown' END;
    INSERT INTO upstream_quality_samples(source,source_id,account_id,group_id,model,effort,kind,verdict,reason,latency_ms,sampled_at)
    VALUES (TG_TABLE_NAME,sample_key,target_account,target_group,target_model,COALESCE(target_effort,''),target_kind,
        sample_verdict,NEW.quality_reason,NEW.latency_ms,target_time)
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$$;

CREATE TRIGGER capture_group_quality AFTER INSERT ON pelican_group_test_results
    FOR EACH ROW EXECUTE FUNCTION capture_upstream_quality_sample();
CREATE TRIGGER capture_scheduled_quality AFTER INSERT ON scheduled_test_results
    FOR EACH ROW EXECUTE FUNCTION capture_upstream_quality_sample();
CREATE TRIGGER capture_manual_quality AFTER INSERT OR UPDATE ON pelican_manual_account_quality
    FOR EACH ROW EXECUTE FUNCTION capture_upstream_quality_sample();

INSERT INTO upstream_quality_samples(source,source_id,account_id,group_id,model,effort,verdict,reason,latency_ms,sampled_at)
SELECT 'pelican_group_test_results',r.id::text,r.account_id,p.group_id,
    COALESCE(NULLIF(r.quality_model_id,''),r.pelican_config->>'model_id',''),
    COALESCE(r.pelican_config->>'reasoning_effort',''),
    CASE WHEN r.status <> 'success' THEN 'failed' WHEN r.quality_verdict IN ('normal','degraded') THEN r.quality_verdict ELSE 'unknown' END,
    r.quality_reason,r.latency_ms,r.finished_at
FROM pelican_group_test_results r JOIN pelican_group_test_plans p ON p.id=r.plan_id
WHERE r.account_id > 0 AND r.finished_at >= NOW()-INTERVAL '7 days' ON CONFLICT DO NOTHING;

INSERT INTO upstream_quality_samples(source,source_id,account_id,group_id,model,effort,verdict,sampled_at)
SELECT 'group_attempt',r.id::text,a.id,p.group_id,
    COALESCE(NULLIF(r.quality_model_id,''),r.pelican_config->>'model_id',''),
    COALESCE(r.pelican_config->>'reasoning_effort',''),'failed',r.finished_at
FROM pelican_group_test_results r JOIN pelican_group_test_plans p ON p.id=r.plan_id
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(r.attempts,'[]'::jsonb)) attempt
JOIN accounts a ON a.id=(attempt->>'account_id')::bigint
WHERE r.finished_at >= NOW()-INTERVAL '7 days' ON CONFLICT DO NOTHING;

INSERT INTO upstream_quality_samples(source,source_id,account_id,model,effort,kind,verdict,reason,latency_ms,sampled_at)
SELECT 'scheduled_test_results',r.id::text,p.account_id,COALESCE(NULLIF(r.pelican_config->>'model_id',''),p.model_id),
    COALESCE(r.pelican_config->>'reasoning_effort',p.reasoning_effort,''),
    CASE WHEN r.pelican_config IS NULL THEN 'answer' ELSE 'pelican' END,
    CASE WHEN r.status <> 'success' THEN 'failed' WHEN r.quality_verdict IN ('normal','degraded') THEN r.quality_verdict
        WHEN r.quality_verdict='correct' THEN 'normal' WHEN r.quality_verdict='incorrect' THEN 'degraded' ELSE 'unknown' END,
    r.quality_reason,r.latency_ms,r.finished_at
FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id=r.plan_id
WHERE r.finished_at >= NOW()-INTERVAL '7 days' AND (r.pelican_config IS NOT NULL OR r.quality_verdict IN ('correct','incorrect','unknown'))
ON CONFLICT DO NOTHING;

INSERT INTO upstream_quality_samples(source,source_id,account_id,model,effort,verdict,reason,latency_ms,sampled_at)
SELECT 'pelican_manual_account_quality',account_id::text || ':' || model_id || ':' || sampled_at::text,
    account_id,model_id,effort,quality_verdict,quality_reason,latency_ms,sampled_at
FROM pelican_manual_account_quality WHERE sampled_at >= NOW()-INTERVAL '7 days' ON CONFLICT DO NOTHING;
