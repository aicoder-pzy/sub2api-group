CREATE OR REPLACE FUNCTION capture_text_intelligence_state() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    previous text_intelligence_states%ROWTYPE;
    question JSONB := text_intelligence_config(NEW.pelican_config);
    current_question JSONB;
    retry_interval_minutes INTEGER;
BEGIN
    IF COALESCE(NEW.pelican_config->>'question_kind','') NOT IN ('candy','custom') THEN RETURN NEW; END IF;
    NEW.intelligence_status := CASE WHEN NEW.status='success' THEN 'normal'
        WHEN starts_with(NEW.error_message,'answer_mismatch') THEN 'unknown' ELSE 'failed' END;
    IF NEW.account_id IS NULL THEN RETURN NEW; END IF;
    SELECT text_intelligence_config(p.pelican_config || jsonb_build_object('model_id',p.model_id)),
        GREATEST(1,LEAST(60,COALESCE(NULLIF(p.pelican_config->>'retry_interval_minutes','')::integer,2)))
        INTO current_question,retry_interval_minutes
        FROM pelican_group_test_plans p WHERE p.id=NEW.plan_id;
    IF question IS DISTINCT FROM current_question THEN RETURN NEW; END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('text-intelligence:'||NEW.plan_id||':'||NEW.account_id,0));
    INSERT INTO text_intelligence_states(plan_id,account_id,config,sampled_at,started_at,result_id)
        VALUES(NEW.plan_id,NEW.account_id,question,'-infinity','-infinity',0) ON CONFLICT DO NOTHING;
    SELECT * INTO previous FROM text_intelligence_states
        WHERE plan_id=NEW.plan_id AND account_id=NEW.account_id FOR UPDATE;
    IF question IS DISTINCT FROM previous.config OR previous.sampled_at<=NEW.finished_at-INTERVAL '24 hours' THEN
        previous.confirmed_status := 'unknown';
        previous.confirm_after := NULL;
        previous.due_at := NULL;
        previous.started_at := '-infinity';
    END IF;
    IF NEW.started_at<previous.started_at THEN
        IF NEW.intelligence_status<>'failed' THEN NEW.intelligence_status := previous.confirmed_status; END IF;
        RETURN NEW;
    END IF;
    IF NEW.status='success' THEN
        previous.confirmed_status := 'normal';
        previous.confirm_after := NULL;
        previous.due_at := NULL;
    ELSIF starts_with(NEW.error_message,'answer_mismatch') THEN
        IF previous.confirmed_status='degraded' OR (previous.confirm_after IS NOT NULL AND NEW.started_at>=previous.confirm_after) THEN
            previous.confirmed_status := 'degraded';
            previous.confirm_after := NULL;
            previous.due_at := NULL;
        ELSIF previous.confirm_after IS NULL THEN
            previous.confirm_after := GREATEST(NEW.finished_at,clock_timestamp())+make_interval(mins => retry_interval_minutes);
            previous.due_at := previous.confirm_after;
        END IF;
    ELSIF previous.due_at IS NOT NULL THEN
        previous.confirm_after := NULL;
        previous.due_at := NULL;
    END IF;
    IF NEW.intelligence_status<>'failed' THEN NEW.intelligence_status := previous.confirmed_status; END IF;
    NEW.intelligence_pending := previous.confirm_after IS NOT NULL;
    UPDATE text_intelligence_states SET config=question,confirmed_status=previous.confirmed_status,
        sampled_at=NEW.finished_at,started_at=NEW.started_at,result_id=NEW.id,
        confirm_after=previous.confirm_after,due_at=previous.due_at
        WHERE plan_id=NEW.plan_id AND account_id=NEW.account_id;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS capture_text_intelligence ON pelican_group_test_results;
CREATE TRIGGER capture_text_intelligence BEFORE INSERT ON pelican_group_test_results
    FOR EACH ROW EXECUTE FUNCTION capture_text_intelligence_state();
