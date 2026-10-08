-- Pelican account tests, group tests, showcase and cost snapshots.
-- Adapted from ranxi2001/sub2api production 1ff6397fae6d1bd9e7f420a5a6620b4f786efb13.
ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS pelican_config JSONB,
    ADD COLUMN IF NOT EXISTS running_until TIMESTAMPTZ;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS pelican_config JSONB;

-- 鹈鹕测智用户展示：定时鹈鹕测试成功生成的 HTML 按展示分组各复制一份，
-- 独立于管理员的测试历史保留与清理（每组最多保留 N 张，可选超过 N 天自动清理）。
-- 旧版本二进制不读写这张表，可继续运行。
CREATE TABLE IF NOT EXISTS pelican_showcase_items (
    id               BIGSERIAL PRIMARY KEY,
    group_id         BIGINT      NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    source_result_id BIGINT      NOT NULL,
    model_id         TEXT        NOT NULL DEFAULT '',
    reasoning_effort TEXT        NOT NULL DEFAULT '',
    response_text    TEXT        NOT NULL,
    latency_ms       BIGINT      NOT NULL DEFAULT 0,
    generated_at     TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (group_id, source_result_id)
);

CREATE INDEX IF NOT EXISTS idx_pelican_showcase_items_group_generated
    ON pelican_showcase_items (group_id, generated_at DESC, id DESC);

COMMENT ON TABLE pelican_showcase_items IS
    '鹈鹕测智用户展示快照：源结果被管理员历史清理后仍保留，按 pelican_showcase_config 的张数与天数清理';
COMMENT ON COLUMN pelican_showcase_items.source_result_id IS
    '来源 scheduled_test_results.id，仅用于去重与追溯，不设外键（源结果按管理员历史规则清理）';

-- 鹈鹕测智改为按分组测试：计划绑定分组，每次由网关调度器按真实请求的规则挑选账号作答，
-- 成功的 HTML 发布到该分组的用户展示页；有计划的分组才出现在展示页。
-- 账号级鹈鹕定时测试保留（管理员排查用），但不再发布到展示页。
-- 旧版本二进制不读写这两张表，可继续运行；回滚后需在系统设置里重新选择展示分组。
CREATE TABLE IF NOT EXISTS pelican_group_test_plans (
    id              BIGSERIAL    PRIMARY KEY,
    group_id        BIGINT       NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    model_id        VARCHAR(100) NOT NULL DEFAULT '',
    cron_expression VARCHAR(100) NOT NULL DEFAULT '*/30 * * * *',
    enabled         BOOLEAN      NOT NULL DEFAULT false,
    pelican_config  JSONB        NOT NULL,
    last_run_at     TIMESTAMPTZ,
    next_run_at     TIMESTAMPTZ,
    running_until   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pelican_group_test_plans_group
    ON pelican_group_test_plans (group_id);
CREATE INDEX IF NOT EXISTS idx_pelican_group_test_plans_due
    ON pelican_group_test_plans (next_run_at) WHERE enabled = true;

-- 与 scheduled_test_results 共用 ID 序列：两者的 ID 都用作 pelican_showcase_items.source_result_id，
-- 共用序列保证与展示表里旧的账号级快照不会撞号。
CREATE TABLE IF NOT EXISTS pelican_group_test_results (
    id             BIGINT      PRIMARY KEY DEFAULT nextval('scheduled_test_results_id_seq'),
    plan_id        BIGINT      NOT NULL REFERENCES pelican_group_test_plans(id) ON DELETE CASCADE,
    account_id     BIGINT,
    account_name   TEXT        NOT NULL DEFAULT '',
    attempts       JSONB       NOT NULL DEFAULT '[]'::jsonb,
    status         VARCHAR(20) NOT NULL,
    response_text  TEXT        NOT NULL DEFAULT '',
    error_message  TEXT        NOT NULL DEFAULT '',
    latency_ms     BIGINT      NOT NULL DEFAULT 0,
    pelican_config JSONB,
    started_at     TIMESTAMPTZ NOT NULL,
    finished_at    TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pelican_group_test_results_plan
    ON pelican_group_test_results (plan_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_pelican_group_test_results_created
    ON pelican_group_test_results (created_at);

COMMENT ON TABLE pelican_group_test_plans IS
    '鹈鹕测智分组测试计划：按 Cron 定时出题，账号由网关调度器按真实请求规则挑选';
COMMENT ON COLUMN pelican_group_test_results.account_id IS
    '最终作答的账号（不设外键，账号删除后保留记录）；NULL 表示没有选到可用账号';
COMMENT ON COLUMN pelican_group_test_results.attempts IS
    '出内容前就报错而换掉的账号：[{account_id, account_name, error}]，按尝试顺序';
COMMENT ON COLUMN pelican_showcase_items.source_result_id IS
    '来源结果 ID：pelican_group_test_results.id（分组测试）或 scheduled_test_results.id（旧版账号级测试），两表共用序列；仅用于去重与追溯，不设外键';


-- Cost snapshots must survive the seven-day / 100-result history retention.
-- Legacy results have no usage data: NULL means unknown, never free.
ALTER TABLE pelican_group_test_results
    ADD COLUMN IF NOT EXISTS cost_usd NUMERIC(20, 10),
    ADD COLUMN IF NOT EXISTS cost_incomplete BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS pelican_group_test_daily_costs (
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    cost_date DATE NOT NULL,
    cost_usd NUMERIC(20, 10) NOT NULL DEFAULT 0,
    unpriced_count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, cost_date)
);

COMMENT ON COLUMN pelican_group_test_results.cost_usd IS
    'USD upstream cost snapshot from reported token usage and account cost multiplier; NULL if unavailable';
COMMENT ON TABLE pelican_group_test_daily_costs IS
    'Group-level lifetime costs by server-local completion date, independent of result and plan retention';
