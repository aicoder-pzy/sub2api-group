-- Enable the existing five-minute confirmation workflow for model auto groups.
-- No group, account or paid testing plan is enabled by this migration.
CREATE OR REPLACE FUNCTION capture_pelican_degradation_state() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    target RECORD;
    previous pelican_degradation_states%ROWTYPE;
    source_model TEXT;
    began_at TIMESTAMPTZ := NEW.sampled_at;
BEGIN
    IF NEW.kind <> 'pelican' THEN RETURN NEW; END IF;
    IF NEW.source = 'pelican_group_test_results' THEN
        SELECT p.model_id, r.started_at INTO source_model, began_at
        FROM pelican_group_test_results r JOIN pelican_group_test_plans p ON p.id=r.plan_id
        WHERE r.id=NEW.source_id::bigint;
    ELSIF NEW.source = 'scheduled_test_results' THEN
        SELECT started_at INTO began_at FROM scheduled_test_results WHERE id=NEW.source_id::bigint;
    END IF;
    FOR target IN
        SELECT DISTINCT g.id, COALESCE(source_model,NEW.model) AS public_model
        FROM groups g
        WHERE g.deleted_at IS NULL AND g.account_scheduling_mode IN ('fastest_failover','quality_preferred','model_auto')
          AND (g.id=NEW.group_id OR (NEW.group_id IS NULL AND EXISTS (
              SELECT 1 FROM account_groups ag WHERE ag.group_id=g.id AND ag.account_id=NEW.account_id)))
        ORDER BY g.id
    LOOP
        INSERT INTO pelican_degradation_states(group_id,model,account_id,public_model,status,sampled_at,sample_id)
        VALUES (target.id,NEW.model,NEW.account_id,target.public_model,'normal','-infinity',0)
        ON CONFLICT DO NOTHING;
        SELECT * INTO previous FROM pelican_degradation_states
        WHERE group_id=target.id AND model=NEW.model AND account_id=NEW.account_id FOR UPDATE;
        IF (NEW.sampled_at,NEW.id)<=(previous.sampled_at,previous.sample_id) THEN CONTINUE; END IF;
        IF previous.sampled_at<=NEW.sampled_at-INTERVAL '24 hours' THEN
            previous.status := 'normal';
            previous.confirm_after := NULL;
            previous.due_at := NULL;
            previous.action := '';
        END IF;
        IF NEW.verdict='normal' THEN
            previous.status := 'normal';
            previous.confirm_after := NULL;
            previous.due_at := NULL;
            previous.action := '';
        ELSIF NEW.verdict='degraded' THEN
            IF previous.status='pending' AND began_at>=previous.confirm_after THEN
                previous.status := 'confirmed';
                previous.due_at := NEW.sampled_at;
                previous.action := 'replacement';
            ELSIF previous.status='normal' THEN
                previous.status := 'pending';
                previous.confirm_after := GREATEST(NEW.sampled_at,clock_timestamp())+INTERVAL '5 minutes';
                previous.due_at := previous.confirm_after;
                previous.action := 'retry';
            END IF;
        ELSIF previous.status='pending' THEN
            -- Failed/inconclusive tests cannot confirm degradation or erase the
            -- first finding. Retry later, within the same hourly budget.
            previous.due_at := NEW.sampled_at+INTERVAL '5 minutes';
        END IF;
        UPDATE pelican_degradation_states SET public_model=COALESCE(source_model,previous.public_model),
            status=previous.status,confirm_after=previous.confirm_after,due_at=previous.due_at,
            action=previous.action,sampled_at=NEW.sampled_at,sample_id=NEW.id
        WHERE group_id=target.id AND model=NEW.model AND account_id=NEW.account_id;
    END LOOP;
    RETURN NEW;
END;
$$;
