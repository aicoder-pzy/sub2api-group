-- Text questions keep their own confirmation state; they never overwrite the
-- drawing-quality evidence used by routing. A first wrong answer is not degraded.
ALTER TABLE pelican_group_test_results
    ADD COLUMN IF NOT EXISTS intelligence_status TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS intelligence_pending BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS text_intelligence_states (
    plan_id BIGINT NOT NULL REFERENCES pelican_group_test_plans(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    config JSONB NOT NULL,
    confirmed_status TEXT NOT NULL DEFAULT 'unknown' CHECK (confirmed_status IN ('unknown','normal','degraded')),
    sampled_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    result_id BIGINT NOT NULL,
    confirm_after TIMESTAMPTZ,
    due_at TIMESTAMPTZ,
    running_until TIMESTAMPTZ,
    PRIMARY KEY (plan_id, account_id)
);
CREATE INDEX IF NOT EXISTS idx_text_intelligence_due ON text_intelligence_states(due_at) WHERE due_at IS NOT NULL;

CREATE OR REPLACE FUNCTION text_intelligence_config(config JSONB) RETURNS JSONB
LANGUAGE SQL IMMUTABLE AS $$
    SELECT jsonb_build_object('model_id',config->>'model_id',
        'question_kind',config->>'question_kind','prompt',config->>'prompt',
        'expected_answer',CASE WHEN config->>'question_kind'='candy' THEN '21' ELSE config->>'expected_answer' END,
        'reasoning_effort',config->>'reasoning_effort',
        'max_output_tokens',COALESCE((config->>'max_output_tokens')::integer,0));
$$;

CREATE OR REPLACE FUNCTION capture_text_intelligence_state() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    previous text_intelligence_states%ROWTYPE;
    question JSONB := text_intelligence_config(NEW.pelican_config);
    current_question JSONB;
BEGIN
    IF COALESCE(NEW.pelican_config->>'question_kind','') NOT IN ('candy','custom') THEN RETURN NEW; END IF;
    NEW.intelligence_status := CASE WHEN NEW.status='success' THEN 'normal'
        WHEN starts_with(NEW.error_message,'answer_mismatch') THEN 'unknown' ELSE 'failed' END;
    IF NEW.account_id IS NULL THEN RETURN NEW; END IF;
    SELECT text_intelligence_config(p.pelican_config || jsonb_build_object('model_id',p.model_id))
        INTO current_question FROM pelican_group_test_plans p WHERE p.id=NEW.plan_id;
    IF question IS DISTINCT FROM current_question THEN RETURN NEW; END IF;
    -- Serialize samples of this exact question/account, including parallel runs.
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
    -- A slow older sample must not overwrite a newer recovery.
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
            previous.confirm_after := GREATEST(NEW.finished_at,clock_timestamp())+INTERVAL '2 minutes';
            previous.due_at := previous.confirm_after;
        END IF;
    ELSIF previous.due_at IS NOT NULL THEN
        -- An API failure is red and breaks the consecutive-answer sequence.
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
